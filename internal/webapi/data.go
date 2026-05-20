package webapi

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"wxview/internal/articles"
	"wxview/internal/contacts"
	"wxview/internal/favorites"
	"wxview/internal/key"
	"wxview/internal/media"
	"wxview/internal/messages"
	"wxview/internal/sessions"
	"wxview/internal/sns"
	"wxview/internal/timeline"
)

const (
	apiSchemaVersion       = 1
	defaultSessionLimit    = 20
	defaultNewMessageLimit = 100
	defaultMessageLimit    = 0
	defaultSearchLimit     = 50
	defaultTimelineLimit   = 200
	defaultDataLimit       = 20
	maxTimelineLimit       = 1000
)

type ListMeta struct {
	SchemaVersion int         `json:"schema_version"`
	Account       AccountMeta `json:"account"`
	ServerID      string      `json:"server_id"`
	Resource      string      `json:"resource"`
	Timezone      string      `json:"timezone,omitempty"`

	Limit    *int  `json:"limit,omitempty"`
	Offset   *int  `json:"offset,omitempty"`
	Returned int   `json:"returned"`
	Total    *int  `json:"total,omitempty"`
	HasMore  *bool `json:"has_more,omitempty"`

	NextURL      string `json:"next_url,omitempty"`
	NextCursor   string `json:"next_cursor,omitempty"`
	NextAfterSeq int64  `json:"next_after_seq,omitempty"`

	Mode            string `json:"mode,omitempty"`
	QueryMode       string `json:"query_mode,omitempty"`
	IndexUsed       *bool  `json:"index_used,omitempty"`
	IndexStatus     string `json:"index_status,omitempty"`
	IndexReason     string `json:"index_reason,omitempty"`
	IndexLagSeconds int64  `json:"index_lag_seconds,omitempty"`

	Username     string `json:"username,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Query        string `json:"query,omitempty"`
	ChatQuery    string `json:"chat_query,omitempty"`
	Start        string `json:"start,omitempty"`
	End          string `json:"end,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	MatchedChats int    `json:"matched_chats,omitempty"`
	TotalMatches *int   `json:"total_matches,omitempty"`

	GroupUsername    string `json:"group_username,omitempty"`
	GroupDisplayName string `json:"group_display_name,omitempty"`
	Owner            string `json:"owner,omitempty"`
	OwnerDisplayName string `json:"owner_display_name,omitempty"`
	FirstRun         *bool  `json:"first_run,omitempty"`
	Reset            *bool  `json:"reset,omitempty"`
	ChangedChats     int    `json:"changed_chats,omitempty"`
	StatePath        string `json:"state_path,omitempty"`
}

type ItemsResponse[T any] struct {
	Meta  ListMeta `json:"meta"`
	Items []T      `json:"items"`
}

type ActionMeta struct {
	SchemaVersion int         `json:"schema_version"`
	Account       AccountMeta `json:"account"`
	ServerID      string      `json:"server_id"`
	Action        string      `json:"action"`
}

type ActionResponse[T any] struct {
	Meta   ActionMeta `json:"meta"`
	Result T          `json:"result"`
}

type RefreshResult struct {
	Scope      string   `json:"scope"`
	Refreshed  []string `json:"refreshed"`
	DurationMS int64    `json:"duration_ms"`
	UsedDaemon bool     `json:"used_daemon"`
	Warnings   []string `json:"warnings,omitempty"`
}

type ResetResult struct {
	Reset     bool   `json:"reset"`
	StatePath string `json:"state_path"`
}

