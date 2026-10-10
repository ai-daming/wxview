package key

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanTmpRemovesExpiredCacheTmpAndSkipsIndex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("SUDO_USER", "")
	cacheTmp := filepath.Join(home, ".wxview", "cache", "wxid_a", "message", "message_fts.db.123.tmp")
	freshTmp := filepath.Join(home, ".wxview", "cache", "wxid_a", "message", "message_0.db.456.tmp")
	indexTmp := filepath.Join(home, ".wxview", "cache", "wxid_a", "index", ".messages-123.db")
	for _, path := range []string{cacheTmp, freshTmp, indexTmp} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("tmp"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(cacheTmp, old, old); err != nil {
		t.Fatal(err)
	}
	result, err := CleanTmp()
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.Kept != 1 {
		t.Fatalf("clean result = %+v, want one removed and one kept cache tmp", result)
	}
	if _, err := os.Stat(cacheTmp); !os.IsNotExist(err) {
		t.Fatalf("expired cache tmp should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(freshTmp); err != nil {
		t.Fatalf("fresh cache tmp should be kept: %v", err)
	}
	if _, err := os.Stat(indexTmp); err != nil {
		t.Fatalf("index tmp should be skipped by cache clean: %v", err)
	}
}
