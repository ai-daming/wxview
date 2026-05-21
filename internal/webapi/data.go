package webapi

import (
	"net/http"
	"strings"

	"wxview/internal/articles"
	"wxview/internal/contacts"
	"wxview/internal/favorites"
	"wxview/internal/messages"
	wxquery "wxview/internal/query"
	"wxview/internal/sessions"
	"wxview/internal/sns"
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
	AvatarURL    string `json:"avatar_url,omitempty"`
	AvatarReason string `json:"avatar_reason,omitempty"`
}

func (h *handler) queryRuntime() wxquery.Runtime {
	return wxquery.Runtime{
		DiscoverContact:  h.discoverContact,
		ContactCachePath: h.contactCachePath,
	}
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().ContactDetail(r.Context(), wxquery.ContactDetailRequest{
		Username:      username,
		ResolveAvatar: true,
		CacheMode:     wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "contact_detail_failed", err)
		return
	}
	meta := h.listMeta(info, "contacts.detail")
	total := 1
	meta.Total = &total
	meta.Returned = 1
	item := contactDetailItem(result.Item)
	if item.AvatarPath != "" {
		item.AvatarURL = mediaURL(item.AvatarPath)
	}
	writeJSON(w, http.StatusOK, ItemsResponse[ContactDetailItem]{
		Meta:  meta,
		Items: []ContactDetailItem{item},
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Members(r.Context(), wxquery.MembersRequest{
		Username:  username,
		Query:     query,
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "members_query_failed", err)
		return
	}
	meta := h.listMeta(info, "members")
	total := result.Group.Count
	meta.Total = &total
	meta.Returned = len(result.Group.Members)
	meta.GroupUsername = result.Group.Username
	meta.GroupDisplayName = result.Group.DisplayName
	meta.Owner = result.Group.Owner
	meta.OwnerDisplayName = result.Group.OwnerDisplayName
	writeJSON(w, http.StatusOK, ItemsResponse[contacts.Member]{Meta: meta, Items: result.Group.Members})
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
	result, err := h.queryRuntime().Sessions(r.Context(), wxquery.SessionsRequest{
		Kind:       kind,
		Query:      q.Get("query"),
		UnreadOnly: unreadOnly,
		Limit:      limit,
		Offset:     offset,
		CacheMode:  wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "sessions_query_failed", err)
		return
	}
	resource := "sessions"
	if unreadOnly {
		resource = "unread"
	}
	meta := h.listMeta(info, resource)
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	writeJSON(w, http.StatusOK, ItemsResponse[sessions.Session]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Messages(r.Context(), wxquery.MessagesRequest{
		Username:      username,
		Date:          q.Get("date"),
		StartText:     q.Get("start"),
		EndText:       q.Get("end"),
		AfterSeq:      afterSeq,
		Limit:         limit,
		Offset:        offset,
		IncludeSource: includeSource,
		NoIndex:       noIndex,
		WantHasMore:   true,
		CacheMode:     wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "messages_query_failed", err)
		return
	}
	meta := h.messageMeta(info, "messages", limit, len(result.Items), result.Execution)
	meta.Offset = intPtr(offset)
	meta.Username = username
	meta.Start = wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart)
	meta.End = wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd)
	meta.HasMore = &result.HasMore
	if result.HasMore && result.NextAfterSeq > 0 {
		meta.NextAfterSeq = result.NextAfterSeq
		meta.NextURL = nextMessagesURL(r, meta.NextAfterSeq, limit)
	}
	h.attachMediaURLs(result.Items)
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Search(r.Context(), wxquery.SearchRequest{
		Query:         query,
		Kind:          kind,
		ChatQuery:     chatQuery,
		Username:      username,
		Date:          q.Get("date"),
		StartText:     q.Get("start"),
		EndText:       q.Get("end"),
		Limit:         limit,
		Offset:        offset,
		IncludeSource: includeSource,
		CacheMode:     wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "search_failed", err)
		return
	}
	meta := h.messageMeta(info, "search", limit, len(result.Items), result.Execution)
	meta.Offset = intPtr(offset)
	meta.Query = query
	meta.Kind = kind
	meta.ChatQuery = chatQuery
	meta.Username = username
	meta.Start = wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart)
	meta.End = wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd)
	meta.TotalMatches = &result.TotalMatches
	meta.Total = &result.TotalMatches
	meta.MatchedChats = result.MatchedChats
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	h.attachMediaURLs(result.Items)
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Timeline(r.Context(), wxquery.TimelineRequest{
		Username:      username,
		Kind:          kind,
		KindProvided:  kindProvided,
		Query:         query,
		Date:          q.Get("date"),
		StartText:     q.Get("start"),
		EndText:       q.Get("end"),
		Limit:         limit,
		Cursor:        strings.TrimSpace(q.Get("cursor")),
		IncludeSource: includeSource,
		NoIndex:       noIndex,
		CacheMode:     wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "timeline_query_failed", err)
		return
	}
	meta := h.messageMeta(info, "timeline", limit, len(result.Items), result.Execution)
	meta.Username = username
	meta.Kind = ""
	meta.Query = ""
	if username == "" {
		meta.Kind = kind
		meta.Query = query
	}
	meta.Start = result.Range.StartLabel
	meta.End = result.Range.EndLabel
	meta.Cursor = strings.TrimSpace(q.Get("cursor"))
	meta.MatchedChats = result.MatchedChats
	meta.HasMore = &result.HasMore
	if result.HasMore {
		meta.NextCursor = result.NextCursor
		meta.NextURL = nextCursorURL(r, result.NextCursor)
	}
	h.attachMediaURLs(result.Items)
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().ResetNewMessages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state_reset_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse[ResetResult]{
		Meta:   h.actionMeta(info, "new_messages.reset"),
		Result: ResetResult{Reset: result.Reset, StatePath: result.StatePath},
	})
}

