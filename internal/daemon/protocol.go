package daemon

const (
	ActionHealth           = "health"
	ActionRefreshContacts  = "refresh_contacts"
	ActionRefreshSessions  = "refresh_sessions"
	ActionRefreshMessages  = "refresh_messages"
	ActionRefreshIndex     = "refresh_index"
	ActionRefreshAvatars   = "refresh_avatars"
	ActionRefreshFavorites = "refresh_favorites"
	ActionRefreshSNS       = "refresh_sns"
	ActionStop             = "stop"
)

type Request struct {
	Action string `json:"action"`
}

type Response struct {
	Discovery *DiscoveryStats `json:"discovery,omitempty"`
	OK        bool            `json:"ok"`
	Message   string          `json:"message,omitempty"`
	Version   string          `json:"version,omitempty"`
}
