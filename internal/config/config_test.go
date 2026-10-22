package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePrivateFileSecuresExistingFileAndDirectory(t *testing.T) {
	requireUnixPermissions(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storeDir, "credentials.json")
	if err := os.WriteFile(path, []byte("old contents"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WritePrivateFile(path, []byte("new contents")); err != nil {
		t.Fatal(err)
	}

	assertMode(t, storeDir, 0700)
	assertMode(t, path, 0600)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new contents" {
		t.Fatalf("unexpected file contents: %q", string(data))
	}
}

func TestCheckAndRepairPermissions(t *testing.T) {
	requireUnixPermissions(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(storeDir, "config.json")
	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{StoreDir: storeDir}
	issues, err := CheckPermissions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("expected root and known file issues; got %d", len(issues))
	}
	if err := RepairPermissions(cfg); err != nil {
		t.Fatal(err)
	}
	issues, err = CheckPermissions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("expected no permission issues after repair, got %d", len(issues))
	}
	assertMode(t, storeDir, 0700)
	assertMode(t, filePath, 0600)
}

func TestRepairPermissionsDoesNotFollowSymlinks(t *testing.T) {
	requireUnixPermissions(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(storeDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(storeDir, "config.json")); err != nil {
		t.Fatal(err)
	}

	if err := RepairPermissions(&Config{StoreDir: storeDir}); err == nil {
		t.Fatal("expected repair to reject a symlink")
	}
	assertMode(t, target, 0644)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func requireUnixPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not supported on Windows")
	}
}
