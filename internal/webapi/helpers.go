package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wxview/internal/app"
	"wxview/internal/contacts"
	"wxview/internal/daemon"
	"wxview/internal/identity"
	"wxview/internal/key"
	"wxview/internal/media"
	"wxview/internal/messages"
	"wxview/internal/msgindex"
	"wxview/internal/timeline"
)

type queryExecutionMeta struct {
	QueryMode       string
	IndexUsed       bool
	IndexStatus     string
	IndexReason     string
	IndexLagSeconds int64
}

func (h *handler) contactCache(ctx context.Context) (key.TargetDB, string, identity.Info, error) {
	target, cachePath, info, err := h.resolveAccount(ctx, false)
	if err != nil {
		return key.TargetDB{}, "", identity.Info{}, err
	}
	if _, err := os.Stat(cachePath); err != nil {
		if os.IsNotExist(err) {
			return key.TargetDB{}, "", identity.Info{}, fmt.Errorf("contact cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
		}
		return key.TargetDB{}, "", identity.Info{}, err
	}
	return target, cachePath, info, nil
}

func (h *handler) listMeta(info identity.Info, resource string) ListMeta {
	return ListMeta{
		SchemaVersion: apiSchemaVersion,
		Account:       toAccountMeta(info.Account),
		ServerID:      h.serverID,
		Resource:      resource,
	}
}

func (h *handler) actionMeta(info identity.Info, action string) ActionMeta {
	return ActionMeta{
		SchemaVersion: apiSchemaVersion,
		Account:       toAccountMeta(info.Account),
		ServerID:      h.serverID,
		Action:        action,
	}
}

func (h *handler) messageMeta(info identity.Info, resource string, limit int, returned int, execution queryExecutionMeta) ListMeta {
	meta := h.listMeta(info, resource)
	meta.Timezone = localTimezoneName()
	meta.Mode = resource
	meta.Limit = intPtr(limit)
	meta.Returned = returned
	if execution.QueryMode != "" {
		meta.QueryMode = execution.QueryMode
		meta.IndexUsed = boolPtr(execution.IndexUsed)
		meta.IndexStatus = execution.IndexStatus
		meta.IndexReason = execution.IndexReason
		meta.IndexLagSeconds = execution.IndexLagSeconds
	}
	return meta
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func parseInt64Query(value string, def int64, min int64, name string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if n < min {
		return 0, fmt.Errorf("%s must be >= %d", name, min)
	}
	return n, nil
}

func parseBoolQuery(value string, def bool, name string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return def, nil
	}
	switch strings.ToLower(value) {
	case "1", "t", "true", "yes", "y":
		return true, nil
	case "0", "f", "false", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", name)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func hasQueryParam(values url.Values, name string) bool {
	_, ok := values[name]
	return ok
}

func nextOffsetURL(r *http.Request, limit int, offset int, hasMore bool) string {
	if !hasMore || limit <= 0 {
		return ""
	}
	next := *r.URL
	values := next.Query()
	values.Set("limit", strconv.Itoa(limit))
	values.Set("offset", strconv.Itoa(offset+limit))
	values.Del("refresh")
	next.RawQuery = values.Encode()
	return next.RequestURI()
}

func nextMessagesURL(r *http.Request, nextAfterSeq int64, limit int) string {
	next := *r.URL
	values := next.Query()
	values.Set("after_seq", strconv.FormatInt(nextAfterSeq, 10))
	values.Del("offset")
	values.Del("refresh")
	if limit > 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	next.RawQuery = values.Encode()
	return next.RequestURI()
}

func nextCursorURL(r *http.Request, cursor string) string {
	if strings.TrimSpace(cursor) == "" {
		return ""
	}
	next := *r.URL
	values := next.Query()
	values.Set("cursor", cursor)
	values.Del("refresh")
	next.RawQuery = values.Encode()
	return next.RequestURI()
}

func writeQueryError(w http.ResponseWriter, code string, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "does not exist") || strings.Contains(lower, "not found"):
		writeError(w, http.StatusNotFound, "not_found", msg)
	case strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "required") ||
		strings.Contains(lower, "cannot be combined") ||
		strings.Contains(lower, "must be"):
		writeError(w, http.StatusBadRequest, code, msg)
	default:
		writeError(w, http.StatusInternalServerError, code, msg)
	}
}

