package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wxview/internal/key"
	"wxview/internal/sqlitedb/sqlitetest"
)

func TestValidateLoopbackAddr(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1:38765",
		"localhost:38765",
		"[::1]:38765",
	} {
		if err := ValidateLoopbackAddr(addr); err != nil {
			t.Fatalf("ValidateLoopbackAddr(%q) returned error: %v", addr, err)
		}
	}
	for _, addr := range []string{
		":38765",
		"0.0.0.0:38765",
		"192.168.1.2:38765",
		"127.0.0.1:notaport",
	} {
		if err := ValidateLoopbackAddr(addr); err == nil {
			t.Fatalf("ValidateLoopbackAddr(%q) should reject non-loopback addr", addr)
		}
	}
}

func TestContactsEnvelopeIncludesAccountAndDisplayName(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache", "wxid_me_suffix", "contact", "contact.db")
	db := sqlitetest.CreateDB(t, cachePath, `
CREATE TABLE contact (
  id INTEGER,
  username TEXT,
  local_type INTEGER,
  alias TEXT,
  remark TEXT,
  nick_name TEXT,
  big_head_url TEXT
);
INSERT INTO contact (id, username, local_type, alias, remark, nick_name, big_head_url) VALUES
  (2, 'wxid_me', 3, 'me_alias', '我自己', 'MeNick', ''),
  (10, 'ai_alpha@chatroom', 0, '', 'AI Alpha', '', 'https://example.com/a.jpg'),
  (11, 'ai_beta@chatroom', 0, '', '', 'AI Beta', 'https://example.com/b.jpg'),
  (12, 'wxid_friend', 3, 'friend_alias', 'AI Friend', '', '');
`)
	db.Close()

	handler := NewHandler(Options{
		Addr:       "127.0.0.1:38765",
		Version:    "test-version",
		InstanceID: "test-server",
		StartedAt:  time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
		DiscoverContact: func() (key.TargetDB, error) {
			return key.TargetDB{
				Account: "wxid_me_suffix",
				DataDir: filepath.Join(dir, "xwechat_files", "wxid_me_suffix", "db_storage"),
			}, nil
		},
		ContactCachePath: func(account string) (string, error) {
			return cachePath, nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/contacts?kind=chatroom&query=AI&limit=1&offset=0&sort=name", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got ContactsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Meta.SchemaVersion != 1 || got.Meta.ServerID != "test-server" {
		t.Fatalf("unexpected meta identity: %+v", got.Meta)
	}
	if got.Meta.Account.Username != "wxid_me" || got.Meta.Account.DisplayName != "我自己" {
		t.Fatalf("unexpected account meta: %+v", got.Meta.Account)
	}
	if got.Meta.Total != 2 || got.Meta.Returned != 1 || !got.Meta.HasMore {
		t.Fatalf("unexpected pagination meta: %+v", got.Meta)
	}
	if got.Meta.NextURL == "" || !strings.Contains(got.Meta.NextURL, "offset=1") || !strings.Contains(got.Meta.NextURL, "limit=1") {
		t.Fatalf("unexpected next_url: %q", got.Meta.NextURL)
	}
	if len(got.Items) != 1 || got.Items[0].Username != "ai_alpha@chatroom" || got.Items[0].DisplayName != "AI Alpha" {
		t.Fatalf("unexpected contact items: %+v", got.Items)
	}
}
