package sessions

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"wxview/internal/contacts"
	"wxview/internal/messages"
	"wxview/internal/sqlitedb"
)

type Session struct {
	Username              string `json:"username"`
	ChatKind              string `json:"chat_kind"`
	ChatDisplayName       string `json:"chat_display_name"`
	ChatAlias             string `json:"chat_alias"`
	ChatRemark            string `json:"chat_remark"`
	ChatNickName          string `json:"chat_nick_name"`
	IsChatroom            bool   `json:"is_chatroom"`
	UnreadCount           int64  `json:"unread_count"`
	Summary               string `json:"summary"`
	SummaryEncoding       string `json:"summary_encoding"`
	LastTimestamp         int64  `json:"last_timestamp"`
	Time                  string `json:"time"`
	LastMsgType           int64  `json:"last_msg_type"`
	LastMsgSubType        int64  `json:"last_msg_sub_type"`
	LastSender            string `json:"last_sender"`
	LastSenderDisplayName string `json:"last_sender_display_name"`
}

type QueryOptions struct {
	Kind       string
	Query      string
	UnreadOnly bool
	Limit      int
	Offset     int
}

type Service struct {
	CacheDB string
}

func NewService(cacheDB string) Service {
	return Service{CacheDB: cacheDB}
}

func (s Service) List(ctx context.Context, opts QueryOptions) ([]Session, error) {
	if opts.Limit < 0 {
		return nil, fmt.Errorf("limit must be >= 0")
	}
	if opts.Offset < 0 {
		return nil, fmt.Errorf("offset must be >= 0")
	}
	if s.CacheDB == "" {
		return nil, fmt.Errorf("session cache path is empty")
	}
	if _, err := os.Stat(s.CacheDB); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session cache does not exist: run `wxview init` with process-memory permissions and then `wxview sessions --refresh`")
		}
		return nil, err
	}

	var rows []queryRow
	if ok, err := tableExists(ctx, s.CacheDB, "SessionTable"); err != nil {
		return nil, err
	} else if ok {
		rows, err = querySessionTable(ctx, s.CacheDB, opts.UnreadOnly)
		if err != nil {
			return nil, err
		}
	} else if ok, err := tableExists(ctx, s.CacheDB, "SessionAbstract"); err != nil {
		return nil, err
	} else if ok {
		rows, err = querySessionAbstract(ctx, s.CacheDB, opts.UnreadOnly)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("session cache has no SessionTable or SessionAbstract table")
	}

	out := make([]Session, 0, len(rows))
	for _, row := range rows {
		msgType, subType := splitLocalType(row.LastMsgType)
		summary, encoding := messages.DecodeContent(row.Summary)
		summary = stripGroupPrefix(strings.ToValidUTF8(summary, ""))
		out = append(out, Session{
			Username:              row.Username,
			ChatKind:              messages.ChatKindUnknown,
			ChatDisplayName:       row.Username,
			IsChatroom:            strings.HasSuffix(row.Username, "@chatroom"),
			UnreadCount:           row.UnreadCount,
			Summary:               summary,
			SummaryEncoding:       encoding,
			LastTimestamp:         row.LastTimestamp,
			Time:                  formatUnix(row.LastTimestamp),
			LastMsgType:           msgType,
			LastMsgSubType:        subType,
			LastSender:            row.LastSender,
			LastSenderDisplayName: row.LastSenderDisplayName,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastTimestamp != out[j].LastTimestamp {
			return out[i].LastTimestamp > out[j].LastTimestamp
		}
		return out[i].Username < out[j].Username
	})
	return paginate(out, opts.Limit, opts.Offset), nil
}

func ApplyChatInfo(list []Session, chatInfo map[string]messages.ChatInfo) {
	for i := range list {
		info := chatInfo[list[i].Username]
		if strings.TrimSpace(info.Username) == "" {
			if list[i].ChatDisplayName == "" {
				list[i].ChatDisplayName = list[i].Username
			}
			continue
		}
		list[i].ChatKind = defaultString(info.Kind, messages.ChatKindUnknown)
		list[i].ChatDisplayName = defaultString(info.DisplayName, list[i].Username)
		list[i].ChatAlias = info.Alias
		list[i].ChatRemark = info.Remark
		list[i].ChatNickName = info.NickName
		list[i].IsChatroom = info.Kind == contacts.KindChatroom || strings.HasSuffix(list[i].Username, "@chatroom")
		if list[i].IsChatroom && list[i].LastSender != "" && list[i].LastSenderDisplayName == "" {
			if senderInfo := chatInfo[list[i].LastSender]; strings.TrimSpace(senderInfo.Username) != "" {
				list[i].LastSenderDisplayName = defaultString(senderInfo.DisplayName, list[i].LastSender)
			}
		}
	}
}

