package query

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"wxview/internal/key"
	"wxview/internal/messages"
)

func TestMessageCursorIdentityAndCorruption(t *testing.T) {
	id := messagePageIdentity{Account: "synthetic", DataDir: "/synthetic", Username: "alice", Order: "desc", Start: 10, End: 20, HasStart: true, HasEnd: true}
	key := messages.PageKey{CreateTime: 15, Seq: 3, LocalID: 2, SourceDB: "message_0.db"}
	token := encodeMessageCursor(id, key)
	got, err := decodeMessageCursor(token, id)
	if err != nil || *got != key {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	for _, field := range []string{"account", "directory", "username", "order", "start", "end", "has_start", "has_end"} {
		changed := id
		switch field {
		case "account":
			changed.Account = "other"
		case "directory":
			changed.DataDir = "/other"
		case "username":
			changed.Username = "bob"
		case "order":
			changed.Order = "asc"
		case "start":
			changed.Start++
		case "end":
			changed.End++
		case "has_start":
			changed.HasStart = false
		case "has_end":
			changed.HasEnd = false
		}
		if _, err := decodeMessageCursor(token, changed); err == nil {
			t.Errorf("accepted changed %s", field)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	raw[0] ^= 1
	for _, bad := range []string{"!", "", token[:len(token)/2], base64.RawURLEncoding.EncodeToString(raw)} {
		if _, err := decodeMessageCursor(bad, id); err == nil {
			t.Error("accepted corrupt cursor")
		}
	}
}

func TestMessageCursorRejectsUnsupportedVersion(t *testing.T) {
	id := messagePageIdentity{Account: "synthetic"}
	raw, _ := json.Marshal(messageCursor{Version: 2, Identity: messageIdentityHash(id), After: messages.PageKey{SourceDB: "message_0.db"}})
	sum := sha256.Sum256(raw)
	if _, err := decodeMessageCursor(base64.RawURLEncoding.EncodeToString(append(raw, sum[:]...)), id); err == nil {
		t.Fatal("accepted unsupported version")
	}
}

func TestInvalidMessagePaginationDoesNotDiscoverAccount(t *testing.T) {
	rt := Runtime{DiscoverContact: func() (key.TargetDB, error) {
		t.Fatal("invalid query discovered account")
		return key.TargetDB{}, fmt.Errorf("unreachable")
	}}
	for _, req := range []MessagesRequest{
		{Username: "alice", Order: "invalid", Limit: 100},
		{Username: "alice", Order: "desc", Limit: 0},
		{Username: "alice", Order: "desc", Limit: 100, Offset: 1},
		{Username: "alice", Order: "desc", Limit: 100, AfterSeq: 1},
		{Username: "alice", Order: "desc", Limit: 100, LegacyPaginationProvided: true},
		{Username: "alice", Cursor: "invalid", Limit: 100},
		{Username: "alice", Order: "desc", Cursor: "invalid", Limit: 100},
		{Username: "alice", Limit: -1},
	} {
		if _, err := rt.Messages(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
}
