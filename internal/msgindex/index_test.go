package msgindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wxview/internal/messages"
	"wxview/internal/sqlitedb"
	"wxview/internal/sqlitedb/sqlitetest"
	"wxview/internal/timeline"
)

func TestBuildStatusAndMessageRefs(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db1 := filepath.Join(dir, "message_0.db")
	db2 := filepath.Join(dir, "message_1.db")
	createIndexMessageDB(t, db1, map[string][]indexMessageRow{
		messages.TableName("alice"): {
			{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"},
			{LocalID: 3, SortSeq: 3001, CreateTime: 300, Status: 2, Content: "third"},
		},
	})
	createIndexMessageDB(t, db2, map[string][]indexMessageRow{
		messages.TableName("alice"): {
			{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "second"},
		},
		messages.TableName("bob"): {
			{LocalID: 4, SortSeq: 4001, CreateTime: 400, Status: 4, Content: "bob message"},
		},
	})

	indexPath := filepath.Join(dir, "index.db")
	result, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db1, db2},
		ChatUsernames:     []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Status != StatusReady || result.Status.IndexedRows != 4 || result.Status.CoveredChats != 2 || result.Status.SourceDBCount != 2 {
		t.Fatalf("unexpected build status: %+v", result.Status)
	}

	status, err := StatusFor(ctx, indexPath, []string{db1, db2})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusReady || status.QueryMode != QueryModeIndex {
		t.Fatalf("status = %+v, want ready index", status)
	}

	refs, err := MessageRefs(ctx, indexPath, MessageQuery{
		Username: "alice",
		Start:    100,
		End:      300,
		HasStart: true,
		HasEnd:   true,
		Limit:    2,
		Offset:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := messages.NewService([]string{db1, db2}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "second" || got[1].Content != "third" {
		t.Fatalf("indexed page = %+v, want second then third", got)
	}

	scan, err := messages.NewService([]string{db1, db2}).List(ctx, messages.QueryOptions{
		Username: "alice",
		Start:    100,
		End:      300,
		HasStart: true,
		HasEnd:   true,
		Limit:    2,
		Offset:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scan) != len(got) || scan[0].ID != got[0].ID || scan[1].ID != got[1].ID {
		t.Fatalf("fast path differs from scan: fast=%+v scan=%+v", got, scan)
	}
}

func TestStatusLagPolicyAndSchemaMismatch(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		messages.TableName("alice"): {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}

	newTime := time.Now().Add(-2 * time.Second)
	if err := os.Chtimes(db, newTime, newTime); err != nil {
		t.Fatal(err)
	}
	status, err := StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusReady || status.QueryMode != QueryModeIndex || status.LagPolicy != "realtime_within_60s" {
		t.Fatalf("status = %+v, want ready near-realtime index", status)
	}

	oldTime := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(db, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	status, err = StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusStale || status.QueryMode != QueryModeScan || status.Reason != "index_lag_exceeds_realtime_window" {
		t.Fatalf("status = %+v, want stale lag fallback", status)
	}

	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}
	execIndexTestSQL(t, ctx, indexPath, "UPDATE meta SET value='999' WHERE key='schema_version';")
	status, err = StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusSchemaMismatch {
		t.Fatalf("status = %+v, want schema_mismatch", status)
	}
}

func TestRefreshCatchesUpIncrementally(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := messages.TableName("alice")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		table: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}
	insertIndexMessageRow(t, db, table, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "second"})

	status, err := StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	status, err = DetailedStatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusStale || status.QueryMode != QueryModeScan {
		t.Fatalf("status = %+v, want stale after source DB append beyond realtime window", status)
	}
	result, err := Refresh(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Status != StatusReady || result.Status.IndexedRows != 2 {
		t.Fatalf("refresh result = %+v, want ready with 2 rows", result.Status)
	}
	refs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "alice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got, err := messages.NewService([]string{db}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "first" || got[1].Content != "second" {
		t.Fatalf("indexed rows after refresh = %+v, want first then second", got)
	}
}

func TestRefreshUsesSessionDeltaBeforeReconcile(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	oldBudget := reconcileTableBudget
	reconcileTableBudget = 0
	defer func() { reconcileTableBudget = oldBudget }()

	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	sessionDB := filepath.Join(dir, "session.db")
	aliceTable := messages.TableName("alice")
	bobTable := messages.TableName("bob")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		aliceTable: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "alice first"}},
		bobTable:   {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "bob first"}},
	})
	createIndexSessionDB(t, sessionDB, map[string]indexSessionRow{
		"alice": {LastTimestamp: 100, UnreadCount: 0, Summary: "alice first"},
		"bob":   {LastTimestamp: 100, UnreadCount: 0, Summary: "bob first"},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		SessionCachePath:  sessionDB,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice", "bob"},
	}); err != nil {
		t.Fatal(err)
	}

	insertIndexMessageRow(t, db, aliceTable, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "alice second"})
	insertIndexMessageRow(t, db, bobTable, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "bob second"})
	updateIndexSessionRow(t, sessionDB, "alice", indexSessionRow{LastTimestamp: 200, UnreadCount: 1, Summary: "alice second"})
	updateIndexSessionRow(t, sessionDB, "bob", indexSessionRow{LastTimestamp: 100, UnreadCount: 9, Summary: "bob summary changed without timestamp"})
	if err := os.Chtimes(db, time.Now().Add(-2*time.Second), time.Now().Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}

	result, err := Refresh(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		SessionCachePath:  sessionDB,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.RefreshMode != "session_delta" || result.Status.ActiveChatsRefreshed != 1 {
		t.Fatalf("refresh status = %+v, want one hot refreshed chat", result.Status)
	}
	aliceRefs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "alice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceRefs) != 2 {
		t.Fatalf("alice refs = %+v, want two indexed rows", aliceRefs)
	}
	bobRefs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "bob", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(bobRefs) != 1 {
		t.Fatalf("bob refs = %+v, want unchanged chat not reconciled in hot path", bobRefs)
	}
	indexDB, err := sqlitedb.OpenReadOnlyLive(ctx, indexPath)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := indexSourcesDB(ctx, indexDB)
	closeErr := indexDB.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	current, err := statSource(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].CacheMTimeNS == current.MTimeNS {
		t.Fatalf("source metadata should stay pending until reconcile: sources=%+v current=%+v", sources, current)
	}
}

