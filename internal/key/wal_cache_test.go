package key

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/messages"
)

func TestWALOnlyRefreshPublishesCommittedMessagesAndPreservesFailedCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	fixture := decrypttest.Load(t)
	root := filepath.Join(home, "synthetic", "db_storage")
	source := filepath.Join(root, "message", "message_0.db")
	fixture.Write(t, source, 0)
	target := TargetDB{Account: "synthetic", DataDir: root, DBPath: source, DBRelPath: "message/message_0.db"}
	store := Store{Version: 1}
	store.Upsert(Entry{Account: target.Account, DataDir: root, DBRelPath: target.DBRelPath, Key: fixture.Key, Salt: fixture.Salt})
	storePath, err := KeyStorePath(target.Account)
	if err != nil {
		t.Fatal(err)
	}
	if err = SaveStore(storePath, store); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, cache, err := EnsureDBCache(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int) {
		items, err := messages.NewService([]string{cache}).List(ctx, messages.QueryOptions{Username: "alice"})
		if err != nil || len(items) != want {
			t.Fatalf("message count: got %d, want %d, err %v", len(items), want, err)
		}
	}
	check(1)
	mainInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	for i, wal := range [][]byte{fixture.WAL1, fixture.WAL2} {
		if err = os.WriteFile(source+"-wal", wal, 0600); err != nil {
			t.Fatal(err)
		}
		if fresh, err := isCacheFresh(target, cache, fixture.Salt); err != nil || fresh {
			t.Fatal("WAL-only change was reported fresh")
		}
		if _, _, err = EnsureDBCache(ctx, target); err != nil {
			t.Fatal(err)
		}
		check(i + 2)
		status, err := cacheStatusesForTargets([]TargetDB{target})
		if err != nil || status[0].Status != CacheStatusFresh {
			t.Fatal("published snapshot was not marked fresh")
		}
	}
	before, err := os.ReadFile(cache)
	if err != nil {
		t.Fatal(err)
	}
	metaPath, err := CacheMetaPath(target.Account)
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	broken := append([]byte(nil), fixture.WAL2...)
	broken[100] ^= 1
	if err = os.WriteFile(source+"-wal", broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = EnsureDBCache(ctx, target); err == nil {
		t.Fatal("corrupt WAL refresh succeeded")
	}
	after, _ := os.ReadFile(cache)
	afterMeta, _ := os.ReadFile(metaPath)
	if !bytes.Equal(before, after) || !bytes.Equal(beforeMeta, afterMeta) {
		t.Fatal("failed WAL refresh replaced cache or metadata")
	}
	check(3)
	mainAfter, err := os.Stat(source)
	if err != nil || mainAfter.ModTime() != mainInfo.ModTime() {
		t.Fatal("WAL-only test changed the main source")
	}
}
