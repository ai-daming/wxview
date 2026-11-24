package key

import (
	"os"
	"time"
)

// CacheReadStatus concerns one local cache revision, never account-wide or
// phone-side completeness. It deliberately contains no paths or key material.
type CacheReadStatus struct {
	DB               string `json:"db"`
	State            string `json:"state"`
	Reason           string `json:"reason,omitempty"`
	CapturedAt       string `json:"captured_at,omitempty"`
	SourceWALPresent *bool  `json:"source_wal_present,omitempty"`
	WALInSnapshot    *bool  `json:"wal_in_snapshot,omitempty"`
}

type CacheReadObservation struct {
	target      TargetDB
	path        string
	info        os.FileInfo
	statErr     error
	entry       cacheMetadataEntry
	metadataErr error
	found       bool
}

type observedMetadata struct {
	info os.FileInfo
	meta cacheMetadata
	err  error
}

// CacheReadObserver reuses existing refresh metadata and reads only dependency
// file stats and source WAL headers. It never discovers accounts, keys or DBs.
type CacheReadObserver struct{ metadata map[string]observedMetadata }

func NewCacheReadObserver() *CacheReadObserver {
	return &CacheReadObserver{metadata: map[string]observedMetadata{}}
}

func (o *CacheReadObserver) entry(target TargetDB) (cacheMetadataEntry, bool, error) {
	path, err := cacheStatusLocalPath(target.Account, cacheMetaRelPath)
	if err != nil {
		return cacheMetadataEntry{}, false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return cacheMetadataEntry{}, false, err
	}
	previous, ok := o.metadata[path]
	if !ok || !sameCacheFile(previous.info, info) {
		previous = observedMetadata{info: info}
		previous.meta, previous.err = loadCacheMetadata(path)
		o.metadata[path] = previous
	}
	entry, found := previous.meta.Files[target.DBRelPath]
	return entry, found, previous.err
}

func (o *CacheReadObserver) Observe(target TargetDB, path string) CacheReadObservation {
	observation := CacheReadObservation{target: target, path: path}
	observation.info, observation.statErr = os.Stat(path)
	observation.entry, observation.found, observation.metadataErr = o.entry(target)
	return observation
}

func (o *CacheReadObserver) Finish(observation CacheReadObservation) CacheReadStatus {
	state := CacheReadStatus{DB: observation.target.DBRelPath, State: "unknown"}
	info, err := os.Stat(observation.path)
	if os.IsNotExist(observation.statErr) && os.IsNotExist(err) {
		state.State = "missing"
		state.Reason = "cache_missing"
		return state
	}
	if observation.statErr != nil {
		state.Reason = "cache_unavailable"
		return state
	}
	if err != nil || !sameCacheFile(observation.info, info) {
		state.Reason = "cache_changed_during_query"
		return state
	}
	if observation.metadataErr != nil || !observation.found {
		state.Reason = "snapshot_metadata_unavailable"
		return state
	}
	entry, found, err := o.entry(observation.target)
	if err != nil || !found || entry != observation.entry {
		state.Reason = "snapshot_metadata_changed_during_query"
		return state
	}
	if entry.SnapshotVersion != 1 || entry.CacheSize != info.Size() || entry.CacheMTimeNS != info.ModTime().UnixNano() ||
		!samePath(entry.CachePath, observation.path) || !samePath(entry.SourcePath, observation.target.DBPath) {
		state.Reason = "cache_revision_unbound"
		return state
	}
	if !entry.RefreshedAt.IsZero() {
		state.CapturedAt = entry.RefreshedAt.UTC().Format(time.RFC3339Nano)
	}
	wal := entry.WALPresent
	state.WALInSnapshot = &wal
	source, err := statSourceSignature(observation.target.DBPath)
	if err != nil {
		state.Reason = "source_unavailable"
		return state
	}
	sourceWAL := source.WALPresent
	state.SourceWALPresent = &sourceWAL
	if source != entry.sourceSignature() {
		state.State = "stale"
		state.Reason = "source_changed"
		return state
	}
	state.State = "fresh"
	return state
}

func sameCacheFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// ReadOnlyCachePath never creates local state, including when no cache exists.
func ReadOnlyCachePath(account, relative string) (string, error) {
	return cacheStatusLocalPath(account, relative)
}
