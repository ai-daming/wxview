package key

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wxview/internal/decrypt"
	"wxview/internal/decrypt/decrypttest"
)

func TestCacheReadObservationTracksWALAndPublication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := decrypttest.Load(t)
	target := TargetDB{Account: "synthetic", DataDir: filepath.Join(home, "source")}
	target = TargetFor(target, "message/message_0.db")
	f.Write(t, target.DBPath, 0)
	store := Store{Version: 1}
	store.Upsert(Entry{Account: target.Account, DataDir: target.DataDir, DBRelPath: target.DBRelPath, Key: f.Key, Salt: f.Salt})
	path, _ := KeyStorePath(target.Account)
	if err := SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	_, cache, err := EnsureDBCache(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewCacheReadObserver()
	before := observer.Observe(target, cache)
	if got := observer.Finish(before); got.State != "fresh" || got.CapturedAt == "" {
		t.Fatalf("fresh capture: %+v", got)
	}
	if err = os.WriteFile(target.DBPath+"-wal", f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "stale" {
		t.Fatalf("WAL change: %+v", got)
	}
	before = observer.Observe(target, cache)
	if _, _, err = EnsureDBCache(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if got := observer.Finish(before); got.State != "unknown" || got.Reason != "cache_changed_during_query" {
		t.Fatalf("replaced cache: %+v", got)
	}
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "fresh" {
		t.Fatalf("new capture: %+v", got)
	}
	// Simulate cache publication before metadata publication: matching source
	// metadata must not bind a different cache version to the query.
	info, _ := os.Stat(cache)
	changed := info.ModTime().Add(time.Second)
	if err = os.Chtimes(cache, changed, changed); err != nil {
		t.Fatal(err)
	}
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "unknown" || got.CapturedAt != "" {
		t.Fatalf("unbound cache: %+v", got)
	}
}

func TestCacheReadObservationUnknownAndMissingDoNotWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	target := TargetFor(TargetDB{Account: "synthetic", DataDir: filepath.Join(home, "source")}, "contact/contact.db")
	cache := filepath.Join(home, "cache.db")
	if err := os.WriteFile(cache, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	observer := NewCacheReadObserver()
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "unknown" {
		t.Fatalf("no metadata/source: %+v", got)
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "missing" {
		t.Fatalf("missing cache: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".wxview")); !os.IsNotExist(err) {
		t.Fatal("observation initialized local state")
	}
}

func testCacheRevision(t *testing.T, path string) decrypt.CacheRevision {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return decrypt.CacheRevision{Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
}

func TestCacheReadObservationLegacyMetadataAndUnavailableSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := decrypttest.Load(t)
	target := TargetFor(TargetDB{Account: "synthetic", DataDir: filepath.Join(home, "source")}, "message/message_0.db")
	f.Write(t, target.DBPath, 0)
	store := Store{Version: 1}
	store.Upsert(Entry{Account: target.Account, DataDir: target.DataDir, DBRelPath: target.DBRelPath, Key: f.Key, Salt: f.Salt})
	path, _ := KeyStorePath(target.Account)
	if err := SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	_, cache, err := EnsureDBCache(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	metaPath, _ := CacheMetaPath(target.Account)
	meta, err := loadCacheMetadata(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	original := meta.Files[target.DBRelPath]
	legacy := original
	legacy.CacheSize = 0
	legacy.CacheMTimeNS = 0
	meta.Files[target.DBRelPath] = legacy
	if err = saveCacheMetadata(metaPath, meta); err != nil {
		t.Fatal(err)
	}
	observer := NewCacheReadObserver()
	if got := observer.Finish(observer.Observe(target, cache)); got.State != "unknown" || got.CapturedAt != "" {
		t.Fatalf("legacy metadata: %+v", got)
	}
	// An explicit refresh upgrades the binding; observation itself did not do so.
	if _, _, err = EnsureDBCache(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	before := observer.Observe(target, cache)
	if err = os.Remove(target.DBPath); err != nil {
		t.Fatal(err)
	}
	if got := observer.Finish(before); got.State != "unknown" || got.Reason != "source_unavailable" || got.CapturedAt == "" {
		t.Fatalf("unavailable source: %+v", got)
	}
}
