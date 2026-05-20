package query

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wxview/internal/app"
	"wxview/internal/articles"
	"wxview/internal/contacts"
	"wxview/internal/daemon"
	"wxview/internal/favorites"
	"wxview/internal/key"
	"wxview/internal/media"
	"wxview/internal/messages"
	"wxview/internal/msgindex"
	"wxview/internal/sessions"
	"wxview/internal/sns"
	"wxview/internal/timeline"
)

type Runtime struct {
	DiscoverContact  func() (key.TargetDB, error)
	ContactCachePath func(account string) (string, error)
	Now              func() time.Time
}

type CacheMode struct {
	Refresh  bool
	ReadOnly bool
}

type ExecutionMeta struct {
	QueryMode       string `json:"query_mode,omitempty"`
	IndexUsed       bool   `json:"index_used"`
	IndexStatus     string `json:"index_status,omitempty"`
	IndexReason     string `json:"index_reason,omitempty"`
	IndexLagSeconds int64  `json:"index_lag_seconds,omitempty"`
}

type TimeRange struct {
	Start    int64
	End      int64
	HasStart bool
	HasEnd   bool
}

type TimelineRange struct {
	Start      int64
	End        int64
	StartLabel string
	EndLabel   string
}

type ContactsRequest struct {
	Kind     string
	Query    string
	Username string
	Sort     string
	Limit    int
	Offset   int
	CacheMode
}

type ContactsResult struct {
	Target    key.TargetDB
	CachePath string
	Items     []contacts.Contact
	Total     int
	HasMore   bool
}

type ContactDetailRequest struct {
	Username      string
	ResolveAvatar bool
	CacheMode
}

type ContactDetailResult struct {
	Target key.TargetDB
	Item   contacts.Detail
}

type MembersRequest struct {
	Username string
	Query    string
	CacheMode
}

type MembersResult struct {
	Group contacts.GroupMembers
}

type SessionsRequest struct {
	Kind       string
	Query      string
	UnreadOnly bool
	Limit      int
	Offset     int
	CacheMode
}

type SessionsResult struct {
	Items   []sessions.Session
	Total   int
	HasMore bool
}

type MessagesRequest struct {
	Username      string
	Date          string
	StartText     string
	EndText       string
	AfterSeq      int64
	Limit         int
	Offset        int
	IncludeSource bool
	NoIndex       bool
	WantHasMore   bool
	CacheMode
}

type MessagesResult struct {
	Target       key.TargetDB
	Items        []messages.Message
	Range        TimeRange
	Execution    ExecutionMeta
	HasMore      bool
	NextAfterSeq int64
}

type SearchRequest struct {
	Query         string
	Kind          string
	ChatQuery     string
	Username      string
	Date          string
	StartText     string
	EndText       string
	Limit         int
	Offset        int
	IncludeSource bool
	CacheMode
}

type SearchResult struct {
	Items        []messages.Message
	Range        TimeRange
	Execution    ExecutionMeta
	TotalMatches int
	MatchedChats int
	HasMore      bool
}

type TimelineRequest struct {
	Username      string
	Kind          string
	KindProvided  bool
	Query         string
	Date          string
	StartText     string
	EndText       string
	Limit         int
	Cursor        string
	IncludeSource bool
	NoIndex       bool
	CacheMode
}

type TimelineResult struct {
	Items        []messages.Message
	Range        TimelineRange
	Execution    ExecutionMeta
	MatchedChats int
	HasMore      bool
	NextCursor   string
}

type NewMessagesRequest struct {
	Limit         int
	IncludeSource bool
	Commit        bool
	Reset         bool
	CacheMode
}

type NewMessagesResult struct {
	Target       key.TargetDB
	Items        []messages.Message
	FirstRun     bool
	Reset        bool
	Limit        int
	ChangedChats int
	StatePath    string
	HasMore      bool
}

type FavoritesRequest struct {
	Type      string
	Query     string
	Limit     int
	Offset    int
	WantTotal bool
	CacheMode
}

type FavoritesResult struct {
	Items    []favorites.Item
	Total    int
	HasTotal bool
	HasMore  bool
}

type ArticleAccountsRequest struct {
	Query string
	CacheMode
}

type ArticleAccountsResult struct {
	Items []articles.Account
	Total int
}

type ArticlesRequest struct {
	Username  string
	Query     string
	Date      string
	StartText string
	EndText   string
	Limit     int
	Offset    int
	WantTotal bool
	CacheMode
}

type ArticlesResult struct {
	Items    []articles.Article
	Range    TimeRange
	Total    int
	HasTotal bool
	HasMore  bool
}

type SNSFeedRequest struct {
	Username  string
	Query     string
	Date      string
	StartText string
	EndText   string
	Limit     int
	Offset    int
	WantTotal bool
	CacheMode
}

type SNSFeedResult struct {
	Items    []sns.Post
	Range    TimeRange
	Total    int
	HasTotal bool
	HasMore  bool
}

type SNSNotificationsRequest struct {
	Date        string
	StartText   string
	EndText     string
	Limit       int
	Offset      int
	IncludeRead bool
	WantTotal   bool
	CacheMode
}

type SNSNotificationsResult struct {
	Items    []sns.Notification
	Range    TimeRange
	Total    int
	HasTotal bool
	HasMore  bool
}

