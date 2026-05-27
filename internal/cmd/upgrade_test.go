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

func TestInstallCandidateWritePathResolvesSymlink(t *testing.T) {
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
	if got := installCandidateWritePath(link); got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestWritablePathInstallTargetUsesEarlierPathDir(t *testing.T) {
	earlyDir := t.TempDir()
	currentDir := t.TempDir()
	laterDir := t.TempDir()
	t.Setenv("PATH", earlyDir+string(os.PathListSeparator)+currentDir+string(os.PathListSeparator)+laterDir)

	got, ok := writablePathInstallTarget(executableName("slack"), filepath.Join(currentDir, executableName("slack")))
	if !ok {
		t.Fatal("expected writable PATH target")
	}
	want := filepath.Join(earlyDir, executableName("slack"))
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestWritablePathInstallTargetSkipsUnrelatedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is Unix-specific")
	}

	earlyDir := t.TempDir()
	safeDir := t.TempDir()
	currentDir := t.TempDir()
	t.Setenv("PATH", earlyDir+string(os.PathListSeparator)+safeDir+string(os.PathListSeparator)+currentDir)

	unrelated := filepath.Join(earlyDir, executableName("slack"))
	if err := os.WriteFile(unrelated, []byte("#!/bin/sh\necho unrelated\n"), 0o755); err != nil {
		t.Fatalf("write unrelated executable: %v", err)
	}

	got, ok := writablePathInstallTarget(executableName("slack"), filepath.Join(currentDir, executableName("slack")))
	if !ok {
		t.Fatal("expected writable PATH target")
	}
	want := filepath.Join(safeDir, executableName("slack"))
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestWritablePathInstallTargetStopsAtCurrentDir(t *testing.T) {
	currentDir := t.TempDir()
	laterDir := t.TempDir()
	t.Setenv("PATH", currentDir+string(os.PathListSeparator)+laterDir)

	if got, ok := writablePathInstallTarget(executableName("slack"), filepath.Join(currentDir, executableName("slack"))); ok {
		t.Fatalf("expected no fallback before current dir, got %s", got)
	}
}

func TestInstallTargetOnPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName("slack"))
	other := filepath.Join(t.TempDir(), executableName("slack"))
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.WriteFile(other, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write other: %v", err)
	}
	t.Setenv("PATH", dir)

	if !installTargetOnPath(target) {
		t.Fatal("expected PATH target to be detected")
	}
	if installTargetOnPath(other) {
		t.Fatal("expected non-PATH target not to be detected")
	}
}

func TestLooksLikeSlacli(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is Unix-specific")
	}

	dir := t.TempDir()
	slacliScript := filepath.Join(dir, "slack")
	otherScript := filepath.Join(dir, "other")
	if err := os.WriteFile(slacliScript, []byte("#!/bin/sh\necho 'slacli test-build'\n"), 0o755); err != nil {
		t.Fatalf("write slacli script: %v", err)
	}
	if err := os.WriteFile(otherScript, []byte("#!/bin/sh\necho 'not this cli'\n"), 0o755); err != nil {
		t.Fatalf("write other script: %v", err)
	}

	if !looksLikeSlacli(slacliScript) {
		t.Fatal("expected script to look like slacli")
	}
	if looksLikeSlacli(otherScript) {
		t.Fatal("expected unrelated script not to look like slacli")
	}
}

func TestSafeUpgradeTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is Unix-specific")
	}

	dir := t.TempDir()
	missing := filepath.Join(dir, executableName("slack"))
	slacliScript := filepath.Join(dir, "slacli-script")
	otherScript := filepath.Join(dir, "other")
	if err := os.WriteFile(slacliScript, []byte("#!/bin/sh\necho 'slacli test-build'\n"), 0o755); err != nil {
		t.Fatalf("write slacli script: %v", err)
	}
	if err := os.WriteFile(otherScript, []byte("#!/bin/sh\necho 'not this cli'\n"), 0o755); err != nil {
		t.Fatalf("write other script: %v", err)
	}

	if !safeUpgradeTarget(missing) {
		t.Fatal("expected missing target to be safe")
	}
	if !safeUpgradeTarget(slacliScript) {
		t.Fatal("expected slacli target to be safe")
	}
	if safeUpgradeTarget(otherScript) {
		t.Fatal("expected unrelated target not to be safe")
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
