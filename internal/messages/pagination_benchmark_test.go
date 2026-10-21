package messages

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"wxview/internal/sqlitedb"
)

func BenchmarkMessagePages(b *testing.B) {
	ctx := context.Background()
	for _, indexed := range []bool{false, true} {
		b.Run(fmt.Sprintf("source_index=%t", indexed), func(b *testing.B) {
			var paths []string
			table := TableName("synthetic")
			for shard := 0; shard < 2; shard++ {
				path := filepath.Join(b.TempDir(), fmt.Sprintf("message_%d.db", shard))
				db, err := sqlitedb.OpenReadWrite(ctx, path)
				if err != nil {
					b.Fatal(err)
				}
				script := fmt.Sprintf(`CREATE TABLE %s(local_id INTEGER PRIMARY KEY,server_id INTEGER,local_type INTEGER,sort_seq INTEGER,real_sender_id INTEGER,create_time INTEGER,status INTEGER,message_content BLOB);
CREATE TABLE Name2Id(user_name TEXT);
WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<50000)
INSERT INTO %s SELECT x,0,1,x/5,0,1700000000+x/10,0,CAST('synthetic message body' AS BLOB) FROM n;`, sqlitedb.QuoteIdent(table), sqlitedb.QuoteIdent(table))
				if err := sqlitedb.ExecScript(ctx, db, script); err != nil {
					b.Fatal(err)
				}
				if indexed {
					if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX page_order ON %s(create_time,sort_seq,local_id)", sqlitedb.QuoteIdent(table))); err != nil {
						b.Fatal(err)
					}
				}
				plan, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+messageRowsQuery(table, "")+" ORDER BY create_time DESC, sort_seq DESC, local_id DESC LIMIT 101")
				if err != nil {
					b.Fatal(err)
				}
				for plan.Next() {
					var id, parent, unused int
					var detail string
					if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
						b.Fatal(err)
					}
					if shard == 0 {
						b.Log(detail)
					}
				}
				plan.Close()
				db.Close()
				paths = append(paths, path)
			}
			service := NewService(paths)
			for _, mode := range []string{"first", "later", "full_scan_baseline"} {
				b.Run(mode, func(b *testing.B) {
					opts := QueryOptions{Username: "synthetic", Order: "desc", Limit: 101}
					if mode == "later" {
						opts.After = &PageKey{CreateTime: 1700002500, Seq: 5000, LocalID: 25000, SourceDB: "message_0.db"}
					}
					if mode == "full_scan_baseline" {
						opts.Limit = 0
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						rows, err := service.List(ctx, opts)
						if err != nil {
							b.Fatal(err)
						}
						if len(rows) < 100 {
							b.Fatal("unexpected short page")
						}
					}
				})
			}
		})
	}
}