type RefreshRequest struct {
	Scope string
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

type refreshStep struct {
	Name   string
	Action string
	Ensure func(context.Context) error
}

func NewRuntime() Runtime {
	return Runtime{}
}

func (rt Runtime) withDefaults() Runtime {
	if rt.DiscoverContact == nil {
		rt.DiscoverContact = key.DiscoverContactDB
	}
	if rt.ContactCachePath == nil {
		rt.ContactCachePath = key.ContactCachePath
	}
	if rt.Now == nil {
		rt.Now = time.Now
	}
	return rt
}

func (rt Runtime) Contacts(ctx context.Context, req ContactsRequest) (ContactsResult, error) {
	if req.Kind == "" {
		req.Kind = contacts.KindAll
	}
	if req.Sort == "" {
		req.Sort = "username"
	}
	target, cachePath, err := rt.ContactCache(ctx, req.CacheMode)
	if err != nil {
		return ContactsResult{}, err
	}
	list, err := contacts.NewService(cachePath).List(ctx)
	if err != nil {
		return ContactsResult{}, err
	}
	filterOpts := contacts.QueryOptions{Kind: req.Kind, Query: req.Query, Username: req.Username, Sort: req.Sort}
	filtered := contacts.ApplyQueryOptions(list, filterOpts)
	pageOpts := filterOpts
	pageOpts.Limit = req.Limit
	pageOpts.Offset = req.Offset
	page := contacts.ApplyQueryOptions(list, pageOpts)
	return ContactsResult{
		Target:    target,
		CachePath: cachePath,
		Items:     page,
		Total:     len(filtered),
		HasMore:   req.Offset+len(page) < len(filtered),
	}, nil
}

func (rt Runtime) ContactDetail(ctx context.Context, req ContactDetailRequest) (ContactDetailResult, error) {
	target, cachePath, err := rt.ContactCache(ctx, req.CacheMode)
	if err != nil {
		return ContactDetailResult{}, err
	}
	item, err := contacts.NewService(cachePath).Detail(ctx, req.Username)
	if err != nil {
		return ContactDetailResult{}, err
	}
	if req.ResolveAvatar {
		if headPath, ok := rt.HeadImageCachePath(ctx, req.CacheMode); ok {
			if cacheDir, err := media.MediaCacheDir(target.Account); err == nil {
				avatar := media.ResolveAvatar(headPath, item.Username, cacheDir)
				item.AvatarStatus = avatar.Status
				item.AvatarPath = avatar.Path
				item.AvatarReason = avatar.Reason
			}
		}
	}
	return ContactDetailResult{Target: target, Item: item}, nil
}

func (rt Runtime) Members(ctx context.Context, req MembersRequest) (MembersResult, error) {
	_, cachePath, err := rt.ContactCache(ctx, req.CacheMode)
	if err != nil {
		return MembersResult{}, err
	}
	username := strings.TrimSpace(req.Username)
	query := strings.TrimSpace(req.Query)
	if username == "" && query == "" {
		return MembersResult{}, fmt.Errorf("username or query is required")
	}
	if username != "" && query != "" {
		return MembersResult{}, fmt.Errorf("username cannot be combined with query")
	}
	service := contacts.NewService(cachePath)
	if query != "" {
		list, err := service.List(ctx)
		if err != nil {
			return MembersResult{}, err
		}
		matched := contacts.ApplyQueryOptions(list, contacts.QueryOptions{Kind: contacts.KindChatroom, Query: query})
		if len(matched) != 1 {
			return MembersResult{}, fmt.Errorf("query matched %d chatrooms; use username for an exact chatroom", len(matched))
		}
		username = matched[0].Username
	}
	result, err := service.Members(ctx, username)
	if err != nil {
		return MembersResult{}, err
	}
	return MembersResult{Group: result}, nil
}

func (rt Runtime) Sessions(ctx context.Context, req SessionsRequest) (SessionsResult, error) {
	if req.Kind == "" {
		req.Kind = contacts.KindAll
	}
	sessionPath, err := rt.SessionCachePath(ctx, req.CacheMode)
	if err != nil {
		return SessionsResult{}, err
	}
	list, err := sessions.NewService(sessionPath).List(ctx, sessions.QueryOptions{UnreadOnly: req.UnreadOnly})
	if err != nil {
		return SessionsResult{}, err
	}
	sessions.ApplyChatInfo(list, rt.LoadExistingChatInfoMap(ctx))
	filtered := sessions.ApplyQueryOptions(list, sessions.QueryOptions{Kind: req.Kind, Query: req.Query})
	page := sessions.ApplyQueryOptions(list, sessions.QueryOptions{Kind: req.Kind, Query: req.Query, Limit: req.Limit, Offset: req.Offset})
	return SessionsResult{
		Items:   page,
		Total:   len(filtered),
		HasMore: req.Offset+len(page) < len(filtered),
	}, nil
}

func (rt Runtime) Messages(ctx context.Context, req MessagesRequest) (MessagesResult, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return MessagesResult{}, fmt.Errorf("username is required")
	}
	rangeValue, err := ParseMessageRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return MessagesResult{}, err
	}
	target, err := rt.ContactTarget()
	if err != nil {
		return MessagesResult{}, err
	}
	cachePaths, err := rt.MessageCachePaths(ctx, req.CacheMode)
	if err != nil {
		return MessagesResult{}, err
	}
	queryLimit := req.Limit
	if req.WantHasMore && req.Limit > 0 {
		queryLimit = req.Limit + 1
	}
	service := messages.NewService(cachePaths)
	list, execution, err := ListMessagesMaybeIndex(ctx, target.Account, cachePaths, service, !req.NoIndex, messages.QueryOptions{
		Username:      username,
		Start:         rangeValue.Start,
		End:           rangeValue.End,
		AfterSeq:      req.AfterSeq,
		HasStart:      rangeValue.HasStart,
		HasEnd:        rangeValue.HasEnd,
		HasAfterSeq:   req.AfterSeq > 0,
		IncludeSource: req.IncludeSource,
		Limit:         queryLimit,
		Offset:        req.Offset,
	})
	if err != nil {
		return MessagesResult{}, err
	}
	rt.EnrichMessageMedia(target, list)
	messages.ApplyChatInfo(list, rt.ChatInfoMapForUsernames(ctx, username))
	page, hasMore := trimMessagePage(list, req.Limit)
	nextAfterSeq := int64(0)
	if hasMore && len(page) > 0 {
		nextAfterSeq = page[len(page)-1].Seq
	}
	return MessagesResult{Target: target, Items: page, Range: rangeValue, Execution: execution, HasMore: hasMore, NextAfterSeq: nextAfterSeq}, nil
}