func readActionParams(r *http.Request) (map[string]string, error) {
	params := map[string]string{}
	if r.Body != nil && r.ContentLength != 0 {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return nil, err
		}
		for key, value := range raw {
			switch v := value.(type) {
			case string:
				params[key] = v
			case float64:
				if v == float64(int64(v)) {
					params[key] = strconv.FormatInt(int64(v), 10)
				} else {
					params[key] = fmt.Sprintf("%v", v)
				}
			case bool:
				params[key] = strconv.FormatBool(v)
			case nil:
			default:
				params[key] = fmt.Sprintf("%v", v)
			}
		}
	}
	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			params[key] = values[len(values)-1]
		}
	}
	return params, nil
}

func contactDetailItem(item contacts.Detail) ContactDetailItem {
	displayName := contacts.DisplayName(contacts.Contact{
		Username: item.Username,
		Alias:    item.Alias,
		Remark:   item.Remark,
		NickName: item.NickName,
		HeadURL:  item.HeadURL,
		Kind:     item.Kind,
	})
	return ContactDetailItem{
		Username:     item.Username,
		Alias:        item.Alias,
		Remark:       item.Remark,
		NickName:     item.NickName,
		DisplayName:  displayName,
		HeadURL:      item.HeadURL,
		SmallHeadURL: item.SmallHeadURL,
		BigHeadURL:   item.BigHeadURL,
		Description:  item.Description,
		VerifyFlag:   item.VerifyFlag,
		LocalType:    item.LocalType,
		Kind:         item.Kind,
		IsChatroom:   item.IsChatroom,
		IsOfficial:   item.IsOfficial,
		AvatarStatus: item.AvatarStatus,
		AvatarPath:   item.AvatarPath,
		AvatarReason: item.AvatarReason,
	}
}

