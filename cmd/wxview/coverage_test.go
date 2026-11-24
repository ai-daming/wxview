package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	wxquery "wxview/internal/query"
)

func TestQueryCoverageCLIEnvelopesAndItemFormats(t *testing.T) {
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
	for _, rel := range []string{"contact/contact.db", "session/session.db", "message/message_0.db"} {
		f.Write(t, filepath.Join(root, filepath.FromSlash(rel)), 0)
		store.Upsert(key.Entry{Account: "synthetic", DataDir: root, DBRelPath: rel, Key: f.Key, Salt: f.Salt})
	}
	path, _ := key.KeyStorePath("synthetic")
	if err := key.SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"contact/contact.db", "session/session.db", "message/message_0.db"} {
		if _, _, err := key.EnsureDBCache(context.Background(), key.TargetFor(key.TargetDB{Account: "synthetic", DataDir: root}, rel)); err != nil {
			t.Fatal(err)
		}
	}
	commands := [][]string{{"messages", "--username", "alice", "--limit", "10"}, {"search", "--username", "alice", "--query", "baseline", "--limit", "10"}, {"timeline", "--username", "alice", "--start", "0", "--end", "500", "--limit", "10"}, {"new-messages", "--limit", "10"}}
	for _, args := range commands {
		t.Run(args[0], func(t *testing.T) {
			var out, stderr bytes.Buffer
			if err := run(append(append([]string{}, args...), "--format", "json"), &out, &stderr); err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Meta struct {
					Coverage *wxquery.DataCoverage `json:"data_coverage"`
				} `json:"meta"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Meta.Coverage == nil || envelope.Meta.Coverage.State != "fresh" {
				t.Fatalf("coverage: %+v", envelope.Meta.Coverage)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"jsonl", "csv", "table"} {
		var out, stderr bytes.Buffer
		if err := run([]string{"messages", "--username", "alice", "--limit", "10", "--format", format}, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(out.Bytes(), []byte("data_coverage")) {
			t.Fatal("item-only format gained envelope metadata")
		}
	}
	var out, stderr bytes.Buffer
	if err := run([]string{"messages", "--username", "alice", "--limit", "10", "--format", "json"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Meta struct {
			Coverage *wxquery.DataCoverage `json:"data_coverage"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta.Coverage.State != "stale" {
		t.Fatal("CLI masked WAL-only source change")
	}
	out.Reset()
	stderr.Reset()
	if err := run([]string{"contacts", "--username", "alice", "--format", "json", "--limit", "1"}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(out.Bytes())) == 0 || bytes.TrimSpace(out.Bytes())[0] != '[' {
		t.Fatal("legacy contacts JSON gained an envelope")
	}

}
