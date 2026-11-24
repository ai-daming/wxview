package msgindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	"wxview/internal/messages"
)

func TestWALSnapshotInvalidatesOldIndexUntilRefresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	f := decrypttest.Load(t)
	root := filepath.Join(home, "synthetic", "db_storage")
	source := filepath.Join(root, "message", "message_0.db")
	f.Write(t, source, 0)
	target := key.TargetDB{Account: "synthetic", DataDir: root, DBPath: source, DBRelPath: "message/message_0.db"}
	store := key.Store{Version: 1}
	store.Upsert(key.Entry{Account: target.Account, DataDir: root, DBRelPath: target.DBRelPath, Key: f.Key, Salt: f.Salt})
	storePath, err := key.KeyStorePath(target.Account)
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SaveStore(storePath, store); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, cache, err := key.EnsureDBCache(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(home, "index.db")
	opts := BuildOptions{Account: target.Account, IndexPath: indexPath, MessageCachePaths: []string{cache}, ChatUsernames: []string{"alice"}}
	if _, err = Build(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source+"-wal", f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = key.EnsureDBCache(ctx, target); err != nil {
		t.Fatal(err)
	}
	usable, err := UseForQuery(ctx, indexPath, []string{cache}, UsabilityOptions{Username: "alice"})
	if err != nil || usable.UseIndex {
		t.Fatal("old index remained usable after publishing newer WAL snapshot")
	}
	if _, err = Refresh(ctx, opts); err != nil {
		t.Fatal(err)
	}
	usable, err = UseForQuery(ctx, indexPath, []string{cache}, UsabilityOptions{Username: "alice"})
	if err != nil || !usable.UseIndex {
		t.Fatal("index did not become usable after catching up")
	}
	refs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := messages.NewService([]string{cache}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := messages.NewService([]string{cache}).List(ctx, messages.QueryOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(indexed) != 2 || len(scanned) != 2 || indexed[1].Content != "wal-first" || indexed[1].ID != scanned[1].ID {
		t.Fatal("indexed and direct queries disagree after WAL refresh")
	}
}