func (rt Runtime) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	queryText := strings.TrimSpace(req.Query)
	if queryText == "" {
		return SearchResult{}, fmt.Errorf("query is required")
	}
	if req.Kind == "" {
		req.Kind = contacts.KindAll
	}
	if strings.TrimSpace(req.Username) != "" && strings.TrimSpace(req.ChatQuery) != "" {
		return SearchResult{}, fmt.Errorf("username cannot be combined with chat_query")
	}
	rangeValue, err := ParseMessageRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return SearchResult{}, err
	}
	target, err := rt.ContactTarget()
	if err != nil {
		return SearchResult{}, err
	}
	chats, matchedChats, err := rt.SelectSearchChats(ctx, req.Username, req.Kind, req.ChatQuery, req.Refresh)
	if err != nil {
		return SearchResult{}, err
	}
	cachePaths, err := rt.MessageCachePaths(ctx, req.CacheMode)
	if err != nil {
		return SearchResult{}, err
	}
	list, total, err := messages.NewService(cachePaths).Search(ctx, messages.SearchOptions{
		Chats:         chats,
		Query:         queryText,
		Start:         rangeValue.Start,
		End:           rangeValue.End,
		HasStart:      rangeValue.HasStart,
		HasEnd:        rangeValue.HasEnd,
		IncludeSource: req.IncludeSource,
		Limit:         req.Limit,
		Offset:        req.Offset,
	})
	if err != nil {
		return SearchResult{}, err
	}
	rt.EnrichMessageMedia(target, list)
	return SearchResult{
		Items:        list,
		Range:        rangeValue,
		Execution:    ScanExecution("index_not_supported_for_search"),
		TotalMatches: total,
		MatchedChats: matchedChats,
		HasMore:      req.Offset+len(list) < total,
	}, nil
}

func (rt Runtime) Timeline(ctx context.Context, req TimelineRequest) (TimelineResult, error) {
	if req.Kind == "" {
		req.Kind = contacts.KindAll
	}
	username := strings.TrimSpace(req.Username)
	queryText := strings.TrimSpace(req.Query)
	if username != "" && (req.KindProvided || queryText != "") {
		return TimelineResult{}, fmt.Errorf("username cannot be combined with kind or query")
	}
	if username == "" && !req.KindProvided && queryText == "" {
		return TimelineResult{}, fmt.Errorf("timeline requires username, kind, or query")
	}
	rangeValue, err := ParseTimelineRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return TimelineResult{}, err
	}
	target, err := rt.ContactTarget()
	if err != nil {
		return TimelineResult{}, err
	}
	chats, matchedChats, err := rt.SelectTimelineChats(ctx, username, req.Kind, queryText, req.KindProvided, req.Refresh)
	if err != nil {
		return TimelineResult{}, err
	}
	cachePaths, err := rt.MessageCachePaths(ctx, req.CacheMode)
	if err != nil {
		return TimelineResult{}, err
	}
	queryIdentity := timeline.QueryIdentity{Username: username, Start: rangeValue.Start, End: rangeValue.End}
	if username == "" {
		queryIdentity.Kind = req.Kind
		queryIdentity.Query = queryText
	}
	queryHash, err := timeline.QueryHash(queryIdentity)
	if err != nil {
		return TimelineResult{}, err
	}
	service := messages.NewService(cachePaths)
	result, execution, err := TimelineMaybeIndex(ctx, target.Account, cachePaths, service, !req.NoIndex, timeline.QueryOptions{
		Chats:         chats,
		Start:         rangeValue.Start,
		End:           rangeValue.End,
		Limit:         req.Limit,
		Cursor:        strings.TrimSpace(req.Cursor),
		QueryHash:     queryHash,
		IncludeSource: req.IncludeSource,
	})
	if err != nil {
		return TimelineResult{}, err
	}
	rt.EnrichMessageMedia(target, result.Items)
	return TimelineResult{
		Items:        result.Items,
		Range:        rangeValue,
		Execution:    execution,
		MatchedChats: matchedChats,
		HasMore:      result.HasMore,
		NextCursor:   result.NextCursor,
	}, nil
}