func TestUseForQueryUsesChatLevelSessionFreshness(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	oldBudget := reconcileTableBudget
	reconcileTableBudget = 0
	defer func() { reconcileTableBudget = oldBudget }()

	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	sessionDB := filepath.Join(dir, "session.db")
	aliceTable := messages.TableName("alice")
	bobTable := messages.TableName("bob")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		aliceTable: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "alice first"}},
		bobTable:   {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "bob first"}},
	})
	createIndexSessionDB(t, sessionDB, map[string]indexSessionRow{
		"alice": {LastTimestamp: 100, Summary: "alice first"},
		"bob":   {LastTimestamp: 100, Summary: "bob first"},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		SessionCachePath:  sessionDB,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice", "bob"},
	}); err != nil {
		t.Fatal(err)
	}
	insertIndexMessageRow(t, db, aliceTable, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "alice second"})
	updateIndexSessionRow(t, sessionDB, "alice", indexSessionRow{LastTimestamp: 200, Summary: "alice second"})
	if _, err := Refresh(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		SessionCachePath:  sessionDB,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice", "bob"},
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(db, old, old); err != nil {
		t.Fatal(err)
	}
	usable, err := UseForQuery(ctx, indexPath, []string{db}, UsabilityOptions{Username: "alice", SessionCachePath: sessionDB})
	if err != nil {
		t.Fatal(err)
	}
	if !usable.UseIndex {
		t.Fatalf("alice should use index by chat-level session freshness: %+v", usable)
	}

	updateIndexSessionRow(t, sessionDB, "bob", indexSessionRow{LastTimestamp: 300, Summary: "bob newer"})
	usable, err = UseForQuery(ctx, indexPath, []string{db}, UsabilityOptions{
		Chats:            []messages.ChatInfo{{Username: "alice"}, {Username: "bob"}},
		SessionCachePath: sessionDB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usable.UseIndex {
		t.Fatalf("timeline should fall back when one selected chat is behind: %+v", usable)
	}
}

func TestRefreshPlanSkipsUnchangedSourcesAndUsesWatermarkMap(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := messages.TableName("alice")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		table: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}
	indexDB, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer indexDB.Close()
	watermarks, err := indexedTableWatermarksDB(ctx, indexDB)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := indexSourcesDB(ctx, indexDB)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareRefreshPlan(ctx, []string{db}, map[string]string{table: "alice"}, watermarks, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("unchanged source refresh plan = %+v, want skipped", plan)
	}

	insertIndexMessageRow(t, db, table, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "second"})
	changedTime := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(db, changedTime, changedTime); err != nil {
		t.Fatal(err)
	}
	plan, err = prepareRefreshPlan(ctx, []string{db}, map[string]string{table: "alice"}, watermarks, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || len(plan[0].Tables) != 1 || plan[0].Tables[0].AfterLocalID != 1 {
		t.Fatalf("changed source refresh plan = %+v, want one table after watermark 1", plan)
	}
}

func TestRefreshResumesInterruptedIndexFromWatermark(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := messages.TableName("alice")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		table: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if err := initializeIndexDB(ctx, indexPath, "wxid_test", StatusInterrupted); err != nil {
		t.Fatal(err)
	}
	if _, err := indexTableIncrementalTxForTest(ctx, indexPath, db, "message_0.db", table, "alice", 0); err != nil {
		t.Fatal(err)
	}
	insertIndexMessageRow(t, db, table, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "second"})

	result, err := Refresh(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Status != StatusReady || result.Status.IndexedRows != 2 {
		t.Fatalf("refresh result = %+v, want resumed ready index with 2 rows", result.Status)
	}
	refs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "alice", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got, err := messages.NewService([]string{db}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Content != "first" || got[1].Content != "second" {
		t.Fatalf("resumed rows = %+v, want first then second without duplicates", got)
	}
}

func TestUseForQueryAllowsHistoricalRangeWhenIndexIsStale(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := messages.TableName("alice")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		table: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}
	insertIndexMessageRow(t, db, table, indexMessageRow{LocalID: 2, SortSeq: 2001, CreateTime: 500, Status: 4, Content: "new tail"})
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(db, old, old); err != nil {
		t.Fatal(err)
	}
	usable, err := UseForQuery(ctx, indexPath, []string{db}, UsabilityOptions{
		Username: "alice",
		Start:    0,
		End:      100,
		HasStart: true,
		HasEnd:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !usable.UseIndex {
		t.Fatalf("historical query should still use index when covered: %+v", usable)
	}
	usable, err = UseForQuery(ctx, indexPath, []string{db}, UsabilityOptions{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if usable.UseIndex {
		t.Fatalf("tail query should not use stale index: %+v", usable)
	}
}

func TestStatusReportsBuildingProgress(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "messages.db")
	tempPath := filepath.Join(dir, ".messages-123.db")
	if err := os.WriteFile(tempPath, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	progress := buildProgress{
		Status:          StatusBuilding,
		PID:             os.Getpid(),
		StartedAt:       "2026-05-18T16:00:00+08:00",
		UpdatedAt:       time.Now().Format(time.RFC3339),
		Account:         "wxid_test",
		IndexPath:       indexPath,
		TempPath:        tempPath,
		CurrentSourceDB: "message_0.db",
		CurrentTable:    messages.TableName("alice"),
		TotalSources:    2,
		TotalTables:     4,
		CheckedTables:   1,
		CoveredTables:   1,
		TotalRows:       100,
		IndexedRows:     25,
		Percent:         25,
	}
	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobPathFor(indexPath), data, 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := StatusFor(ctx, indexPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusBuilding || status.JobState != JobStateActive || status.ProgressPercent != 25 || status.ProgressRows != 25 || status.ProgressTotalRows != 100 {
		t.Fatalf("status = %+v, want building progress", status)
	}
	if status.ProgressTables != 1 || status.ProgressTotalTables != 4 || !strings.Contains(status.ProgressCurrent, "message_0.db") {
		t.Fatalf("status current/progress table fields not populated: %+v", status)
	}
}

func TestStatusMergesProgressWithIndexStatsAndInterruptedJobState(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	table := messages.TableName("alice")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		table: {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "first"}},
	})
	indexPath := filepath.Join(dir, "messages.db")
	if err := initializeIndexDB(ctx, indexPath, "wxid_test", StatusBuilding); err != nil {
		t.Fatal(err)
	}
	if _, err := indexTableIncrementalTxForTest(ctx, indexPath, db, "message_0.db", table, "alice", -1); err != nil {
		t.Fatal(err)
	}
	progress := buildProgress{
		Status:      StatusBuilding,
		PID:         os.Getpid(),
		StartedAt:   time.Now().Format(time.RFC3339),
		UpdatedAt:   time.Now().Format(time.RFC3339),
		Account:     "wxid_test",
		IndexPath:   indexPath,
		TotalTables: 2,
	}
	data, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobPathFor(indexPath), data, 0o600); err != nil {
		t.Fatal(err)
	}
	status, err := StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.SchemaVersion != SchemaVersion || status.IndexedRows != 1 || status.ProgressRows != 1 || status.ProgressTables != 1 || status.CoveredChats != 1 {
		t.Fatalf("status = %+v, want progress merged with real index stats", status)
	}

	MarkInterrupted(indexPath)
	status, err = StatusFor(ctx, indexPath, []string{db})
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != StatusInterrupted || status.JobState != JobStateInterrupted {
		t.Fatalf("status = %+v, want interrupted job_state interrupted", status)
	}
}

func TestCleanIndexTmpRemovesOrphansAndKeepsActiveTemp(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "messages.db")
	activeTmp := filepath.Join(dir, ".messages-active.db")
	orphanTmp := filepath.Join(dir, ".messages-orphan.db")
	if err := os.WriteFile(activeTmp, []byte("active"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanTmp, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := buildProgress{
		Status:    StatusBuilding,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().Format(time.RFC3339),
		TempPath:  activeTmp,
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobPathFor(indexPath), data, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CleanIndexTmpResult(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.Kept != 1 {
		t.Fatalf("clean result = %+v, want one removed and one kept", result)
	}
	if _, err := os.Stat(activeTmp); err != nil {
		t.Fatalf("active temp should be kept: %v", err)
	}
	if _, err := os.Stat(orphanTmp); !os.IsNotExist(err) {
		t.Fatalf("orphan temp should be removed, stat err=%v", err)
	}
}

func TestTimelineRefsCursorOrder(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		messages.TableName("alice"): {
			{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "alice one"},
			{LocalID: 3, SortSeq: 3001, CreateTime: 300, Status: 4, Content: "alice three"},
		},
		messages.TableName("bob"): {
			{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "bob two"},
		},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice", "bob"},
	}); err != nil {
		t.Fatal(err)
	}

	chats := []messages.ChatInfo{{Username: "alice"}, {Username: "bob"}}
	refs, err := TimelineRefs(ctx, indexPath, TimelineQuery{Chats: chats, Start: 0, End: 1000, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	got, err := messages.NewService([]string{db}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ChatUsername != "alice" || got[1].ChatUsername != "bob" {
		t.Fatalf("first page = %+v, want alice then bob", got)
	}
	nextRefs, err := TimelineRefs(ctx, indexPath, TimelineQuery{
		Chats:    chats,
		Start:    0,
		End:      1000,
		After:    timeline.KeyFor(got[1]),
		HasAfter: true,
		Limit:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := messages.NewService([]string{db}).ListByRefs(ctx, nextRefs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Content != "alice three" {
		t.Fatalf("next page = %+v, want alice three", next)
	}
}

func TestCoversChatsDetectsMissingIndexedChat(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		messages.TableName("alice"): {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "alice one"}},
		messages.TableName("bob"):   {{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "bob two"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}

	covered, err := CoversChats(ctx, indexPath, []messages.ChatInfo{{Username: "alice"}})
	if err != nil {
		t.Fatal(err)
	}
	if !covered {
		t.Fatal("alice should be covered by the index")
	}
	covered, err = CoversChats(ctx, indexPath, []messages.ChatInfo{{Username: "alice"}, {Username: "bob"}})
	if err != nil {
		t.Fatal(err)
	}
	if covered {
		t.Fatal("index should not be treated as covering bob")
	}
}

func TestUseForQueryAllowsReadyTimelineWithInactiveSelectedChat(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		messages.TableName("active@chatroom"): {{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "active one"}},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"active@chatroom"},
	}); err != nil {
		t.Fatal(err)
	}

	usable, err := UseForQuery(ctx, indexPath, []string{db}, UsabilityOptions{
		Chats: []messages.ChatInfo{
			{Username: "active@chatroom"},
			{Username: "inactive@chatroom"},
		},
		Start:    0,
		End:      200,
		HasStart: true,
		HasEnd:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !usable.UseIndex {
		t.Fatalf("ready global index should serve broad timeline even when selected contacts include inactive chats: %+v", usable)
	}
}

func TestSearchRefsFTSAndUnsupportedFallbackSignal(t *testing.T) {
	requireFTS5(t)
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "message_0.db")
	createIndexMessageDB(t, db, map[string][]indexMessageRow{
		messages.TableName("alice"): {
			{LocalID: 1, SortSeq: 1001, CreateTime: 100, Status: 4, Content: "AI project update"},
			{LocalID: 2, SortSeq: 2001, CreateTime: 200, Status: 4, Content: "ordinary note"},
		},
	})
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{
		Account:           "wxid_test",
		IndexPath:         indexPath,
		MessageCachePaths: []string{db},
		ChatUsernames:     []string{"alice"},
	}); err != nil {
		t.Fatal(err)
	}

	refs, total, err := SearchRefs(ctx, indexPath, SearchQuery{
		Chats: []messages.ChatInfo{{Username: "alice"}},
		Query: "AI",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := messages.NewService([]string{db}).ListByRefs(ctx, refs, messages.RefQueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].Content != "AI project update" {
		t.Fatalf("search result total=%d items=%+v, want AI project update", total, got)
	}
	if _, _, err := SearchRefs(ctx, indexPath, SearchQuery{Query: "\x01", Limit: 10}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported query error = %v, want ErrUnavailable", err)
	}
}

type indexMessageRow struct {
	LocalID      int64
	SortSeq      int64
	CreateTime   int64
	Status       int64
	LocalType    int64
	RealSenderID int64
	SenderName   string
	Content      string
}

type indexSessionRow struct {
	LastTimestamp int64
	UnreadCount   int64
	Summary       string
}

func createIndexMessageDB(t *testing.T, path string, tables map[string][]indexMessageRow) {
	t.Helper()
	db := sqlitetest.CreateDB(t, path, "CREATE TABLE Name2Id(user_name TEXT PRIMARY KEY, is_session INTEGER);")
	defer db.Close()
	sqlitetest.Exec(t, db, "INSERT INTO Name2Id(rowid, user_name, is_session) VALUES (?, ?, 0);", 2, "self_user")
	for table, rows := range tables {
		sqlitetest.Exec(t, db, fmt.Sprintf(`
CREATE TABLE %s (
  local_id INTEGER PRIMARY KEY,
  server_id INTEGER,
  local_type INTEGER,
  sort_seq INTEGER,
  real_sender_id INTEGER,
  create_time INTEGER,
  status INTEGER,
  message_content BLOB,
  WCDB_CT_message_content INTEGER
);
`, sqlitedb.QuoteIdent(table)))
		for _, row := range rows {
			if row.SenderName != "" && row.RealSenderID != 0 && row.RealSenderID != 2 {
				sqlitetest.Exec(t, db, "INSERT INTO Name2Id(rowid, user_name, is_session) VALUES (?, ?, 0);", row.RealSenderID, row.SenderName)
			}
			insertIndexMessageRowDB(t, db, table, row)
		}
	}
}

func createIndexSessionDB(t *testing.T, path string, rows map[string]indexSessionRow) {
	t.Helper()
	db := sqlitetest.CreateDB(t, path, `
CREATE TABLE SessionTable(
  username TEXT PRIMARY KEY,
  unread_count INTEGER,
  summary BLOB,
  last_timestamp INTEGER,
  last_msg_type INTEGER,
  last_msg_sender TEXT,
  last_sender_display_name TEXT
);`)
	defer db.Close()
	for username, row := range rows {
		sqlitetest.Exec(t, db, `
INSERT INTO SessionTable(username, unread_count, summary, last_timestamp, last_msg_type, last_msg_sender, last_sender_display_name)
VALUES (?, ?, ?, ?, 1, '', '');`, username, row.UnreadCount, []byte(row.Summary), row.LastTimestamp)
	}
}

func updateIndexSessionRow(t *testing.T, path string, username string, row indexSessionRow) {
	t.Helper()
	db, err := sqlitedb.OpenReadWrite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlitetest.Exec(t, db, `
INSERT INTO SessionTable(username, unread_count, summary, last_timestamp, last_msg_type, last_msg_sender, last_sender_display_name)
VALUES (?, ?, ?, ?, 1, '', '')
ON CONFLICT(username) DO UPDATE SET
  unread_count=excluded.unread_count,
  summary=excluded.summary,
  last_timestamp=excluded.last_timestamp;`, username, row.UnreadCount, []byte(row.Summary), row.LastTimestamp)
}

func insertIndexMessageRow(t *testing.T, path string, table string, row indexMessageRow) {
	t.Helper()
	db, err := sqlitedb.OpenReadWrite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	insertIndexMessageRowDB(t, db, table, row)
}

func insertIndexMessageRowDB(t *testing.T, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, table string, row indexMessageRow) {
	t.Helper()
	localType := row.LocalType
	if localType == 0 {
		localType = 1
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (local_id, server_id, local_type, sort_seq, real_sender_id, create_time, status, message_content, WCDB_CT_message_content) VALUES (?, 0, ?, ?, ?, ?, ?, ?, 0);",
		sqlitedb.QuoteIdent(table),
	)
	sqlitetest.Exec(t, db, query,
		row.LocalID,
		localType,
		row.SortSeq,
		row.RealSenderID,
		row.CreateTime,
		row.Status,
		[]byte(row.Content),
	)
}

func requireFTS5(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fts.db")
	db := sqlitetest.CreateDB(t, path, "")
	defer db.Close()
	if _, err := db.ExecContext(context.Background(), "CREATE VIRTUAL TABLE message_fts USING fts5(search_text);"); err != nil {
		t.Fatalf("modernc sqlite FTS5 unavailable: %v", err)
	}
}

func execIndexTestSQL(t *testing.T, ctx context.Context, path string, query string, args ...any) {
	t.Helper()
	db, err := sqlitedb.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlitetest.Exec(t, db, query, args...)
}

func indexTableIncrementalTxForTest(ctx context.Context, indexPath string, sourcePath string, sourceDBName string, table string, username string, afterLocalID int64) (int64, error) {
	indexDB, err := sqlitedb.OpenReadWrite(ctx, indexPath)
	if err != nil {
		return 0, err
	}
	defer indexDB.Close()
	sourceDB, err := sqlitedb.OpenReadOnly(ctx, sourcePath)
	if err != nil {
		return 0, err
	}
	defer sourceDB.Close()
	return indexTableIncrementalTx(ctx, indexDB, sourceDB, sourcePath, sourceDBName, table, username, afterLocalID, nil)
}
