package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"slacli/internal/config"
)

func TestCredentialSavesSecureExistingFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not supported on Windows")
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{StoreDir: storeDir}

	credentialsPath := cfg.CredentialsPath()
	if err := os.WriteFile(credentialsPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveCredentials(cfg, &Credentials{AccessToken: "test-token"}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, storeDir, 0700)
	assertMode(t, credentialsPath, 0600)

	xoxcPath := cfg.XoxcCredentialsPath()
	if err := os.WriteFile(xoxcPath, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := SaveXoxcCredentials(cfg, &XoxcCredentials{Token: "test-token", Cookie: "test-cookie"}); err != nil {
		t.Fatal(err)
	}
	assertMode(t, xoxcPath, 0600)
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
