package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wxview/internal/buildinfo"
	"wxview/internal/contacts"
	"wxview/internal/messages"
	wxquery "wxview/internal/query"
	"wxview/internal/sessions"
)

func TestWriteContactsJSON(t *testing.T) {
	list := []contacts.Contact{{
		Username: "u1",
		Alias:    "a1",
		Remark:   "r1",
		NickName: "n1",
		HeadURL:  "https://example.com/1",
		Kind:     contacts.KindFriend,
	}}
	var out bytes.Buffer
	if err := writeContacts(&out, list, "json"); err != nil {
		t.Fatal(err)
	}
	var got []contacts.Contact
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].HeadURL != "https://example.com/1" || got[0].Kind != contacts.KindFriend {
		t.Fatalf("unexpected json output: %+v", got)
	}
}

func TestWriteContactsCSV(t *testing.T) {
	list := []contacts.Contact{{
		Username: "u1",
		Alias:    "alias,one",
		Remark:   "remark \"quoted\"",
		NickName: "nick\nname",
		HeadURL:  "https://example.com/1",
		Kind:     contacts.KindFriend,
	}}
	var out bytes.Buffer
	if err := writeContacts(&out, list, "csv"); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d csv rows, want 2: %q", len(rows), out.String())
	}
	wantHeader := []string{"username", "alias", "remark", "nick_name", "head_url", "kind"}
	for i, want := range wantHeader {
		if rows[0][i] != want {
			t.Fatalf("header[%d] = %q, want %q", i, rows[0][i], want)
		}
	}
	if rows[1][1] != "alias,one" || rows[1][2] != `remark "quoted"` || rows[1][3] != "nick\nname" {
		t.Fatalf("csv row was not round-tripped correctly: %#v", rows[1])
	}
}

func TestWriteContactsJSONL(t *testing.T) {
	list := []contacts.Contact{
		{Username: "u1", NickName: "u1", Kind: contacts.KindFriend},
		{Username: "u2", NickName: "u2", Kind: contacts.KindChatroom},
	}
	var out bytes.Buffer
	if err := writeContacts(&out, list, "jsonl"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d jsonl lines, want 2: %q", len(lines), out.String())
	}
	for _, line := range lines {
		var c contacts.Contact
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("invalid jsonl line %q: %v", line, err)
		}
	}
}

func TestWriteMessagesJSONL(t *testing.T) {
	list := []messages.Message{
		{ID: "wechat:msg:message_0.db:Msg_x:1", ChatUsername: "alice", FromUsername: "self", Direction: "out", IsSelf: true, Seq: 1001, CreateTime: 1700000000, Time: "2023-11-14 22:13:20", Type: 1, Content: "hello <tag> & link", ContentDetail: map[string]string{"type": "text", "text": "hello <tag> & link"}, ContentEncoding: "text", LocalID: 1, RawType: 1, Status: 2, SourceDB: "message_0.db", TableName: "Msg_x"},
		{ID: "wechat:msg:message_1.db:Msg_x:2", ChatUsername: "alice", FromUsername: "alice", Direction: "in", Seq: 1002, CreateTime: 1700000001, Time: "2023-11-14 22:13:21", Type: 1, Content: "world", ContentDetail: map[string]string{"type": "text", "text": "world"}, ContentEncoding: "text", LocalID: 2, RawType: 1, Status: 4, SourceDB: "message_1.db", TableName: "Msg_x"},
	}
	var out bytes.Buffer
	if err := writeMessages(&out, list, "jsonl", false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d jsonl lines, want 2: %q", len(lines), out.String())
	}
	var got messages.Message
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("invalid jsonl line %q: %v", lines[0], err)
	}
	if got.Content != "hello <tag> & link" || got.ChatUsername != "alice" || got.FromUsername != "self" {
		t.Fatalf("unexpected message jsonl: %+v", got)
	}
	if strings.Contains(lines[0], `\u003c`) || strings.Contains(lines[0], `\u0026`) {
		t.Fatalf("message jsonl should not HTML-escape content: %s", lines[0])
	}
	if strings.Contains(lines[0], `"source"`) {
		t.Fatalf("source should be omitted by default: %s", lines[0])
	}
}