func (h *handler) writeNewMessages(w http.ResponseWriter, r *http.Request, limit int, includeSource bool, commit bool, reset bool) {
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().NewMessages(r.Context(), wxquery.NewMessagesRequest{
		Limit:         limit,
		IncludeSource: includeSource,
		Commit:        commit,
		Reset:         reset,
		CacheMode:     wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "new_messages_query_failed", err)
		return
	}
	meta := h.messageMeta(info, "new-messages", limit, len(result.Items), wxquery.ExecutionMeta{})
	meta.Mode = "new_messages"
	meta.HasMore = &result.HasMore
	meta.FirstRun = boolPtr(result.FirstRun)
	meta.Reset = boolPtr(reset)
	meta.ChangedChats = result.ChangedChats
	meta.StatePath = result.StatePath
	h.attachMediaURLs(result.Items)
	writeJSON(w, http.StatusOK, ItemsResponse[messages.Message]{Meta: meta, Items: result.Items})
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
	result, err := h.queryRuntime().Favorites(r.Context(), wxquery.FavoritesRequest{
		Type:      q.Get("type"),
		Query:     q.Get("query"),
		Limit:     limit,
		Offset:    offset,
		WantTotal: true,
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "favorites_query_failed", err)
		return
	}
	meta := h.listMeta(info, "favorites")
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	writeJSON(w, http.StatusOK, ItemsResponse[favorites.Item]{Meta: meta, Items: result.Items})
}

func (h *handler) handleArticleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().ArticleAccounts(r.Context(), wxquery.ArticleAccountsRequest{
		Query:     r.URL.Query().Get("query"),
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "article_accounts_query_failed", err)
		return
	}
	meta := h.listMeta(info, "articles.accounts")
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	writeJSON(w, http.StatusOK, ItemsResponse[articles.Account]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Articles(r.Context(), wxquery.ArticlesRequest{
		Username:  strings.TrimSpace(q.Get("username")),
		Query:     strings.TrimSpace(q.Get("query")),
		Date:      q.Get("date"),
		StartText: q.Get("start"),
		EndText:   q.Get("end"),
		Limit:     limit,
		Offset:    offset,
		WantTotal: true,
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "articles_query_failed", err)
		return
	}
	meta := h.listMeta(info, "articles")
	meta.Timezone = wxquery.LocalTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	meta.Username = strings.TrimSpace(q.Get("username"))
	meta.Query = strings.TrimSpace(q.Get("query"))
	meta.Start = wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart)
	meta.End = wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[articles.Article]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	query := strings.TrimSpace(q.Get("query"))
	result, err := h.queryRuntime().SNSFeed(r.Context(), wxquery.SNSFeedRequest{
		Username:  strings.TrimSpace(q.Get("username")),
		Query:     query,
		Date:      q.Get("date"),
		StartText: q.Get("start"),
		EndText:   q.Get("end"),
		Limit:     limit,
		Offset:    offset,
		WantTotal: true,
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "sns_feed_query_failed", err)
		return
	}
	resource := "sns.feed"
	if r.URL.Path == "/v1/sns/search" {
		resource = "sns.search"
	}
	meta := h.listMeta(info, resource)
	meta.Timezone = wxquery.LocalTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	meta.Username = strings.TrimSpace(q.Get("username"))
	meta.Query = query
	meta.Start = wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart)
	meta.End = wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[sns.Post]{Meta: meta, Items: result.Items})
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
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().SNSNotifications(r.Context(), wxquery.SNSNotificationsRequest{
		Date:        q.Get("date"),
		StartText:   q.Get("start"),
		EndText:     q.Get("end"),
		Limit:       limit,
		Offset:      offset,
		IncludeRead: includeRead,
		WantTotal:   true,
		CacheMode:   wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "sns_notifications_query_failed", err)
		return
	}
	meta := h.listMeta(info, "sns.notifications")
	meta.Timezone = wxquery.LocalTimezoneName()
	meta.Limit = intPtr(limit)
	meta.Offset = intPtr(offset)
	meta.Returned = len(result.Items)
	meta.Total = &result.Total
	meta.HasMore = &result.HasMore
	meta.NextURL = nextOffsetURL(r, limit, offset, result.HasMore)
	meta.Start = wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart)
	meta.End = wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd)
	writeJSON(w, http.StatusOK, ItemsResponse[sns.Notification]{Meta: meta, Items: result.Items})
}
