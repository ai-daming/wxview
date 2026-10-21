package msgindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"wxview/internal/app"
	"wxview/internal/contacts"
	"wxview/internal/messages"
	"wxview/internal/sessions"
	"wxview/internal/sqlitedb"
	"wxview/internal/timeline"
)

const (
	SchemaVersion = 1

	StatusMissing        = "missing"
	StatusBuilding       = "building"
	StatusReady          = "ready"
	StatusRefreshing     = "refreshing"
	StatusInterrupted    = "interrupted"
	StatusStale          = "stale"
	StatusSchemaMismatch = "schema_mismatch"
)

const (
	QueryModeIndex = "index"
	QueryModeScan  = "scan"

	JobStateNone        = "none"
	JobStateActive      = "active"
	JobStateInterrupted = "interrupted"
)

const (
	indexRelPath      = "index/messages.db"
	jobName           = "messages.job.json"
	lockName          = "messages.lock"
	oldProgressName   = "messages.progress.json"
	unitSep           = "\x1f"
	sqliteBusyTimeout = sqlitedb.BusyTimeoutMS
	realtimeLagWindow = 60 * time.Second
	jobHeartbeatStale = 2 * time.Minute
)

var (
	reconcileTableBudget = 300
	reconcileTimeBudget  = 5 * time.Second
)

var (
	ErrUnavailable = errors.New("message index unavailable")
	msgTableRE     = regexp.MustCompile(`^Msg_[0-9a-f]{32}$`)
)

type Status struct {
	Status        string `json:"status"`
	QueryMode     string `json:"query_mode"`
	SchemaVersion int    `json:"schema_version"`
	SourceDBCount int    `json:"source_db_count"`
	IndexedRows   int64  `json:"indexed_rows"`
	IndexedTables int    `json:"indexed_tables,omitempty"`
	SourceRows    int64  `json:"source_rows"`
	CoveredChats  int    `json:"covered_chats"`
	BuiltAt       string `json:"built_at"`
	RefreshedAt   string `json:"refreshed_at,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Path          string `json:"path"`
	JobState      string `json:"job_state"`
	LagSeconds    int64  `json:"lag_seconds"`
	LagPolicy     string `json:"lag_policy"`

	IndexedMaxCreateTime int64  `json:"indexed_max_create_time,omitempty"`
	SourceMaxCreateTime  int64  `json:"source_max_create_time,omitempty"`
	TempPath             string `json:"temp_path,omitempty"`
	TempSize             int64  `json:"temp_size,omitempty"`
	TempMTime            string `json:"temp_mtime,omitempty"`

	ProgressPercent         float64 `json:"progress_percent,omitempty"`
	ProgressRows            int64   `json:"progress_rows,omitempty"`
	ProgressTotalRows       int64   `json:"progress_total_rows,omitempty"`
	ProgressTables          int     `json:"progress_tables,omitempty"`
	ProgressTotalTables     int     `json:"progress_total_tables,omitempty"`
	CheckedTables           int     `json:"checked_tables,omitempty"`
	CoveredTables           int     `json:"covered_tables,omitempty"`
	ProgressCurrent         string  `json:"progress_current,omitempty"`
	ProgressUpdatedAt       string  `json:"progress_updated_at,omitempty"`
	RefreshMode             string  `json:"refresh_mode,omitempty"`
	SessionLagSeconds       int64   `json:"session_lag_seconds,omitempty"`
	ActiveChatsChecked      int     `json:"active_chats_checked,omitempty"`
	ActiveChatsRefreshed    int     `json:"active_chats_refreshed,omitempty"`
	ReconcileSourcesPending int     `json:"reconcile_sources_pending,omitempty"`
	ReconcileTablesChecked  int     `json:"reconcile_tables_checked,omitempty"`
}

type BuildOptions struct {
	Account           string
	IndexPath         string
	ContactCachePath  string
	SessionCachePath  string
	MessageCachePaths []string
	ChatUsernames     []string
}

type BuildResult struct {
	Status     Status `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type CleanResult struct {
	Path         string `json:"path"`
	Removed      int    `json:"removed"`
	RemovedBytes int64  `json:"removed_bytes"`
	Kept         int    `json:"kept"`
}

type MessageQuery struct {
	Order       string
	After       *messages.PageKey
	Username    string
	Start       int64
	End         int64
	AfterSeq    int64
	HasStart    bool
	HasEnd      bool
	HasAfterSeq bool
	Limit       int
	Offset      int
}

type TimelineQuery struct {
	Chats    []messages.ChatInfo
	Start    int64
	End      int64
	After    timeline.SortKey
	HasAfter bool
	Limit    int
}

type SearchQuery struct {
	Chats    []messages.ChatInfo
	Query    string
	Start    int64
	End      int64
	HasStart bool
	HasEnd   bool
	Limit    int
	Offset   int
}

type sourceRow struct {
	SourceDB     string `json:"source_db"`
	CachePath    string `json:"cache_path"`
	CacheSize    int64  `json:"cache_size"`
	CacheMTimeNS int64  `json:"cache_mtime_ns"`
	IndexedAt    string `json:"indexed_at"`
	RowCount     int64  `json:"row_count"`
	TableCount   int    `json:"table_count"`
}

type indexStatsRow struct {
	SchemaVersion           string `json:"schema_version"`
	State                   string `json:"state"`
	BuiltAt                 string `json:"built_at"`
	RefreshedAt             string `json:"refreshed_at"`
	SourceRows              int64  `json:"source_rows"`
	SourceDBCount           int    `json:"source_db_count"`
	IndexedRows             int64  `json:"indexed_rows"`
	IndexedTables           int    `json:"indexed_tables"`
	CoveredChats            int    `json:"covered_chats"`
	MaxCreateTime           int64  `json:"max_create_time"`
	SessionLagSeconds       int64  `json:"session_lag_seconds"`
	RefreshMode             string `json:"refresh_mode"`
	ActiveChatsChecked      int    `json:"active_chats_checked"`
	ActiveChatsRefreshed    int    `json:"active_chats_refreshed"`
	ReconcileSourcesPending int    `json:"reconcile_sources_pending"`
	ReconcileTablesChecked  int    `json:"reconcile_tables_checked"`
}

type refRow struct {
	SourceDB     string `json:"source_db"`
	TableName    string `json:"table_name"`
	ChatUsername string `json:"chat_username"`
	LocalID      int64  `json:"local_id"`
}

type sourceStat struct {
	Path        string
	SourceDB    string
	Size        int64
	MTimeNS     int64
	DisplayTime string
}

type buildSourceSummary struct {
	SourceDB      string
	CachePath     string
	CacheSize     int64
	MTimeNS       int64
	RowCount      int64
	TableCount    int
	MaxCreateTime int64
}

type chatTableMetaRow struct {
	SourceDB      string `json:"source_db"`
	TableName     string `json:"table_name"`
	ChatUsername  string `json:"chat_username"`
	RowCount      int64  `json:"row_count"`
	MinCreateTime int64  `json:"min_create_time"`
	MaxCreateTime int64  `json:"max_create_time"`
	MaxSortSeq    int64  `json:"max_sort_seq"`
	MaxLocalID    int64  `json:"max_local_id"`
}

type tableWatermarkRow struct {
	TableName     string
	RowCount      int64
	MaxLocalID    int64
	MaxCreateTime int64
}

type plannedSource struct {
	Stat   sourceStat
	Tables []plannedTable
}

type plannedTable struct {
	Name         string
	ChatUsername string
}

type refreshSourcePlan struct {
	Stat           sourceStat
	Tables         []refreshTablePlan
	Reconcile      bool
	CheckedThrough string
	Exhausted      bool
	CheckedTables  int
}

type refreshTablePlan struct {
	Name         string
	ChatUsername string
	AfterLocalID int64
	SessionHint  *sessions.IndexHint
}

type sessionSnapshotRow struct {
	Username      string
	TableName     string
	LastTimestamp int64
	RefreshedAt   string
}

type refreshPlanStats struct {
	RefreshMode             string
	SessionLagSeconds       int64
	ActiveChatsChecked      int
	ActiveChatsRefreshed    int
	ReconcileSourcesPending int
	ReconcileTablesChecked  int
}

type buildProgress struct {
	Status                  string  `json:"status"`
	JobID                   string  `json:"job_id,omitempty"`
	PID                     int     `json:"pid,omitempty"`
	StartedAt               string  `json:"started_at"`
	UpdatedAt               string  `json:"updated_at"`
	Account                 string  `json:"account"`
	IndexPath               string  `json:"index_path"`
	TempPath                string  `json:"temp_path,omitempty"`
	CurrentSourceDB         string  `json:"current_source_db,omitempty"`
	CurrentTable            string  `json:"current_table,omitempty"`
	TotalSources            int     `json:"total_sources"`
	TotalTables             int     `json:"total_tables"`
	CheckedTables           int     `json:"checked_tables"`
	CoveredTables           int     `json:"covered_tables"`
	TotalRows               int64   `json:"total_rows"`
	IndexedRows             int64   `json:"indexed_rows"`
	Percent                 float64 `json:"percent"`
	RefreshMode             string  `json:"refresh_mode,omitempty"`
	SessionLagSeconds       int64   `json:"session_lag_seconds,omitempty"`
	ActiveChatsChecked      int     `json:"active_chats_checked,omitempty"`
	ActiveChatsRefreshed    int     `json:"active_chats_refreshed,omitempty"`
	ReconcileSourcesPending int     `json:"reconcile_sources_pending,omitempty"`
	ReconcileTablesChecked  int     `json:"reconcile_tables_checked,omitempty"`
}

type progressTracker struct {
	path      string
	progress  buildProgress
	lastWrite time.Time
}

type sourceMessageRow struct {
	LocalID      int64
	LocalType    int64
	SortSeq      int64
	ServerID     int64
	RealSenderID int64
	SenderName   string
	Status       int64
	CreateTime   int64
	Content      []byte
}

type UsabilityOptions struct {
	Username         string
	Chats            []messages.ChatInfo
	SessionCachePath string
	Start            int64
	End              int64
	HasStart         bool
	HasEnd           bool
}

type Usability struct {
	UseIndex  bool
	IndexPath string
	Status    Status
	Reason    string
}

func IsUnavailable(err error) bool {
	return errors.Is(err, ErrUnavailable)
}

func Path(account string) (string, error) {
	return app.CacheDBPath(account, indexRelPath)
}

func LocalPath(account string) (string, error) {
	base, err := app.BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "cache", app.SafeAccountDir(account), filepath.FromSlash(indexRelPath)), nil
}