func TestWriteSessionsJSON(t *testing.T) {
	list := []sessions.Session{{
		Username:        "room@chatroom",
		ChatKind:        contacts.KindChatroom,
		ChatDisplayName: "项目群",
		UnreadCount:     2,
		Summary:         "hello",
		LastTimestamp:   1700000000,
		Time:            "2023-11-14 22:13:20",
		LastMsgType:     1,
	}}
	var out bytes.Buffer
	if err := writeSessions(&out, list, "json"); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Count int                `json:"count"`
		Items []sessions.Session `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 1 || len(got.Items) != 1 || got.Items[0].ChatDisplayName != "项目群" || got.Items[0].UnreadCount != 2 {
		t.Fatalf("unexpected sessions json: %+v", got)
	}
}

func TestWriteMessageEnvelopeJSON(t *testing.T) {
	list := []messages.Message{{
		ID:              "wechat:msg:message_0.db:Msg_x:1",
		ChatUsername:    "alice",
		ChatKind:        messages.ChatKindUnknown,
		ChatDisplayName: "alice",
		FromUsername:    "self",
		Direction:       "out",
		IsSelf:          true,
		Seq:             1001,
		CreateTime:      1700000000,
		Time:            "2023-11-14 22:13:20",
		Type:            1,
		Content:         "hello <tag> & link",
		ContentDetail:   map[string]string{"type": "text", "text": "hello <tag> & link"},
		ContentEncoding: "text",
	}}
	var out bytes.Buffer
	meta := messagesMeta{
		SchemaVersion: messageEnvelopeSchemaVersion,
		Timezone:      "Asia/Shanghai",
		Mode:          "messages",
		Username:      "alice",
		Limit:         1,
		Returned:      1,
		HasMore:       false,
	}
	if err := writeMessageEnvelope(&out, meta, list); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `\u003c`) || strings.Contains(out.String(), `\u0026`) {
		t.Fatalf("message envelope should not HTML-escape content: %s", out.String())
	}
	var got struct {
		Meta  messagesMeta       `json:"meta"`
		Items []messages.Message `json:"items"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Meta.SchemaVersion != 1 || got.Meta.Timezone != "Asia/Shanghai" || got.Meta.Mode != "messages" || len(got.Items) != 1 || got.Items[0].Content != "hello <tag> & link" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
}

func TestTimingTraceWritesDurations(t *testing.T) {
	var out bytes.Buffer
	trace := newTimingTrace(true, &out, "messages")
	trace.Mark("query_rows", "rows=2", "query_mode=index")
	text := out.String()
	for _, want := range []string{
		"trace_time command=messages phase=query_rows",
		"duration_ms=",
		"total_ms=",
		"rows=2",
		"query_mode=index",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("trace output missing %q:\n%s", want, text)
		}
	}
}

func TestLocalTimezoneNamePrefersTZ(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	if got := wxquery.LocalTimezoneName(); got != "Asia/Shanghai" {
		t.Fatalf("timezone = %q, want Asia/Shanghai", got)
	}
}

func TestVersionCommandsPrintVersionOnly(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"version"}} {
		var out bytes.Buffer
		if err := run(args, &out, &out); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if got, want := strings.TrimSpace(out.String()), buildinfo.String(); got != want {
			t.Fatalf("run(%v) = %q, want %q", args, got, want)
		}
		if strings.Contains(out.String(), "wxview - Read local WeChat data") {
			t.Fatalf("run(%v) should not print root help:\n%s", args, out.String())
		}
	}
}

func TestDaemonStatusPrintsVersionWhenStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"daemon", "status"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"daemon socket:",
		"version: " + buildinfo.String(),
		"status: stopped",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daemon status missing %q:\n%s", want, text)
		}
	}
}