func sessionCachePathReadOnly() (string, error) {
	if _, path, ok := key.SessionCachePathIfExists(); ok {
		return path, nil
	}
	return "", fmt.Errorf("session cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
}

func messageCachePathsReadOnly() ([]string, error) {
	paths, allExist, err := key.MessageCachePaths()
	if err != nil {
		return nil, err
	}
	if !allExist || len(paths) == 0 {
		return nil, fmt.Errorf("message cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	return paths, nil
}

func articleCachePathsReadOnly() ([]string, error) {
	paths, err := messageCachePathsReadOnly()
	if err != nil {
		return nil, err
	}
	if bizPaths, _, err := key.BizMessageCachePaths(); err == nil {
		for _, path := range bizPaths {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

func favoriteCachePathReadOnly() (key.TargetDB, string, error) {
	target, path, ok := key.FavoriteCachePathIfExists()
	if !ok {
		return key.TargetDB{}, "", fmt.Errorf("favorite cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	return target, path, nil
}

func snsCachePathReadOnly() (string, error) {
	if _, path, ok := key.SNSCachePathIfExists(); ok {
		return path, nil
	}
	return "", fmt.Errorf("sns cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
}

func headImageCachePathReadOnly() (string, bool) {
	_, path, ok := key.HeadImageCachePathIfExists()
	return path, ok
}

func mediaResolverFor(target key.TargetDB) (*media.Resolver, error) {
	cacheDir, err := media.MediaCacheDir(target.Account)
	if err != nil {
		return nil, err
	}
	resourceDBs := []string{}
	if path, err := key.CachePath(target.Account, "message/message_resource.db"); err == nil {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			resourceDBs = append(resourceDBs, path)
		}
	}
	if paths, _, err := key.MediaCachePaths(); err == nil {
		for _, path := range paths {
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				resourceDBs = append(resourceDBs, path)
			}
		}
	}
	resolver := media.NewResolver(target.DataDir, cacheDir, resourceDBs...)
	return &resolver, nil
}

func enrichMessageMedia(target key.TargetDB, list []messages.Message) {
	if len(list) == 0 {
		return
	}
	resolver, err := mediaResolverFor(target)
	if err != nil {
		return
	}
	messages.EnrichMediaDetails(list, resolver)
}

func loadExistingChatInfoMap(ctx context.Context) map[string]messages.ChatInfo {
	_, path, ok := key.HasContactCache()
	if !ok {
		return nil
	}
	list, err := contacts.NewService(path).List(ctx)
	if err != nil {
		return nil
	}
	return chatInfoMapFromContacts(list)
}

func chatInfoForUsername(ctx context.Context, username string) messages.ChatInfo {
	username = strings.TrimSpace(username)
	if username == "" {
		return messages.ChatInfo{}
	}
	_, path, ok := key.HasContactCache()
	if !ok {
		return messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
	}
	contact, found, err := contacts.NewService(path).Lookup(ctx, username)
	if err != nil || !found {
		return messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
	}
	return chatInfoFromContact(contact)
}

func chatInfoMapForUsernames(ctx context.Context, usernames ...string) map[string]messages.ChatInfo {
	out := make(map[string]messages.ChatInfo, len(usernames))
	for _, username := range usernames {
		info := chatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) != "" {
			out[info.Username] = info
		}
	}
	return out
}

func chatInfoMapFromContacts(list []contacts.Contact) map[string]messages.ChatInfo {
	out := make(map[string]messages.ChatInfo, len(list))
	for _, contact := range list {
		out[contact.Username] = chatInfoFromContact(contact)
	}
	return out
}

func chatInfoFromContact(contact contacts.Contact) messages.ChatInfo {
	return messages.ChatInfo{
		Username:    contact.Username,
		Kind:        contact.Kind,
		DisplayName: contacts.DisplayName(contact),
		Alias:       contact.Alias,
		Remark:      contact.Remark,
		NickName:    contact.NickName,
	}
}

func selectSearchChats(ctx context.Context, username string, kind string, query string) ([]messages.ChatInfo, int, error) {
	if username != "" {
		info := chatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) == "" {
			info = messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
		}
		return []messages.ChatInfo{info}, 1, nil
	}
	list, err := listContactsReadOnly(ctx)
	if err != nil {
		return nil, 0, err
	}
	selected := contacts.ApplyQueryOptions(list, contacts.QueryOptions{Kind: kind, Query: query, Sort: "username"})
	chats := make([]messages.ChatInfo, 0, len(selected))
	for _, contact := range selected {
		chats = append(chats, chatInfoFromContact(contact))
	}
	return chats, len(chats), nil
}

func selectTimelineChats(ctx context.Context, username string, kind string, query string, kindProvided bool) ([]messages.ChatInfo, int, error) {
	if username != "" {
		info := chatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) == "" {
			info = messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
		}
		return []messages.ChatInfo{info}, 1, nil
	}
	list, err := listContactsReadOnly(ctx)
	if err != nil {
		return nil, 0, err
	}
	opts := contacts.QueryOptions{Kind: kind, Query: query, Sort: "username"}
	if !kindProvided && query != "" {
		opts.Kind = contacts.KindAll
	}
	selected := contacts.ApplyQueryOptions(list, opts)
	chats := make([]messages.ChatInfo, 0, len(selected))
	for _, contact := range selected {
		chats = append(chats, chatInfoFromContact(contact))
	}
	return chats, len(chats), nil
}

func listContactsReadOnly(ctx context.Context) ([]contacts.Contact, error) {
	_, path, ok := key.HasContactCache()
	if !ok {
		return nil, fmt.Errorf("contact cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	return contacts.NewService(path).List(ctx)
}

func listMessagesMaybeIndex(ctx context.Context, account string, cachePaths []string, service messages.Service, useIndex bool, opts messages.QueryOptions) ([]messages.Message, queryExecutionMeta, error) {
	if !useIndex {
		list, err := service.List(ctx, opts)
		return list, scanExecution("index_disabled_by_flag"), err
	}
	indexPath, usability, ok := queryIndexUsability(ctx, account, cachePaths, msgindex.UsabilityOptions{
		Username: opts.Username,
		Start:    opts.Start,
		End:      opts.End,
		HasStart: opts.HasStart,
		HasEnd:   opts.HasEnd,
	})
	if ok {
		refs, err := msgindex.MessageRefs(ctx, indexPath, msgindex.MessageQuery{
			Username:    opts.Username,
			Start:       opts.Start,
			End:         opts.End,
			AfterSeq:    opts.AfterSeq,
			HasStart:    opts.HasStart,
			HasEnd:      opts.HasEnd,
			HasAfterSeq: opts.HasAfterSeq,
			Limit:       opts.Limit,
			Offset:      opts.Offset,
		})
		if err == nil {
			list, err := service.ListByRefs(ctx, refs, messages.RefQueryOptions{IncludeSource: opts.IncludeSource, MediaResolver: opts.MediaResolver})
			if err == nil {
				return list, indexExecution(usability), nil
			}
		}
	}
	list, err := service.List(ctx, opts)
	return list, scanExecutionFromUsability(usability), err
}

func timelineMaybeIndex(ctx context.Context, account string, cachePaths []string, service messages.Service, useIndex bool, opts timeline.QueryOptions) (timeline.Result, queryExecutionMeta, error) {
	if !useIndex {
		result, err := timeline.List(ctx, service, opts)
		return result, scanExecution("index_disabled_by_flag"), err
	}
	indexPath, usability, ok := queryIndexUsability(ctx, account, cachePaths, msgindex.UsabilityOptions{
		Chats:    opts.Chats,
		Start:    opts.Start,
		End:      opts.End,
		HasStart: true,
		HasEnd:   true,
	})
	if ok {
		var after timeline.SortKey
		hasAfter := false
		if strings.TrimSpace(opts.Cursor) != "" {
			var err error
			after, err = timeline.DecodeCursor(opts.Cursor, opts.QueryHash)
			if err != nil {
				return timeline.Result{}, scanExecutionFromUsability(usability), err
			}
			hasAfter = true
		}
		refs, err := msgindex.TimelineRefs(ctx, indexPath, msgindex.TimelineQuery{
			Chats:    opts.Chats,
			Start:    opts.Start,
			End:      opts.End,
			After:    after,
			HasAfter: hasAfter,
			Limit:    opts.Limit + 1,
		})
		if err == nil {
			items, err := service.ListByRefs(ctx, refs, messages.RefQueryOptions{IncludeSource: opts.IncludeSource})
			if err == nil {
				chatInfo := make(map[string]messages.ChatInfo, len(opts.Chats))
				for _, chat := range opts.Chats {
					chatInfo[chat.Username] = chat
				}
				messages.ApplyChatInfo(items, chatInfo)
				result := timeline.Result{Items: items}
				if len(result.Items) > opts.Limit {
					result.HasMore = true
					result.Items = result.Items[:opts.Limit]
				}
				if result.HasMore && len(result.Items) > 0 {
					next, err := timeline.EncodeCursor(opts.QueryHash, timeline.KeyFor(result.Items[len(result.Items)-1]))
					if err != nil {
						return timeline.Result{}, scanExecutionFromUsability(usability), err
					}
					result.NextCursor = next
				}
				return result, indexExecution(usability), nil
			}
		}
	}
	result, err := timeline.List(ctx, service, opts)
	return result, scanExecutionFromUsability(usability), err
}

func queryIndexUsability(ctx context.Context, account string, cachePaths []string, opts msgindex.UsabilityOptions) (string, msgindex.Usability, bool) {
	indexPath, err := msgindex.LocalPath(account)
	if err != nil {
		return "", msgindex.Usability{Reason: "index_path_error"}, false
	}
	if strings.TrimSpace(opts.SessionCachePath) == "" {
		if target, path, ok := key.SessionCachePathIfExists(); ok && target.Account == account {
			opts.SessionCachePath = path
		}
	}
	usability, err := msgindex.UseForQuery(ctx, indexPath, cachePaths, opts)
	if err != nil || !usability.UseIndex {
		if err != nil && strings.TrimSpace(usability.Reason) == "" {
			usability.Reason = "index_status_error"
		}
		return "", usability, false
	}
	return indexPath, usability, true
}

func indexExecution(usability msgindex.Usability) queryExecutionMeta {
	return queryExecutionMeta{
		QueryMode:       msgindex.QueryModeIndex,
		IndexUsed:       true,
		IndexStatus:     usability.Status.Status,
		IndexReason:     firstNonEmpty(usability.Reason, "index_usable"),
		IndexLagSeconds: usability.Status.LagSeconds,
	}
}

func scanExecution(reason string) queryExecutionMeta {
	return queryExecutionMeta{QueryMode: msgindex.QueryModeScan, IndexUsed: false, IndexReason: reason}
}

func scanExecutionFromUsability(usability msgindex.Usability) queryExecutionMeta {
	meta := scanExecution(firstNonEmpty(usability.Reason, "index_unusable"))
	meta.IndexStatus = usability.Status.Status
	meta.IndexLagSeconds = usability.Status.LagSeconds
	return meta
}

func trimMessagePage(list []messages.Message, limit int) ([]messages.Message, bool) {
	if limit <= 0 || len(list) <= limit {
		return list, false
	}
	return list[:limit], true
}

func parseMessageRange(dateText string, startText string, endText string) (int64, int64, bool, bool, error) {
	dateText = strings.TrimSpace(dateText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if dateText != "" && (startText != "" || endText != "") {
		return 0, 0, false, false, fmt.Errorf("date cannot be combined with start or end")
	}
	if dateText != "" {
		start, end, err := parseDateRange(dateText)
		if err != nil {
			return 0, 0, false, false, err
		}
		return start, end, true, true, nil
	}
	start, hasStart, err := parseTimeBound(startText, "start", false)
	if err != nil {
		return 0, 0, false, false, err
	}
	end, hasEnd, err := parseTimeBound(endText, "end", true)
	if err != nil {
		return 0, 0, false, false, err
	}
	if hasStart && hasEnd && start > end {
		return 0, 0, false, false, fmt.Errorf("start must not be later than end")
	}
	return start, end, hasStart, hasEnd, nil
}

func parseTimelineRange(dateText string, startText string, endText string) (int64, int64, string, string, error) {
	dateText = strings.TrimSpace(dateText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if dateText != "" && (startText != "" || endText != "") {
		return 0, 0, "", "", fmt.Errorf("date cannot be combined with start or end")
	}
	if dateText != "" {
		start, end, err := parseDateRange(dateText)
		if err != nil {
			return 0, 0, "", "", err
		}
		return start, end, formatMetaTime(start, true), formatMetaTime(end, true), nil
	}
	if startText == "" || endText == "" {
		return 0, 0, "", "", fmt.Errorf("timeline requires date or both start and end")
	}
	start, hasStart, err := parseTimeBound(startText, "start", false)
	if err != nil {
		return 0, 0, "", "", err
	}
	end, hasEnd, err := parseTimeBound(endText, "end", true)
	if err != nil {
		return 0, 0, "", "", err
	}
	if !hasStart || !hasEnd {
		return 0, 0, "", "", fmt.Errorf("timeline requires both start and end")
	}
	if start > end {
		return 0, 0, "", "", fmt.Errorf("start must not be later than end")
	}
	return start, end, formatMetaTime(start, true), formatMetaTime(end, true), nil
}

func parseDateRange(value string) (int64, int64, error) {
	value = strings.TrimSpace(value)
	now := time.Now()
	switch value {
	case "today":
		year, month, day := now.Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
		return start.Unix(), start.Add(24*time.Hour - time.Second).Unix(), nil
	case "yesterday":
		year, month, day := now.AddDate(0, 0, -1).Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
		return start.Unix(), start.Add(24*time.Hour - time.Second).Unix(), nil
	default:
		start, hasStart, err := parseTimeBound(value, "date", false)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid date %q: use today, yesterday, or YYYY-MM-DD", value)
		}
		end, hasEnd, err := parseTimeBound(value, "date", true)
		if err != nil || !hasStart || !hasEnd {
			return 0, 0, fmt.Errorf("invalid date %q: use today, yesterday, or YYYY-MM-DD", value)
		}
		if end-start != 24*60*60-1 {
			return 0, 0, fmt.Errorf("invalid date %q: use today, yesterday, or YYYY-MM-DD", value)
		}
		return start, end, nil
	}
}

func parseTimeBound(value string, field string, isEnd bool) (int64, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, nil
	}
	if allDigits(value) {
		ts, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf("invalid %s time %q", field, value)
		}
		return ts, true, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Unix(), true, nil
	}
	layouts := []struct {
		layout   string
		dateOnly bool
	}{
		{"2006-01-02 15:04:05", false},
		{"2006-01-02 15:04", false},
		{"2006-01-02", true},
	}
	for _, item := range layouts {
		t, err := time.ParseInLocation(item.layout, value, time.Local)
		if err != nil {
			continue
		}
		if item.dateOnly && isEnd {
			t = t.Add(24*time.Hour - time.Second)
		}
		return t.Unix(), true, nil
	}
	return 0, false, fmt.Errorf("invalid %s time %q: use Unix seconds, YYYY-MM-DD, YYYY-MM-DD HH:MM, YYYY-MM-DD HH:MM:SS, or RFC3339", field, value)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func formatMetaTime(ts int64, ok bool) string {
	if !ok || ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func localTimezoneName() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" {
		return tz
	}
	if time.Local != nil {
		if name := strings.TrimSpace(time.Local.String()); name != "" && name != "Local" {
			return name
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if idx := strings.LastIndex(target, "/zoneinfo/"); idx >= 0 {
			return strings.TrimPrefix(target[idx+len("/zoneinfo/"):], "/")
		}
	}
	_, offset := time.Now().Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("UTC%s%02d:%02d", sign, offset/3600, (offset%3600)/60)
}

type newMessagesState struct {
	Sessions map[string]int64 `json:"sessions"`
}

func newMessagesStatePathForAccount(account string) string {
	base, err := app.BaseDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "cache", app.SafeAccountDir(account), "state", "new_messages.json")
}

func newMessagesStatePath(account string) (string, error) {
	path, err := app.CacheDBPath(account, "state/new_messages.json")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := app.ChownForSudo(filepath.Dir(path)); err != nil {
		return "", err
	}
	return path, nil
}

func loadNewMessagesState(path string) (map[string]int64, bool, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]int64{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]int64{}, false, nil
		}
		return nil, false, err
	}
	var state newMessagesState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false, fmt.Errorf("parse new-messages state: %w", err)
	}
	if state.Sessions == nil {
		state.Sessions = map[string]int64{}
	}
	return state.Sessions, true, nil
}

func saveNewMessagesState(path string, sessions map[string]int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(newMessagesState{Sessions: sessions}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if err := app.ChownForSudo(path); err != nil {
		return err
	}
	return app.ChownForSudo(filepath.Dir(path))
}

func copyState(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func daemonClient() (daemon.Client, error) {
	socketPath, err := app.SocketPath()
	if err != nil {
		return daemon.Client{}, err
	}
	return daemon.Client{SocketPath: socketPath, Timeout: time.Minute}, nil
}

func isUnknownDaemonAction(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown daemon action")
}

func ensureContact(ctx context.Context) error {
	_, _, err := key.EnsureContactCache(ctx)
	return err
}

func ensureSessions(ctx context.Context) error {
	_, _, err := key.EnsureSessionCache(ctx)
	return err
}

func ensureMessages(ctx context.Context) error {
	_, err := key.EnsureMessageRelatedCaches(ctx)
	return err
}

func ensureFavorites(ctx context.Context) error {
	_, _, err := key.EnsureFavoriteCache(ctx)
	return err
}

func ensureSNS(ctx context.Context) error {
	_, _, err := key.EnsureSNSCache(ctx)
	return err
}
