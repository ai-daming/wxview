package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	"wxview/internal/messages"
)

func TestWatcherRefreshesWALOnlyCommittedMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, "source", "db_storage")
	source := filepath.Join(root, "message/message_0.db")
	f := decrypttest.Load(t)
	f.Write(t, source, 0)
	account := key.TargetDB{Account: "synthetic", DataDir: root, DBPath: filepath.Join(root, "contact/contact.db"), DBRelPath: "contact/contact.db"}
	store := key.Store{Version: 1}
	store.Upsert(key.Entry{Account: account.Account, DataDir: root, DBRelPath: "message/message_0.db", Key: f.Key, Salt: f.Salt})
	path, err := key.KeyStorePath(account.Account)
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	s := Server{}
	s.discovery.discover = func() (key.TargetDB, error) { return account, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err = s.refreshMessages(ctx); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	pathCalls := 0
	done := make(chan struct{})
	refreshed := make(chan error, 1)
	paths := func() []string {
		p := s.watchPaths("messages")
		pathCalls++
		if pathCalls == 2 {
			close(ready)
		} // Initial signature has already been sampled.
		return p
	}
	go func() {
		defer close(done)
		WatchFiles(ctx, paths, 5*time.Millisecond, 5*time.Millisecond, func() {
			select {
			case refreshed <- s.refreshMessages(ctx):
			default:
			}
		})
	}()
	defer func() { cancel(); <-done }()
	<-ready
	if err = os.WriteFile(source+"-wal", f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-refreshed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WAL-only update did not trigger cache refresh")
	}
	cache, err := key.CachePath(account.Account, "message/message_0.db")
	if err != nil {
		t.Fatal(err)
	}
	items, err := messages.NewService([]string{cache}).List(ctx, messages.QueryOptions{Username: "alice"})
	if err != nil || len(items) != 2 || items[1].Content != "wal-first" {
		t.Fatal("watcher did not publish the committed WAL message")
	}
}
