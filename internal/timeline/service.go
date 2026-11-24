package timeline

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"wxview/internal/messages"
)

const cursorVersion = 1

type QueryIdentity struct {
	Username string `json:"username,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Query    string `json:"query,omitempty"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
}

type QueryOptions struct {
	Chats         []messages.ChatInfo
	Start         int64
	End           int64
	Limit         int
	Cursor        string
	QueryHash     string
	IncludeSource bool
}

type Result struct {
	Items      []messages.Message
	HasMore    bool
	NextCursor string
}

type cursorPayload struct {
	Version   int     `json:"v"`
	QueryHash string  `json:"query_hash"`
	After     SortKey `json:"after"`
}

type SortKey struct {
	CreateTime   int64  `json:"create_time"`
	Seq          int64  `json:"seq"`
	ChatUsername string `json:"chat_username"`
	LocalID      int64  `json:"local_id"`
	SourceDB     string `json:"source_db,omitempty"`
}

func QueryHash(identity QueryIdentity) (string, error) {
	payload, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func List(ctx context.Context, service messages.Service, opts QueryOptions) (Result, error) {
	if opts.Limit <= 0 {
		return Result{}, fmt.Errorf("limit must be > 0")
	}
	var after SortKey
	if strings.TrimSpace(opts.Cursor) != "" {
		afterKey, err := DecodeCursor(opts.Cursor, opts.QueryHash)
		if err != nil {
			return Result{}, err
		}
		after = afterKey
	}

	var all []messages.Message
	for _, chat := range uniqueChats(opts.Chats) {
		rows, err := service.List(ctx, messages.QueryOptions{
			Username:      chat.Username,
			Start:         opts.Start,
			End:           opts.End,
			HasStart:      true,
			HasEnd:        true,
			IncludeSource: opts.IncludeSource,
		})
		if err != nil {
			return Result{}, err
		}
		for i := range rows {
			messages.ApplyChatInfoToMessage(&rows[i], chat)
		}
		all = append(all, rows...)
	}

	sortMessages(all)
	if strings.TrimSpace(opts.Cursor) != "" {
		all = filterAfter(all, after)
	}

	result := Result{Items: all}
	if len(result.Items) > opts.Limit {
		result.HasMore = true
		result.Items = result.Items[:opts.Limit]
	}
	if result.HasMore && len(result.Items) > 0 {
		next, err := encodeCursor(cursorPayload{
			Version:   cursorVersion,
			QueryHash: opts.QueryHash,
			After:     KeyFor(result.Items[len(result.Items)-1]),
		})
		if err != nil {
			return Result{}, err
		}
		result.NextCursor = next
	}
	return result, nil
}

func uniqueChats(chats []messages.ChatInfo) []messages.ChatInfo {
	seen := map[string]bool{}
	out := make([]messages.ChatInfo, 0, len(chats))
	for _, chat := range chats {
		chat.Username = strings.TrimSpace(chat.Username)
		if chat.Username == "" || seen[chat.Username] {
			continue
		}
		if strings.TrimSpace(chat.DisplayName) == "" {
			chat.DisplayName = chat.Username
		}
		if strings.TrimSpace(chat.Kind) == "" {
			chat.Kind = messages.ChatKindUnknown
		}
		seen[chat.Username] = true
		out = append(out, chat)
	}
	return out
}

func DecodeCursor(token string, queryHash string) (SortKey, error) {
	payload, err := decodeCursor(token)
	if err != nil {
		return SortKey{}, err
	}
	if payload.Version != cursorVersion {
		return SortKey{}, fmt.Errorf("invalid cursor version: restart timeline from the first page")
	}
	if payload.QueryHash != queryHash {
		return SortKey{}, fmt.Errorf("cursor does not match query: restart timeline from the first page")
	}
	return payload.After, nil
}

func filterAfter(list []messages.Message, after SortKey) []messages.Message {
	out := make([]messages.Message, 0, len(list))
	for _, msg := range list {
		if CompareKey(KeyFor(msg), after) > 0 {
			out = append(out, msg)
		}
	}
	return out
}

func sortMessages(list []messages.Message) {
	sort.SliceStable(list, func(i, j int) bool {
		return CompareKey(KeyFor(list[i]), KeyFor(list[j])) < 0
	})
}

func KeyFor(msg messages.Message) SortKey {
	return SortKey{
		CreateTime:   msg.CreateTime,
		Seq:          msg.Seq,
		ChatUsername: msg.ChatUsername,
		LocalID:      msg.LocalID,
		SourceDB:     msg.SourceDB,
	}
}

func CompareKey(left SortKey, right SortKey) int {
	if left.CreateTime != right.CreateTime {
		if left.CreateTime < right.CreateTime {
			return -1
		}
		return 1
	}
	if left.Seq != right.Seq {
		if left.Seq < right.Seq {
			return -1
		}
		return 1
	}
	if left.ChatUsername != right.ChatUsername {
		if left.ChatUsername < right.ChatUsername {
			return -1
		}
		return 1
	}
	if left.LocalID != right.LocalID {
		if left.LocalID < right.LocalID {
			return -1
		}
		return 1
	}
	if left.SourceDB != right.SourceDB {
		if left.SourceDB < right.SourceDB {
			return -1
		}
		return 1
	}
	return 0
}

func encodeCursor(payload cursorPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(token string) (cursorPayload, error) {
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursorPayload{}, fmt.Errorf("invalid cursor: restart timeline from the first page")
	}
	var payload cursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return cursorPayload{}, fmt.Errorf("invalid cursor: restart timeline from the first page")
	}
	return payload, nil
}
