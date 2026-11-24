package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	"wxview/internal/messages"
)

func TestMessagesCLIRefreshReadsWALOnlyMessage(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("account discovery supports macOS/Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	root := filepath.Join(home, "Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/synthetic/db_storage")
	t.Setenv("WXVIEW_WECHAT_DB_STORAGE", root)
	f := decrypttest.Load(t)
	store := key.Store{Version: 1}
	const fts = "message/message_fts.db"
	for _, rel := range []string{"contact/contact.db", "message/message_0.db", "session/session.db", fts} {
		f.Write(t, filepath.Join(root, filepath.FromSlash(rel)), 0)
		store.Upsert(key.Entry{Account: "synthetic", DataDir: root, DBRelPath: rel, Key: f.Key, Salt: f.Salt})
	}
	path, err := key.KeyStorePath("synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	readItems := func(args ...string) []messages.Message {
		t.Helper()
		var out, stderr bytes.Buffer
		if err := run(args, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Meta struct {
				QueryMode string `json:"query_mode"`
				IndexUsed *bool  `json:"index_used"`
			} `json:"meta"`
			Items []messages.Message `json:"items"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Meta.QueryMode != "scan" || envelope.Meta.IndexUsed == nil || *envelope.Meta.IndexUsed {
			t.Fatal("expected a direct cache scan")
		}
		return envelope.Items
	}
	query := func(refresh bool, want int) {
		t.Helper()
		args := []string{"messages", "--username", "alice", "--format", "json", "--limit", "10"}
		if refresh {
			args = append(args, "--refresh")
		}
		if items := readItems(args...); len(items) != want {
			t.Fatalf("CLI returned %d rows, want %d", len(items), want)
		}
	}
	query(true, 1)
	if err = os.WriteFile(filepath.Join(root, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	query(false, 1)
	query(true, 2)
	items := readItems("timeline", "--username", "alice", "--start", "0", "--end", "500", "--limit", "10", "--format", "json")
	if len(items) != 2 || items[1].Content != "wal-first" {
		t.Fatal("timeline did not return the committed WAL message")
	}
	items = readItems("search", "--username", "alice", "--query", "AL-FIR", "--format", "json", "--refresh")
	if len(items) != 1 || items[0].Content != "wal-first" {
		t.Fatal("search did not return the committed WAL message by substring")
	}
	cache, err := key.ReadOnlyCachePath("synthetic", fts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("message refresh must skip WeChat FTS cache, stat error: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(root, fts))
	if err != nil || !bytes.Equal(source, f.Main) {
		t.Fatal("message refresh changed the WeChat FTS source")
	}
}
