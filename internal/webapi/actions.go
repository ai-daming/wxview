package webapi

import (
	"net/http"
	"strings"

	wxquery "wxview/internal/query"
)

func (h *handler) handleCacheRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
		return
	}
	params, err := readActionParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	scope := strings.TrimSpace(params["scope"])
	if scope == "" {
		scope = "all"
	}
	scope, err = wxquery.NormalizeRefreshScope(scope)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}
	result, err := h.queryRuntime().CacheRefresh(r.Context(), wxquery.RefreshRequest{Scope: scope})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_scope", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse[wxquery.RefreshResult]{
		Meta:   h.actionMeta(info, "cache.refresh"),
		Result: result,
	})
}
