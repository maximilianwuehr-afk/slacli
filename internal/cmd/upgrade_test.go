package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameExecutableFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "slack")
	link := filepath.Join(dir, "slacli")

	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if !sameExecutable(link, target) {
		t.Fatal("expected symlink and target to resolve to the same executable")
	}
}
