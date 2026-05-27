package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSameExecutableFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName("slack"))
	link := filepath.Join(dir, executableName("slacli"))

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

func TestExecutableInstallTargetResolvesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName("slack"))
	link := filepath.Join(dir, executableName("slacli"))

	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if got := executableInstallTarget(link); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestEnsureSlacliAliasCreatesAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertion is Unix-specific")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "slack")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	alias, err := ensureSlacliAlias(target)
	if err != nil {
		t.Fatalf("ensure alias: %v", err)
	}
	if filepath.Base(alias) != "slacli" {
		t.Fatalf("expected slacli alias, got %s", alias)
	}
	if !sameExecutable(alias, target) {
		t.Fatal("expected alias to resolve to target")
	}
	linkTarget, err := os.Readlink(alias)
	if err != nil {
		t.Fatalf("read alias link: %v", err)
	}
	if linkTarget != "slack" {
		t.Fatalf("expected relative link to slack, got %s", linkTarget)
	}
}

func TestInstallBinaryReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, executableName("slack"))

	if err := os.WriteFile(source, []byte("new"), 0o755); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := installBinary(source, target); err != nil {
		t.Fatalf("install binary: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("expected replacement content, got %q", string(data))
	}
}

func TestShouldTryRelease(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{ref: "latest", want: true},
		{ref: "v1.2.3", want: true},
		{ref: "main", want: false},
		{ref: "master", want: false},
		{ref: "a986676", want: false},
	}

	for _, tt := range tests {
		if got := shouldTryRelease(tt.ref); got != tt.want {
			t.Fatalf("shouldTryRelease(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestSourceFallbackRefUsesLatestReleaseTag(t *testing.T) {
	tests := []struct {
		ref            string
		releaseVersion string
		want           string
	}{
		{ref: "latest", releaseVersion: "v1.2.3", want: "v1.2.3"},
		{ref: "latest", releaseVersion: "", want: "latest"},
		{ref: "main", releaseVersion: "v1.2.3", want: "main"},
		{ref: "v1.2.3", releaseVersion: "v1.2.3", want: "v1.2.3"},
	}

	for _, tt := range tests {
		if got := sourceFallbackRef(tt.ref, tt.releaseVersion); got != tt.want {
			t.Fatalf("sourceFallbackRef(%q, %q) = %q, want %q", tt.ref, tt.releaseVersion, got, tt.want)
		}
	}
}