type ContactDetailItem struct {
	Username     string `json:"username"`
	Alias        string `json:"alias"`
	Remark       string `json:"remark"`
	NickName     string `json:"nick_name"`
	DisplayName  string `json:"display_name"`
	HeadURL      string `json:"head_url"`
	SmallHeadURL string `json:"small_head_url"`
	BigHeadURL   string `json:"big_head_url"`
	Description  string `json:"description"`
	VerifyFlag   int    `json:"verify_flag"`
	LocalType    int    `json:"local_type"`
	Kind         string `json:"kind"`
	IsChatroom   bool   `json:"is_chatroom"`
	IsOfficial   bool   `json:"is_official"`
	AvatarStatus string `json:"avatar_status,omitempty"`
	AvatarPath   string `json:"avatar_path,omitempty"`
	AvatarReason string `json:"avatar_reason,omitempty"`
}

func (h *handler) handleContactDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		writeError(w, http.StatusBadRequest, "missing_username", "username is required")
		return
	}
	_, cachePath, info, err := h.contactCache(r.Context())
	if err != nil {
		writeQueryError(w, "contact_cache_failed", err)
		return
	}
	item, err := contacts.NewService(cachePath).Detail(r.Context(), username)
	if err != nil {
		writeQueryError(w, "contact_detail_failed", err)
		return
	}
	if headPath, ok := headImageCachePathReadOnly(); ok {
		if target, _, ok := key.HasContactCache(); ok {
			if cacheDir, err := media.MediaCacheDir(target.Account); err == nil {
				avatar := media.ResolveAvatar(headPath, item.Username, cacheDir)
				item.AvatarStatus = avatar.Status
				item.AvatarPath = avatar.Path
				item.AvatarReason = avatar.Reason
			}
		}
	}
	meta := h.listMeta(info, "contacts.detail")
	total := 1
	meta.Total = &total
	meta.Returned = 1
	writeJSON(w, http.StatusOK, ItemsResponse[ContactDetailItem]{
		Meta:  meta,
		Items: []ContactDetailItem{contactDetailItem(item)},
	})
}

func (h *handler) handleMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	username := strings.TrimSpace(q.Get("username"))
	query := strings.TrimSpace(q.Get("query"))
	if username == "" && query == "" {
		writeError(w, http.StatusBadRequest, "missing_selector", "username or query is required")
		return
	}
	if username != "" && query != "" {
		writeError(w, http.StatusBadRequest, "invalid_selector", "username cannot be combined with query")
		return
	}
	_, cachePath, info, err := h.contactCache(r.Context())
	if err != nil {
		writeQueryError(w, "contact_cache_failed", err)
		return
	}
	service := contacts.NewService(cachePath)
	if query != "" {
		list, err := service.List(r.Context())
		if err != nil {
			writeQueryError(w, "contacts_query_failed", err)
			return
		}
		matched := contacts.ApplyQueryOptions(list, contacts.QueryOptions{Kind: contacts.KindChatroom, Query: query})
		if len(matched) != 1 {
			writeError(w, http.StatusBadRequest, "ambiguous_query", fmt.Sprintf("query matched %d chatrooms; use username for an exact chatroom", len(matched)))
			return
		}
		username = matched[0].Username
	}
	result, err := service.Members(r.Context(), username)
	if err != nil {
		writeQueryError(w, "members_query_failed", err)
		return
	}
	meta := h.listMeta(info, "members")
	total := result.Count
	meta.Total = &total
	meta.Returned = len(result.Members)
	meta.GroupUsername = result.Username
	meta.GroupDisplayName = result.DisplayName
	meta.Owner = result.Owner
	meta.OwnerDisplayName = result.OwnerDisplayName
	writeJSON(w, http.StatusOK, ItemsResponse[contacts.Member]{Meta: meta, Items: result.Members})
}

