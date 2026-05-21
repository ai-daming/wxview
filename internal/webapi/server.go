package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"wxview/internal/contacts"
	"wxview/internal/identity"
	"wxview/internal/key"
	wxquery "wxview/internal/query"
)

const DefaultAddr = "127.0.0.1:38765"

const (
	ExposureLoopback = "loopback"
	ExposureNetwork  = "network"
)

const contactsDefaultLimit = 50
const contactsMaxLimit = 500
const longRequestTimeout = 30 * time.Minute

type Options struct {
	Addr             string
	Version          string
	InstanceID       string
	StartedAt        time.Time
	DiscoverContact  func() (key.TargetDB, error)
	ContactCachePath func(account string) (string, error)
}

type ServerInfo struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Addr      string `json:"addr"`
	Exposure  string `json:"exposure"`
	StartedAt string `json:"started_at"`
}

type AccountMeta struct {
	Account            string `json:"account"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	IdentitySource     string `json:"identity_source"`
	DataDirFingerprint string `json:"data_dir_fingerprint"`
}

type ContactItem struct {
	Username    string `json:"username"`
	Alias       string `json:"alias"`
	Remark      string `json:"remark"`
	NickName    string `json:"nick_name"`
	DisplayName string `json:"display_name"`
	HeadURL     string `json:"head_url"`
	Kind        string `json:"kind"`
}

type ContactsMeta struct {
	SchemaVersion int         `json:"schema_version"`
	Account       AccountMeta `json:"account"`
	ServerID      string      `json:"server_id"`
	Resource      string      `json:"resource"`
	Limit         int         `json:"limit"`
	Offset        int         `json:"offset"`
	Returned      int         `json:"returned"`
	Total         int         `json:"total"`
	HasMore       bool        `json:"has_more"`
	NextURL       string      `json:"next_url,omitempty"`
}

type ContactsResponse struct {
	Meta  ContactsMeta  `json:"meta"`
	Items []ContactItem `json:"items"`
}

type MeResponse struct {
	SchemaVersion int              `json:"schema_version"`
	Server        ServerInfo       `json:"server"`
	Account       identity.Account `json:"account"`
}

type HealthResponse struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
	ServerID  string `json:"server_id"`
	Exposure  string `json:"exposure"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type handler struct {
	addr             string
	version          string
	serverID         string
	startedAt        time.Time
	discoverContact  func() (key.TargetDB, error)
	contactCachePath func(account string) (string, error)
}

func Serve(ctx context.Context, opts Options) error {
	if opts.Addr == "" {
		opts.Addr = DefaultAddr
	}
	if err := ValidateAddr(opts.Addr); err != nil {
		return err
	}
	handler := NewHandler(opts)
	server := &http.Server{
		Addr:              opts.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       longRequestTimeout,
		WriteTimeout:      longRequestTimeout,
	}
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func NewHandler(opts Options) http.Handler {
	if opts.Addr == "" {
		opts.Addr = DefaultAddr
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.InstanceID == "" {
		opts.InstanceID = defaultServerID(opts.Addr)
	}
	if opts.DiscoverContact == nil {
		opts.DiscoverContact = key.DiscoverContactDB
	}
	if opts.ContactCachePath == nil {
		opts.ContactCachePath = key.ContactCachePath
	}
	return &handler{
		addr:             opts.Addr,
		version:          opts.Version,
		serverID:         opts.InstanceID,
		startedAt:        opts.StartedAt,
		discoverContact:  opts.DiscoverContact,
		contactCachePath: opts.ContactCachePath,
	}
}

func ValidateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid api addr %q: use host:port, for example %s", addr, DefaultAddr)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("invalid api addr %q: host must be explicit; use 0.0.0.0 to listen on all IPv4 interfaces", addr)
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("invalid api addr %q: host must not contain whitespace", addr)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return fmt.Errorf("invalid api addr %q: port must be 1-65535", addr)
	}
	return nil
}

func AddrExposure(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ExposureNetwork
	}
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return ExposureLoopback
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return ExposureLoopback
	}
	return ExposureNetwork
}

func LocalClientAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !ip.IsUnspecified() {
		return addr
	}
	if ip.To4() != nil {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return net.JoinHostPort("::1", port)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/health":
		h.handleHealth(w, r)
	case "/v1/me":
		h.handleMe(w, r)
	case "/v1/media":
		h.handleMedia(w, r)
	case "/v1/contacts":
		h.handleContacts(w, r)
	case "/v1/contacts/detail":
		h.handleContactDetail(w, r)
	case "/v1/members":
		h.handleMembers(w, r)
	case "/v1/sessions":
		h.handleSessions(w, r, false)
	case "/v1/unread":
		h.handleSessions(w, r, true)
	case "/v1/messages":
		h.handleMessages(w, r)
	case "/v1/search":
		h.handleSearch(w, r)
	case "/v1/timeline":
		h.handleTimeline(w, r)
	case "/v1/new-messages":
		h.handleNewMessagesPreview(w, r)
	case "/v1/new-messages/poll":
		h.handleNewMessagesPoll(w, r)
	case "/v1/new-messages/reset":
		h.handleNewMessagesReset(w, r)
	case "/v1/favorites":
		h.handleFavorites(w, r)
	case "/v1/articles/accounts":
		h.handleArticleAccounts(w, r)
	case "/v1/articles":
		h.handleArticles(w, r)
	case "/v1/sns/feed":
		h.handleSNSFeed(w, r)
	case "/v1/sns/search":
		h.handleSNSFeed(w, r)
	case "/v1/sns/notifications":
		h.handleSNSNotifications(w, r)
	case "/v1/cache/refresh":
		h.handleCacheRefresh(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "endpoint not found")
	}
}

