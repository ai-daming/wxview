package key

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wxview/internal/app"
	"wxview/internal/decrypt"
)

const (
	CacheGroupAll       = "all"
	CacheGroupContacts  = "contacts"
	CacheGroupMessages  = "messages"
	CacheGroupMedia     = "media"
	CacheGroupSessions  = "sessions"
	CacheGroupAvatars   = "avatars"
	CacheGroupFavorites = "favorites"
	CacheGroupSNS       = "sns"

	CacheStatusFresh         = "fresh"
	CacheStatusStale         = "stale"
	CacheStatusMissingCache  = "missing_cache"
	CacheStatusMissingSource = "missing_source"

	KeyStatusAvailable = "available"
	KeyStatusMissing   = "missing"
	KeyStatusInvalid   = "invalid"
	KeyStatusUnknown   = "unknown"
)

type CacheStatusItem struct {
	Group        string `json:"group"`
	Account      string `json:"account"`
	DataDir      string `json:"data_dir"`
	DBRelPath    string `json:"db_rel_path"`
	Status       string `json:"status"`
	KeyStatus    string `json:"key_status"`
	SourcePath   string `json:"source_path"`
	CachePath    string `json:"cache_path"`
	SourceExists bool   `json:"source_exists"`
	CacheExists  bool   `json:"cache_exists"`
	SourceSize   int64  `json:"source_size"`
	CacheSize    int64  `json:"cache_size"`
	SourceMTime  string `json:"source_mtime,omitempty"`
	CacheMTime   string `json:"cache_mtime,omitempty"`
	RefreshedAt  string `json:"refreshed_at,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type storeLoad struct {
	store Store
	err   error
}

type metaLoad struct {
	meta cacheMetadata
	err  error
}

func NormalizeCacheStatusGroup(group string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "", "all":
		return CacheGroupAll, nil
	case "contact", "contacts":
		return CacheGroupContacts, nil
	case "message", "messages":
		return CacheGroupMessages, nil
	case "media":
		return CacheGroupMedia, nil
	case "session", "sessions":
		return CacheGroupSessions, nil
	case "avatar", "avatars", "head_image", "head-image":
		return CacheGroupAvatars, nil
	case "favorite", "favorites":
		return CacheGroupFavorites, nil
	case "sns", "moments":
		return CacheGroupSNS, nil
	default:
		return "", fmt.Errorf("invalid cache group %q: use all, contacts, messages, media, sessions, avatars, favorites, or sns", group)
	}
}

func CacheStatuses(group string) ([]CacheStatusItem, error) {
	targets, err := cacheStatusTargets(group)
	if err != nil {
		return nil, err
	}
	return cacheStatusesForTargets(targets)
}

func cacheStatusTargets(group string) ([]TargetDB, error) {
	normalized, err := NormalizeCacheStatusGroup(group)
	if err != nil {
		return nil, err
	}
	switch normalized {
	case CacheGroupAll:
		targets, err := DiscoverRequiredDBs()
		if err != nil {
			return nil, err
		}
		auxTargets, err := DiscoverMessageAuxDBs()
		if err != nil {
			return nil, err
		}
		targets = append(targets, auxTargets...)
		targets = append(targets, DiscoverOptionalDataDBs()...)
		return dedupeCacheTargets(targets), nil
	case CacheGroupContacts:
		target, err := DiscoverContactDB()
		if err != nil {
			return nil, err
		}
		return []TargetDB{target}, nil
	case CacheGroupMessages:
		targets, err := DiscoverMessageDBs()
		if err != nil {
			return nil, err
		}
		if bizTargets, err := DiscoverBizMessageDBs(); err == nil {
			targets = append(targets, bizTargets...)
		}
		auxTargets, err := DiscoverMessageAuxDBs()
		if err != nil {
			return nil, err
		}
		targets = append(targets, auxTargets...)
		return dedupeCacheTargets(targets), nil
	case CacheGroupMedia:
		return optionalManyCacheTargets(DiscoverMediaDBs)
	case CacheGroupSessions:
		return optionalOneCacheTarget(DiscoverSessionDB)
	case CacheGroupAvatars:
		return optionalOneCacheTarget(DiscoverHeadImageDB)
	case CacheGroupFavorites:
		return optionalOneCacheTarget(DiscoverFavoriteDB)
	case CacheGroupSNS:
		return optionalOneCacheTarget(DiscoverSNSDB)
	default:
		return nil, fmt.Errorf("unsupported cache group: %s", normalized)
	}
}

func optionalOneCacheTarget(discover func() (TargetDB, bool)) ([]TargetDB, error) {
	if _, err := DiscoverContactDB(); err != nil {
		return nil, err
	}
	target, ok := discover()
	if !ok {
		return nil, nil
	}
	return []TargetDB{target}, nil
}

func optionalManyCacheTargets(discover func() ([]TargetDB, error)) ([]TargetDB, error) {
	targets, err := discover()
	if err == nil {
		return dedupeCacheTargets(targets), nil
	}
	if _, contactErr := DiscoverContactDB(); contactErr != nil {
		return nil, contactErr
	}
	return nil, nil
}

func cacheStatusesForTargets(targets []TargetDB) ([]CacheStatusItem, error) {
	targets = dedupeCacheTargets(targets)
	sortCacheTargets(targets)

	stores := map[string]storeLoad{}
	metas := map[string]metaLoad{}
	items := make([]CacheStatusItem, 0, len(targets))
	for _, target := range targets {
		store := stores[target.Account]
		if _, ok := stores[target.Account]; !ok {
			storePath, err := cacheStatusLocalPath(target.Account, keyStoreRelPath)
			if err != nil {
				store.err = err
			} else {
				store.store, store.err = LoadStore(storePath)
			}
			stores[target.Account] = store
		}

		meta := metas[target.Account]
		if _, ok := metas[target.Account]; !ok {
			metaPath, err := cacheStatusLocalPath(target.Account, cacheMetaRelPath)
			if err != nil {
				meta.err = err
			} else {
				meta.meta, meta.err = loadCacheMetadata(metaPath)
			}
			metas[target.Account] = meta
		}

		item, err := cacheStatusForTarget(target, store, meta)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func cacheStatusForTarget(target TargetDB, store storeLoad, meta metaLoad) (CacheStatusItem, error) {
	cachePath, err := cacheStatusLocalPath(target.Account, target.DBRelPath)
	if err != nil {
		return CacheStatusItem{}, err
	}
	item := CacheStatusItem{
		Group:      cacheGroupForRelPath(target.DBRelPath),
		Account:    target.Account,
		DataDir:    target.DataDir,
		DBRelPath:  target.DBRelPath,
		KeyStatus:  KeyStatusUnknown,
		SourcePath: target.DBPath,
		CachePath:  cachePath,
	}

	sourceInfo, err := os.Stat(target.DBPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			item.Status = CacheStatusMissingSource
			item.Reason = "source_file_missing"
			return item, nil
		}
		return CacheStatusItem{}, err
	}
	item.SourceExists = true
	item.SourceSize = sourceInfo.Size()
	item.SourceMTime = formatCacheStatusTime(sourceInfo.ModTime())

	keyStatus, sourceSalt, keyReason := inspectCacheKeyStatus(target, store)
	item.KeyStatus = keyStatus

	cacheInfo, err := os.Stat(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			item.Status = CacheStatusMissingCache
			item.Reason = "cache_file_missing"
			return item, nil
		}
		return CacheStatusItem{}, err
	}
	item.CacheExists = true
	item.CacheSize = cacheInfo.Size()
	item.CacheMTime = formatCacheStatusTime(cacheInfo.ModTime())

	reasons := make([]string, 0)
	if meta.err != nil {
		reasons = append(reasons, "metadata_unreadable")
	} else if entry, ok := meta.meta.Files[target.DBRelPath]; ok {
		item.RefreshedAt = formatCacheStatusTime(entry.RefreshedAt)
		if entry.CacheSize != cacheInfo.Size() || entry.CacheMTimeNS != cacheInfo.ModTime().UnixNano() {
			reasons = append(reasons, "cache_revision_unbound")
		}
		signature, signatureErr := statSourceSignature(target.DBPath)
		if signatureErr != nil {
			reasons = append(reasons, "source_snapshot_unavailable")
		} else if entry.SnapshotVersion != 1 {
			reasons = append(reasons, "snapshot_metadata_missing")
		} else if entry.sourceSignature() != signature {
			reasons = append(reasons, "source_snapshot_changed")
		}

		if !samePath(entry.SourcePath, target.DBPath) {
			reasons = append(reasons, "source_path_changed")
		}
		if !samePath(entry.CachePath, cachePath) {
			reasons = append(reasons, "cache_path_changed")
		}
		if entry.SourceSize != sourceInfo.Size() {
			reasons = append(reasons, "source_size_changed")
		}
		if entry.SourceMTimeNS != sourceInfo.ModTime().UnixNano() {
			reasons = append(reasons, "source_mtime_changed")
		}
		if sourceSalt == "" {
			reasons = append(reasons, keyReason)
		} else if !strings.EqualFold(entry.SourceSalt, sourceSalt) {
			reasons = append(reasons, "source_salt_changed")
		}
	} else {
		reasons = append(reasons, "metadata_missing")
	}

	if len(reasons) == 0 {
		item.Status = CacheStatusFresh
		return item, nil
	}
	item.Status = CacheStatusStale
	item.Reason = strings.Join(compactCacheReasons(reasons), ",")
	if item.Reason == "" {
		item.Reason = "stale"
	}
	return item, nil
}

func cacheStatusLocalPath(account string, relPath string) (string, error) {
	base, err := app.BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cache", app.SafeAccountDir(account), filepath.FromSlash(relPath)), nil
}

func inspectCacheKeyStatus(target TargetDB, store storeLoad) (string, string, string) {
	if store.err != nil {
		return KeyStatusUnknown, "", "key_store_unreadable"
	}
	page1, saltBytes, err := decrypt.ReadPage1(target.DBPath)
	if err != nil {
		return KeyStatusUnknown, "", "source_salt_unreadable"
	}
	salt := hex.EncodeToString(saltBytes)
	entry, ok := store.store.Find(target.DataDir, target.DBRelPath, salt)
	if !ok {
		return KeyStatusMissing, salt, ""
	}
	if !decrypt.ValidateRawHexKey(page1, entry.Key) {
		return KeyStatusInvalid, salt, ""
	}
	return KeyStatusAvailable, salt, ""
}

func cacheGroupForRelPath(relPath string) string {
	switch {
	case relPath == contactRelPath:
		return CacheGroupContacts
	case relPath == sessionRelPath:
		return CacheGroupSessions
	case relPath == headImageRelPath:
		return CacheGroupAvatars
	case relPath == favoriteRelPath:
		return CacheGroupFavorites
	case relPath == snsRelPath:
		return CacheGroupSNS
	case strings.HasPrefix(relPath, "message/media_"):
		return CacheGroupMedia
	case strings.HasPrefix(relPath, "message/"):
		return CacheGroupMessages
	default:
		return "other"
	}
}

func dedupeCacheTargets(targets []TargetDB) []TargetDB {
	seen := map[string]bool{}
	out := make([]TargetDB, 0, len(targets))
	for _, target := range targets {
		key := target.Account + "\x00" + target.DBRelPath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, target)
	}
	return out
}

func sortCacheTargets(targets []TargetDB) {
	sort.SliceStable(targets, func(i, j int) bool {
		leftGroup := cacheGroupWeight(cacheGroupForRelPath(targets[i].DBRelPath))
		rightGroup := cacheGroupWeight(cacheGroupForRelPath(targets[j].DBRelPath))
		if leftGroup != rightGroup {
			return leftGroup < rightGroup
		}
		leftBase := filepath.Base(targets[i].DBRelPath)
		rightBase := filepath.Base(targets[j].DBRelPath)
		for _, prefix := range []string{"message_", "biz_message_", "media_"} {
			leftIndex, leftOK := numberedShardIndex(leftBase, prefix)
			rightIndex, rightOK := numberedShardIndex(rightBase, prefix)
			if leftOK && rightOK {
				return leftIndex < rightIndex
			}
		}
		return targets[i].DBRelPath < targets[j].DBRelPath
	})
}

func cacheGroupWeight(group string) int {
	switch group {
	case CacheGroupContacts:
		return 0
	case CacheGroupMessages:
		return 1
	case CacheGroupMedia:
		return 2
	case CacheGroupSessions:
		return 3
	case CacheGroupAvatars:
		return 4
	case CacheGroupFavorites:
		return 5
	case CacheGroupSNS:
		return 6
	default:
		return 99
	}
}

func compactCacheReasons(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason != "" {
			out = append(out, reason)
		}
	}
	return out
}

func formatCacheStatusTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
