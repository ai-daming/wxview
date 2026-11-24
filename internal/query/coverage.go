package query

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wxview/internal/decrypt"
	"wxview/internal/key"
	"wxview/internal/sqlitedb"
)

type DataCoverage struct {
	Scope      string       `json:"scope"`
	Account    string       `json:"account"`
	State      string       `json:"state"`
	Reason     string       `json:"reason,omitempty"`
	ObservedAt string       `json:"observed_at"`
	Sources    []DataSource `json:"sources"`
}

type DataSource struct {
	key.CacheReadStatus
	Role     string `json:"role"`
	Required bool   `json:"required"`
}

type readScopeKey struct{}
type dependencyRead struct {
	observation key.CacheReadObservation
	role        string
	required    bool
	failed      bool
}
type readCoverage struct {
	unresolved     map[string]DataSource
	mu             sync.Mutex
	observer       *key.CacheReadObserver
	account        key.TargetDB
	contactPath    string
	reads          map[string]*dependencyRead
	indexPath      string
	indexInfo      os.FileInfo
	indexWALInfo   os.FileInfo
	indexSignature decrypt.SourceSignature
	indexErr       error
}

func (rt Runtime) beginRead(ctx context.Context) (Runtime, context.Context, *readCoverage) {
	rt = rt.withDefaults()
	c := &readCoverage{observer: key.NewCacheReadObserver(), reads: map[string]*dependencyRead{}, unresolved: map[string]DataSource{}}
	discover := rt.DiscoverContact
	var once sync.Once
	var target key.TargetDB
	var err error
	rt.DiscoverContact = func() (key.TargetDB, error) {
		once.Do(func() { target, err = discover(); c.account = target })
		return target, err
	}
	contactPath := rt.ContactCachePath
	rt.ContactCachePath = func(account string) (string, error) {
		path, err := contactPath(account)
		if err == nil {
			c.contactPath = path
		}
		return path, err
	}
	ctx = context.WithValue(ctx, readScopeKey{}, c)
	ctx = sqlitedb.WithReadObserver(ctx, func(path string) { c.observe(path, false) })
	return rt, ctx, c
}

func coverageFor(ctx context.Context) *readCoverage {
	c, _ := ctx.Value(readScopeKey{}).(*readCoverage)
	return c
}

func observeCache(ctx context.Context, path string, required bool) {
	if c := coverageFor(ctx); c != nil {
		c.observe(path, required)
	}
}
func failedCacheRead(ctx context.Context, path string) {
	if c := coverageFor(ctx); c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r := c.reads[path]; r != nil {
			r.failed = true
		}
	}
}

func unresolvedDependency(ctx context.Context, db, role, reason string) {
	if c := coverageFor(ctx); c != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.unresolved[db] = DataSource{CacheReadStatus: key.CacheReadStatus{DB: db, State: "unknown", Reason: reason}, Role: role}
	}
}

func (c *readCoverage) observe(path string, required bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := c.reads[path]; previous != nil {
		previous.required = previous.required || required
		return
	}
	if c.account.Account == "" {
		return
	}
	root, err := key.ReadOnlyCachePath(c.account.Account, "")
	if err != nil {
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)
	if path == c.contactPath {
		rel = "contact/contact.db"
	}
	if rel == "index/messages.db" {
		if c.indexPath == "" {
			c.indexPath = path
			c.indexInfo, _ = os.Stat(path)
			c.indexWALInfo, _ = os.Stat(path + "-wal")
			c.indexSignature, c.indexErr = decrypt.StatSource(path)
		}
		return
	}
	if strings.HasPrefix(rel, "../") || !strings.HasSuffix(rel, ".db") {
		return
	}
	role := strings.SplitN(rel, "/", 2)[0]
	switch {
	case rel == "contact/contact.db":
		role = "contacts"
	case rel == "session/session.db":
		role = "sessions"
	case rel == "favorite/favorite.db":
		role = "favorites"
	case strings.HasPrefix(rel, "message/media_") || rel == "message/message_resource.db" || strings.HasPrefix(rel, "head_image/"):
		role = "media"
	case strings.HasPrefix(rel, "message/biz_message_"):
		role = "articles"
	case strings.HasPrefix(rel, "message/"):
		role = "messages"
		required = true
	}
	c.reads[path] = &dependencyRead{observation: c.observer.Observe(key.TargetFor(c.account, rel), path), role: role, required: required}
}

func (c *readCoverage) finish(indexUsed bool) *DataCoverage {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &DataCoverage{Scope: "local_synced_data", Account: c.account.Account, State: "fresh", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Sources: []DataSource{}}
	changed := false
	for _, read := range c.reads {
		status := c.observer.Finish(read.observation)
		if read.failed {
			status.State = "unknown"
			status.Reason = "cache_read_failed"
		}
		if strings.Contains(status.Reason, "changed_during_query") {
			changed = true
		}
		result.Sources = append(result.Sources, DataSource{status, read.role, read.required})
	}
	for db, problem := range c.unresolved {
		found := false
		for i := range result.Sources {
			if result.Sources[i].DB == db {
				result.Sources[i].CacheReadStatus = problem.CacheReadStatus
				found = true
			}
		}
		if !found {
			result.Sources = append(result.Sources, problem)
		}
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].DB < result.Sources[j].DB })
	rank := map[string]int{"fresh": 0, "unknown": 1, "stale": 2, "partial": 3}
	for _, source := range result.Sources {
		state := source.State
		if state == "missing" {
			state = "partial"
		}
		if rank[state] > rank[result.State] {
			result.State = state
		}
	}
	if len(result.Sources) == 0 {
		result.State = "unknown"
		result.Reason = "no_cache_observation"
	}
	if changed {
		result.State = "unknown"
		result.Reason = "cache_changed_during_query"
	}
	if indexUsed && c.indexPath != "" {
		now, err := os.Stat(c.indexPath)
		signature, sigErr := decrypt.StatSource(c.indexPath)
		walInfo, _ := os.Stat(c.indexPath + "-wal")
		sameWAL := (c.indexWALInfo == nil && walInfo == nil) || sameObservedFile(c.indexWALInfo, walInfo)
		if err != nil || !sameObservedFile(c.indexInfo, now) || c.indexErr != nil || sigErr != nil || signature != c.indexSignature || !sameWAL {
			result.State = "unknown"
			result.Reason = "index_changed_during_query"
		}
	}
	result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return result
}
func sameObservedFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