func (h *handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		OK:        true,
		Version:   h.version,
		StartedAt: h.startedAt.Format(time.RFC3339),
		ServerID:  h.serverID,
		Exposure:  AddrExposure(h.addr),
	})
}

func (h *handler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	detail := r.URL.Query().Get("detail")
	if detail != "" && detail != "paths" {
		writeError(w, http.StatusBadRequest, "invalid_detail", "detail must be paths")
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), detail == "paths")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, MeResponse{
		SchemaVersion: identity.SchemaVersion,
		Server:        h.serverInfo(),
		Account:       info.Account,
	})
}

func (h *handler) handleContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	query := r.URL.Query()
	kind := strings.TrimSpace(query.Get("kind"))
	if kind == "" {
		kind = contacts.KindAll
	}
	if !validKind(kind) {
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be all, friend, chatroom, or other")
		return
	}
	sortBy := strings.TrimSpace(query.Get("sort"))
	if sortBy == "" {
		sortBy = "username"
	}
	if !validSort(sortBy) {
		writeError(w, http.StatusBadRequest, "invalid_sort", "sort must be username or name")
		return
	}
	limit, err := parseIntQuery(query.Get("limit"), contactsDefaultLimit, 0, contactsMaxLimit, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := parseIntQuery(query.Get("offset"), 0, 0, int(^uint(0)>>1), "offset")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}

	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().Contacts(r.Context(), wxquery.ContactsRequest{
		Kind:      kind,
		Query:     query.Get("query"),
		Username:  query.Get("username"),
		Sort:      sortBy,
		Limit:     limit,
		Offset:    offset,
		CacheMode: wxquery.CacheMode{ReadOnly: true},
	})
	if err != nil {
		writeQueryError(w, "contacts_query_failed", err)
		return
	}

	writeJSON(w, http.StatusOK, ContactsResponse{
		Meta: ContactsMeta{
			SchemaVersion: identity.SchemaVersion,
			Account:       toAccountMeta(info.Account),
			ServerID:      h.serverID,
			Resource:      "contacts",
			Limit:         limit,
			Offset:        offset,
			Returned:      len(result.Items),
			Total:         result.Total,
			HasMore:       result.HasMore,
			NextURL:       nextOffsetURL(r, limit, offset, result.HasMore),
		},
		Items: contactItems(result.Items),
	})
}

func (h *handler) resolveAccount(ctx context.Context, includePaths bool) (key.TargetDB, string, identity.Info, error) {
	target, err := h.discoverContact()
	if err != nil {
		return key.TargetDB{}, "", identity.Info{}, err
	}
	cachePath, err := h.contactCachePath(target.Account)
	if err != nil {
		return key.TargetDB{}, "", identity.Info{}, err
	}
	info, err := identity.Resolve(ctx, target, cachePath, includePaths)
	if err != nil {
		return key.TargetDB{}, "", identity.Info{}, err
	}
	return target, cachePath, info, nil
}

func (h *handler) serverInfo() ServerInfo {
	return ServerInfo{
		ID:        h.serverID,
		Version:   h.version,
		Addr:      h.addr,
		Exposure:  AddrExposure(h.addr),
		StartedAt: h.startedAt.Format(time.RFC3339),
	}
}

func parseIntQuery(value string, def int, min int, max int, name string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return def, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%s must be between %d and %d", name, min, max)
	}
	return n, nil
}

func contactItems(list []contacts.Contact) []ContactItem {
	items := make([]ContactItem, 0, len(list))
	for _, contact := range list {
		items = append(items, ContactItem{
			Username:    contact.Username,
			Alias:       contact.Alias,
			Remark:      contact.Remark,
			NickName:    contact.NickName,
			DisplayName: contacts.DisplayName(contact),
			HeadURL:     contact.HeadURL,
			Kind:        contact.Kind,
		})
	}
	return items
}

func toAccountMeta(account identity.Account) AccountMeta {
	return AccountMeta{
		Account:            account.Account,
		Username:           account.Username,
		DisplayName:        account.DisplayName,
		IdentitySource:     account.IdentitySource,
		DataDirFingerprint: account.DataDirFingerprint,
	}
}

func validKind(kind string) bool {
	switch kind {
	case contacts.KindAll, contacts.KindFriend, contacts.KindChatroom, contacts.KindOther:
		return true
	default:
		return false
	}
}

func validSort(sortBy string) bool {
	switch sortBy {
	case "username", "name":
		return true
	default:
		return false
	}
}

func defaultServerID(addr string) string {
	replacer := strings.NewReplacer(":", "-", ".", "-", "[", "", "]", "")
	return fmt.Sprintf("wxview-%s-%d", replacer.Replace(addr), os.Getpid())
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}
