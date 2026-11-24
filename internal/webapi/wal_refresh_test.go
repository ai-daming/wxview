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
			Meta struct {
				Coverage *query.DataCoverage `json:"data_coverage"`
			} `json:"meta"`
			Items []messages.Message `json:"items"`
		}
		if err = json.Unmarshal(r.Body.Bytes(), &response); err != nil || len(response.Items) != want {
			t.Fatal("HTTP query has unexpected message count")
		}
		wantState := "stale"
		if want == 2 {
			wantState = "fresh"
		}
		if response.Meta.Coverage == nil || response.Meta.Coverage.State != wantState {
			t.Fatalf("coverage=%+v, want %s", response.Meta.Coverage, wantState)
		}
	}
	if err = os.WriteFile(filepath.Join(root, "message/message_0.db-wal"), f.WAL1, 0600); err != nil {
		t.Fatal(err)
	}
	check(1) // GET refresh=true must remain ignored.
	request(http.MethodPost, "/v1/cache/refresh?scope=messages")
	check(2)
	const searchPath = "/v1/search?username=alice&query=AL-FIR&limit=10"
	const timelinePath = "/v1/timeline?username=alice&start=0&end=500&limit=10"
	for _, path := range []string{searchPath, timelinePath, "/v1/new-messages", "/v1/contacts?limit=10", "/v1/sessions?limit=10"} {
		response := request(http.MethodGet, path)
		var decoded struct {
			Meta struct {
				Coverage  *query.DataCoverage `json:"data_coverage"`
				QueryMode string              `json:"query_mode"`
				IndexUsed *bool               `json:"index_used"`
			} `json:"meta"`
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Meta.Coverage == nil || decoded.Meta.Coverage.State != "fresh" {
			t.Fatalf("%s coverage=%+v", path, decoded.Meta.Coverage)
		}
		if path == searchPath || path == timelinePath {
			if decoded.Meta.QueryMode != "scan" || decoded.Meta.IndexUsed == nil || *decoded.Meta.IndexUsed {
				t.Fatalf("%s did not use a direct cache scan", path)
			}
			want := 1
			if path == timelinePath {
				want = 2
			}
			if len(decoded.Items) != want {
				t.Fatalf("%s returned %d items, want %d", path, len(decoded.Items), want)
			}
			var last messages.Message
			if err := json.Unmarshal(decoded.Items[want-1], &last); err != nil || last.Content != "wal-first" {
				t.Fatalf("%s missed the committed WAL message", path)
			}
		}
	}
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
	// Missing provenance is unknown and GET must not repair it implicitly.
	metadata, _ := key.ReadOnlyCachePath("synthetic", "mtime.json")
	if err = os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	response := request(http.MethodGet, "/v1/messages?username=alice&limit=10&refresh=true")
	var decoded struct {
		Meta struct {
			Coverage *query.DataCoverage `json:"data_coverage"`
		} `json:"meta"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Meta.Coverage == nil || decoded.Meta.Coverage.State != "unknown" {
		t.Fatal("missing provenance was presented as fresh")
	}
	if _, err = os.Stat(metadata); !os.IsNotExist(err) {
		t.Fatal("GET recreated refresh metadata")
	}
	contact, _ := key.ReadOnlyCachePath("synthetic", "contact/contact.db")
	if err = os.Remove(contact); err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodGet, "/v1/messages?username=alice&limit=10")
	if err = json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Meta.Coverage.State != "partial" {
		t.Fatal("missing optional contact cache was not reported")
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/v1/contacts?limit=10", nil))
	if missing.Code < 400 {
		t.Fatal("missing required contact cache was downgraded to success")
	}

}