func StatusFor(ctx context.Context, indexPath string, messageCachePaths []string) (Status, error) {
	status := baseStatus(indexPath)
	if strings.TrimSpace(indexPath) == "" {
		return status, fmt.Errorf("index path is empty")
	}
	if _, err := os.Stat(indexPath); err != nil {
		if os.IsNotExist(err) {
			job, jobState := loadCurrentJob(indexPath)
			if jobState == JobStateActive {
				return statusFromProgress(indexPath, job, sourceStat{}, StatusBuilding, JobStateActive), nil
			}
			if jobState == JobStateInterrupted {
				return statusFromProgress(indexPath, job, sourceStat{}, StatusInterrupted, JobStateInterrupted), nil
			}
			if legacy := buildingStatus(indexPath); legacy.Status == StatusBuilding {
				legacy.JobState = JobStateInterrupted
				legacy.Status = StatusInterrupted
				legacy.QueryMode = QueryModeScan
				legacy.Reason = "previous_index_job_interrupted_refresh_can_resume"
				return legacy, nil
			}
			return status, nil
		}
		return status, err
	}

	indexDB, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		status.Status = StatusStale
		status.QueryMode = QueryModeScan
		status.Reason = "index_unreadable"
		return status, nil
	}
	defer indexDB.Close()

	stats, err := indexStatsDB(ctx, indexDB)
	if err != nil {
		status.Status = StatusStale
		status.QueryMode = QueryModeScan
		status.Reason = "index_unreadable"
		return status, nil
	}
	status.SchemaVersion = parseSchemaVersion(stats.SchemaVersion)
	status.Status = normalizeIndexState(stats.State)
	status.SourceDBCount = stats.SourceDBCount
	status.IndexedRows = stats.IndexedRows
	status.IndexedTables = stats.IndexedTables
	status.SourceRows = stats.SourceRows
	status.CoveredChats = stats.CoveredChats
	status.BuiltAt = stats.BuiltAt
	status.RefreshedAt = stats.RefreshedAt
	status.IndexedMaxCreateTime = stats.MaxCreateTime
	status.RefreshMode = stats.RefreshMode
	status.SessionLagSeconds = stats.SessionLagSeconds
	status.ActiveChatsChecked = stats.ActiveChatsChecked
	status.ActiveChatsRefreshed = stats.ActiveChatsRefreshed
	status.ReconcileSourcesPending = stats.ReconcileSourcesPending
	status.ReconcileTablesChecked = stats.ReconcileTablesChecked
	if status.SchemaVersion != SchemaVersion {
		status.Status = StatusSchemaMismatch
		status.QueryMode = QueryModeScan
		status.Reason = "schema_version_mismatch"
		return status, nil
	}

	job, jobState := loadCurrentJob(indexPath)
	status.JobState = jobState
	switch status.Status {
	case StatusBuilding:
		if jobState == JobStateActive {
			return mergeProgressStatus(statusFromProgress(indexPath, job, sourceStat{}, StatusBuilding, JobStateActive), status), nil
		}
		return mergeProgressStatus(statusFromProgress(indexPath, job, sourceStat{}, StatusInterrupted, JobStateInterrupted), status), nil
	case StatusInterrupted:
		return mergeProgressStatus(statusFromProgress(indexPath, job, sourceStat{}, StatusInterrupted, jobState), status), nil
	case StatusRefreshing:
		if jobState != JobStateActive {
			status.Status = StatusReady
		}
	case "":
		status.Status = StatusReady
	}

	sources, err := indexSourcesDB(ctx, indexDB)
	if err != nil {
		status.Status = StatusStale
		status.QueryMode = QueryModeScan
		status.Reason = "source_db_unreadable"
		return status, nil
	}
	current, reason, err := currentSourceStats(messageCachePaths)
	if err != nil {
		return Status{}, err
	}
	if reason != "" {
		status.Status = StatusStale
		status.QueryMode = QueryModeScan
		status.Reason = reason
		return status, nil
	}
	status.SourceDBCount = len(current)
	sourceByName := make(map[string]sourceRow, len(sources))
	for _, source := range sources {
		sourceByName[source.SourceDB] = source
	}
	currentSeen := make(map[string]bool, len(current))
	changed := false
	var newestChange time.Time
	for _, stat := range current {
		row, ok := sourceByName[stat.SourceDB]
		if !ok {
			changed = true
			if t := time.Unix(0, stat.MTimeNS); t.After(newestChange) {
				newestChange = t
			}
			continue
		}
		currentSeen[stat.SourceDB] = true
		if row.CachePath != stat.Path {
			status.Status = StatusStale
			status.QueryMode = QueryModeScan
			status.Reason = "message_cache_path_changed"
			return status, nil
		}
		if row.CacheSize != stat.Size || row.CacheMTimeNS != stat.MTimeNS {
			changed = true
			if t := time.Unix(0, stat.MTimeNS); t.After(newestChange) {
				newestChange = t
			}
		}
	}
	for _, source := range sources {
		if !currentSeen[source.SourceDB] {
			status.Status = StatusStale
			status.QueryMode = QueryModeScan
			status.Reason = "message_cache_removed"
			return status, nil
		}
	}
	if changed {
		status.LagSeconds = secondsSince(newestChange)
		if status.LagSeconds > int64(realtimeLagWindow.Seconds()) {
			status.Status = StatusStale
			status.QueryMode = QueryModeScan
			status.LagPolicy = "exceeds_60s"
			status.Reason = "index_lag_exceeds_realtime_window"
			return status, nil
		}
		status.QueryMode = QueryModeIndex
		status.LagPolicy = "realtime_within_60s"
		status.Reason = "index_is_near_realtime"
		return status, nil
	}
	status.LagSeconds = 0
	status.QueryMode = QueryModeIndex
	status.LagPolicy = "realtime_within_60s"
	status.Reason = "index_is_current"
	return status, nil
}

func DetailedStatusFor(ctx context.Context, indexPath string, messageCachePaths []string) (Status, error) {
	status, err := StatusFor(ctx, indexPath, messageCachePaths)
	if err != nil {
		return status, err
	}
	if status.SchemaVersion != SchemaVersion || status.Status == StatusMissing || status.Status == StatusBuilding || status.Status == StatusInterrupted {
		return status, nil
	}
	if status.Status == StatusStale && status.Reason != "index_lag_exceeds_realtime_window" {
		return status, nil
	}
	summaries, err := scanSourceSummaries(ctx, messageCachePaths, nil)
	if err != nil {
		status.Status = StatusStale
		status.QueryMode = QueryModeScan
		status.Reason = "source_db_unreadable"
		return status, nil
	}
	var sourceRows int64
	var sourceMax int64
	for _, summary := range summaries {
		sourceRows += summary.RowCount
		if summary.MaxCreateTime > sourceMax {
			sourceMax = summary.MaxCreateTime
		}
	}
	status.SourceRows = sourceRows
	status.SourceMaxCreateTime = sourceMax
	if sourceMax > status.IndexedMaxCreateTime {
		status.LagSeconds = sourceMax - status.IndexedMaxCreateTime
		if status.LagSeconds > int64(realtimeLagWindow.Seconds()) {
			status.Status = StatusStale
			status.QueryMode = QueryModeScan
			status.LagPolicy = "exceeds_60s"
			status.Reason = "index_lag_exceeds_realtime_window"
			return status, nil
		}
		status.QueryMode = QueryModeIndex
		status.LagPolicy = "realtime_within_60s"
		status.Reason = "index_is_near_realtime"
		return status, nil
	}
	status.LagSeconds = 0
	status.QueryMode = QueryModeIndex
	status.LagPolicy = "realtime_within_60s"
	if status.Status == StatusStale && status.Reason == "index_lag_exceeds_realtime_window" {
		status.Status = StatusReady
	}
	status.Reason = "index_is_current"
	return status, nil
}

