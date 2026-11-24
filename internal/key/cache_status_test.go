package key

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wxview/internal/decrypt"
)

func TestCacheStatusesReportsFreshCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	target, sourceBytes := writeCacheStatusSource(t, home, "message/message_0.db")
	cachePath, err := CachePath(target.Account, target.DBRelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	sig, err := statSourceSignature(target.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateCacheMetadata(target, cachePath, hex.EncodeToString(sourceBytes[:decrypt.SaltSize]), sig, testCacheRevision(t, cachePath)); err != nil {
		t.Fatal(err)
	}

	items, err := cacheStatusesForTargets([]TargetDB{target})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one item, got %d", len(items))
	}
	item := items[0]
	if item.Status != CacheStatusFresh {
		t.Fatalf("expected fresh cache, got %#v", item)
	}
	if item.KeyStatus != KeyStatusMissing {
		t.Fatalf("expected missing key status, got %#v", item)
	}
	if item.Group != CacheGroupMessages {
		t.Fatalf("expected messages group, got %#v", item)
	}
}

func TestCacheStatusesReportsStaleMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	target, sourceBytes := writeCacheStatusSource(t, home, "message/message_0.db")
	cachePath, err := CachePath(target.Account, target.DBRelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	sig, err := statSourceSignature(target.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateCacheMetadata(target, cachePath, hex.EncodeToString(sourceBytes[:decrypt.SaltSize]), sig, testCacheRevision(t, cachePath)); err != nil {
		t.Fatal(err)
	}
	changed := time.Unix(1700000123, 0)
	if err := os.Chtimes(target.DBPath, changed, changed); err != nil {
		t.Fatal(err)
	}

	items, err := cacheStatusesForTargets([]TargetDB{target})
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	if item.Status != CacheStatusStale {
		t.Fatalf("expected stale cache, got %#v", item)
	}
	if !strings.Contains(item.Reason, "source_mtime_changed") {
		t.Fatalf("expected source_mtime_changed reason, got %#v", item)
	}
}

func TestCacheStatusesReportsMissingCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	target, _ := writeCacheStatusSource(t, home, "session/session.db")
	items, err := cacheStatusesForTargets([]TargetDB{target})
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	if item.Status != CacheStatusMissingCache {
		t.Fatalf("expected missing cache, got %#v", item)
	}
	if item.Group != CacheGroupSessions {
		t.Fatalf("expected sessions group, got %#v", item)
	}
}

func TestCacheStatusesDoesNotCreateLocalState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	target, _ := writeCacheStatusSource(t, home, "contact/contact.db")
	if _, err := cacheStatusesForTargets([]TargetDB{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".wxview")); !os.IsNotExist(err) {
		t.Fatalf("cache status should not create ~/.wxview, stat err=%v", err)
	}
}

func TestNormalizeCacheStatusGroupAliases(t *testing.T) {
	for input, want := range map[string]string{
		"":           CacheGroupAll,
		"message":    CacheGroupMessages,
		"head_image": CacheGroupAvatars,
		"favorite":   CacheGroupFavorites,
		"moments":    CacheGroupSNS,
	} {
		got, err := NormalizeCacheStatusGroup(input)
		if err != nil {
			t.Fatalf("NormalizeCacheStatusGroup(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeCacheStatusGroup(%q) = %q, want %q", input, got, want)
		}
	}
}

func writeCacheStatusSource(t *testing.T, home string, relPath string) (TargetDB, []byte) {
	t.Helper()
	dataDir := filepath.Join(home, "xwechat_files", "wxid_a", "db_storage")
	dbPath := filepath.Join(dataDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := bytes.Repeat([]byte{0x41}, decrypt.PageSize)
	for i := 0; i < decrypt.SaltSize; i++ {
		source[i] = byte(i + 1)
	}
	if err := os.WriteFile(dbPath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(1700000000, 123)
	if err := os.Chtimes(dbPath, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return TargetDB{
		Account:   "wxid_a",
		DataDir:   dataDir,
		DBRelPath: filepath.ToSlash(relPath),
		DBPath:    dbPath,
	}, source
}