func (h *handler) handleSessions(w http.ResponseWriter, r *http.Request, unreadOnly bool) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	kind := strings.TrimSpace(q.Get("kind"))
	if kind == "" {
		kind = contacts.KindAll
	}
	if !validKind(kind) {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be all, friend, chatroom, or other")
		return
	}
	limit, err := parseIntQuery(q.Get("limit"), defaultSessionLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	sessionPath, err := sessionCachePathReadOnly()
	if err != nil {
		writeQueryError(w, "session_cache_failed", err)
		return
	}
	list, err := sessions.NewService(sessionPath).List(r.Context(), sessions.QueryOptions{UnreadOnly: unreadOnly})
	if err != nil {
		writeQueryError(w, "sessions_query_failed", err)
		return
	}
	sessions.ApplyChatInfo(list, loadExistingChatInfoMap(r.Context()))
	filtered := sessions.ApplyQueryOptions(list, sessions.QueryOptions{Kind: kind, Query: q.Get("query")})
	total := len(filtered)
	page := sessions.ApplyQueryOptions(list, sessions.QueryOptions{Kind: kind, Query: q.Get("query"), Limit: limit, Offset: offset})
	hasMore := offset+len(page) < total
	resource := "sessions"
	if unreadOnly {
		resource = "unread"
	}
	meta := h.listMeta(info, resource)
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(page)
	meta.Total = &total
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	writeJSON(w, http.StatusOK, ItemsResponse[sessions.Session]{Meta: meta, Items: page})
}

func (h *handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	username := strings.TrimSpace(q.Get("username"))
	if username == "" {
		writeError(w, http.StatusBadRequest, "missing_username", "username is required")
		return
	}
	limit, err := parseIntQuery(q.Get("limit"), defaultMessageLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	afterSeq, err := parseInt64Query(q.Get("after_seq"), 0, 0, "after_seq")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_after_seq", err.Error())
		return
	}
	includeSource, err := parseBoolQuery(q.Get("source"), false, "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", err.Error())
		return
	}
	noIndex, err := parseBoolQuery(q.Get("no_index"), false, "no_index")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_no_index", err.Error())
		return
	}
	start, end, hasStart, hasEnd, err := parseMessageRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	target, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	cachePaths, err := messageCachePathsReadOnly()
	if err != nil {
		writeQueryError(w, "message_cache_failed", err)
		return
	}
	queryLimit := limit
	if limit > 0 {
		queryLimit = limit + 1
	}
	service := messages.NewService(cachePaths)
	list, execution, err := listMessagesMaybeIndex(r.Context(), target.Account, cachePaths, service, !noIndex, messages.QueryOptions{
		Username:      username,
		Start:         start,
		End:           end,
		AfterSeq:      afterSeq,
		HasStart:      hasStart,
		HasEnd:        hasEnd,
		HasAfterSeq:   afterSeq > 0,
		IncludeSource: includeSource,
		Limit:         queryLimit,
		Offset:        offset,
	})
	if err != nil {
		writeQueryError(w, "messages_query_failed", err)
		return
	}
	enrichMessageMedia(target, list)
	messages.ApplyChatInfo(list, chatInfoMapForUsernames(r.Context(), username))
	page, hasMore := trimMessagePage(list, limit)
	meta := h.messageMeta(info, "messages", limit, len(page), execution)
	meta.Offset = intPtr(offset)
	meta.Username = username
	meta.Start = formatMetaTime(start, hasStart)
	meta.End = formatMetaTime(end, hasEnd)
	meta.HasMore = &hasMore
	if hasMore && len(page) > 0 {
		meta.NextAfterSeq = page[len(page)-1].Seq
		meta.NextURL = nextMessagesURL(r, meta.NextAfterSeq, limit)
	}
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: page})
}