func (rt Runtime) NewMessages(ctx context.Context, req NewMessagesRequest) (NewMessagesResult, error) {
	target, err := rt.ContactTarget()
	if err != nil {
		return NewMessagesResult{}, err
	}
	sessionPath, err := rt.SessionCachePath(ctx, req.CacheMode)
	if err != nil {
		return NewMessagesResult{}, err
	}
	statePath, err := rt.NewMessagesStatePath(target.Account, !req.Commit)
	if err != nil {
		return NewMessagesResult{}, err
	}
	state, loaded, err := loadNewMessagesState(statePath)
	if err != nil {
		return NewMessagesResult{}, err
	}
	if req.Reset {
		state = map[string]int64{}
		loaded = false
	}
	sessionList, err := sessions.NewService(sessionPath).List(ctx, sessions.QueryOptions{})
	if err != nil {
		return NewMessagesResult{}, err
	}
	now := rt.withDefaults().Now()
	fallback := now.Add(-24 * time.Hour).Unix()
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
	cachePaths, err := rt.MessageCachePaths(ctx, req.CacheMode)
	if err != nil {
		return NewMessagesResult{}, err
	}
	service := messages.NewService(cachePaths)
	var list []messages.Message
	for username, since := range changedSince {
		rows, err := service.List(ctx, messages.QueryOptions{
			Username:      username,
			Start:         since + 1,
			HasStart:      true,
			IncludeSource: req.IncludeSource,
		})
		if err != nil {
			return NewMessagesResult{}, err
		}
		list = append(list, rows...)
	}
	messages.ApplyChatInfo(list, rt.LoadExistingChatInfoMap(ctx))
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].CreateTime != list[j].CreateTime {
			return list[i].CreateTime < list[j].CreateTime
		}
		if list[i].Seq != list[j].Seq {
			return list[i].Seq < list[j].Seq
		}
		return list[i].ID < list[j].ID
	})
	page, hasMore := trimMessagePage(list, req.Limit)
	rt.EnrichMessageMedia(target, page)
	if req.Commit {
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
			return NewMessagesResult{}, err
		}
	}
	return NewMessagesResult{
		Target:       target,
		Items:        page,
		FirstRun:     !loaded,
		Reset:        req.Reset,
		Limit:        req.Limit,
		ChangedChats: len(changedSince),
		StatePath:    statePath,
		HasMore:      hasMore,
	}, nil
}

func (rt Runtime) Favorites(ctx context.Context, req FavoritesRequest) (FavoritesResult, error) {
	target, cachePath, err := rt.FavoriteCachePath(ctx, req.CacheMode)
	if err != nil {
		return FavoritesResult{}, err
	}
	service := favorites.NewServiceWithDataDir(cachePath, target.DataDir)
	var total int
	if req.WantTotal {
		items, err := service.List(ctx, favorites.QueryOptions{Type: req.Type, Query: req.Query})
		if err != nil {
			return FavoritesResult{}, err
		}
		total = len(items)
	}
	items, err := service.List(ctx, favorites.QueryOptions{Type: req.Type, Query: req.Query, Limit: req.Limit, Offset: req.Offset})
	if err != nil {
		return FavoritesResult{}, err
	}
	return FavoritesResult{Items: items, Total: total, HasTotal: req.WantTotal, HasMore: req.WantTotal && req.Offset+len(items) < total}, nil
}

func (rt Runtime) ArticleAccounts(ctx context.Context, req ArticleAccountsRequest) (ArticleAccountsResult, error) {
	_, contactPath, err := rt.ContactCache(ctx, req.CacheMode)
	if err != nil {
		return ArticleAccountsResult{}, err
	}
	items, err := articles.NewService(contactPath, nil).Accounts(ctx, req.Query)
	if err != nil {
		return ArticleAccountsResult{}, err
	}
	return ArticleAccountsResult{Items: items, Total: len(items)}, nil
}

func (rt Runtime) Articles(ctx context.Context, req ArticlesRequest) (ArticlesResult, error) {
	rangeValue, err := ParseMessageRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return ArticlesResult{}, err
	}
	_, contactPath, err := rt.ContactCache(ctx, req.CacheMode)
	if err != nil {
		return ArticlesResult{}, err
	}
	messagePaths, err := rt.ArticleCachePaths(ctx, req.CacheMode)
	if err != nil {
		return ArticlesResult{}, err
	}
	service := articles.NewService(contactPath, messagePaths)
	baseOpts := articles.QueryOptions{
		Username: strings.TrimSpace(req.Username),
		Query:    strings.TrimSpace(req.Query),
		Start:    rangeValue.Start,
		End:      rangeValue.End,
		HasStart: rangeValue.HasStart,
		HasEnd:   rangeValue.HasEnd,
	}
	var total int
	if req.WantTotal {
		items, err := service.List(ctx, baseOpts)
		if err != nil {
			return ArticlesResult{}, err
		}
		total = len(items)
	}
	pageOpts := baseOpts
	pageOpts.Limit = req.Limit
	pageOpts.Offset = req.Offset
	items, err := service.List(ctx, pageOpts)
	if err != nil {
		return ArticlesResult{}, err
	}
	return ArticlesResult{Items: items, Range: rangeValue, Total: total, HasTotal: req.WantTotal, HasMore: req.WantTotal && req.Offset+len(items) < total}, nil
}

