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

const upgradeModule = "github.com/maximilianwuehr/slacli/cmd/slack"

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

	ref := strings.TrimPrefix(strings.TrimSpace(upgradeRef), "@")
	if ref == "" {
		ref = "latest"
	}
	target := upgradeModule + "@" + ref

	output.Info("Installing %s", target)
	install := exec.Command("go", "install", target)
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	install.Env = withEnv(os.Environ(), "GOPROXY=direct")
	if err := install.Run(); err != nil {
		return fmt.Errorf("go install %s: %w", target, err)
	}

	output.Success("Upgrade complete")
	output.Info("Installed target: %s", target)

	installedPath, err := goBinPath("slack")
	if err == nil {
		output.Info("Installed binary: %s", installedPath)
		if current, currentErr := os.Executable(); currentErr == nil && current != installedPath {
			output.Warn(fmt.Sprintf("Current executable is %s; ensure %s is first in PATH", current, filepath.Dir(installedPath)))
		}
	}

	return nil
}

func withEnv(env []string, kv string) []string {
	key := strings.SplitN(kv, "=", 2)[0]
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, key+"=") {
			continue
		}
		out = append(out, item)
	}
	return append(out, kv)
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