func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	query := strings.TrimSpace(firstNonEmpty(q.Get("query"), q.Get("q")))
	if query == "" {
		writeError(w, http.StatusBadRequest, "missing_query", "query is required")
		return
	}
	kind := strings.TrimSpace(q.Get("kind"))
	if kind == "" {
		kind = contacts.KindAll
	}
	if !validKind(kind) {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be all, friend, chatroom, or other")
		return
	}
	chatQuery := strings.TrimSpace(firstNonEmpty(q.Get("chat_query"), q.Get("chat-query")))
	username := strings.TrimSpace(q.Get("username"))
	if username != "" && chatQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_selector", "username cannot be combined with chat_query")
		return
	}
	limit, err := parseIntQuery(q.Get("limit"), defaultSearchLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	includeSource, err := parseBoolQuery(q.Get("source"), false, "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", err.Error())
		return
	}
	start, end, hasStart, hasEnd, err := parseMessageRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	target, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	chats, matchedChats, err := selectSearchChats(r.Context(), username, kind, chatQuery)
	if err != nil {
		writeQueryError(w, "chat_selection_failed", err)
		return
	}
	cachePaths, err := messageCachePathsReadOnly()
	if err != nil {
		writeQueryError(w, "message_cache_failed", err)
		return
	}
	list, total, err := messages.NewService(cachePaths).Search(r.Context(), messages.SearchOptions{
		Chats:         chats,
		Query:         query,
		Start:         start,
		End:           end,
		HasStart:      hasStart,
		HasEnd:        hasEnd,
		IncludeSource: includeSource,
		Limit:         limit,
		Offset:        offset,
	})
	if err != nil {
		writeQueryError(w, "search_failed", err)
		return
	}
	enrichMessageMedia(target, list)
	hasMore := offset+len(list) < total
	execution := scanExecution("index_not_supported_for_search")
	meta := h.messageMeta(info, "search", limit, len(list), execution)
	meta.Offset = intPtr(offset)
	meta.Query = query
	meta.Kind = kind
	meta.ChatQuery = chatQuery
	meta.Username = username
	meta.Start = formatMetaTime(start, hasStart)
	meta.End = formatMetaTime(end, hasEnd)
	meta.TotalMatches = &total
	meta.Total = &total
	meta.MatchedChats = matchedChats
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: list})
}

func (h *handler) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	kind := strings.TrimSpace(q.Get("kind"))
	kindProvided := hasQueryParam(q, "kind")
	if kind == "" {
		kind = contacts.KindAll
	}
	if !validKind(kind) {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be all, friend, chatroom, or other")
		return
	}
	username := strings.TrimSpace(q.Get("username"))
	query := strings.TrimSpace(q.Get("query"))
	if username != "" && (kindProvided || query != "") {
		writeError(w, http.StatusBadRequest, "invalid_selector", "username cannot be combined with kind or query")
		return
	}
	if username == "" && !kindProvided && query == "" {
		writeError(w, http.StatusBadRequest, "missing_selector", "timeline requires username, kind, or query")
		return
	}
	limit, err := parseIntQuery(q.Get("limit"), defaultTimelineLimit, 1, maxTimelineLimit, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	includeSource, err := parseBoolQuery(q.Get("source"), false, "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", err.Error())
		return
	}
	noIndex, err := parseBoolQuery(q.Get("no_index"), false, "no_index")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_no_index", err.Error())
		return
	}
	start, end, startLabel, endLabel, err := parseTimelineRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	target, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	chats, matchedChats, err := selectTimelineChats(r.Context(), username, kind, query, kindProvided)
	if err != nil {
		writeQueryError(w, "chat_selection_failed", err)
		return
	}
	cachePaths, err := messageCachePathsReadOnly()
	if err != nil {
		writeQueryError(w, "message_cache_failed", err)
		return
	}
	queryIdentity := timeline.QueryIdentity{Username: username, Start: start, End: end}
	if username == "" {
		queryIdentity.Kind = kind
		queryIdentity.Query = query
	}
	queryHash, err := timeline.QueryHash(queryIdentity)
	if err != nil {
		writeQueryError(w, "timeline_query_hash_failed", err)
		return
	}
	service := messages.NewService(cachePaths)
	result, execution, err := timelineMaybeIndex(r.Context(), target.Account, cachePaths, service, !noIndex, timeline.QueryOptions{
		Chats:         chats,
		Start:         start,
		End:           end,
		Limit:         limit,
		Cursor:        strings.TrimSpace(q.Get("cursor")),
		QueryHash:     queryHash,
		IncludeSource: includeSource,
	})
	if err != nil {
		writeQueryError(w, "timeline_query_failed", err)
		return
	}
	enrichMessageMedia(target, result.Items)
	meta := h.messageMeta(info, "timeline", limit, len(result.Items), execution)
	meta.Username = username
	meta.Kind = ""
	meta.Query = ""
	if username == "" {
		meta.Kind = kind
		meta.Query = query
	}
	meta.Start = startLabel
	meta.End = endLabel
	meta.Cursor = strings.TrimSpace(q.Get("cursor"))
	meta.MatchedChats = matchedChats
	meta.HasMore = &result.HasMore
	if result.HasMore {
		meta.NextCursor = result.NextCursor
		meta.NextURL = nextCursorURL(r, result.NextCursor)
	}
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: result.Items})
}

