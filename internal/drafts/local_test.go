package drafts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocalDraftsUsePrivateStorePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file mode bits are not supported on Windows")
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.Mkdir(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create("C123", "draft text", ""); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]os.FileMode{
		storeDir:                               0700,
		filepath.Join(storeDir, "drafts.json"): 0600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", path, got, want)
		}
	}
}