func TestIndexStatusUsesLocalCacheWithoutWechatDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	accountDir := filepath.Join(home, ".wxview", "cache", "wxid_local", "contact")
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountDir, "contact.db"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"index", "status"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "status: missing") {
		t.Fatalf("index status should use local cache and report missing index:\n%s", text)
	}
	if !strings.Contains(text, filepath.Join(home, ".wxview", "cache", "wxid_local", "index", "messages.db")) {
		t.Fatalf("index status used unexpected path:\n%s", text)
	}
}

func TestLocalMessageCachePathsReadOnlySortsNumericShards(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	messageDir := filepath.Join(home, ".wxview", "cache", "wxid_local", "message")
	if err := os.MkdirAll(messageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"message_10.db", "message_2.db", "message_0.db", "message_fts.db", "biz_message_1.db"} {
		if err := os.WriteFile(filepath.Join(messageDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := localMessageCachePathsReadOnly("wxid_local")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(paths))
	for _, path := range paths {
		got = append(got, filepath.Base(path))
	}
	want := []string{"message_0.db", "message_2.db", "message_10.db"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("message cache paths = %#v, want %#v", got, want)
	}
}

func TestWriteMessagesCSV(t *testing.T) {
	list := []messages.Message{{
		ID:              "wechat:msg:message_0.db:Msg_x:1",
		ChatUsername:    "alice",
		FromUsername:    "self",
		Direction:       "out",
		IsSelf:          true,
		Seq:             1001,
		CreateTime:      1700000000,
		Time:            "2023-11-14 22:13:20",
		Type:            1,
		LocalID:         1,
		RawType:         1,
		Status:          2,
		Content:         "hello, \"quoted\"\nline",
		ContentDetail:   map[string]string{"type": "text", "text": "hello, \"quoted\"\nline"},
		ContentEncoding: "text",
		SourceDB:        "message_0.db",
		TableName:       "Msg_x",
	}}
	var out bytes.Buffer
	if err := writeMessages(&out, list, "csv", true); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d csv rows, want 2: %q", len(rows), out.String())
	}
	if rows[0][0] != "id" || rows[0][17] != "content" || rows[0][18] != "content_detail" || rows[0][20] != "source_db" {
		t.Fatalf("unexpected message csv header: %#v", rows[0])
	}
	if rows[1][17] != "hello, \"quoted\"\nline" || rows[1][18] != "hello, \"quoted\"\nline" || rows[1][20] != "message_0.db" {
		t.Fatalf("message content was not round-tripped correctly: %#v", rows[1])
	}
}

func TestWriteCount(t *testing.T) {
	var out bytes.Buffer
	if err := writeCount(&out, 12, "json"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != `{"count":12}` {
		t.Fatalf("unexpected json count: %q", out.String())
	}

	out.Reset()
	if err := writeCount(&out, 12, "csv"); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "count" || rows[1][0] != "12" {
		t.Fatalf("unexpected csv count: %#v", rows)
	}

	out.Reset()
	if err := writeCount(&out, 12, "table"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "12\n" {
		t.Fatalf("unexpected table count: %q", out.String())
	}
}

func TestParseTimeBoundDateEndIncludesFullDay(t *testing.T) {
	got, err := wxquery.ParseMessageRange("", "2026-05-14", "2026-05-14", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasStart || !got.HasEnd {
		t.Fatal("expected parsed bounds")
	}
	if got.End-got.Start != 24*60*60-1 {
		t.Fatalf("date-only end should include full day; start=%d end=%d", got.Start, got.End)
	}
}

func TestParseMessageRangeDate(t *testing.T) {
	got, err := wxquery.ParseMessageRange("2026-05-14", "", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasStart || !got.HasEnd {
		t.Fatal("expected date to provide both bounds")
	}
	if got.End-got.Start != 24*60*60-1 {
		t.Fatalf("date should expand to one full day; start=%d end=%d", got.Start, got.End)
	}
}

func TestParseMessageRangeRejectsDateWithExplicitBounds(t *testing.T) {
	_, err := wxquery.ParseMessageRange("today", "2026-05-14", "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "date cannot be combined") {
		t.Fatalf("expected date/start conflict error, got %v", err)
	}
}
