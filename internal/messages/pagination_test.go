package messages

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"wxview/internal/sqlitedb"
)

func TestStablePagesAcrossShards(t *testing.T) {
	for _, count := range []int{0, 1, 99, 100, 101, 251} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(fmt.Sprintf("%s/%d", order, count), func(t *testing.T) {
				dir := t.TempDir()
				paths := []string{filepath.Join(dir, "message_0.db"), filepath.Join(dir, "message_1.db")}
				batches := make([][]messageRow, 2)
				// Equal timestamps, sequences and local IDs across shards exercise every tie-breaker.
				for i := 0; i < count; i++ {
					batches[i%2] = append(batches[i%2], messageRow{LocalID: int64(i/2 + 1), SortSeq: int64(i / 10), CreateTime: 100 + int64(i/20), Content: "synthetic"})
				}
				for i, path := range paths {
					createMessageDB(t, path, TableName("alice"), batches[i])
				}
				svc := NewService(paths)
				opts := QueryOptions{Username: "alice", Order: order, Limit: 100}
				seen := map[string]bool{}
				var last *PageKey
				for pages := 0; ; pages++ {
					if pages > 4 {
						t.Fatal("pagination did not terminate")
					}
					page, err := svc.List(context.Background(), opts)
					if err != nil {
						t.Fatal(err)
					}
					for _, m := range page {
						if seen[m.ID] {
							t.Fatalf("duplicate %s", m.ID)
						}
						seen[m.ID] = true
						k := KeyForPage(m)
						if last != nil && !PageKeyLess(*last, k, order) {
							t.Fatalf("nonmonotonic page: %v -> %v", *last, k)
						}
						last = &k
					}
					if len(page) < 100 {
						break
					}
					opts.After = last
				}
				if len(seen) != count {
					t.Fatalf("got %d want %d", len(seen), count)
				}
			})
		}
	}
}

func TestPageMutationUsesKeyBoundaryNotSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message_0.db")
	table := TableName("alice")
	createMessageDB(t, path, table, []messageRow{
		{LocalID: 1, SortSeq: 1, CreateTime: 100}, {LocalID: 2, SortSeq: 2, CreateTime: 200},
		{LocalID: 3, SortSeq: 3, CreateTime: 300}, {LocalID: 4, SortSeq: 4, CreateTime: 400},
	})
	ctx := context.Background()
	service := NewService([]string{path})
	opts := QueryOptions{Username: "alice", Order: "desc", Start: 100, End: 400, HasStart: true, HasEnd: true, Limit: 2}
	first, err := service.List(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	k := KeyForPage(first[1])
	opts.After = &k
	db, err := sqlitedb.OpenReadWrite(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	// Delete the boundary row; backfill one row on each side and insert after end.
	_, err = db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE local_id=3;
 INSERT INTO %s(local_id,sort_seq,create_time) VALUES(5,5,500),(6,6,350),(7,7,250);`, sqlitedb.QuoteIdent(table), sqlitedb.QuoteIdent(table)))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	second, err := service.List(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || second[0].CreateTime != 250 || second[1].CreateTime != 200 {
		t.Fatalf("unexpected continuation: %+v", second)
	}
	opts.Start = 401
	opts.End = 499
	opts.After = nil
	empty, err := service.List(ctx, opts)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty range: %v %v", empty, err)
	}
}
