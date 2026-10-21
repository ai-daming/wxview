package webapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"wxview/internal/contacts"
	"wxview/internal/identity"
	wxquery "wxview/internal/query"
)

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

func (h *handler) messageMeta(info identity.Info, resource string, limit int, returned int, execution wxquery.ExecutionMeta) ListMeta {
	meta := h.listMeta(info, resource)
	meta.Timezone = wxquery.LocalTimezoneName()
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
		strings.Contains(lower, "requires") ||
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
		AvatarURL:    mediaURL(item.AvatarPath),
		AvatarReason: item.AvatarReason,
	}
}

func nextMessagesCursorURL(r *http.Request, result wxquery.MessagesResult) string {
	next := *r.URL
	values := next.Query()
	values.Del("date")
	values.Del("offset")
	values.Del("after_seq")
	values.Del("refresh")
	values.Set("order", result.Order)
	values.Set("cursor", result.NextCursor)
	if result.Range.HasStart {
		values.Set("start", strconv.FormatInt(result.Range.Start, 10))
	}
	if result.Range.HasEnd {
		values.Set("end", strconv.FormatInt(result.Range.End, 10))
	}
	next.RawQuery = values.Encode()
	return next.RequestURI()
}