func ApplyQueryOptions(list []Session, opts QueryOptions) []Session {
	return paginate(filter(list, opts), opts.Limit, opts.Offset)
}

type queryRow struct {
	Username              string
	UnreadCount           int64
	Summary               []byte
	LastTimestamp         int64
	LastMsgType           int64
	LastSender            string
	LastSenderDisplayName string
}

func querySessionTable(ctx context.Context, dbPath string, unreadOnly bool) ([]queryRow, error) {
	where := "WHERE last_timestamp > 0"
	if unreadOnly {
		where += " AND unread_count > 0"
	}
	query := fmt.Sprintf(`
SELECT
  COALESCE(username, ''),
  COALESCE(unread_count, 0),
  COALESCE(summary, X''),
  COALESCE(last_timestamp, 0),
  COALESCE(last_msg_type, 0),
  COALESCE(last_msg_sender, ''),
  COALESCE(last_sender_display_name, '')
FROM SessionTable
%s
ORDER BY last_timestamp DESC;
`, where)
	return queryRows(ctx, dbPath, query)
}

func querySessionAbstract(ctx context.Context, dbPath string, unreadOnly bool) ([]queryRow, error) {
	where := "WHERE m_uLastTime > 0"
	if unreadOnly {
		where += " AND m_uUnReadCount > 0"
	}
	query := fmt.Sprintf(`
SELECT
  COALESCE(m_nsUserName, ''),
  COALESCE(m_uUnReadCount, 0),
  X'',
  COALESCE(m_uLastTime, 0),
  0,
  '',
  ''
FROM SessionAbstract
%s
ORDER BY m_uLastTime DESC;
`, where)
	return queryRows(ctx, dbPath, query)
}

func queryRows(ctx context.Context, dbPath string, query string) ([]queryRow, error) {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query session cache: %w", err)
	}
	defer rows.Close()
	out := []queryRow{}
	for rows.Next() {
		var row queryRow
		if err := rows.Scan(
			&row.Username,
			&row.UnreadCount,
			&row.Summary,
			&row.LastTimestamp,
			&row.LastMsgType,
			&row.LastSender,
			&row.LastSenderDisplayName,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func tableExists(ctx context.Context, dbPath string, table string) (bool, error) {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	return sqlitedb.TableExists(ctx, db, table)
}

func filter(list []Session, opts QueryOptions) []Session {
	kind := strings.TrimSpace(opts.Kind)
	query := strings.ToLower(strings.TrimSpace(opts.Query))
	if kind == "" || kind == contacts.KindAll {
		kind = contacts.KindAll
	}
	if kind == contacts.KindAll && query == "" {
		return list
	}
	out := make([]Session, 0, len(list))
	for _, item := range list {
		if kind != contacts.KindAll && item.ChatKind != kind {
			continue
		}
		if query != "" && !sessionMatches(item, query) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func sessionMatches(item Session, query string) bool {
	fields := []string{
		item.Username,
		item.ChatDisplayName,
		item.ChatAlias,
		item.ChatRemark,
		item.ChatNickName,
		item.Summary,
		item.LastSender,
		item.LastSenderDisplayName,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func paginate(list []Session, limit int, offset int) []Session {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(list) {
		return []Session{}
	}
	end := len(list)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return append([]Session(nil), list[offset:end]...)
}

func stripGroupPrefix(summary string) string {
	if sender, text, ok := strings.Cut(summary, ":\n"); ok && sender != "" {
		return text
	}
	return summary
}

func splitLocalType(raw int64) (int64, int64) {
	value := uint64(raw)
	return int64(value & 0xffffffff), int64(value >> 32)
}

func formatUnix(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
