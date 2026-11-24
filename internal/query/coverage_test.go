package query

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	"wxview/internal/messages"
	"wxview/internal/sqlitedb"
)

func coverageRuntime(t *testing.T) (Runtime, key.TargetDB, string, decrypttest.Fixture, *int) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	target := key.TargetFor(key.TargetDB{Account: "synthetic", DataDir: filepath.Join(home, "source")}, "contact/contact.db")
	f := decrypttest.Load(t)
	store := key.Store{Version: 1}
	for _, rel := range []string{"contact/contact.db", "session/session.db", "message/message_0.db"} {
		f.Write(t, filepath.Join(target.DataDir, filepath.FromSlash(rel)), 0)
		store.Upsert(key.Entry{Account: target.Account, DataDir: target.DataDir, DBRelPath: rel, Key: f.Key, Salt: f.Salt})
	}
	path, err := key.KeyStorePath(target.Account)
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"contact/contact.db", "session/session.db", "message/message_0.db"} {
		if _, _, err = key.EnsureDBCache(context.Background(), key.TargetFor(target, rel)); err != nil {
			t.Fatal(err)
		}
	}
	cache, _ := key.ReadOnlyCachePath(target.Account, "message/message_0.db")
	calls := new(int)
	rt := Runtime{DiscoverContact: func() (key.TargetDB, error) { *calls++; return target, nil }, Now: func() time.Time { return time.Unix(400, 0) }}
	return rt, target, cache, f, calls
}
func requireCoverage(t *testing.T, coverage *DataCoverage, state string) {
	t.Helper()
	if coverage == nil || coverage.State != state {
		t.Fatalf("coverage=%+v, want %s", coverage, state)
	}
}

func TestQueryCoverageTracksWALAndActualDependencies(t *testing.T) {
	rt, target, _, f, calls := coverageRuntime(t)
	ctx := context.Background()
	// An unrelated optional DB must not degrade this query.
	if err := os.MkdirAll(filepath.Join(target.DataDir, "favorite"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target.DataDir, "favorite/favorite.db"), []byte("unused"), 0600); err != nil {
		t.Fatal(err)
	}
	req := MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}}
	first, err := rt.Messages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, first.Coverage, "fresh")
	if *calls != 1 {
		t.Fatalf("account probes=%d", *calls)
	}
	if len(first.Coverage.Sources) != 2 {
		t.Fatalf("unexpected dependencies: %+v", first.Coverage.Sources)
	}
	empty, err := rt.Messages(ctx, MessagesRequest{Username: "alice", StartText: "500", CacheMode: req.CacheMode})
	if err != nil || len(empty.Items) != 0 {
		t.Fatal("normal empty query failed", err)
	}
	requireCoverage(t, empty.Coverage, "fresh")
	if err = os.WriteFile(filepath.Join(target.DataDir, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	stale, err := rt.Messages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, stale.Coverage, "stale")
	if len(stale.Items) != 1 {
		t.Fatal("read initialized/refreshed the cache")
	}
	// All message query use cases carry the same source semantics.
	search, err := rt.Search(ctx, SearchRequest{Username: "alice", Query: "baseline", Limit: 10, CacheMode: req.CacheMode})
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, search.Coverage, "stale")
	timeline, err := rt.Timeline(ctx, TimelineRequest{Username: "alice", StartText: "0", EndText: "500", Limit: 10, CacheMode: req.CacheMode})
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, timeline.Coverage, "stale")
	preview, err := rt.NewMessages(ctx, NewMessagesRequest{Limit: 10, CacheMode: req.CacheMode})
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, preview.Coverage, "stale")
	if _, err = os.Stat(preview.StatePath); !os.IsNotExist(err) {
		t.Fatal("preview advanced checkpoint")
	}
}

func TestQueryCoverageMissingAndConcurrentReplacement(t *testing.T) {
	rt, target, cache, f, _ := coverageRuntime(t)
	ctx := context.Background()
	contact, _ := key.ReadOnlyCachePath(target.Account, "contact/contact.db")
	rt.ContactCachePath = func(string) (string, error) {
		if err := os.WriteFile(filepath.Join(target.DataDir, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := key.EnsureDBCache(ctx, key.TargetFor(target, "message/message_0.db")); err != nil {
			t.Fatal(err)
		}
		return contact, nil
	}
	result, err := rt.Messages(ctx, MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatal("race did not occur after row read")
	}
	requireCoverage(t, result.Coverage, "unknown")
	for _, source := range result.Coverage.Sources {
		if source.DB == "message/message_0.db" && source.CapturedAt != "" {
			t.Fatal("new capture timestamp applied to older query")
		}
	}
	rt.ContactCachePath = nil
	if err = os.Remove(contact); err != nil {
		t.Fatal(err)
	}
	result, err = rt.Messages(ctx, MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, result.Coverage, "partial")
	if err = os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.Messages(ctx, MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}}); err == nil {
		t.Fatal("required cache error was downgraded")
	}
}

func TestQueryCoverageIncludesMediaOnlyWhenUsed(t *testing.T) {
	rt, target, cache, f, _ := coverageRuntime(t)
	ctx := context.Background()
	f.Write(t, filepath.Join(target.DataDir, "message/media_0.db"), 0)
	req := MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}}
	result, err := rt.Messages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	requireCoverage(t, result.Coverage, "fresh")
	for _, source := range result.Coverage.Sources {
		if source.Role == "media" {
			t.Fatal("plain text query included unused media DB")
		}
	}
	db, err := sqlitedb.OpenReadWrite(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, "UPDATE "+messages.TableName("alice")+" SET local_type=34,message_content='<msg><voicemsg voicelength=\"1000\" /></msg>'")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	result, err = rt.Messages(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, source := range result.Coverage.Sources {
		if source.DB == "message/media_0.db" {
			found = true
			if source.State != "missing" || source.Required {
				t.Fatalf("media gap: %+v", source)
			}
		}
	}
	if !found {
		t.Fatal("attempted voice dependency missing from coverage")
	}
}

func TestReadOnlyQueryDoesNotInitializeMissingState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rt := Runtime{DiscoverContact: func() (key.TargetDB, error) {
		return key.TargetFor(key.TargetDB{Account: "synthetic", DataDir: filepath.Join(home, "source")}, "contact/contact.db"), nil
	}}
	if _, err := rt.Contacts(context.Background(), ContactsRequest{CacheMode: CacheMode{ReadOnly: true}}); err == nil {
		t.Fatal("missing required cache was accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".wxview")); !os.IsNotExist(err) {
		t.Fatal("read-only query created local state")
	}
}

func TestQueryCoverageReportsOptionalReadAndPathErrors(t *testing.T) {
	for _, mode := range []string{"unreadable database", "unavailable path"} {
		t.Run(mode, func(t *testing.T) {
			rt, target, _, _, _ := coverageRuntime(t)
			if mode == "unreadable database" {
				path, _ := key.ReadOnlyCachePath(target.Account, "contact/contact.db")
				if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				rt.ContactCachePath = func(string) (string, error) { return "", errors.New("synthetic unavailable cache path") }
			}
			result, err := rt.Messages(context.Background(), MessagesRequest{Username: "alice", CacheMode: CacheMode{ReadOnly: true}})
			if err != nil {
				t.Fatal(err)
			}
			requireCoverage(t, result.Coverage, "unknown")
			found := false
			for _, source := range result.Coverage.Sources {
				if source.DB == "contact/contact.db" {
					found = true
					if source.State != "unknown" {
						t.Fatal(source)
					}
				}
			}
			if !found {
				t.Fatal("known metadata dependency failure was omitted")
			}
		})
	}
}
