package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"wxview/internal/key"
	"wxview/internal/sqlitedb/sqlitetest"
)

func TestResolveUsesSelfContactWhenPresent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache", "wxid_self_suffix", "contact", "contact.db")
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
INSERT INTO contact (id, username, local_type, alias, remark, nick_name, big_head_url)
VALUES (2, 'wxid_self', 3, 'self_alias', '我的备注', '我的昵称', 'https://example.com/head.jpg');
`)
	db.Close()

	info, err := Resolve(ctx, key.TargetDB{
		Account: "wxid_self_suffix",
		DataDir: filepath.Join(dir, "xwechat_files", "wxid_self_suffix", "db_storage"),
	}, cachePath, false)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Account
	if info.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", info.SchemaVersion, SchemaVersion)
	}
	if got.Username != "wxid_self" || got.DisplayName != "我的备注" || got.Alias != "self_alias" || got.NickName != "我的昵称" {
		t.Fatalf("unexpected self identity: %+v", got)
	}
	if got.IdentitySource != "contact_cache" {
		t.Fatalf("identity source = %q, want contact_cache", got.IdentitySource)
	}
	if got.DataDir != "" {
		t.Fatalf("data_dir should be hidden by default, got %q", got.DataDir)
	}
	if !strings.HasPrefix(got.DataDirFingerprint, "sha256:") {
		t.Fatalf("unexpected data dir fingerprint: %q", got.DataDirFingerprint)
	}
	if got.CacheDir != filepath.Join(dir, "cache", "wxid_self_suffix") {
		t.Fatalf("cache dir = %q", got.CacheDir)
	}
}

func TestResolveFallsBackToAccountWhenSelfContactMissing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "cache", "plain_account", "contact", "contact.db")
	info, err := Resolve(ctx, key.TargetDB{
		Account: "plain_account",
		DataDir: filepath.Join(dir, "db_storage"),
	}, cachePath, true)
	if err != nil {
		t.Fatal(err)
	}
	got := info.Account
	if got.Username != "plain_account" || got.DisplayName != "plain_account" {
		t.Fatalf("unexpected fallback identity: %+v", got)
	}
	if got.IdentitySource != "account" {
		t.Fatalf("identity source = %q, want account", got.IdentitySource)
	}
	if got.DataDir == "" {
		t.Fatalf("data_dir should be included with detail paths")
	}
}