func Build(ctx context.Context, opts BuildOptions) (BuildResult, error) {
	started := time.Now()
	indexPath, err := resolveIndexPath(opts)
	if err != nil {
		return BuildResult{}, err
	}
	opts.IndexPath = indexPath
	if err := Reset(ctx, opts); err != nil {
		return BuildResult{}, err
	}
	result, err := Refresh(ctx, opts)
	if err != nil {
		return BuildResult{}, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func Refresh(ctx context.Context, opts BuildOptions) (BuildResult, error) {
	started := time.Now()
	if len(opts.MessageCachePaths) == 0 {
		return BuildResult{}, fmt.Errorf("message cache does not exist: run `wxview init` or `wxview messages --refresh --username USERNAME` first")
	}
	indexPath, err := resolveIndexPath(opts)
	if err != nil {
		return BuildResult{}, err
	}
	opts.IndexPath = indexPath
	tableToChat, err := buildTableMap(ctx, opts)
	if err != nil {
		return BuildResult{}, err
	}
	if len(tableToChat) == 0 {
		return BuildResult{}, fmt.Errorf("no contact usernames available for message index refresh")
	}
	lock, locked, err := acquireIndexLock(indexPath)
	if err != nil {
		return BuildResult{}, err
	}
	if !locked {
		status, statusErr := StatusFor(ctx, indexPath, opts.MessageCachePaths)
		if statusErr != nil {
			return BuildResult{}, statusErr
		}
		status.Reason = "index_refresh_already_running"
		return BuildResult{Status: status, DurationMS: time.Since(started).Milliseconds()}, nil
	}
	defer lock.release()
	if err := CleanIndexTmp(indexPath); err != nil {
		return BuildResult{}, err
	}
	if err := refreshIndexFile(ctx, opts, tableToChat); err != nil {
		markInterrupted(indexPath)
		return BuildResult{}, err
	}
	status, err := StatusFor(ctx, indexPath, opts.MessageCachePaths)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		Status:     status,
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func Reset(ctx context.Context, opts BuildOptions) error {
	indexPath, err := resolveIndexPath(opts)
	if err != nil {
		return err
	}
	lock, locked, err := acquireIndexLock(indexPath)
	if err != nil {
		return err
	}
	if !locked {
		return fmt.Errorf("message index job is already active")
	}
	defer lock.release()
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = os.Remove(jobPathFor(indexPath))
	_ = os.Remove(oldProgressPathFor(indexPath))
	return CleanIndexTmp(indexPath)
}

func CleanIndexTmp(indexPath string) error {
	_, err := CleanIndexTmpResult(indexPath)
	return err
}

func CleanIndexTmpResult(indexPath string) (CleanResult, error) {
	indexDir := filepath.Dir(indexPath)
	result := CleanResult{Path: indexDir}
	activeTemp := ""
	if job, state := loadCurrentJob(indexPath); state == JobStateActive {
		activeTemp = job.TempPath
	}
	matches, err := filepath.Glob(filepath.Join(indexDir, ".messages-*.db"))
	if err != nil {
		return result, err
	}
	for _, path := range matches {
		if activeTemp != "" && sameCleanPath(path, activeTemp) {
			result.Kept++
			continue
		}
		info, statErr := os.Stat(path)
		if statErr == nil {
			result.RemovedBytes += info.Size()
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return result, err
		}
		result.Removed++
	}
	if job, state := loadCurrentJob(indexPath); state != JobStateActive || job.PID == 0 {
		if err := os.Remove(oldProgressPathFor(indexPath)); err == nil {
			result.Removed++
		}
	}
	return result, nil
}

func sameCleanPath(a string, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA == nil {
		a = aa
	}
	if errB == nil {
		b = bb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func UseForQuery(ctx context.Context, indexPath string, messageCachePaths []string, opts UsabilityOptions) (Usability, error) {
	status, err := StatusFor(ctx, indexPath, messageCachePaths)
	if err != nil {
		return Usability{Status: status, Reason: "index_status_error"}, err
	}
	out := Usability{Status: status, Reason: status.Reason}
	switch status.Status {
	case StatusReady, StatusRefreshing, StatusStale:
	default:
		out.Reason = status.Reason
		return out, nil
	}
	historicalGloballyCovered := opts.HasEnd && status.IndexedMaxCreateTime > 0 && opts.End <= status.IndexedMaxCreateTime
	chatFresh := false
	needChatFresh := status.Status == StatusStale || status.QueryMode != QueryModeIndex
	var currentHints map[string]sessions.IndexHint
	if needChatFresh {
		currentHints, _ = loadSessionHints(ctx, opts.SessionCachePath)
	}
	if strings.TrimSpace(opts.Username) != "" {
		ok, err := HasChat(ctx, indexPath, opts.Username)
		if err != nil || !ok {
			out.Reason = "chat_not_covered_by_index"
			return out, err
		}
		if needChatFresh {
			chatFresh, err = chatFreshForQuery(ctx, indexPath, currentHints, opts.Username, opts.End, opts.HasEnd)
			if err != nil {
				return out, err
			}
		}
	} else if len(opts.Chats) > 0 {
		if status.QueryMode != QueryModeIndex || status.Status == StatusStale {
			ok, err := CoversChats(ctx, indexPath, opts.Chats)
			if err != nil || !ok {
				out.Reason = "chat_not_covered_by_index"
				return out, err
			}
		}
		if needChatFresh {
			chatFresh, err = chatsFreshForQuery(ctx, indexPath, currentHints, opts.Chats, opts.End, opts.HasEnd)
			if err != nil {
				return out, err
			}
		}
	}
	if status.Status == StatusStale {
		if status.Reason != "index_lag_exceeds_realtime_window" {
			out.Reason = status.Reason
			return out, nil
		}
		if !historicalGloballyCovered && !chatFresh {
			out.Reason = status.Reason
			return out, nil
		}
	}
	if status.QueryMode != QueryModeIndex && !historicalGloballyCovered && !chatFresh {
		out.Reason = status.Reason
		return out, nil
	}
	out.UseIndex = true
	out.IndexPath = indexPath
	out.Reason = "index_usable"
	return out, nil
}

func MessageRefs(ctx context.Context, indexPath string, query MessageQuery) ([]messages.RowRef, error) {
	username := strings.TrimSpace(query.Username)
	if username == "" {
		return nil, fmt.Errorf("username is required")
	}
	if query.Limit < 0 || query.Offset < 0 {
		return nil, fmt.Errorf("limit and offset must be >= 0")
	}
	order, err := messages.NormalizeOrder(query.Order)
	if err != nil {
		return nil, err
	}
	tableName := messages.TableName(username)
	ok, err := chatTableExists(ctx, indexPath, tableName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: chat table is not indexed", ErrUnavailable)
	}

	clauses := []string{"table_name = ?"}
	args := []any{tableName}
	if query.HasStart {
		clauses = append(clauses, "create_time >= ?")
		args = append(args, query.Start)
	}
	if query.HasEnd {
		clauses = append(clauses, "create_time <= ?")
		args = append(args, query.End)
	}
	if query.HasAfterSeq {
		clauses = append(clauses, "sort_seq > ?")
		args = append(args, query.AfterSeq)
	}
	if query.After != nil {
		op := ">"
		if order == "desc" {
			op = "<"
		}
		clauses = append(clauses, "(create_time, sort_seq, local_id, source_db) "+op+" (?, ?, ?, ?)")
		args = append(args, query.After.CreateTime, query.After.Seq, query.After.LocalID, query.After.SourceDB)
	}
	direction := " ASC"
	if order == "desc" {
		direction = " DESC"
	}
	stmt := `
SELECT source_db, table_name, chat_username, local_id
FROM message_index
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY create_time` + direction + ", sort_seq" + direction + ", local_id" + direction + ", source_db" + direction
	stmt += limitOffsetSQL(query.Limit, query.Offset)
	stmt += ";"
	return queryRefs(ctx, indexPath, stmt, args...)
}

func HasChat(ctx context.Context, indexPath string, username string) (bool, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return false, nil
	}
	return chatTableExists(ctx, indexPath, messages.TableName(username))
}

func CoversChats(ctx context.Context, indexPath string, chats []messages.ChatInfo) (bool, error) {
	seen := make(map[string]bool, len(chats))
	for _, chat := range chats {
		username := strings.TrimSpace(chat.Username)
		if username == "" || seen[username] {
			continue
		}
		seen[username] = true
		ok, err := HasChat(ctx, indexPath, username)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func chatFreshForQuery(ctx context.Context, indexPath string, currentHints map[string]sessions.IndexHint, username string, end int64, hasEnd bool) (bool, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return false, nil
	}
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	return chatFreshForQueryDB(ctx, db, currentHints, username, end, hasEnd)
}

func chatsFreshForQuery(ctx context.Context, indexPath string, currentHints map[string]sessions.IndexHint, chats []messages.ChatInfo, end int64, hasEnd bool) (bool, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	seen := map[string]bool{}
	for _, chat := range chats {
		username := strings.TrimSpace(chat.Username)
		if username == "" || seen[username] {
			continue
		}
		seen[username] = true
		ok, err := chatFreshForQueryDB(ctx, db, currentHints, username, end, hasEnd)
		if err != nil || !ok {
			return false, err
		}
	}
	return len(seen) > 0, nil
}

func chatFreshForQueryDB(ctx context.Context, db *sql.DB, currentHints map[string]sessions.IndexHint, username string, end int64, hasEnd bool) (bool, error) {
	tableName := messages.TableName(username)
	var maxCreateTime int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(max(max_create_time), 0) FROM chat_table WHERE table_name = ?;", tableName).Scan(&maxCreateTime)
	if err != nil {
		return false, err
	}
	if maxCreateTime <= 0 {
		return false, nil
	}
	if hasEnd && end <= maxCreateTime {
		return true, nil
	}
	if currentHints != nil {
		if hint, ok := currentHints[username]; ok {
			return maxCreateTime >= hint.LastTimestamp, nil
		}
	}
	ok, err := sqlitedb.TableExists(ctx, db, "session_snapshot")
	if err != nil || !ok {
		return false, err
	}
	var lastTimestamp int64
	err = db.QueryRowContext(ctx, "SELECT last_timestamp FROM session_snapshot WHERE username = ? LIMIT 1;", username).Scan(&lastTimestamp)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return maxCreateTime >= lastTimestamp, nil
}

func TimelineRefs(ctx context.Context, indexPath string, query TimelineQuery) ([]messages.RowRef, error) {
	if query.Limit <= 0 {
		return nil, fmt.Errorf("limit must be > 0")
	}
	tableNames := make([]string, 0, len(query.Chats))
	seen := map[string]bool{}
	for _, chat := range query.Chats {
		username := strings.TrimSpace(chat.Username)
		if username == "" {
			continue
		}
		tableName := messages.TableName(username)
		if seen[tableName] {
			continue
		}
		seen[tableName] = true
		tableNames = append(tableNames, tableName)
	}
	if len(tableNames) == 0 {
		return []messages.RowRef{}, nil
	}
	sort.Strings(tableNames)
	args := make([]any, 0, len(tableNames)+4)
	for _, tableName := range tableNames {
		args = append(args, tableName)
	}
	clauses := []string{
		"table_name IN (" + sqlitedb.Placeholders(len(tableNames)) + ")",
		"create_time >= ?",
		"create_time <= ?",
	}
	args = append(args, query.Start, query.End)
	if query.HasAfter {
		afterSQL, afterArgs := timelineAfterSQL(query.After)
		clauses = append(clauses, afterSQL)
		args = append(args, afterArgs...)
	}
	stmt := `
SELECT source_db, table_name, chat_username, local_id
FROM message_index
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY create_time ASC, sort_seq ASC, chat_username ASC, local_id ASC, source_db ASC
LIMIT ` + strconv.Itoa(query.Limit) + ";"
	return queryRefs(ctx, indexPath, stmt, args...)
}

func SearchRefs(ctx context.Context, indexPath string, query SearchQuery) ([]messages.RowRef, int, error) {
	needle := strings.TrimSpace(query.Query)
	if needle == "" {
		return nil, 0, fmt.Errorf("query is required")
	}
	if query.Limit < 0 || query.Offset < 0 {
		return nil, 0, fmt.Errorf("limit and offset must be >= 0")
	}
	if !ftsQuerySupported(needle) {
		return nil, 0, fmt.Errorf("%w: query is not suitable for FTS fast path", ErrUnavailable)
	}
	clauses := []string{"message_fts MATCH ?"}
	args := []any{ftsPhrase(needle)}
	if len(query.Chats) > 0 {
		usernames := make([]string, 0, len(query.Chats))
		seen := map[string]bool{}
		for _, chat := range query.Chats {
			username := strings.TrimSpace(chat.Username)
			if username == "" || seen[username] {
				continue
			}
			seen[username] = true
			usernames = append(usernames, username)
		}
		if len(usernames) == 0 {
			return []messages.RowRef{}, 0, nil
		}
		sort.Strings(usernames)
		clauses = append(clauses, "chat_username IN ("+sqlitedb.Placeholders(len(usernames))+")")
		for _, username := range usernames {
			args = append(args, username)
		}
	}
	if query.HasStart {
		clauses = append(clauses, "create_time >= ?")
		args = append(args, query.Start)
	}
	if query.HasEnd {
		clauses = append(clauses, "create_time <= ?")
		args = append(args, query.End)
	}
	where := strings.Join(clauses, " AND ")
	total, err := queryCount(ctx, indexPath, "SELECT count(*) FROM message_fts WHERE "+where+";", args...)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	stmt := `
SELECT source_db, table_name, chat_username, local_id
FROM message_fts
WHERE ` + where + `
ORDER BY create_time DESC, sort_seq DESC, local_id DESC, source_db ASC`
	stmt += limitOffsetSQL(query.Limit, query.Offset)
	stmt += ";"
	refs, err := queryRefs(ctx, indexPath, stmt, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return refs, int(total), nil
}

func refreshIndexFile(ctx context.Context, opts BuildOptions, tableToChat map[string]string) error {
	indexPath := opts.IndexPath
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}
	mode, err := refreshMode(ctx, indexPath)
	if err != nil {
		return err
	}
	if mode == StatusSchemaMismatch || mode == StatusMissing {
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		mode = StatusBuilding
	}
	if _, err := os.Stat(indexPath); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err := initializeIndexDB(ctx, indexPath, opts.Account, StatusBuilding); err != nil {
			return err
		}
		mode = StatusBuilding
	}
	if mode == "" {
		mode = StatusBuilding
	}
	if mode == StatusReady || mode == StatusRefreshing {
		return refreshReadyIndex(ctx, opts, tableToChat)
	}
	return buildOrResumeIndex(ctx, opts, tableToChat)
}

func refreshMode(ctx context.Context, indexPath string) (string, error) {
	if _, err := os.Stat(indexPath); err != nil {
		if os.IsNotExist(err) {
			return StatusMissing, nil
		}
		return "", err
	}
	stats, err := indexStats(ctx, indexPath)
	if err != nil {
		return StatusSchemaMismatch, nil
	}
	if parseSchemaVersion(stats.SchemaVersion) != SchemaVersion {
		return StatusSchemaMismatch, nil
	}
	return normalizeIndexState(stats.State), nil
}

func buildOrResumeIndex(ctx context.Context, opts BuildOptions, tableToChat map[string]string) error {
	indexPath := opts.IndexPath
	if err := setMeta(ctx, indexPath, map[string]string{
		"schema_version": strconv.Itoa(SchemaVersion),
		"account":        opts.Account,
		"state":          StatusBuilding,
	}); err != nil {
		return err
	}
	indexDB, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		return err
	}
	defer indexDB.Close()
	if err := ensureIndexSchemaDB(ctx, indexDB); err != nil {
		return err
	}
	tracker := newProgressTracker(jobPathFor(indexPath), opts.Account, indexPath, "", 0, 0, 0)
	tracker.progress.PID = os.Getpid()
	tracker.progress.JobID = strconv.FormatInt(time.Now().UnixNano(), 10)
	tracker.progress.CurrentTable = "preparing"
	if err := tracker.write(); err != nil {
		return err
	}
	plan, totalTables, err := prepareBuildPlan(ctx, opts.MessageCachePaths, tableToChat)
	if err != nil {
		return err
	}
	currentRows := indexedRowCountDB(ctx, indexDB)
	watermarks, err := indexedTableWatermarksDB(ctx, indexDB)
	if err != nil {
		return err
	}
	completedTables := completedTableKeysFromWatermarks(watermarks)
	if err := tracker.setTotals(len(plan), totalTables, 0); err != nil {
		return err
	}
	tracker.progress.IndexedRows = currentRows
	tracker.progress.CoveredTables = minInt(len(completedTables), totalTables)
	if totalTables > 0 {
		tracker.progress.Percent = float64(tracker.progress.CoveredTables) * 100 / float64(totalTables)
	}
	if err := tracker.write(); err != nil {
		return err
	}
	indexedAt := time.Now().UTC().Format(time.RFC3339Nano)
	for _, source := range plan {
		sourceDB, err := sqlitedb.OpenReadOnly(ctx, source.Stat.Path)
		if err != nil {
			return err
		}
		for _, table := range source.Tables {
			afterLocalID := maxLocalIDFromWatermarks(watermarks, source.Stat.SourceDB, table.Name)
			if err := tracker.startTable(source.Stat.SourceDB, table.Name); err != nil {
				_ = sourceDB.Close()
				return err
			}
			progressKey := indexedTableKey(source.Stat.SourceDB, table.Name)
			alreadyCompleted := completedTables[progressKey]
			_, err = indexTableIncrementalTx(ctx, indexDB, sourceDB, source.Stat.Path, source.Stat.SourceDB, table.Name, table.ChatUsername, afterLocalID, tracker)
			if err != nil {
				_ = sourceDB.Close()
				return err
			}
			if !alreadyCompleted {
				completedTables[progressKey] = true
			}
			if err := tracker.finishTable(len(completedTables)); err != nil {
				_ = sourceDB.Close()
				return err
			}
		}
		if err := sourceDB.Close(); err != nil {
			return err
		}
		if err := refreshSourceDB(ctx, indexDB, source.Stat, indexedAt); err != nil {
			return err
		}
	}
	indexedRows := indexedRowCountDB(ctx, indexDB)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sessionLag := int64(0)
	activeChats := 0
	if hints, ok := loadSessionHints(ctx, opts.SessionCachePath); ok {
		activeChats = len(hints)
		for _, hint := range hints {
			if err := upsertSessionSnapshotDB(ctx, indexDB, hint, now); err != nil {
				return err
			}
		}
	}
	if err := setMetaDB(ctx, indexDB, map[string]string{
		"schema_version":            strconv.Itoa(SchemaVersion),
		"account":                   opts.Account,
		"state":                     StatusReady,
		"built_at":                  firstNonEmpty(metaValueDB(ctx, indexDB, "built_at"), now),
		"refreshed_at":              now,
		"source_total_rows":         strconv.FormatInt(indexedRows, 10),
		"indexed_rows":              strconv.FormatInt(indexedRows, 10),
		"source_db_count":           strconv.Itoa(len(opts.MessageCachePaths)),
		"refresh_mode":              "full_build",
		"session_lag_seconds":       strconv.FormatInt(sessionLag, 10),
		"active_chats_checked":      strconv.Itoa(activeChats),
		"active_chats_refreshed":    strconv.Itoa(activeChats),
		"reconcile_sources_pending": "0",
		"reconcile_tables_checked":  "0",
	}); err != nil {
		return err
	}
	_ = os.Remove(jobPathFor(indexPath))
	return app.ChownTreeForSudo(filepath.Dir(indexPath))
}

func refreshReadyIndex(ctx context.Context, opts BuildOptions, tableToChat map[string]string) error {
	indexPath := opts.IndexPath
	indexDB, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		return err
	}
	defer indexDB.Close()
	if err := ensureIndexSchemaDB(ctx, indexDB); err != nil {
		return err
	}
	if err := setMetaDB(ctx, indexDB, map[string]string{"state": StatusRefreshing}); err != nil {
		return err
	}
	currentRows := indexedRowCountDB(ctx, indexDB)
	tracker := newProgressTracker(jobPathFor(indexPath), opts.Account, indexPath, "", len(opts.MessageCachePaths), 0, 0)
	tracker.progress.Status = StatusRefreshing
	tracker.progress.PID = os.Getpid()
	tracker.progress.JobID = strconv.FormatInt(time.Now().UnixNano(), 10)
	tracker.progress.IndexedRows = currentRows
	if err := tracker.write(); err != nil {
		return err
	}
	watermarks, err := indexedTableWatermarksDB(ctx, indexDB)
	if err != nil {
		return err
	}
	sources, err := indexSourcesDB(ctx, indexDB)
	if err != nil {
		return err
	}
	var plan []refreshSourcePlan
	planStats := refreshPlanStats{RefreshMode: "source_scan"}
	if hints, ok := loadSessionHints(ctx, opts.SessionCachePath); ok {
		snapshots, err := loadSessionSnapshotsDB(ctx, indexDB)
		if err != nil {
			return err
		}
		hotPlan, hotStats, err := prepareHotRefreshPlan(ctx, opts.MessageCachePaths, tableToChat, watermarks, sources, hints, snapshots)
		if err != nil {
			return err
		}
		reconcilePlan, reconcileStats, err := prepareReconcilePlan(ctx, indexDB, opts.MessageCachePaths, tableToChat, watermarks, sources, reconcileTableBudget)
		if err != nil {
			return err
		}
		plan = append(hotPlan, reconcilePlan...)
		planStats = hotStats
		planStats.RefreshMode = "session_delta"
		planStats.ReconcileSourcesPending = reconcileStats.ReconcileSourcesPending
		planStats.ReconcileTablesChecked = reconcileStats.ReconcileTablesChecked
	} else {
		plan, err = prepareRefreshPlan(ctx, opts.MessageCachePaths, tableToChat, watermarks, sources)
		if err != nil {
			return err
		}
	}
	totalTables := 0
	for _, source := range plan {
		totalTables += len(source.Tables)
	}
	tracker.progress.TotalTables = totalTables
	tracker.progress.RefreshMode = planStats.RefreshMode
	tracker.progress.SessionLagSeconds = planStats.SessionLagSeconds
	tracker.progress.ActiveChatsChecked = planStats.ActiveChatsChecked
	tracker.progress.ReconcileSourcesPending = planStats.ReconcileSourcesPending
	tracker.progress.ReconcileTablesChecked = planStats.ReconcileTablesChecked
	if err := tracker.write(); err != nil {
		return err
	}
	completedTables := completedTableKeysFromWatermarks(watermarks)
	tracker.progress.CoveredTables = len(completedTables)
	indexedAt := time.Now().UTC().Format(time.RFC3339Nano)
	pendingHintSources := pendingSessionHintSources(plan)
	refreshedHints := map[string]bool{}
	reconcileDeadline := time.Now().Add(reconcileTimeBudget)
	for _, source := range plan {
		sourceDB := (*sql.DB)(nil)
		if len(source.Tables) > 0 {
			sourceDB, err = sqlitedb.OpenReadOnly(ctx, source.Stat.Path)
			if err != nil {
				return err
			}
		}
		sourceCompleted := true
		lastReconcileProcessed := ""
		for tableIndex, table := range source.Tables {
			if source.Reconcile && time.Now().After(reconcileDeadline) {
				sourceCompleted = false
				source.Tables = source.Tables[tableIndex:]
				break
			}
			if err := tracker.startTable(source.Stat.SourceDB, table.Name); err != nil {
				if sourceDB != nil {
					_ = sourceDB.Close()
				}
				return err
			}
			_, err := indexTableIncrementalTx(ctx, indexDB, sourceDB, source.Stat.Path, source.Stat.SourceDB, table.Name, table.ChatUsername, table.AfterLocalID, tracker)
			if err != nil {
				if sourceDB != nil {
					_ = sourceDB.Close()
				}
				return err
			}
			if source.Reconcile {
				lastReconcileProcessed = table.Name
			}
			progressKey := indexedTableKey(source.Stat.SourceDB, table.Name)
			if !completedTables[progressKey] {
				completedTables[progressKey] = true
			}
			if table.SessionHint != nil {
				if remaining := pendingHintSources[table.SessionHint.Username] - 1; remaining <= 0 {
					delete(pendingHintSources, table.SessionHint.Username)
					if err := upsertSessionSnapshotDB(ctx, indexDB, *table.SessionHint, indexedAt); err != nil {
						if sourceDB != nil {
							_ = sourceDB.Close()
						}
						return err
					}
					refreshedHints[table.SessionHint.Username] = true
					tracker.progress.ActiveChatsRefreshed = len(refreshedHints)
				} else {
					pendingHintSources[table.SessionHint.Username] = remaining
				}
			}
			if err := tracker.finishTable(len(completedTables)); err != nil {
				if sourceDB != nil {
					_ = sourceDB.Close()
				}
				return err
			}
		}
		if sourceDB != nil {
			if err := sourceDB.Close(); err != nil {
				return err
			}
		}
		if source.Reconcile {
			if !sourceCompleted {
				if lastReconcileProcessed != "" {
					if err := setReconcileCursorDB(ctx, indexDB, source.Stat.SourceDB, lastReconcileProcessed); err != nil {
						return err
					}
				}
				if source.Exhausted {
					planStats.ReconcileSourcesPending++
				}
				continue
			}
			if source.CheckedThrough != "" {
				if source.Exhausted {
					if err := clearReconcileCursorDB(ctx, indexDB, source.Stat.SourceDB); err != nil {
						return err
					}
				} else if err := setReconcileCursorDB(ctx, indexDB, source.Stat.SourceDB, source.CheckedThrough); err != nil {
					return err
				}
			}
			if !source.Exhausted {
				continue
			}
		}
		if planStats.RefreshMode == "session_delta" && !source.Reconcile {
			continue
		}
		if err := refreshSourceDB(ctx, indexDB, source.Stat, indexedAt); err != nil {
			return err
		}
	}
	indexedRows := indexedRowCountDB(ctx, indexDB)
	if planStats.RefreshMode == "session_delta" {
		if hints, ok := loadSessionHints(ctx, opts.SessionCachePath); ok {
			if err := bootstrapCoveredSessionSnapshotsDB(ctx, indexDB, hints, watermarks, indexedAt); err != nil {
				return err
			}
			snapshots, err := loadSessionSnapshotsDB(ctx, indexDB)
			if err != nil {
				return err
			}
			planStats.SessionLagSeconds = sessionLagSeconds(hints, snapshots)
		}
		planStats.ActiveChatsRefreshed = len(refreshedHints)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := setMetaDB(ctx, indexDB, map[string]string{
		"schema_version":            strconv.Itoa(SchemaVersion),
		"account":                   opts.Account,
		"state":                     StatusReady,
		"refreshed_at":              now,
		"source_total_rows":         strconv.FormatInt(indexedRows, 10),
		"indexed_rows":              strconv.FormatInt(indexedRows, 10),
		"source_db_count":           strconv.Itoa(len(opts.MessageCachePaths)),
		"refresh_mode":              planStats.RefreshMode,
		"session_lag_seconds":       strconv.FormatInt(planStats.SessionLagSeconds, 10),
		"active_chats_checked":      strconv.Itoa(planStats.ActiveChatsChecked),
		"active_chats_refreshed":    strconv.Itoa(planStats.ActiveChatsRefreshed),
		"reconcile_sources_pending": strconv.Itoa(planStats.ReconcileSourcesPending),
		"reconcile_tables_checked":  strconv.Itoa(planStats.ReconcileTablesChecked),
	}); err != nil {
		return err
	}
	_ = os.Remove(jobPathFor(indexPath))
	return app.ChownTreeForSudo(filepath.Dir(indexPath))
}

func indexTableIncrementalTx(ctx context.Context, indexDB *sql.DB, sourceConn *sql.DB, dbPath string, sourceDB string, tableName string, chatUsername string, afterLocalID int64, tracker *progressTracker) (int64, error) {
	tx, err := indexDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	rollback := func(cause error) (int64, error) {
		_ = tx.Rollback()
		return 0, cause
	}
	insertIndex, err := tx.PrepareContext(ctx, `
INSERT OR IGNORE INTO message_index(source_db, table_name, chat_username, local_id, create_time, sort_seq, server_id, local_type, status, real_sender_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`)
	if err != nil {
		return rollback(err)
	}
	defer insertIndex.Close()
	insertFTS, err := tx.PrepareContext(ctx, `
INSERT INTO message_fts(search_text, source_db, table_name, chat_username, local_id, create_time, sort_seq)
VALUES (?, ?, ?, ?, ?, ?, ?);`)
	if err != nil {
		return rollback(err)
	}
	defer insertFTS.Close()
	var added int64
	if err := streamSourceRowsAfter(ctx, sourceConn, dbPath, tableName, afterLocalID, func(row sourceMessageRow) error {
		decoded, _ := messages.DecodeContent(row.Content)
		result, err := insertIndex.ExecContext(ctx,
			sourceDB,
			tableName,
			chatUsername,
			row.LocalID,
			row.CreateTime,
			row.SortSeq,
			row.ServerID,
			row.LocalType,
			row.Status,
			row.RealSenderID,
		)
		if err != nil {
			return fmt.Errorf("insert message index source_db=%s table=%s local_id=%d: %w", sourceDB, tableName, row.LocalID, err)
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			return nil
		}
		searchText := messages.SearchText(chatUsername, row.LocalType, row.Status, row.RealSenderID, row.SenderName, decoded)
		if strings.TrimSpace(searchText) != "" {
			if _, err := insertFTS.ExecContext(ctx,
				searchText,
				sourceDB,
				tableName,
				chatUsername,
				row.LocalID,
				row.CreateTime,
				row.SortSeq,
			); err != nil {
				return fmt.Errorf("insert message FTS source_db=%s table=%s local_id=%d: %w", sourceDB, tableName, row.LocalID, err)
			}
		}
		added++
		if tracker != nil {
			return tracker.addRow()
		}
		return nil
	}); err != nil {
		return rollback(err)
	}
	if err := refreshChatTableTx(ctx, tx, sourceDB, tableName, chatUsername); err != nil {
		return rollback(err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit message index source_db=%s table=%s: %w", sourceDB, tableName, err)
	}
	return added, nil
}

func loadSessionHints(ctx context.Context, sessionCachePath string) (map[string]sessions.IndexHint, bool) {
	if strings.TrimSpace(sessionCachePath) == "" {
		return nil, false
	}
	hints, err := sessions.NewService(sessionCachePath).IndexHints(ctx)
	if err != nil {
		return nil, false
	}
	out := make(map[string]sessions.IndexHint, len(hints))
	for _, hint := range hints {
		if strings.TrimSpace(hint.Username) == "" || strings.TrimSpace(hint.TableName) == "" {
			continue
		}
		out[hint.Username] = hint
	}
	return out, len(out) > 0
}

func loadSessionSnapshotsDB(ctx context.Context, db *sql.DB) (map[string]sessionSnapshotRow, error) {
	ok, err := sqlitedb.TableExists(ctx, db, "session_snapshot")
	if err != nil {
		return nil, err
	}
	out := map[string]sessionSnapshotRow{}
	if !ok {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT username, table_name, last_timestamp, refreshed_at FROM session_snapshot;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row sessionSnapshotRow
		if err := rows.Scan(&row.Username, &row.TableName, &row.LastTimestamp, &row.RefreshedAt); err != nil {
			return nil, err
		}
		out[row.Username] = row
	}
	return out, rows.Err()
}

func upsertSessionSnapshotDB(ctx context.Context, db *sql.DB, hint sessions.IndexHint, refreshedAt string) error {
	_, err := db.ExecContext(ctx, `
INSERT OR REPLACE INTO session_snapshot(username, table_name, last_timestamp, refreshed_at)
VALUES (?, ?, ?, ?);`,
		hint.Username,
		hint.TableName,
		hint.LastTimestamp,
		refreshedAt,
	)
	return err
}

func bootstrapCoveredSessionSnapshotsDB(ctx context.Context, db *sql.DB, hints map[string]sessions.IndexHint, watermarks map[string]chatTableMetaRow, refreshedAt string) error {
	for _, hint := range hints {
		if hint.LastTimestamp <= 0 {
			continue
		}
		if maxCreateTimeForTable(watermarks, hint.TableName) < hint.LastTimestamp {
			continue
		}
		if err := upsertSessionSnapshotDB(ctx, db, hint, refreshedAt); err != nil {
			return err
		}
	}
	return nil
}

func prepareHotRefreshPlan(ctx context.Context, messageCachePaths []string, tableToChat map[string]string, watermarks map[string]chatTableMetaRow, indexedSources []sourceRow, hints map[string]sessions.IndexHint, snapshots map[string]sessionSnapshotRow) ([]refreshSourcePlan, refreshPlanStats, error) {
	stats := refreshPlanStats{
		RefreshMode:        "session_delta",
		ActiveChatsChecked: len(hints),
		SessionLagSeconds:  sessionLagSeconds(hints, snapshots),
	}
	current, changed, err := changedSourceStats(messageCachePaths, indexedSources)
	if err != nil {
		return nil, stats, err
	}
	currentByName := make(map[string]sourceStat, len(current))
	for _, stat := range current {
		currentByName[stat.SourceDB] = stat
	}
	sourcePlans := map[string]*refreshSourcePlan{}
	orderedSources := []string{}
	tableExistsCache := map[string]map[string]bool{}
	tableProbeDBs := map[string]*sql.DB{}
	defer closeSourceProbeDBs(tableProbeDBs)
	for _, hint := range sortedSessionHints(hints) {
		if !sessionHintNeedsRefresh(hint, snapshots[hint.Username], watermarks) {
			continue
		}
		chatUsername := firstNonEmpty(tableToChat[hint.TableName], hint.Username)
		for _, sourceDB := range knownSourcesForTable(watermarks, hint.TableName) {
			stat, ok := currentByName[sourceDB]
			if !ok {
				continue
			}
			hintCopy := hint
			addRefreshTablePlan(sourcePlans, &orderedSources, stat, refreshTablePlan{
				Name:         hint.TableName,
				ChatUsername: chatUsername,
				AfterLocalID: maxLocalIDFromWatermarks(watermarks, stat.SourceDB, hint.TableName),
				SessionHint:  &hintCopy,
			})
		}
		for _, stat := range changed {
			if hasWatermarkForTable(watermarks, stat.SourceDB, hint.TableName) {
				continue
			}
			ok, err := sourceTableExists(ctx, stat, hint.TableName, tableExistsCache, tableProbeDBs)
			if err != nil {
				return nil, stats, err
			}
			if !ok {
				continue
			}
			hintCopy := hint
			addRefreshTablePlan(sourcePlans, &orderedSources, stat, refreshTablePlan{
				Name:         hint.TableName,
				ChatUsername: chatUsername,
				AfterLocalID: maxLocalIDFromWatermarks(watermarks, stat.SourceDB, hint.TableName),
				SessionHint:  &hintCopy,
			})
		}
	}
	out := make([]refreshSourcePlan, 0, len(orderedSources))
	sort.Strings(orderedSources)
	for _, sourceDB := range orderedSources {
		plan := sourcePlans[sourceDB]
		sort.SliceStable(plan.Tables, func(i, j int) bool { return plan.Tables[i].Name < plan.Tables[j].Name })
		out = append(out, *plan)
	}
	return out, stats, nil
}

func prepareReconcilePlan(ctx context.Context, indexDB *sql.DB, messageCachePaths []string, tableToChat map[string]string, watermarks map[string]chatTableMetaRow, indexedSources []sourceRow, tableBudget int) ([]refreshSourcePlan, refreshPlanStats, error) {
	stats := refreshPlanStats{}
	if tableBudget <= 0 {
		return nil, stats, nil
	}
	_, changed, err := changedSourceStats(messageCachePaths, indexedSources)
	if err != nil {
		return nil, stats, err
	}
	plans := make([]refreshSourcePlan, 0, len(changed))
	remaining := tableBudget
	for _, stat := range changed {
		if remaining <= 0 {
			stats.ReconcileSourcesPending++
			continue
		}
		sourceDB, err := sqlitedb.OpenReadOnly(ctx, stat.Path)
		if err != nil {
			return nil, stats, err
		}
		cursor := metaValueDB(ctx, indexDB, reconcileCursorKey(stat.SourceDB))
		names, checkedThrough, exhausted, checked, err := listMessageTablesAfterDB(ctx, sourceDB, cursor, remaining)
		closeErr := sourceDB.Close()
		if err != nil {
			return nil, stats, err
		}
		if closeErr != nil {
			return nil, stats, closeErr
		}
		stats.ReconcileTablesChecked += checked
		remaining -= checked
		source := refreshSourcePlan{
			Stat:           stat,
			Reconcile:      true,
			CheckedThrough: checkedThrough,
			Exhausted:      exhausted,
			CheckedTables:  checked,
		}
		for _, tableName := range names {
			chatUsername := tableToChat[tableName]
			if chatUsername == "" {
				continue
			}
			source.Tables = append(source.Tables, refreshTablePlan{
				Name:         tableName,
				ChatUsername: chatUsername,
				AfterLocalID: maxLocalIDFromWatermarks(watermarks, stat.SourceDB, tableName),
			})
		}
		if !source.Exhausted {
			stats.ReconcileSourcesPending++
		}
		plans = append(plans, source)
	}
	return plans, stats, nil
}

func prepareRefreshPlan(ctx context.Context, messageCachePaths []string, tableToChat map[string]string, watermarks map[string]chatTableMetaRow, indexedSources []sourceRow) ([]refreshSourcePlan, error) {
	plan := make([]refreshSourcePlan, 0, len(messageCachePaths))
	sourceByName := make(map[string]sourceRow, len(indexedSources))
	for _, source := range indexedSources {
		sourceByName[source.SourceDB] = source
	}
	for _, dbPath := range messageCachePaths {
		stat, err := statSource(dbPath)
		if err != nil {
			return nil, err
		}
		if sourceIsUnchanged(stat, sourceByName[stat.SourceDB]) {
			continue
		}
		sourceDB, err := sqlitedb.OpenReadOnly(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		tables, err := listMessageTablesDB(ctx, sourceDB)
		closeErr := sourceDB.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		source := refreshSourcePlan{Stat: stat}
		for _, tableName := range tables {
			chatUsername := tableToChat[tableName]
			if chatUsername == "" {
				continue
			}
			lastLocalID := maxLocalIDFromWatermarks(watermarks, stat.SourceDB, tableName)
			source.Tables = append(source.Tables, refreshTablePlan{
				Name:         tableName,
				ChatUsername: chatUsername,
				AfterLocalID: lastLocalID,
			})
		}
		plan = append(plan, source)
	}
	return plan, nil
}

func changedSourceStats(messageCachePaths []string, indexedSources []sourceRow) ([]sourceStat, []sourceStat, error) {
	sourceByName := make(map[string]sourceRow, len(indexedSources))
	for _, source := range indexedSources {
		sourceByName[source.SourceDB] = source
	}
	current := make([]sourceStat, 0, len(messageCachePaths))
	changed := make([]sourceStat, 0, len(messageCachePaths))
	for _, dbPath := range messageCachePaths {
		stat, err := statSource(dbPath)
		if err != nil {
			return nil, nil, err
		}
		current = append(current, stat)
		if !sourceIsUnchanged(stat, sourceByName[stat.SourceDB]) {
			changed = append(changed, stat)
		}
	}
	sort.Slice(current, func(i, j int) bool { return current[i].SourceDB < current[j].SourceDB })
	sort.Slice(changed, func(i, j int) bool { return changed[i].SourceDB < changed[j].SourceDB })
	return current, changed, nil
}

func sortedSessionHints(hints map[string]sessions.IndexHint) []sessions.IndexHint {
	out := make([]sessions.IndexHint, 0, len(hints))
	for _, hint := range hints {
		out = append(out, hint)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastTimestamp != out[j].LastTimestamp {
			return out[i].LastTimestamp > out[j].LastTimestamp
		}
		return out[i].Username < out[j].Username
	})
	return out
}

func sessionHintNeedsRefresh(hint sessions.IndexHint, snapshot sessionSnapshotRow, watermarks map[string]chatTableMetaRow) bool {
	if hint.LastTimestamp <= 0 {
		return false
	}
	maxCreateTime := maxCreateTimeForTable(watermarks, hint.TableName)
	if maxCreateTime <= 0 {
		return strings.TrimSpace(snapshot.Username) != ""
	}
	return maxCreateTime < hint.LastTimestamp
}

func sessionLagSeconds(hints map[string]sessions.IndexHint, snapshots map[string]sessionSnapshotRow) int64 {
	var maxHint int64
	var maxSnapshot int64
	for username, hint := range hints {
		if hint.LastTimestamp > maxHint {
			maxHint = hint.LastTimestamp
		}
		if snapshot := snapshots[username]; snapshot.LastTimestamp > maxSnapshot {
			maxSnapshot = snapshot.LastTimestamp
		}
	}
	if maxHint <= maxSnapshot {
		return 0
	}
	return maxHint - maxSnapshot
}

func maxCreateTimeForTable(watermarks map[string]chatTableMetaRow, tableName string) int64 {
	var out int64
	for _, row := range watermarks {
		if row.TableName == tableName && row.MaxCreateTime > out {
			out = row.MaxCreateTime
		}
	}
	return out
}

func knownSourcesForTable(watermarks map[string]chatTableMetaRow, tableName string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, row := range watermarks {
		if row.TableName != tableName || seen[row.SourceDB] {
			continue
		}
		seen[row.SourceDB] = true
		out = append(out, row.SourceDB)
	}
	sort.Strings(out)
	return out
}

func hasWatermarkForTable(watermarks map[string]chatTableMetaRow, sourceDB string, tableName string) bool {
	_, ok := watermarks[indexedTableKey(sourceDB, tableName)]
	return ok
}

func addRefreshTablePlan(sourcePlans map[string]*refreshSourcePlan, orderedSources *[]string, stat sourceStat, table refreshTablePlan) {
	plan := sourcePlans[stat.SourceDB]
	if plan == nil {
		plan = &refreshSourcePlan{Stat: stat}
		sourcePlans[stat.SourceDB] = plan
		*orderedSources = append(*orderedSources, stat.SourceDB)
	}
	for _, existing := range plan.Tables {
		if existing.Name == table.Name && existing.ChatUsername == table.ChatUsername && existing.AfterLocalID == table.AfterLocalID {
			return
		}
	}
	plan.Tables = append(plan.Tables, table)
}

func sourceTableExists(ctx context.Context, stat sourceStat, tableName string, cache map[string]map[string]bool, dbs map[string]*sql.DB) (bool, error) {
	if cache[stat.SourceDB] != nil {
		if ok, seen := cache[stat.SourceDB][tableName]; seen {
			return ok, nil
		}
	}
	db := dbs[stat.SourceDB]
	if db == nil {
		var err error
		db, err = sqlitedb.OpenReadOnly(ctx, stat.Path)
		if err != nil {
			return false, err
		}
		dbs[stat.SourceDB] = db
	}
	ok, tableErr := sqlitedb.TableExists(ctx, db, tableName)
	if tableErr != nil {
		return false, tableErr
	}
	if cache[stat.SourceDB] == nil {
		cache[stat.SourceDB] = map[string]bool{}
	}
	cache[stat.SourceDB][tableName] = ok
	return ok, nil
}

func closeSourceProbeDBs(dbs map[string]*sql.DB) {
	for _, db := range dbs {
		_ = db.Close()
	}
}

func listMessageTablesAfterDB(ctx context.Context, db *sql.DB, after string, limit int) ([]string, string, bool, int, error) {
	if limit <= 0 {
		return nil, after, false, 0, nil
	}
	stmt := "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Msg_%'"
	args := []any{}
	if strings.TrimSpace(after) != "" {
		stmt += " AND name > ?"
		args = append(args, after)
	}
	stmt += " ORDER BY name LIMIT ?;"
	args = append(args, limit+1)
	rows, err := db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return nil, after, false, 0, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, after, false, 0, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, after, false, 0, err
	}
	exhausted := len(names) <= limit
	if len(names) > limit {
		names = names[:limit]
	}
	checkedThrough := after
	if len(names) > 0 {
		checkedThrough = names[len(names)-1]
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if msgTableRE.MatchString(name) {
			out = append(out, name)
		}
	}
	return out, checkedThrough, exhausted, len(names), nil
}

func pendingSessionHintSources(plan []refreshSourcePlan) map[string]int {
	out := map[string]int{}
	for _, source := range plan {
		for _, table := range source.Tables {
			if table.SessionHint == nil {
				continue
			}
			out[table.SessionHint.Username]++
		}
	}
	return out
}

func reconcileCursorKey(sourceDB string) string {
	return "reconcile_cursor:" + sourceDB
}

func setReconcileCursorDB(ctx context.Context, db *sql.DB, sourceDB string, tableName string) error {
	return setMetaDB(ctx, db, map[string]string{reconcileCursorKey(sourceDB): tableName})
}

func clearReconcileCursorDB(ctx context.Context, db *sql.DB, sourceDB string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM meta WHERE key = ?;", reconcileCursorKey(sourceDB))
	return err
}

func initializeIndexDB(ctx context.Context, indexPath string, account string, state string) error {
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return err
	}
	db, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := sqlitedb.ExecScript(ctx, db, schemaSQL()); err != nil {
		return fmt.Errorf("initialize message index schema: %w", err)
	}
	return setMetaDB(ctx, db, map[string]string{
		"schema_version": strconv.Itoa(SchemaVersion),
		"account":        account,
		"state":          state,
	})
}

func ensureIndexSchemaDB(ctx context.Context, db *sql.DB) error {
	if err := sqlitedb.ExecScript(ctx, db, schemaSQL()); err != nil {
		return fmt.Errorf("ensure message index schema: %w", err)
	}
	if err := ensureSessionSnapshotSchemaDB(ctx, db); err != nil {
		return err
	}
	return nil
}

func setMeta(ctx context.Context, indexPath string, values map[string]string) error {
	db, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return setMetaDB(ctx, db, values)
}

func setMetaDB(ctx context.Context, db *sql.DB, values map[string]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?);")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for key, value := range values {
		if _, err := stmt.ExecContext(ctx, key, value); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func ensureSessionSnapshotSchemaDB(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(session_snapshot);")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			_ = rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	compatible := columns["username"] &&
		columns["table_name"] &&
		columns["last_timestamp"] &&
		columns["refreshed_at"] &&
		!columns["unread_count"] &&
		!columns["summary_hash"]
	if compatible {
		return nil
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS session_snapshot;"); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE session_snapshot(
  username TEXT PRIMARY KEY,
  table_name TEXT NOT NULL,
  last_timestamp INTEGER NOT NULL,
  refreshed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_snapshot_table ON session_snapshot(table_name);
`)
	return err
}

func metaValue(ctx context.Context, indexPath string, key string) string {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return ""
	}
	defer db.Close()
	return metaValueDB(ctx, db, key)
}

func metaValueDB(ctx context.Context, db *sql.DB, key string) string {
	var value string
	if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ? LIMIT 1;", key).Scan(&value); err != nil {
		return ""
	}
	return value
}

func schemaSQL() string {
	return fmt.Sprintf(`
PRAGMA busy_timeout=%d;
PRAGMA synchronous=NORMAL;
CREATE TABLE IF NOT EXISTS meta(
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS source_db(
  source_db TEXT PRIMARY KEY,
  cache_path TEXT NOT NULL,
  cache_size INTEGER NOT NULL,
  cache_mtime_ns INTEGER NOT NULL,
  indexed_at TEXT NOT NULL,
  row_count INTEGER NOT NULL,
  table_count INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS chat_table(
  table_name TEXT NOT NULL,
  chat_username TEXT NOT NULL,
  source_db TEXT NOT NULL,
  row_count INTEGER NOT NULL,
  min_create_time INTEGER NOT NULL,
  max_create_time INTEGER NOT NULL,
  max_sort_seq INTEGER NOT NULL,
  max_local_id INTEGER NOT NULL,
  PRIMARY KEY(source_db, table_name)
);
CREATE TABLE IF NOT EXISTS session_snapshot(
  username TEXT PRIMARY KEY,
  table_name TEXT NOT NULL,
  last_timestamp INTEGER NOT NULL,
  refreshed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_session_snapshot_table ON session_snapshot(table_name);
CREATE TABLE IF NOT EXISTS message_index(
  source_db TEXT NOT NULL,
  table_name TEXT NOT NULL,
  chat_username TEXT NOT NULL,
  local_id INTEGER NOT NULL,
  create_time INTEGER NOT NULL,
  sort_seq INTEGER NOT NULL,
  server_id INTEGER NOT NULL,
  local_type INTEGER NOT NULL,
  status INTEGER NOT NULL,
  real_sender_id INTEGER NOT NULL,
  PRIMARY KEY(source_db, table_name, local_id)
);
CREATE INDEX IF NOT EXISTS idx_message_chat_time ON message_index(table_name, create_time, sort_seq, local_id, source_db);
CREATE INDEX IF NOT EXISTS idx_message_chat_seq ON message_index(table_name, sort_seq, create_time, local_id, source_db);
CREATE INDEX IF NOT EXISTS idx_message_timeline ON message_index(create_time, sort_seq, chat_username, local_id, source_db);
CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(
  search_text,
  source_db UNINDEXED,
  table_name UNINDEXED,
  chat_username UNINDEXED,
  local_id UNINDEXED,
  create_time UNINDEXED,
  sort_seq UNINDEXED
);
`, sqliteBusyTimeout)
}

func buildTableMap(ctx context.Context, opts BuildOptions) (map[string]string, error) {
	tableToChat := map[string]string{}
	if len(opts.ChatUsernames) > 0 {
		for _, username := range opts.ChatUsernames {
			username = strings.TrimSpace(username)
			if username == "" {
				continue
			}
			tableToChat[messages.TableName(username)] = username
		}
		return tableToChat, nil
	}
	if strings.TrimSpace(opts.ContactCachePath) == "" {
		return nil, fmt.Errorf("contact cache path is required")
	}
	list, err := contacts.NewService(opts.ContactCachePath).List(ctx)
	if err != nil {
		return nil, err
	}
	for _, contact := range list {
		username := strings.TrimSpace(contact.Username)
		if username == "" {
			continue
		}
		tableToChat[messages.TableName(username)] = username
	}
	return tableToChat, nil
}

func prepareBuildPlan(ctx context.Context, paths []string, tableToChat map[string]string) ([]plannedSource, int, error) {
	plan := make([]plannedSource, 0, len(paths))
	totalTables := 0
	for _, dbPath := range paths {
		stat, err := statSource(dbPath)
		if err != nil {
			return nil, 0, err
		}
		db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
		if err != nil {
			return nil, 0, err
		}
		tables, err := listMessageTablesDB(ctx, db)
		closeErr := db.Close()
		if err != nil {
			return nil, 0, err
		}
		if closeErr != nil {
			return nil, 0, closeErr
		}
		source := plannedSource{Stat: stat}
		for _, tableName := range tables {
			chatUsername := tableToChat[tableName]
			if chatUsername == "" {
				continue
			}
			source.Tables = append(source.Tables, plannedTable{
				Name:         tableName,
				ChatUsername: chatUsername,
			})
			totalTables++
		}
		plan = append(plan, source)
	}
	return plan, totalTables, nil
}

func listMessageTables(ctx context.Context, dbPath string) ([]string, error) {
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return listMessageTablesDB(ctx, db)
}

func listMessageTablesDB(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'Msg_%' ORDER BY name;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if msgTableRE.MatchString(name) {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

func newProgressTracker(path string, account string, indexPath string, tempPath string, totalSources int, totalTables int, totalRows int64) *progressTracker {
	now := time.Now().Format(time.RFC3339)
	return &progressTracker{
		path: path,
		progress: buildProgress{
			Status:       StatusBuilding,
			StartedAt:    now,
			UpdatedAt:    now,
			Account:      account,
			IndexPath:    indexPath,
			TempPath:     tempPath,
			TotalSources: totalSources,
			TotalTables:  totalTables,
			TotalRows:    totalRows,
		},
	}
}

func (t *progressTracker) startTable(sourceDB string, tableName string) error {
	t.progress.CurrentSourceDB = sourceDB
	t.progress.CurrentTable = tableName
	return t.write()
}

func (t *progressTracker) setTotals(totalSources int, totalTables int, totalRows int64) error {
	t.progress.TotalSources = totalSources
	t.progress.TotalTables = totalTables
	t.progress.CheckedTables = 0
	t.progress.CoveredTables = 0
	t.progress.TotalRows = totalRows
	t.progress.IndexedRows = 0
	t.progress.CurrentSourceDB = ""
	t.progress.CurrentTable = "indexing"
	if totalTables > 0 || totalRows > 0 {
		t.progress.Percent = 0
	} else {
		t.progress.Percent = 100
	}
	return t.write()
}

func (t *progressTracker) addRow() error {
	t.progress.IndexedRows++
	if t.progress.TotalRows > 0 {
		t.progress.Percent = 5 + float64(t.progress.IndexedRows)*95/float64(t.progress.TotalRows)
		if t.progress.Percent > 100 {
			t.progress.Percent = 100
		}
	}
	if t.progress.IndexedRows%10000 != 0 && time.Since(t.lastWrite) < 2*time.Second {
		return nil
	}
	return t.write()
}

func (t *progressTracker) finishTable(coveredTables int) error {
	t.progress.CheckedTables++
	t.progress.CoveredTables = coveredTables
	if t.progress.TotalRows <= 0 && t.progress.TotalTables > 0 {
		t.progress.Percent = float64(t.progress.CoveredTables) * 100 / float64(t.progress.TotalTables)
		if t.progress.Percent > 100 {
			t.progress.Percent = 100
		}
	}
	return t.write()
}

func (t *progressTracker) write() error {
	if t == nil || t.path == "" {
		return nil
	}
	t.progress.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(t.progress, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(t.path, data, 0o600); err != nil {
		return err
	}
	t.lastWrite = time.Now()
	return nil
}

func streamSourceRowsAfter(ctx context.Context, sourceDB *sql.DB, dbPath string, tableName string, afterLocalID int64, visit func(sourceMessageRow) error) error {
	whereSQL := ""
	args := []any{}
	if afterLocalID >= 0 {
		whereSQL = "WHERE local_id > ?"
		args = append(args, afterLocalID)
	}
	query := fmt.Sprintf(`
SELECT
  local_id,
  COALESCE(local_type, 0),
  COALESCE(sort_seq, 0),
  COALESCE(server_id, 0),
  COALESCE(real_sender_id, 0),
  COALESCE((SELECT user_name FROM Name2Id WHERE rowid = COALESCE(real_sender_id, 0)), ''),
  COALESCE(status, 0),
  COALESCE(create_time, 0),
  COALESCE(message_content, X'')
FROM %s
%s
ORDER BY local_id ASC;
`, sqlitedb.QuoteIdent(tableName), whereSQL)
	rows, err := sourceDB.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query source rows db=%s table=%s: %w", dbPath, tableName, err)
	}
	defer rows.Close()
	for rows.Next() {
		var row sourceMessageRow
		if err := rows.Scan(
			&row.LocalID,
			&row.LocalType,
			&row.SortSeq,
			&row.ServerID,
			&row.RealSenderID,
			&row.SenderName,
			&row.Status,
			&row.CreateTime,
			&row.Content,
		); err != nil {
			return fmt.Errorf("scan source row db=%s table=%s: %w", dbPath, tableName, err)
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate source rows db=%s table=%s: %w", dbPath, tableName, err)
	}
	return nil
}

func indexStats(ctx context.Context, indexPath string) (indexStatsRow, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return indexStatsRow{}, err
	}
	defer db.Close()
	return indexStatsDB(ctx, db)
}

func indexStatsDB(ctx context.Context, db *sql.DB) (indexStatsRow, error) {
	query := `
SELECT
  COALESCE((SELECT value FROM meta WHERE key='schema_version'), '') AS schema_version,
  COALESCE((SELECT value FROM meta WHERE key='state'), 'ready') AS state,
  COALESCE((SELECT value FROM meta WHERE key='built_at'), '') AS built_at,
  COALESCE((SELECT value FROM meta WHERE key='refreshed_at'), '') AS refreshed_at,
  MAX(
    CAST(COALESCE((SELECT value FROM meta WHERE key='source_total_rows'), '0') AS INTEGER),
    COALESCE((SELECT SUM(row_count) FROM source_db), 0),
    COALESCE((SELECT SUM(row_count) FROM chat_table), 0)
  ) AS source_rows,
  CAST(COALESCE((SELECT value FROM meta WHERE key='source_db_count'), (SELECT CAST(count(*) AS TEXT) FROM source_db), '0') AS INTEGER) AS source_db_count,
  MAX(
    CAST(COALESCE((SELECT value FROM meta WHERE key='indexed_rows'), '0') AS INTEGER),
    COALESCE((SELECT SUM(row_count) FROM source_db), 0),
    COALESCE((SELECT SUM(row_count) FROM chat_table), 0)
  ) AS indexed_rows,
  (SELECT count(*) FROM chat_table) AS indexed_tables,
  (SELECT count(DISTINCT chat_username) FROM chat_table) AS covered_chats,
  COALESCE((SELECT max(max_create_time) FROM chat_table), 0) AS max_create_time,
  CAST(COALESCE((SELECT value FROM meta WHERE key='session_lag_seconds'), '0') AS INTEGER) AS session_lag_seconds,
  COALESCE((SELECT value FROM meta WHERE key='refresh_mode'), '') AS refresh_mode,
  CAST(COALESCE((SELECT value FROM meta WHERE key='active_chats_checked'), '0') AS INTEGER) AS active_chats_checked,
  CAST(COALESCE((SELECT value FROM meta WHERE key='active_chats_refreshed'), '0') AS INTEGER) AS active_chats_refreshed,
  CAST(COALESCE((SELECT value FROM meta WHERE key='reconcile_sources_pending'), '0') AS INTEGER) AS reconcile_sources_pending,
  CAST(COALESCE((SELECT value FROM meta WHERE key='reconcile_tables_checked'), '0') AS INTEGER) AS reconcile_tables_checked;
`
	var row indexStatsRow
	if err := db.QueryRowContext(ctx, query).Scan(
		&row.SchemaVersion,
		&row.State,
		&row.BuiltAt,
		&row.RefreshedAt,
		&row.SourceRows,
		&row.SourceDBCount,
		&row.IndexedRows,
		&row.IndexedTables,
		&row.CoveredChats,
		&row.MaxCreateTime,
		&row.SessionLagSeconds,
		&row.RefreshMode,
		&row.ActiveChatsChecked,
		&row.ActiveChatsRefreshed,
		&row.ReconcileSourcesPending,
		&row.ReconcileTablesChecked,
	); err != nil {
		return indexStatsRow{}, err
	}
	return row, nil
}

func indexSources(ctx context.Context, indexPath string) ([]sourceRow, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return indexSourcesDB(ctx, db)
}

func indexSourcesDB(ctx context.Context, db *sql.DB) ([]sourceRow, error) {
	rows, err := db.QueryContext(ctx, "SELECT source_db, cache_path, cache_size, cache_mtime_ns, indexed_at, row_count, table_count FROM source_db ORDER BY source_db;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sourceRow{}
	for rows.Next() {
		var row sourceRow
		if err := rows.Scan(&row.SourceDB, &row.CachePath, &row.CacheSize, &row.CacheMTimeNS, &row.IndexedAt, &row.RowCount, &row.TableCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func chatTableExists(ctx context.Context, indexPath string, tableName string) (bool, error) {
	count, err := queryCount(ctx, indexPath, "SELECT count(*) FROM chat_table WHERE table_name = ?;", tableName)
	return count > 0, err
}

func indexedCompletedTableKeys(ctx context.Context, indexPath string) (map[string]bool, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	watermarks, err := indexedTableWatermarksDB(ctx, db)
	if err != nil {
		return nil, err
	}
	return completedTableKeysFromWatermarks(watermarks), nil
}

func indexedTableWatermarksDB(ctx context.Context, db *sql.DB) (map[string]chatTableMetaRow, error) {
	rows, err := db.QueryContext(ctx, "SELECT source_db, table_name, chat_username, row_count, min_create_time, max_create_time, max_sort_seq, max_local_id FROM chat_table;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]chatTableMetaRow)
	for rows.Next() {
		var row chatTableMetaRow
		if err := rows.Scan(&row.SourceDB, &row.TableName, &row.ChatUsername, &row.RowCount, &row.MinCreateTime, &row.MaxCreateTime, &row.MaxSortSeq, &row.MaxLocalID); err != nil {
			return nil, err
		}
		out[indexedTableKey(row.SourceDB, row.TableName)] = row
	}
	return out, rows.Err()
}

func completedTableKeysFromWatermarks(watermarks map[string]chatTableMetaRow) map[string]bool {
	out := make(map[string]bool, len(watermarks))
	for key := range watermarks {
		out[key] = true
	}
	return out
}

func maxLocalIDFromWatermarks(watermarks map[string]chatTableMetaRow, sourceDB string, tableName string) int64 {
	if row, ok := watermarks[indexedTableKey(sourceDB, tableName)]; ok {
		return row.MaxLocalID
	}
	return -1
}

func sourceIsUnchanged(stat sourceStat, row sourceRow) bool {
	return row.SourceDB == stat.SourceDB &&
		row.CachePath == stat.Path &&
		row.CacheSize == stat.Size &&
		row.CacheMTimeNS == stat.MTimeNS
}

func indexedTableKey(sourceDB string, tableName string) string {
	return sourceDB + unitSep + tableName
}

func queryRefs(ctx context.Context, indexPath string, query string, args ...any) ([]messages.RowRef, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []messages.RowRef{}
	for rows.Next() {
		var row refRow
		if err := rows.Scan(&row.SourceDB, &row.TableName, &row.ChatUsername, &row.LocalID); err != nil {
			return nil, err
		}
		out = append(out, messages.RowRef{
			SourceDB:     row.SourceDB,
			TableName:    row.TableName,
			ChatUsername: row.ChatUsername,
			LocalID:      row.LocalID,
		})
	}
	return out, rows.Err()
}

func queryCount(ctx context.Context, dbPath string, query string, args ...any) (int64, error) {
	db, err := sqlitedb.OpenReadOnlyLive(ctx, dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return queryCountDB(ctx, db, query, args...)
}

func queryCountDB(ctx context.Context, db *sql.DB, query string, args ...any) (int64, error) {
	var count int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}

func indexedRowCountDB(ctx context.Context, db *sql.DB) int64 {
	count, err := queryCountDB(ctx, db, "SELECT COALESCE(SUM(row_count), 0) FROM chat_table;")
	if err == nil {
		return count
	}
	return 0
}

func currentSourceStats(paths []string) ([]sourceStat, string, error) {
	if len(paths) == 0 {
		return nil, "no_message_caches", nil
	}
	out := make([]sourceStat, 0, len(paths))
	for _, path := range paths {
		stat, err := statSource(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, "message_cache_missing", nil
			}
			return nil, "", err
		}
		out = append(out, stat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceDB < out[j].SourceDB })
	return out, "", nil
}

func scanSourceSummaries(ctx context.Context, paths []string, tableToChat map[string]string) ([]buildSourceSummary, error) {
	out := make([]buildSourceSummary, 0, len(paths))
	for _, dbPath := range paths {
		stat, err := statSource(dbPath)
		if err != nil {
			return nil, err
		}
		tables, err := listMessageTables(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		filtered := make([]string, 0, len(tables))
		for _, table := range tables {
			if tableToChat != nil && tableToChat[table] == "" {
				continue
			}
			filtered = append(filtered, table)
		}
		watermarks, err := queryTableWatermarks(ctx, dbPath, filtered)
		if err != nil {
			return nil, err
		}
		summary := buildSourceSummary{
			SourceDB:   stat.SourceDB,
			CachePath:  stat.Path,
			CacheSize:  stat.Size,
			MTimeNS:    stat.MTimeNS,
			TableCount: len(watermarks),
		}
		for _, watermark := range watermarks {
			summary.RowCount += watermark.RowCount
			if watermark.MaxCreateTime > summary.MaxCreateTime {
				summary.MaxCreateTime = watermark.MaxCreateTime
			}
		}
		out = append(out, summary)
	}
	return out, nil
}

func queryTableWatermarks(ctx context.Context, dbPath string, tables []string) ([]tableWatermarkRow, error) {
	if len(tables) == 0 {
		return nil, nil
	}
	db, err := sqlitedb.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows := make([]tableWatermarkRow, 0, len(tables))
	for _, table := range tables {
		query := fmt.Sprintf("SELECT count(*), COALESCE(max(local_id), -1), COALESCE(max(create_time), 0) FROM %s;", sqlitedb.QuoteIdent(table))
		row := tableWatermarkRow{TableName: table}
		if err := db.QueryRowContext(ctx, query).Scan(&row.RowCount, &row.MaxLocalID, &row.MaxCreateTime); err != nil {
			return nil, fmt.Errorf("scan source watermark db=%s table=%s: %w", dbPath, table, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func baseStatus(indexPath string) Status {
	return Status{
		Status:    StatusMissing,
		QueryMode: QueryModeScan,
		Path:      indexPath,
		Reason:    "index_missing",
		JobState:  JobStateNone,
		LagPolicy: "not_applicable",
	}
}

func normalizeIndexState(value string) string {
	switch strings.TrimSpace(value) {
	case StatusBuilding:
		return StatusBuilding
	case StatusRefreshing:
		return StatusRefreshing
	case StatusInterrupted:
		return StatusInterrupted
	case StatusReady, "", "fresh":
		return StatusReady
	default:
		return StatusStale
	}
}

func secondsSince(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	if d := time.Since(t); d > 0 {
		return int64(d.Round(time.Second).Seconds())
	}
	return 0
}

func resolveIndexPath(opts BuildOptions) (string, error) {
	if strings.TrimSpace(opts.IndexPath) != "" {
		return opts.IndexPath, nil
	}
	if strings.TrimSpace(opts.Account) == "" {
		return "", fmt.Errorf("account is required")
	}
	return Path(opts.Account)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func loadCurrentJob(indexPath string) (buildProgress, string) {
	if job, ok := loadBuildProgress(jobPathFor(indexPath), ""); ok {
		return job, classifyJob(job)
	}
	if job, ok := loadBuildProgress(oldProgressPathFor(indexPath), ""); ok {
		return job, JobStateInterrupted
	}
	return buildProgress{}, JobStateNone
}

func classifyJob(job buildProgress) string {
	if job.Status == StatusInterrupted {
		return JobStateInterrupted
	}
	if job.PID <= 0 {
		return JobStateInterrupted
	}
	if !processAlive(job.PID) {
		return JobStateInterrupted
	}
	updated, err := time.Parse(time.RFC3339, job.UpdatedAt)
	if err != nil {
		updated, err = time.Parse(time.RFC3339Nano, job.UpdatedAt)
	}
	if err != nil || time.Since(updated) > jobHeartbeatStale {
		return JobStateInterrupted
	}
	return JobStateActive
}

type indexLock struct {
	path string
}

func acquireIndexLock(indexPath string) (indexLock, bool, error) {
	lockPath := lockPathFor(indexPath)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return indexLock{}, false, err
	}
	data := []byte(fmt.Sprintf(`{"pid":%d,"created_at":%q}`+"\n", os.Getpid(), time.Now().Format(time.RFC3339)))
	f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if _, writeErr := f.Write(data); writeErr != nil {
			_ = f.Close()
			_ = os.Remove(lockPath)
			return indexLock{}, false, writeErr
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(lockPath)
			return indexLock{}, false, err
		}
		return indexLock{path: lockPath}, true, nil
	}
	if !os.IsExist(err) {
		return indexLock{}, false, err
	}
	if _, state := loadCurrentJob(indexPath); state == JobStateActive {
		return indexLock{}, false, nil
	}
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return indexLock{}, false, err
	}
	f, err = os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return indexLock{}, false, nil
		}
		return indexLock{}, false, err
	}
	if _, writeErr := f.Write(data); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return indexLock{}, false, writeErr
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(lockPath)
		return indexLock{}, false, err
	}
	return indexLock{path: lockPath}, true, nil
}

func (l indexLock) release() {
	if l.path != "" {
		_ = os.Remove(l.path)
	}
}

func markInterrupted(indexPath string) {
	job, ok := loadBuildProgress(jobPathFor(indexPath), "")
	if ok {
		job.Status = StatusInterrupted
		job.PID = 0
		job.UpdatedAt = time.Now().Format(time.RFC3339)
		data, err := json.MarshalIndent(job, "", "  ")
		if err == nil {
			data = append(data, '\n')
			_ = os.WriteFile(jobPathFor(indexPath), data, 0o600)
		}
	}
	stats, err := indexStats(context.Background(), indexPath)
	if err == nil && normalizeIndexState(stats.State) == StatusBuilding {
		_ = setMeta(context.Background(), indexPath, map[string]string{"state": StatusInterrupted})
	}
}

func MarkInterrupted(indexPath string) {
	markInterrupted(indexPath)
}

func buildingStatus(indexPath string) Status {
	status := Status{Status: StatusMissing, Path: indexPath, Reason: "index_missing"}
	progress, hasProgress := loadBuildProgress(oldProgressPathFor(indexPath), "")
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(indexPath), ".messages-*.db"))
	if err != nil || len(matches) == 0 {
		if hasProgress && progressIsRecent(progress) {
			return statusFromProgressLegacy(indexPath, progress, sourceStat{})
		}
		return status
	}
	var newest sourceStat
	found := false
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		current := sourceStat{
			Path:        path,
			Size:        info.Size(),
			MTimeNS:     info.ModTime().UnixNano(),
			DisplayTime: info.ModTime().Format(time.RFC3339),
		}
		if !found || current.MTimeNS > newest.MTimeNS {
			newest = current
			found = true
		}
	}
	if !found {
		return status
	}
	out := Status{
		Status:    StatusBuilding,
		QueryMode: QueryModeScan,
		Reason:    "build_in_progress",
		Path:      indexPath,
		TempPath:  newest.Path,
		TempSize:  newest.Size,
		TempMTime: newest.DisplayTime,
	}
	if hasProgress && (progress.TempPath == "" || progress.TempPath == newest.Path) {
		progressStatus := statusFromProgressLegacy(indexPath, progress, newest)
		progressStatus.TempPath = out.TempPath
		progressStatus.TempSize = out.TempSize
		progressStatus.TempMTime = out.TempMTime
		return progressStatus
	}
	return out
}

func statSource(path string) (sourceStat, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sourceStat{}, err
	}
	return sourceStat{
		Path:        path,
		SourceDB:    filepath.Base(path),
		Size:        info.Size(),
		MTimeNS:     info.ModTime().UnixNano(),
		DisplayTime: info.ModTime().Format(time.RFC3339),
	}, nil
}

func jobPathFor(indexPath string) string {
	return filepath.Join(filepath.Dir(indexPath), jobName)
}

func oldProgressPathFor(indexPath string) string {
	return filepath.Join(filepath.Dir(indexPath), oldProgressName)
}

func lockPathFor(indexPath string) string {
	return filepath.Join(filepath.Dir(indexPath), lockName)
}

func loadBuildProgress(path string, tempPath string) (buildProgress, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return buildProgress{}, false
	}
	var progress buildProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return buildProgress{}, false
	}
	if progress.Status != StatusBuilding && progress.Status != StatusRefreshing && progress.Status != StatusInterrupted {
		return buildProgress{}, false
	}
	if tempPath != "" && progress.TempPath != tempPath {
		return buildProgress{}, false
	}
	return progress, true
}

func statusFromProgressLegacy(indexPath string, progress buildProgress, temp sourceStat) Status {
	out := Status{
		Status:                  StatusBuilding,
		QueryMode:               QueryModeScan,
		Reason:                  "build_in_progress",
		Path:                    indexPath,
		JobState:                JobStateActive,
		LagPolicy:               "not_applicable",
		TempPath:                progress.TempPath,
		SourceDBCount:           progress.TotalSources,
		IndexedRows:             progress.IndexedRows,
		ProgressPercent:         progress.Percent,
		ProgressRows:            progress.IndexedRows,
		ProgressTotalRows:       progress.TotalRows,
		ProgressTables:          progress.CheckedTables,
		ProgressTotalTables:     progress.TotalTables,
		CheckedTables:           progress.CheckedTables,
		CoveredTables:           progress.CoveredTables,
		ProgressUpdatedAt:       progress.UpdatedAt,
		RefreshMode:             progress.RefreshMode,
		SessionLagSeconds:       progress.SessionLagSeconds,
		ActiveChatsChecked:      progress.ActiveChatsChecked,
		ActiveChatsRefreshed:    progress.ActiveChatsRefreshed,
		ReconcileSourcesPending: progress.ReconcileSourcesPending,
		ReconcileTablesChecked:  progress.ReconcileTablesChecked,
	}
	if progress.CurrentSourceDB != "" || progress.CurrentTable != "" {
		out.ProgressCurrent = strings.Trim(strings.Join([]string{progress.CurrentSourceDB, progress.CurrentTable}, " "), " ")
	}
	if temp.Path != "" {
		out.TempPath = temp.Path
		out.TempSize = temp.Size
		out.TempMTime = temp.DisplayTime
	}
	return out
}

func statusFromProgress(indexPath string, progress buildProgress, temp sourceStat, state string, jobState string) Status {
	out := statusFromProgressLegacy(indexPath, progress, temp)
	out.Status = state
	out.JobState = jobState
	if state == StatusInterrupted {
		out.Reason = "previous_index_job_interrupted_refresh_can_resume"
	} else if state == StatusRefreshing {
		out.Reason = "index_refresh_in_progress"
	} else {
		out.Reason = "initial_index_build_in_progress"
	}
	return out
}

func mergeProgressStatus(progress Status, indexed Status) Status {
	progress.SchemaVersion = indexed.SchemaVersion
	progress.BuiltAt = indexed.BuiltAt
	progress.RefreshedAt = indexed.RefreshedAt
	progress.CoveredChats = indexed.CoveredChats
	progress.IndexedTables = indexed.IndexedTables
	progress.IndexedMaxCreateTime = indexed.IndexedMaxCreateTime
	if progress.RefreshMode == "" {
		progress.RefreshMode = indexed.RefreshMode
	}
	if progress.SessionLagSeconds == 0 {
		progress.SessionLagSeconds = indexed.SessionLagSeconds
	}
	if progress.ActiveChatsChecked == 0 {
		progress.ActiveChatsChecked = indexed.ActiveChatsChecked
	}
	if progress.ActiveChatsRefreshed == 0 {
		progress.ActiveChatsRefreshed = indexed.ActiveChatsRefreshed
	}
	if progress.ReconcileSourcesPending == 0 {
		progress.ReconcileSourcesPending = indexed.ReconcileSourcesPending
	}
	if progress.ReconcileTablesChecked == 0 {
		progress.ReconcileTablesChecked = indexed.ReconcileTablesChecked
	}
	if progress.SourceDBCount == 0 {
		progress.SourceDBCount = indexed.SourceDBCount
	}
	if progress.SourceRows == 0 {
		progress.SourceRows = indexed.SourceRows
	}
	if indexed.IndexedRows > progress.IndexedRows {
		progress.IndexedRows = indexed.IndexedRows
	}
	if progress.IndexedRows > progress.ProgressRows {
		progress.ProgressRows = progress.IndexedRows
	}
	if progress.IndexedTables > progress.CoveredTables {
		progress.CoveredTables = progress.IndexedTables
	}
	if progress.IndexedTables > progress.ProgressTables {
		progress.ProgressTables = progress.IndexedTables
		if progress.ProgressTotalTables > 0 {
			progress.ProgressPercent = float64(progress.ProgressTables) * 100 / float64(progress.ProgressTotalTables)
			if progress.ProgressPercent > 100 {
				progress.ProgressPercent = 100
			}
		}
	}
	if progress.Status == StatusBuilding && progress.IndexedRows > 0 {
		progress.Reason = "index_build_or_resume_in_progress"
	}
	return progress
}

func progressIsRecent(progress buildProgress) bool {
	if progress.UpdatedAt == "" {
		return false
	}
	updated, err := time.Parse(time.RFC3339, progress.UpdatedAt)
	if err != nil {
		return false
	}
	return time.Since(updated) < 15*time.Minute
}

func parseSchemaVersion(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func limitOffsetSQL(limit int, offset int) string {
	if limit <= 0 {
		if offset > 0 {
			return " LIMIT -1 OFFSET " + strconv.Itoa(offset)
		}
		return ""
	}
	out := " LIMIT " + strconv.Itoa(limit)
	if offset > 0 {
		out += " OFFSET " + strconv.Itoa(offset)
	}
	return out
}

func refreshChatTableTx(ctx context.Context, tx *sql.Tx, sourceDB string, tableName string, chatUsername string) error {
	_, err := tx.ExecContext(ctx, `
INSERT OR REPLACE INTO chat_table(table_name, chat_username, source_db, row_count, min_create_time, max_create_time, max_sort_seq, max_local_id)
SELECT ?, ?, ?,
  count(*),
  COALESCE(min(create_time), 0),
  COALESCE(max(create_time), 0),
  COALESCE(max(sort_seq), 0),
  COALESCE(max(local_id), 0)
FROM message_index
WHERE source_db = ? AND table_name = ?;
`, tableName, chatUsername, sourceDB, sourceDB, tableName)
	if err != nil {
		return fmt.Errorf("refresh chat table source_db=%s table=%s: %w", sourceDB, tableName, err)
	}
	return nil
}

func refreshSourceDB(ctx context.Context, db *sql.DB, stat sourceStat, indexedAt string) error {
	_, err := db.ExecContext(ctx, `
INSERT OR REPLACE INTO source_db(source_db, cache_path, cache_size, cache_mtime_ns, indexed_at, row_count, table_count)
VALUES (?, ?, ?, ?, ?,
  COALESCE((SELECT SUM(row_count) FROM chat_table WHERE source_db = ?), 0),
  COALESCE((SELECT count(*) FROM chat_table WHERE source_db = ?), 0)
);
`, stat.SourceDB, stat.Path, stat.Size, stat.MTimeNS, indexedAt, stat.SourceDB, stat.SourceDB)
	if err != nil {
		return fmt.Errorf("refresh index source metadata source_db=%s: %w", stat.SourceDB, err)
	}
	return nil
}

func timelineAfterSQL(after timeline.SortKey) (string, []any) {
	return `(
  create_time > ? OR
  (create_time = ? AND sort_seq > ?) OR
  (create_time = ? AND sort_seq = ? AND chat_username > ?) OR
  (create_time = ? AND sort_seq = ? AND chat_username = ? AND local_id > ?) OR
  (create_time = ? AND sort_seq = ? AND chat_username = ? AND local_id = ? AND source_db > ?)
)`, []any{
			after.CreateTime,
			after.CreateTime, after.Seq,
			after.CreateTime, after.Seq, after.ChatUsername,
			after.CreateTime, after.Seq, after.ChatUsername, after.LocalID,
			after.CreateTime, after.Seq, after.ChatUsername, after.LocalID, after.SourceDB,
		}
}

func ftsPhrase(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `"`, `""`)
	return `"` + value + `"`
}

func ftsQuerySupported(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < 0x20 {
			return false
		}
	}
	return true
}
