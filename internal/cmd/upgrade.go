package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"slacli/internal/output"
)

const upgradeRepoURL = "https://github.com/maximilianwuehr-afk/slacli.git"
const upgradeRepoAPIURL = "https://api.github.com/repos/maximilianwuehr-afk/slacli"

var upgradeRef string

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade from GitHub",
	Long: `Upgrade installs the latest slack CLI from GitHub.

By default it replaces the currently running install when that directory is
writable, keeps the slacli alias available, and verifies the installed binary.
For latest releases and tags it downloads the matching GitHub release asset.
For branches, commits, or missing release assets it falls back to building from
source, which requires Go and git on PATH.`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeRef, "ref", "latest", "GitHub tag, branch, commit, or latest")
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	ref := strings.TrimPrefix(strings.TrimSpace(upgradeRef), "@")
	if ref == "" {
		ref = "latest"
	}

	target, err := upgradeInstallTarget()
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "slacli-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	binaryPath := filepath.Join(tmpDir, executableName("slacli-upgrade"))
	version := ""
	source := ""
	sourceRef := ref

	if shouldTryRelease(ref) {
		releaseVersion, ok, err := downloadReleaseBinary(ref, binaryPath)
		if err != nil {
			output.Warn(fmt.Sprintf("Release download failed; falling back to source build: %v", err))
		} else if ok {
			version = releaseVersion
			source = "release"
		} else {
			sourceRef = sourceFallbackRef(ref, releaseVersion)
			output.Info("No compatible release asset found; building from source")
		}
	}

	if source == "" {
		sourceVersion, err := buildUpgradeFromSource(sourceRef, binaryPath, tmpDir)
		if err != nil {
			return err
		}
		version = sourceVersion
		source = "source"
	}

	if err := installBinary(binaryPath, target.Path); err != nil {
		return fmt.Errorf("install binary to %s: %w", target.Path, err)
	}

	if alias, err := ensureSlacliAlias(target.Path); err != nil {
		output.Warn(fmt.Sprintf("Could not update slacli alias %s: %v", alias, err))
	}

	verified, err := verifyInstalledBinary(target.Path)
	if err != nil {
		return err
	}

	output.Success("Upgrade complete")
	output.Info("Installed target: %s@%s (%s)", upgradeRepoURL, version, source)
	output.Info("Installed binary: %s", target.Path)
	output.Info("Verified: %s", verified)
	if target.Fallback {
		output.Warn(fmt.Sprintf("Installed to %s because the current install directory was not writable; ensure %s is first in PATH", target.Path, filepath.Dir(target.Path)))
	}

	return nil
}

func buildUpgradeFromSource(ref, outputPath, tmpDir string) (string, error) {
	if _, err := exec.LookPath("go"); err != nil {
		return "", fmt.Errorf("go toolchain required to build from source: %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git required to build from source: %w", err)
	}

	checkoutDir := filepath.Join(tmpDir, "slacli")
	output.Info("Fetching %s", upgradeRepoURL)
	clone := exec.Command("git", "clone", "--quiet", upgradeRepoURL, checkoutDir)
	clone.Stdout = os.Stderr
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		return "", fmt.Errorf("git clone %s: %w", upgradeRepoURL, err)
	}

	if ref != "latest" {
		checkout := exec.Command("git", "-C", checkoutDir, "checkout", "--quiet", ref)
		checkout.Stdout = os.Stderr
		checkout.Stderr = os.Stderr
		if err := checkout.Run(); err != nil {
			return "", fmt.Errorf("git checkout %s: %w", ref, err)
		}
	}

	commit, err := gitCommit(checkoutDir)
	if err != nil {
		return "", err
	}

	output.Info("Building %s@%s", upgradeRepoURL, commit)
	ldflags := fmt.Sprintf("-s -w -X slacli/internal/cmd.Version=%s -X slacli/internal/cmd.BuildDate=%s", commit, time.Now().UTC().Format(time.RFC3339))
	build := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", outputPath, "./cmd/slack")
	build.Dir = checkoutDir
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("go build ./cmd/slack: %w", err)
	}
	return commit, nil
}

type installTarget struct {
	Path     string
	Fallback bool
}