func (rt Runtime) SNSFeed(ctx context.Context, req SNSFeedRequest) (SNSFeedResult, error) {
	rangeValue, err := ParseMessageRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return SNSFeedResult{}, err
	}
	cachePath, err := rt.SNSCachePath(ctx, req.CacheMode)
	if err != nil {
		return SNSFeedResult{}, err
	}
	service := sns.NewService(cachePath)
	baseOpts := sns.QueryOptions{Username: strings.TrimSpace(req.Username), Query: strings.TrimSpace(req.Query), Start: rangeValue.Start, End: rangeValue.End, HasStart: rangeValue.HasStart, HasEnd: rangeValue.HasEnd}
	var total int
	if req.WantTotal {
		items, err := service.Feed(ctx, baseOpts)
		if err != nil {
			return SNSFeedResult{}, err
		}
		total = len(items)
	}
	pageOpts := baseOpts
	pageOpts.Limit = req.Limit
	pageOpts.Offset = req.Offset
	items, err := service.Feed(ctx, pageOpts)
	if err != nil {
		return SNSFeedResult{}, err
	}
	return SNSFeedResult{Items: items, Range: rangeValue, Total: total, HasTotal: req.WantTotal, HasMore: req.WantTotal && req.Offset+len(items) < total}, nil
}

func (rt Runtime) SNSNotifications(ctx context.Context, req SNSNotificationsRequest) (SNSNotificationsResult, error) {
	rangeValue, err := ParseMessageRange(req.Date, req.StartText, req.EndText, rt.withDefaults().Now())
	if err != nil {
		return SNSNotificationsResult{}, err
	}
	cachePath, err := rt.SNSCachePath(ctx, req.CacheMode)
	if err != nil {
		return SNSNotificationsResult{}, err
	}
	service := sns.NewService(cachePath)
	baseOpts := sns.QueryOptions{Start: rangeValue.Start, End: rangeValue.End, HasStart: rangeValue.HasStart, HasEnd: rangeValue.HasEnd, IncludeRead: req.IncludeRead}
	var total int
	if req.WantTotal {
		items, err := service.Notifications(ctx, baseOpts)
		if err != nil {
			return SNSNotificationsResult{}, err
		}
		total = len(items)
	}
	pageOpts := baseOpts
	pageOpts.Limit = req.Limit
	pageOpts.Offset = req.Offset
	items, err := service.Notifications(ctx, pageOpts)
	if err != nil {
		return SNSNotificationsResult{}, err
	}
	return SNSNotificationsResult{Items: items, Range: rangeValue, Total: total, HasTotal: req.WantTotal, HasMore: req.WantTotal && req.Offset+len(items) < total}, nil
}

func (rt Runtime) ResetNewMessages(ctx context.Context) (ResetResult, error) {
	target, err := rt.ContactTarget()
	if err != nil {
		return ResetResult{}, err
	}
	statePath, err := rt.NewMessagesStatePath(target.Account, false)
	if err != nil {
		return ResetResult{}, err
	}
	if err := saveNewMessagesState(statePath, map[string]int64{}); err != nil {
		return ResetResult{}, err
	}
	return ResetResult{Reset: true, StatePath: statePath}, nil
}