func (h *handler) handleNewMessagesPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	limit, err := parseIntQuery(q.Get("limit"), defaultNewMessageLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	includeSource, err := parseBoolQuery(q.Get("source"), false, "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", err.Error())
		return
	}
	h.writeNewMessages(w, r, limit, includeSource, false, false)
}

func (h *handler) handleNewMessagesPoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	params, err := readActionParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	limit, err := parseIntQuery(params["limit"], defaultNewMessageLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	includeSource, err := parseBoolQuery(params["source"], false, "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", err.Error())
		return
	}
	h.writeNewMessages(w, r, limit, includeSource, true, false)
}

func (h *handler) handleNewMessagesReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	target, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	statePath, err := newMessagesStatePath(target.Account)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_path_failed", err.Error())
		return
	}
	if err := saveNewMessagesState(statePath, map[string]int64{}); err != nil {
		writeError(w, http.StatusInternalServerError, "state_reset_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse[ResetResult]{
		Meta:   h.actionMeta(info, "new_messages.reset"),
		Result: ResetResult{Reset: true, StatePath: statePath},
	})
}

func (h *handler) writeNewMessages(w http.ResponseWriter, r *http.Request, limit int, includeSource bool, commit bool, reset bool) {
	target, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	sessionPath, err := sessionCachePathReadOnly()
	if err != nil {
		writeQueryError(w, "session_cache_failed", err)
		return
	}
	statePath := newMessagesStatePathForAccount(target.Account)
	state, loaded, err := loadNewMessagesState(statePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_load_failed", err.Error())
		return
	}
	if reset {
		state = map[string]int64{}
		loaded = false
	}
	sessionList, err := sessions.NewService(sessionPath).List(r.Context(), sessions.QueryOptions{})
	if err != nil {
		writeQueryError(w, "sessions_query_failed", err)
		return
	}
	fallback := time.Now().Add(-24 * time.Hour).Unix()
	currentState := make(map[string]int64, len(sessionList))
	changedSince := map[string]int64{}
	for _, item := range sessionList {
		if item.Username == "" || item.LastTimestamp <= 0 {
			continue
		}
		currentState[item.Username] = item.LastTimestamp
		since := fallback
		if loaded {
			if previous, ok := state[item.Username]; ok {
				since = previous
			}
		}
		if item.LastTimestamp > since {
			changedSince[item.Username] = since
		}
	}
	cachePaths, err := messageCachePathsReadOnly()
	if err != nil {
		writeQueryError(w, "message_cache_failed", err)
		return
	}
	service := messages.NewService(cachePaths)
	var list []messages.Message
	for username, since := range changedSince {
		rows, err := service.List(r.Context(), messages.QueryOptions{
			Username:      username,
			Start:         since + 1,
			HasStart:      true,
			IncludeSource: includeSource,
		})
		if err != nil {
			writeQueryError(w, "new_messages_query_failed", err)
			return
		}
		list = append(list, rows...)
	}
	messages.ApplyChatInfo(list, loadExistingChatInfoMap(r.Context()))
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].CreateTime != list[j].CreateTime {
			return list[i].CreateTime < list[j].CreateTime
		}
		if list[i].Seq != list[j].Seq {
			return list[i].Seq < list[j].Seq
		}
		return list[i].ID < list[j].ID
	})
	page, hasMore := trimMessagePage(list, limit)
	enrichMessageMedia(target, page)
	if commit {
		newState := copyState(currentState)
		for username, since := range changedSince {
			newState[username] = since
		}
		for _, item := range page {
			if item.ChatUsername == "" || item.CreateTime <= 0 {
				continue
			}
			if item.CreateTime > newState[item.ChatUsername] {
				newState[item.ChatUsername] = item.CreateTime
			}
		}
		if err := saveNewMessagesState(statePath, newState); err != nil {
			writeError(w, http.StatusInternalServerError, "state_save_failed", err.Error())
			return
		}
	}
	meta := h.messageMeta(info, "new-messages", limit, len(page), queryExecutionMeta{})
	meta.Mode = "new_messages"
	meta.HasMore = &hasMore
	meta.FirstRun = boolPtr(!loaded)
	meta.Reset = boolPtr(reset)
	meta.ChangedChats = len(changedSince)
	meta.StatePath = statePath
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: page})
}