func upgradeInstallTarget() (installTarget, error) {
	current, err := os.Executable()
	if err == nil && current != "" {
		target := executableInstallTarget(current)
		if directoryWritable(filepath.Dir(target)) {
			return installTarget{Path: target}, nil
		}
		output.Warn(fmt.Sprintf("Current install directory is not writable: %s", filepath.Dir(target)))
	}

	fallbackPath, err := userBinPath(executableName("slack"))
	if err != nil {
		return installTarget{}, fmt.Errorf("resolve fallback bin path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(fallbackPath), 0o755); err != nil {
		return installTarget{}, fmt.Errorf("create fallback bin dir: %w", err)
	}
	if !directoryWritable(filepath.Dir(fallbackPath)) {
		return installTarget{}, fmt.Errorf("fallback install directory is not writable: %s", filepath.Dir(fallbackPath))
	}
	return installTarget{Path: fallbackPath, Fallback: true}, nil
}

func gitCommit(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func executableInstallTarget(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}

func executableName(name string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func sameExecutable(a, b string) bool {
	aInfo, aStatErr := os.Stat(a)
	bInfo, bStatErr := os.Stat(b)
	if aStatErr == nil && bStatErr == nil && os.SameFile(aInfo, bInfo) {
		return true
	}

	aPath, aErr := filepath.EvalSymlinks(a)
	if aErr != nil {
		aPath = a
	}
	bPath, bErr := filepath.EvalSymlinks(b)
	if bErr != nil {
		bPath = b
	}
	aPath, _ = filepath.Abs(aPath)
	bPath, _ = filepath.Abs(bPath)
	return aPath == bPath
}

func directoryWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".slacli-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func userBinPath(binary string) (string, error) {
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		return filepath.Join(gobin, binary), nil
	}
	if gopath := strings.TrimSpace(os.Getenv("GOPATH")); gopath != "" {
		return filepath.Join(gopath, "bin", binary), nil
	}

	if _, err := exec.LookPath("go"); err == nil {
		out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
		if err != nil {
			return "", err
		}
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(lines) >= 2 {
			binDir := strings.TrimSpace(lines[0])
			if binDir == "" {
				binDir = filepath.Join(strings.TrimSpace(lines[1]), "bin")
			}
			if binDir != "" {
				return filepath.Join(binDir, binary), nil
			}
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "go", "bin", binary), nil
}

func shouldTryRelease(ref string) bool {
	if ref == "latest" {
		return true
	}
	lower := strings.ToLower(ref)
	if lower == "main" || lower == "master" || lower == "develop" || strings.HasPrefix(lower, "origin/") {
		return false
	}
	if isLikelyCommit(ref) {
		return false
	}
	return true
}

func sourceFallbackRef(ref, releaseVersion string) string {
	if ref == "latest" && releaseVersion != "" {
		return releaseVersion
	}
	return ref
}

func isLikelyCommit(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, r := range ref {
		if r < '0' || (r > '9' && r < 'A') || (r > 'F' && r < 'a') || r > 'f' {
			return false
		}
	}
	return true
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func downloadReleaseBinary(ref, outputPath string) (string, bool, error) {
	releaseURL := upgradeRepoAPIURL + "/releases/latest"
	if ref != "latest" {
		releaseURL = upgradeRepoAPIURL + "/releases/tags/" + url.PathEscape(ref)
	}

	var release githubRelease
	if err := githubJSON(releaseURL, &release); err != nil {
		if errors.Is(err, errReleaseNotFound) {
			return "", false, nil
		}
		return "", false, err
	}

	assetName := releaseAssetName()
	for _, asset := range release.Assets {
		if asset.Name != assetName {
			continue
		}
		output.Info("Downloading %s@%s", upgradeRepoURL, release.TagName)
		if err := downloadFile(asset.BrowserDownloadURL, outputPath); err != nil {
			return "", false, err
		}
		if err := os.Chmod(outputPath, 0o755); err != nil {
			return "", false, fmt.Errorf("chmod release binary: %w", err)
		}
		return release.TagName, true, nil
	}

	return release.TagName, false, nil
}

var errReleaseNotFound = errors.New("release not found")

func githubJSON(rawURL string, dst interface{}) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	addGitHubHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errReleaseNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func downloadFile(rawURL, outputPath string) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	addGitHubHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("download %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func addGitHubHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "slacli-upgrade")
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func releaseAssetName() string {
	return fmt.Sprintf("slacli-%s-%s%s", runtime.GOOS, runtime.GOARCH, executableExt())
}

func executableExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func installBinary(sourcePath, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(sourcePath, 0o755); err != nil {
		return err
	}

	tmpTarget, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".new-*")
	if err != nil {
		return err
	}
	tmpTargetPath := tmpTarget.Name()
	if err := tmpTarget.Close(); err != nil {
		_ = os.Remove(tmpTargetPath)
		return err
	}
	if err := copyFile(sourcePath, tmpTargetPath, 0o755); err != nil {
		_ = os.Remove(tmpTargetPath)
		return err
	}

	if runtime.GOOS == "windows" {
		_ = os.Remove(targetPath)
	}
	if err := os.Rename(tmpTargetPath, targetPath); err != nil {
		_ = os.Remove(tmpTargetPath)
		return err
	}
	return nil
}

func ensureSlacliAlias(targetPath string) (string, error) {
	aliasPath := filepath.Join(filepath.Dir(targetPath), executableName("slacli"))
	if samePath(aliasPath, targetPath) || sameExecutable(aliasPath, targetPath) {
		return aliasPath, nil
	}

	info, err := os.Lstat(aliasPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return aliasPath, fmt.Errorf("path exists and is not a symlink")
		}
		if err := os.Remove(aliasPath); err != nil {
			return aliasPath, err
		}
	} else if !os.IsNotExist(err) {
		return aliasPath, err
	}

	if runtime.GOOS == "windows" {
		return aliasPath, copyFile(targetPath, aliasPath, 0o755)
	}

	linkTarget, err := filepath.Rel(filepath.Dir(aliasPath), targetPath)
	if err != nil {
		linkTarget = targetPath
	}
	return aliasPath, os.Symlink(linkTarget, aliasPath)
}

func samePath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr != nil || bErr != nil {
		return a == b
	}
	return aAbs == bAbs
}

func copyFile(sourcePath, targetPath string, mode os.FileMode) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = target.Close() }()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}
	return target.Chmod(mode)
}

func verifyInstalledBinary(binaryPath string) (string, error) {
	out, err := exec.Command(binaryPath, "version").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("verify installed binary: %w: %s", err, text)
	}
	if !strings.Contains(text, "slacli") {
		return "", fmt.Errorf("verify installed binary: unexpected version output %q", text)
	}
	return text, nil
}
