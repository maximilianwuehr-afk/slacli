package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"slacli/internal/output"
)

const upgradeRepoURL = "https://github.com/maximilianwuehr-afk/slacli.git"

var upgradeRef string

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade from GitHub",
	Long: `Upgrade installs the latest slack CLI from GitHub.

It requires Go on PATH and installs the binary into GOBIN, or GOPATH/bin when
GOBIN is unset.`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().StringVar(&upgradeRef, "ref", "latest", "GitHub tag, branch, commit, or latest")
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain required for upgrade: %w", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git required for upgrade: %w", err)
	}

	ref := strings.TrimPrefix(strings.TrimSpace(upgradeRef), "@")
	if ref == "" {
		ref = "latest"
	}

	tmpDir, err := os.MkdirTemp("", "slacli-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	checkoutDir := filepath.Join(tmpDir, "slacli")
	output.Info("Fetching %s", upgradeRepoURL)
	clone := exec.Command("git", "clone", "--quiet", upgradeRepoURL, checkoutDir)
	clone.Stdout = os.Stderr
	clone.Stderr = os.Stderr
	if err := clone.Run(); err != nil {
		return fmt.Errorf("git clone %s: %w", upgradeRepoURL, err)
	}

	if ref != "latest" {
		checkout := exec.Command("git", "-C", checkoutDir, "checkout", "--quiet", ref)
		checkout.Stdout = os.Stderr
		checkout.Stderr = os.Stderr
		if err := checkout.Run(); err != nil {
			return fmt.Errorf("git checkout %s: %w", ref, err)
		}
	}

	commit, err := gitCommit(checkoutDir)
	if err != nil {
		return err
	}

	output.Info("Installing %s@%s", upgradeRepoURL, commit)
	install := exec.Command("go", "install", "./cmd/slack")
	install.Dir = checkoutDir
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("go install ./cmd/slack: %w", err)
	}

	output.Success("Upgrade complete")
	output.Info("Installed target: %s@%s", upgradeRepoURL, commit)

	installedPath, err := goBinPath("slack")
	if err == nil {
		output.Info("Installed binary: %s", installedPath)
		if current, currentErr := os.Executable(); currentErr == nil && !sameExecutable(current, installedPath) {
			output.Warn(fmt.Sprintf("Current executable is %s; ensure %s is first in PATH", current, filepath.Dir(installedPath)))
		}
	}

	return nil
}

func gitCommit(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func sameExecutable(a, b string) bool {
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

func goBinPath(binary string) (string, error) {
	out, err := exec.Command("go", "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("unexpected go env output")
	}
	binDir := strings.TrimSpace(lines[0])
	if binDir == "" {
		binDir = filepath.Join(strings.TrimSpace(lines[1]), "bin")
	}
	return filepath.Join(binDir, binary), nil
}