func (h *handler) handleFavorites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	limit, err := parseIntQuery(q.Get("limit"), defaultDataLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	target, cachePath, err := favoriteCachePathReadOnly()
	if err != nil {
		writeQueryError(w, "favorite_cache_failed", err)
		return
	}
	service := favorites.NewServiceWithDataDir(cachePath, target.DataDir)
	totalItems, err := service.List(r.Context(), favorites.QueryOptions{Type: q.Get("type"), Query: q.Get("query")})
	if err != nil {
		writeQueryError(w, "favorites_query_failed", err)
		return
	}
	items, err := service.List(r.Context(), favorites.QueryOptions{Type: q.Get("type"), Query: q.Get("query"), Limit: limit, Offset: offset})
	if err != nil {
		writeQueryError(w, "favorites_query_failed", err)
		return
	}
	total := len(totalItems)
	hasMore := offset+len(items) < total
	meta := h.listMeta(info, "favorites")
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(items)
	meta.Total = &total
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	writeJSON(w, http.StatusOK, ItemsResponse[favorites.Item]{Meta: meta, Items: items})
}

func (h *handler) handleArticleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	_, cachePath, info, err := h.contactCache(r.Context())
	if err != nil {
		writeQueryError(w, "contact_cache_failed", err)
		return
	}
	items, err := articles.NewService(cachePath, nil).Accounts(r.Context(), r.URL.Query().Get("query"))
	if err != nil {
		writeQueryError(w, "article_accounts_query_failed", err)
		return
	}
	total := len(items)
	meta := h.listMeta(info, "articles.accounts")
	meta.Returned = len(items)
	meta.Total = &total
	writeJSON(w, http.StatusOK, ItemsResponse[articles.Account]{Meta: meta, Items: items})
}

func (h *handler) handleArticles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	limit, err := parseIntQuery(q.Get("limit"), defaultDataLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	start, end, hasStart, hasEnd, err := parseMessageRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	_, contactPath, info, err := h.contactCache(r.Context())
	if err != nil {
		writeQueryError(w, "contact_cache_failed", err)
		return
	}
	messagePaths, err := articleCachePathsReadOnly()
	if err != nil {
		writeQueryError(w, "article_cache_failed", err)
		return
	}
	service := articles.NewService(contactPath, messagePaths)
	baseOpts := articles.QueryOptions{
		Username: strings.TrimSpace(q.Get("username")),
		Query:    strings.TrimSpace(q.Get("query")),
		Start:    start,
		End:      end,
		HasStart: hasStart,
		HasEnd:   hasEnd,
	}
	totalItems, err := service.List(r.Context(), baseOpts)
	if err != nil {
		writeQueryError(w, "articles_query_failed", err)
		return
	}
	pageOpts := baseOpts
	pageOpts.Limit = limit
	pageOpts.Offset = offset
	items, err := service.List(r.Context(), pageOpts)
	if err != nil {
		writeQueryError(w, "articles_query_failed", err)
		return
	}
	total := len(totalItems)
	hasMore := offset+len(items) < total
	meta := h.listMeta(info, "articles")
	meta.Timezone = localTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(items)
	meta.Total = &total
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	meta.Username = strings.TrimSpace(q.Get("username"))
	meta.Query = strings.TrimSpace(q.Get("query"))
	meta.Start = formatMetaTime(start, hasStart)
	meta.End = formatMetaTime(end, hasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[articles.Article]{Meta: meta, Items: items})
}