func (rt Runtime) CacheRefresh(ctx context.Context, req RefreshRequest) (RefreshResult, error) {
	scope, err := NormalizeRefreshScope(req.Scope)
	if err != nil {
		return RefreshResult{}, err
	}
	steps, ok := refreshStepsForScope(scope)
	if !ok {
		return RefreshResult{}, fmt.Errorf("scope must be all, contacts, sessions, messages, favorites, articles, or sns")
	}
	start := time.Now()
	result := RefreshResult{Scope: scope}
	client, clientErr := daemonClient()
	daemonHealthy := clientErr == nil && client.Healthy(ctx)
	result.UsedDaemon = daemonHealthy
	for _, step := range steps {
		if runRefreshStep(ctx, client, daemonHealthy, step, &result) {
			result.Refreshed = append(result.Refreshed, step.Name)
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	return result, nil
}

func NormalizeRefreshScope(scope string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "all"
	}
	if _, ok := refreshStepsForScope(scope); !ok {
		return "", fmt.Errorf("scope must be all, contacts, sessions, messages, favorites, articles, or sns")
	}
	return scope, nil
}

func (rt Runtime) ContactTarget() (key.TargetDB, error) {
	rt = rt.withDefaults()
	return rt.DiscoverContact()
}

func (rt Runtime) ContactCache(ctx context.Context, mode CacheMode) (key.TargetDB, string, error) {
	rt = rt.withDefaults()
	if mode.Refresh {
		if err := refreshContactCache(ctx); err != nil {
			return key.TargetDB{}, "", err
		}
	}
	target, err := rt.DiscoverContact()
	if err != nil {
		return key.TargetDB{}, "", err
	}
	cachePath, err := rt.ContactCachePath(target.Account)
	if err != nil {
		return key.TargetDB{}, "", err
	}
	if _, err := os.Stat(cachePath); err == nil {
		return target, cachePath, nil
	} else if !os.IsNotExist(err) {
		return key.TargetDB{}, "", err
	}
	if mode.ReadOnly {
		return key.TargetDB{}, "", fmt.Errorf("contact cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	res, path, err := key.EnsureContactCache(ctx)
	if err != nil {
		return key.TargetDB{}, "", err
	}
	return res.Target, path, nil
}

func (rt Runtime) SessionCachePath(ctx context.Context, mode CacheMode) (string, error) {
	_, path, err := rt.SessionCacheTargetPath(ctx, mode)
	return path, err
}

func (rt Runtime) SessionCacheTargetPath(ctx context.Context, mode CacheMode) (key.TargetDB, string, error) {
	if mode.Refresh {
		return refreshSessionCache(ctx)
	}
	if target, path, ok := key.SessionCachePathIfExists(); ok {
		return target, path, nil
	}
	if mode.ReadOnly {
		return key.TargetDB{}, "", fmt.Errorf("session cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	res, path, err := key.EnsureSessionCache(ctx)
	return res.Target, path, err
}

func (rt Runtime) MessageCachePaths(ctx context.Context, mode CacheMode) ([]string, error) {
	if mode.Refresh {
		if err := refreshMessageCaches(ctx); err != nil {
			return nil, err
		}
	}
	paths, allExist, err := key.MessageCachePaths()
	if err != nil {
		return nil, err
	}
	if allExist && len(paths) > 0 {
		return paths, nil
	}
	if mode.ReadOnly {
		return nil, fmt.Errorf("message cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	return key.EnsureMessageCaches(ctx)
}

func (rt Runtime) ArticleCachePaths(ctx context.Context, mode CacheMode) ([]string, error) {
	paths, err := rt.MessageCachePaths(ctx, mode)
	if err != nil {
		return nil, err
	}
	bizPaths, bizExist, err := key.BizMessageCachePaths()
	if err != nil {
		return nil, err
	}
	if !bizExist && !mode.ReadOnly {
		_ = ensureBizMessageCachesBestEffort(ctx)
		bizPaths, _, _ = key.BizMessageCachePaths()
	}
	for _, path := range bizPaths {
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (rt Runtime) FavoriteCachePath(ctx context.Context, mode CacheMode) (key.TargetDB, string, error) {
	if mode.Refresh {
		return refreshFavoriteCache(ctx)
	}
	if target, path, ok := key.FavoriteCachePathIfExists(); ok {
		return target, path, nil
	}
	if mode.ReadOnly {
		return key.TargetDB{}, "", fmt.Errorf("favorite cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	res, path, err := key.EnsureFavoriteCache(ctx)
	return res.Target, path, err
}

func (rt Runtime) SNSCachePath(ctx context.Context, mode CacheMode) (string, error) {
	if mode.Refresh {
		return refreshSNSCache(ctx)
	}
	if _, path, ok := key.SNSCachePathIfExists(); ok {
		return path, nil
	}
	if mode.ReadOnly {
		return "", fmt.Errorf("sns cache does not exist; run `wxview init` or POST /v1/cache/refresh first")
	}
	_, path, err := key.EnsureSNSCache(ctx)
	return path, err
}

func HeadImageCachePathReadOnly() (string, bool) {
	_, path, ok := key.HeadImageCachePathIfExists()
	return path, ok
}

func (rt Runtime) HeadImageCachePath(ctx context.Context, mode CacheMode) (string, bool) {
	if mode.Refresh && !mode.ReadOnly {
		if client, err := daemonClient(); err == nil && client.Healthy(ctx) {
			_, _ = client.Call(ctx, daemon.ActionRefreshAvatars)
		}
		if _, path, err := key.EnsureHeadImageCache(ctx); err == nil {
			return path, true
		}
		return "", false
	}
	if _, path, ok := key.HeadImageCachePathIfExists(); ok {
		return path, true
	}
	if mode.ReadOnly {
		return "", false
	}
	if _, path, err := key.EnsureHeadImageCache(ctx); err == nil {
		return path, true
	}
	return "", false
}

func (rt Runtime) LoadExistingChatInfoMap(ctx context.Context) map[string]messages.ChatInfo {
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

func (rt Runtime) ChatInfoForUsername(ctx context.Context, username string) messages.ChatInfo {
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

func (rt Runtime) ChatInfoMapForUsernames(ctx context.Context, usernames ...string) map[string]messages.ChatInfo {
	out := make(map[string]messages.ChatInfo, len(usernames))
	for _, username := range usernames {
		info := rt.ChatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) != "" {
			out[info.Username] = info
		}
	}
	return out
}

func (rt Runtime) SelectSearchChats(ctx context.Context, username string, kind string, queryText string, refresh bool) ([]messages.ChatInfo, int, error) {
	if strings.TrimSpace(username) != "" {
		if refresh {
			if err := refreshContactCache(ctx); err != nil {
				return nil, 0, err
			}
		}
		info := rt.ChatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) == "" {
			info = messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
		}
		return []messages.ChatInfo{info}, 1, nil
	}
	list, err := rt.listContacts(ctx, CacheMode{Refresh: refresh, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	selected := contacts.ApplyQueryOptions(list, contacts.QueryOptions{Kind: kind, Query: queryText, Sort: "username"})
	chats := make([]messages.ChatInfo, 0, len(selected))
	for _, contact := range selected {
		chats = append(chats, chatInfoFromContact(contact))
	}
	return chats, len(chats), nil
}

func (rt Runtime) SelectTimelineChats(ctx context.Context, username string, kind string, queryText string, kindProvided bool, refresh bool) ([]messages.ChatInfo, int, error) {
	if strings.TrimSpace(username) != "" {
		if refresh {
			if err := refreshContactCache(ctx); err != nil {
				return nil, 0, err
			}
		}
		info := rt.ChatInfoForUsername(ctx, username)
		if strings.TrimSpace(info.Username) == "" {
			info = messages.ChatInfo{Username: username, Kind: messages.ChatKindUnknown, DisplayName: username}
		}
		return []messages.ChatInfo{info}, 1, nil
	}
	list, err := rt.listContacts(ctx, CacheMode{Refresh: refresh, ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	opts := contacts.QueryOptions{Kind: kind, Query: queryText, Sort: "username"}
	if !kindProvided && strings.TrimSpace(queryText) != "" {
		opts.Kind = contacts.KindAll
	}
	selected := contacts.ApplyQueryOptions(list, opts)
	chats := make([]messages.ChatInfo, 0, len(selected))
	for _, contact := range selected {
		chats = append(chats, chatInfoFromContact(contact))
	}
	return chats, len(chats), nil
}

func (rt Runtime) listContacts(ctx context.Context, mode CacheMode) ([]contacts.Contact, error) {
	_, path, err := rt.ContactCache(ctx, mode)
	if err != nil {
		return nil, err
	}
	return contacts.NewService(path).List(ctx)
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

func (rt Runtime) EnrichMessageMedia(target key.TargetDB, list []messages.Message) {
	if len(list) == 0 {
		return
	}
	resolver, err := mediaResolverFor(target)
	if err != nil {
		return
	}
	messages.EnrichMediaDetails(list, resolver)
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

func ListMessagesMaybeIndex(ctx context.Context, account string, cachePaths []string, service messages.Service, useIndex bool, opts messages.QueryOptions) ([]messages.Message, ExecutionMeta, error) {
	if !useIndex {
		list, err := service.List(ctx, opts)
		return list, ScanExecution("index_disabled_by_flag"), err
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

func TimelineMaybeIndex(ctx context.Context, account string, cachePaths []string, service messages.Service, useIndex bool, opts timeline.QueryOptions) (timeline.Result, ExecutionMeta, error) {
	if !useIndex {
		result, err := timeline.List(ctx, service, opts)
		return result, ScanExecution("index_disabled_by_flag"), err
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

func indexExecution(usability msgindex.Usability) ExecutionMeta {
	return ExecutionMeta{
		QueryMode:       msgindex.QueryModeIndex,
		IndexUsed:       true,
		IndexStatus:     usability.Status.Status,
		IndexReason:     firstNonEmpty(usability.Reason, "index_usable"),
		IndexLagSeconds: usability.Status.LagSeconds,
	}
}

func ScanExecution(reason string) ExecutionMeta {
	return ExecutionMeta{QueryMode: msgindex.QueryModeScan, IndexUsed: false, IndexReason: reason}
}

func scanExecutionFromUsability(usability msgindex.Usability) ExecutionMeta {
	meta := ScanExecution(firstNonEmpty(usability.Reason, "index_unusable"))
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

func ParseMessageRange(dateText string, startText string, endText string, now time.Time) (TimeRange, error) {
	dateText = strings.TrimSpace(dateText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if dateText != "" && (startText != "" || endText != "") {
		return TimeRange{}, fmt.Errorf("date cannot be combined with start or end")
	}
	if dateText != "" {
		start, end, err := parseDateRange(dateText, now)
		if err != nil {
			return TimeRange{}, err
		}
		return TimeRange{Start: start, End: end, HasStart: true, HasEnd: true}, nil
	}
	start, hasStart, err := parseTimeBound(startText, "start", false)
	if err != nil {
		return TimeRange{}, err
	}
	end, hasEnd, err := parseTimeBound(endText, "end", true)
	if err != nil {
		return TimeRange{}, err
	}
	if hasStart && hasEnd && start > end {
		return TimeRange{}, fmt.Errorf("start must not be later than end")
	}
	return TimeRange{Start: start, End: end, HasStart: hasStart, HasEnd: hasEnd}, nil
}

func ParseTimelineRange(dateText string, startText string, endText string, now time.Time) (TimelineRange, error) {
	dateText = strings.TrimSpace(dateText)
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)
	if dateText != "" && (startText != "" || endText != "") {
		return TimelineRange{}, fmt.Errorf("date cannot be combined with start or end")
	}
	if dateText != "" {
		start, end, err := parseDateRange(dateText, now)
		if err != nil {
			return TimelineRange{}, err
		}
		return TimelineRange{Start: start, End: end, StartLabel: FormatMetaTime(start, true), EndLabel: FormatMetaTime(end, true)}, nil
	}
	if startText == "" || endText == "" {
		return TimelineRange{}, fmt.Errorf("timeline requires date or both start and end")
	}
	start, hasStart, err := parseTimeBound(startText, "start", false)
	if err != nil {
		return TimelineRange{}, err
	}
	end, hasEnd, err := parseTimeBound(endText, "end", true)
	if err != nil {
		return TimelineRange{}, err
	}
	if !hasStart || !hasEnd {
		return TimelineRange{}, fmt.Errorf("timeline requires both start and end")
	}
	if start > end {
		return TimelineRange{}, fmt.Errorf("start must not be later than end")
	}
	return TimelineRange{Start: start, End: end, StartLabel: FormatMetaTime(start, true), EndLabel: FormatMetaTime(end, true)}, nil
}

func parseDateRange(value string, now time.Time) (int64, int64, error) {
	value = strings.TrimSpace(value)
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

func FormatMetaTime(ts int64, ok bool) string {
	if !ok || ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04:05")
}

func LocalTimezoneName() string {
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

func (rt Runtime) NewMessagesStatePath(account string, readOnly bool) (string, error) {
	if readOnly {
		base, err := app.BaseDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "cache", app.SafeAccountDir(account), "state", "new_messages.json"), nil
	}
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

func refreshContactCache(ctx context.Context) error {
	client, err := daemonClient()
	if err != nil {
		return err
	}
	if client.Healthy(ctx) {
		_, err := client.Call(ctx, daemon.ActionRefreshContacts)
		return err
	}
	_, _, err = key.EnsureContactCache(ctx)
	return err
}

func refreshSessionCache(ctx context.Context) (key.TargetDB, string, error) {
	client, err := daemonClient()
	if err != nil {
		return key.TargetDB{}, "", err
	}
	if client.Healthy(ctx) {
		if _, err := client.Call(ctx, daemon.ActionRefreshSessions); err != nil && !isUnknownDaemonAction(err) {
			return key.TargetDB{}, "", err
		}
		if target, path, ok := key.SessionCachePathIfExists(); ok {
			return target, path, nil
		}
		return key.TargetDB{}, "", fmt.Errorf("session cache was not found after daemon refresh")
	}
	res, path, err := key.EnsureSessionCache(ctx)
	return res.Target, path, err
}

func refreshMessageCaches(ctx context.Context) error {
	client, err := daemonClient()
	if err != nil {
		return err
	}
	if client.Healthy(ctx) {
		_, err := client.Call(ctx, daemon.ActionRefreshMessages)
		return err
	}
	_, err = key.EnsureMessageRelatedCaches(ctx)
	return err
}

func refreshFavoriteCache(ctx context.Context) (key.TargetDB, string, error) {
	client, err := daemonClient()
	if err != nil {
		return key.TargetDB{}, "", err
	}
	if client.Healthy(ctx) {
		if _, err := client.Call(ctx, daemon.ActionRefreshFavorites); err != nil && !isUnknownDaemonAction(err) {
			return key.TargetDB{}, "", err
		}
		if target, path, ok := key.FavoriteCachePathIfExists(); ok {
			return target, path, nil
		}
		return key.TargetDB{}, "", fmt.Errorf("favorite cache was not found after daemon refresh")
	}
	res, path, err := key.EnsureFavoriteCache(ctx)
	return res.Target, path, err
}

func refreshSNSCache(ctx context.Context) (string, error) {
	client, err := daemonClient()
	if err != nil {
		return "", err
	}
	if client.Healthy(ctx) {
		if _, err := client.Call(ctx, daemon.ActionRefreshSNS); err != nil && !isUnknownDaemonAction(err) {
			return "", err
		}
		if _, path, ok := key.SNSCachePathIfExists(); ok {
			return path, nil
		}
		return "", fmt.Errorf("sns cache was not found after daemon refresh")
	}
	_, path, err := key.EnsureSNSCache(ctx)
	return path, err
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

func ensureBizMessageCachesBestEffort(ctx context.Context) error {
	targets, err := key.DiscoverBizMessageDBs()
	if err != nil {
		return nil
	}
	for _, target := range targets {
		if _, _, err := key.EnsureDBCache(ctx, target); err != nil && !key.IsMissingKeyError(err) {
			return err
		}
	}
	return nil
}

func refreshStepsForScope(scope string) ([]refreshStep, bool) {
	switch scope {
	case "all":
		return []refreshStep{
			{Name: "contacts", Action: daemon.ActionRefreshContacts, Ensure: ensureContact},
			{Name: "sessions", Action: daemon.ActionRefreshSessions, Ensure: ensureSessions},
			{Name: "messages", Action: daemon.ActionRefreshMessages, Ensure: ensureMessages},
			{Name: "favorites", Action: daemon.ActionRefreshFavorites, Ensure: ensureFavorites},
			{Name: "sns", Action: daemon.ActionRefreshSNS, Ensure: ensureSNS},
		}, true
	case "contacts":
		return []refreshStep{{Name: "contacts", Action: daemon.ActionRefreshContacts, Ensure: ensureContact}}, true
	case "sessions":
		return []refreshStep{{Name: "sessions", Action: daemon.ActionRefreshSessions, Ensure: ensureSessions}}, true
	case "messages":
		return []refreshStep{{Name: "messages", Action: daemon.ActionRefreshMessages, Ensure: ensureMessages}}, true
	case "favorites":
		return []refreshStep{{Name: "favorites", Action: daemon.ActionRefreshFavorites, Ensure: ensureFavorites}}, true
	case "articles":
		return []refreshStep{
			{Name: "contacts", Action: daemon.ActionRefreshContacts, Ensure: ensureContact},
			{Name: "messages", Action: daemon.ActionRefreshMessages, Ensure: ensureMessages},
		}, true
	case "sns":
		return []refreshStep{{Name: "sns", Action: daemon.ActionRefreshSNS, Ensure: ensureSNS}}, true
	default:
		return nil, false
	}
}

func runRefreshStep(ctx context.Context, client daemon.Client, daemonHealthy bool, step refreshStep, result *RefreshResult) bool {
	if daemonHealthy && step.Action != "" {
		if _, err := client.Call(ctx, step.Action); err == nil {
			return true
		} else if !isUnknownDaemonAction(err) {
			result.Warnings = append(result.Warnings, step.Name+": daemon refresh failed: "+err.Error())
		}
	}
	if step.Ensure == nil {
		return false
	}
	if err := step.Ensure(ctx); err != nil {
		result.Warnings = append(result.Warnings, step.Name+": in-process refresh failed: "+err.Error())
		return false
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
