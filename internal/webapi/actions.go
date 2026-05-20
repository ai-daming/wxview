package webapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"wxview/internal/daemon"
)

type refreshStep struct {
	Name   string
	Action string
	Ensure func(context.Context) error
}

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
	steps, ok := refreshStepsForScope(scope)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_scope", "scope must be all, contacts, sessions, messages, favorites, articles, or sns")
		return
	}
	_, _, info, err := h.resolveAccount(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "account_discovery_failed", err.Error())
		return
	}

	start := time.Now()
	result := RefreshResult{Scope: scope}
	client, clientErr := daemonClient()
	daemonHealthy := clientErr == nil && client.Healthy(r.Context())
	result.UsedDaemon = daemonHealthy
	for _, step := range steps {
		if runRefreshStep(r.Context(), client, daemonHealthy, step, &result) {
			result.Refreshed = append(result.Refreshed, step.Name)
		}
	}
	result.DurationMS = time.Since(start).Milliseconds()
	writeJSON(w, http.StatusOK, ActionResponse[RefreshResult]{
		Meta:   h.actionMeta(info, "cache.refresh"),
		Result: result,
	})
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