func (h *handler) handleSNSFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	limit, err := parseIntQuery(q.Get("limit"), defaultDataLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	start, end, hasStart, hasEnd, err := parseMessageRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	cachePath, err := snsCachePathReadOnly()
	if err != nil {
		writeQueryError(w, "sns_cache_failed", err)
		return
	}
	query := strings.TrimSpace(q.Get("query"))
	baseOpts := sns.QueryOptions{
		Username: strings.TrimSpace(q.Get("username")),
		Query:    query,
		Start:    start,
		End:      end,
		HasStart: hasStart,
		HasEnd:   hasEnd,
	}
	totalItems, err := sns.NewService(cachePath).Feed(r.Context(), baseOpts)
	if err != nil {
		writeQueryError(w, "sns_feed_query_failed", err)
		return
	}
	pageOpts := baseOpts
	pageOpts.Limit = limit
	pageOpts.Offset = offset
	items, err := sns.NewService(cachePath).Feed(r.Context(), pageOpts)
	if err != nil {
		writeQueryError(w, "sns_feed_query_failed", err)
		return
	}
	total := len(totalItems)
	hasMore := offset+len(items) < total
	resource := "sns.feed"
	if r.URL.Path == "/v1/sns/search" {
		resource = "sns.search"
	}
	meta := h.listMeta(info, resource)
	meta.Timezone = localTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(items)
	meta.Total = &total
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	meta.Username = strings.TrimSpace(q.Get("username"))
	meta.Query = query
	meta.Start = formatMetaTime(start, hasStart)
	meta.End = formatMetaTime(end, hasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[sns.Post]{Meta: meta, Items: items})
}

func (h *handler) handleSNSNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	q := r.URL.Query()
	limit, err := parseIntQuery(q.Get("limit"), defaultDataLimit, 0, maxInt(), "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(q.Get("offset"), 0, 0, maxInt(), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	includeRead, err := parseBoolQuery(q.Get("include_read"), false, "include_read")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_include_read", err.Error())
		return
	}
	start, end, hasStart, hasEnd, err := parseMessageRange(q.Get("date"), q.Get("start"), q.Get("end"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_time_range", err.Error())
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	cachePath, err := snsCachePathReadOnly()
	if err != nil {
		writeQueryError(w, "sns_cache_failed", err)
		return
	}
	baseOpts := sns.QueryOptions{Start: start, End: end, HasStart: hasStart, HasEnd: hasEnd, IncludeRead: includeRead}
	totalItems, err := sns.NewService(cachePath).Notifications(r.Context(), baseOpts)
	if err != nil {
		writeQueryError(w, "sns_notifications_query_failed", err)
		return
	}
	pageOpts := baseOpts
	pageOpts.Limit = limit
	pageOpts.Offset = offset
	items, err := sns.NewService(cachePath).Notifications(r.Context(), pageOpts)
	if err != nil {
		writeQueryError(w, "sns_notifications_query_failed", err)
		return
	}
	total := len(totalItems)
	hasMore := offset+len(items) < total
	meta := h.listMeta(info, "sns.notifications")
	meta.Timezone = localTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(items)
	meta.Total = &total
	meta.HasMore = &hasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, hasMore)
	meta.Start = formatMetaTime(start, hasStart)
	meta.End = formatMetaTime(end, hasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[sns.Notification]{Meta: meta, Items: items})
}
