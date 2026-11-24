package sessions

import (
	"context"
	"path/filepath"
	"testing"

	"wxview/internal/contacts"
	"wxview/internal/messages"
	"wxview/internal/sqlitedb/sqlitetest"
)

func TestListSessionTableAndApplyChatInfo(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "session.db")
	sql := `
CREATE TABLE SessionTable(
  username TEXT,
  unread_count INTEGER,
  summary BLOB,
  last_timestamp INTEGER,
  last_msg_type INTEGER,
  last_msg_sender TEXT,
  last_sender_display_name TEXT
);
INSERT INTO SessionTable(username, unread_count, summary, last_timestamp, last_msg_type, last_msg_sender, last_sender_display_name)
VALUES ('room@chatroom', 3, X'777869645F613A0A68656C6C6F', 1700000100, 25769803825, 'wxid_a', '');
INSERT INTO SessionTable(username, unread_count, summary, last_timestamp, last_msg_type, last_msg_sender, last_sender_display_name)
VALUES ('wxid_b', 0, X'6869', 1700000000, 1, '', '');
`
	runSQLite(t, dbPath, sql)

	got, err := NewService(dbPath).List(context.Background(), QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].Username != "room@chatroom" || got[0].Summary != "hello" || got[0].LastMsgType != 49 || got[0].LastMsgSubType != 6 {
		t.Fatalf("unexpected first session: %+v", got[0])
	}

	ApplyChatInfo(got, map[string]messages.ChatInfo{
		"room@chatroom": {Username: "room@chatroom", Kind: contacts.KindChatroom, DisplayName: "项目群"},
		"wxid_a":        {Username: "wxid_a", Kind: contacts.KindFriend, DisplayName: "Alice"},
	})
	filtered := ApplyQueryOptions(got, QueryOptions{Kind: contacts.KindChatroom, Query: "Alice"})
	if len(filtered) != 1 || filtered[0].ChatDisplayName != "项目群" || filtered[0].LastSenderDisplayName != "Alice" {
		t.Fatalf("unexpected filtered session: %+v", filtered)
	}
}

func TestListUnreadSessionAbstract(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "session.db")
	sql := `
CREATE TABLE SessionAbstract(
  m_nsUserName TEXT,
  m_uUnReadCount INTEGER,
  m_uLastTime INTEGER
);
INSERT INTO SessionAbstract(m_nsUserName, m_uUnReadCount, m_uLastTime) VALUES ('wxid_a', 0, 1700000000);
INSERT INTO SessionAbstract(m_nsUserName, m_uUnReadCount, m_uLastTime) VALUES ('wxid_b', 2, 1700000200);
`
	runSQLite(t, dbPath, sql)

	got, err := NewService(dbPath).List(context.Background(), QueryOptions{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Username != "wxid_b" || got[0].UnreadCount != 2 {
		t.Fatalf("unexpected unread sessions: %+v", got)
	}
}

func runSQLite(t *testing.T, path string, sql string) {
	t.Helper()
	db := sqlitetest.CreateDB(t, path, sql)
	db.Close()
}
