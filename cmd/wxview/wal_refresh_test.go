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
	for _, rel := range []string{"contact/contact.db", "message/message_0.db", "session/session.db"} {
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
	query := func(refresh bool, want int) {
		args := []string{"messages", "--username", "alice", "--format", "json", "--limit", "10"}
		if refresh {
			args = append(args, "--refresh")
		}
		var out, stderr bytes.Buffer
		if err := run(args, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Items []messages.Message `json:"items"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Items) != want {
			t.Fatalf("CLI returned %d rows, want %d", len(envelope.Items), want)
		}
	}
	query(true, 1)
	if err = os.WriteFile(filepath.Join(root, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	query(false, 1)
	query(true, 2)
}
