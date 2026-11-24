package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wxview/internal/decrypt/decrypttest"
	"wxview/internal/key"
	"wxview/internal/messages"
	"wxview/internal/query"
)

func TestWebAPIWALRefreshAndReadOnlyPreview(t *testing.T) {
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
		source := filepath.Join(root, filepath.FromSlash(rel))
		f.Write(t, source, 0)
		store.Upsert(key.Entry{Account: "synthetic", DataDir: root, DBRelPath: rel, Key: f.Key, Salt: f.Salt})
	}
	path, err := key.KeyStorePath("synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if err = key.SaveStore(path, store); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"contact/contact.db", "message/message_0.db", "session/session.db"} {
		if _, _, err = key.EnsureDBCache(context.Background(), key.TargetDB{Account: "synthetic", DataDir: root, DBRelPath: rel, DBPath: filepath.Join(root, filepath.FromSlash(rel))}); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(Options{Version: "synthetic", Addr: "127.0.0.1:38765"})
	request := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, path, nil))
		if r.Code != http.StatusOK {
			t.Fatalf("HTTP %d for %s: %s", r.Code, path, r.Body.String())
		}
		return r
	}
	check := func(want int) {
		r := request(http.MethodGet, "/v1/messages?username=alice&limit=10&refresh=true")
		var response struct {
			Items []messages.Message `json:"items"`
		}
		if err = json.Unmarshal(r.Body.Bytes(), &response); err != nil || len(response.Items) != want {
			t.Fatal("HTTP query has unexpected message count")
		}
	}
	if err = os.WriteFile(filepath.Join(root, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	check(1) // GET refresh=true must remain ignored.
	request(http.MethodPost, "/v1/cache/refresh?scope=messages")
	check(2)
	state, err := (query.Runtime{}).NewMessagesStatePath("synthetic", true)
	if err != nil {
		t.Fatal(err)
	}
	before, readErr := os.ReadFile(state)
	request(http.MethodGet, "/v1/new-messages")
	request(http.MethodGet, "/v1/new-messages")
	after, afterErr := os.ReadFile(state)
	if !bytes.Equal(before, after) || (os.IsNotExist(readErr) != os.IsNotExist(afterErr)) {
		t.Fatal("GET preview advanced incremental state")
	}
}
