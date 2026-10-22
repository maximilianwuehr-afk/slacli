package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"slacli/internal/auth"
	"slacli/internal/config"
	"slacli/internal/db"
)

func TestAuthenticationDoctorCheckWithNoExpiry(t *testing.T) {
	check := authenticationDoctorCheck(&auth.Credentials{AccessToken: "test-token"}, nil)
	if check.Status != "ok" {
		t.Fatalf("status = %q, want ok", check.Status)
	}
	if check.Message != "Token expiry is not set" {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestSyncStateDoctorCheckWarnsForStaleFixture(t *testing.T) {
	now := time.Date(2026, time.October, 22, 12, 0, 0, 0, time.UTC)
	storeDir := t.TempDir()
	cfg := &config.Config{StoreDir: storeDir}
	fixture := `{"last_sync":"` + now.Add(-25*time.Hour).Format(time.RFC3339) + `"}`
	if err := os.WriteFile(cfg.SyncStatePath(), []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadSyncState(cfg)
	if err != nil {
		t.Fatal(err)
	}

	check := syncStateDoctorCheck(state, nil, now)
	if check.Status != "warning" {
		t.Fatalf("status = %q, want warning", check.Status)
	}
	if !strings.Contains(check.Message, "older than 24 hours") {
		t.Fatalf("message = %q, want stale-sync warning", check.Message)
	}
}

func TestSyncStateDoctorCheckAcceptsFreshSync(t *testing.T) {
	now := time.Date(2026, time.October, 22, 12, 0, 0, 0, time.UTC)
	check := syncStateDoctorCheck(&db.SyncState{
		LastSync: now.Add(-23 * time.Hour).Format(time.RFC3339),
	}, nil, now)
	if check.Status != "ok" {
		t.Fatalf("status = %q, want ok", check.Status)
	}
}

func TestPermissionDoctorCheckRepairsKnownStoreModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not supported on Windows")
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(storeDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	check := permissionDoctorCheck(&config.Config{StoreDir: storeDir})
	if check.Status != "ok" {
		t.Fatalf("status = %q, message = %q", check.Status, check.Message)
	}
	if !strings.Contains(check.Message, "Repaired permissions") {
		t.Fatalf("message = %q, want repair result", check.Message)
	}
	assertDoctorMode(t, storeDir, 0700)
	assertDoctorMode(t, configPath, 0600)
}

func assertDoctorMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}
