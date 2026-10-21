package msgindex

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"wxview/internal/messages"
)

func TestMessagePaginationScanIndexInterchangeable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "message_0.db"), filepath.Join(dir, "message_1.db")}
	for shard, path := range paths {
		var rows []indexMessageRow
		for i := 0; i < 127; i++ {
			rows = append(rows, indexMessageRow{LocalID: int64(i + 1), SortSeq: int64(i / 5), CreateTime: 100 + int64(i/10), Content: fmt.Sprintf("synthetic-%d-%d", shard, i)})
		}
		createIndexMessageDB(t, path, map[string][]indexMessageRow{messages.TableName("alice"): rows})
	}
	indexPath := filepath.Join(dir, "index.db")
	if _, err := Build(ctx, BuildOptions{Account: "synthetic", IndexPath: indexPath, MessageCachePaths: paths, ChatUsernames: []string{"alice"}}); err != nil {
		t.Fatal(err)
	}
	service := messages.NewService(paths)
	for _, order := range []string{"asc", "desc"} {
		var after *messages.PageKey
		seen := map[string]bool{}
		for page := 0; page < 4; page++ {
			opts := messages.QueryOptions{Username: "alice", Order: order, After: after, Limit: 100, Start: 100, End: 112, HasStart: true, HasEnd: true}
			scan, err := service.List(ctx, opts)
			if err != nil {
				t.Fatal(err)
			}
			refs, err := MessageRefs(ctx, indexPath, MessageQuery{Username: "alice", Order: order, After: after, Limit: 100, Start: 100, End: 112, HasStart: true, HasEnd: true})
			if err != nil {
				t.Fatal(err)
			}
			indexed, err := service.ListByRefs(ctx, refs, messages.RefQueryOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(scan) != len(indexed) {
				t.Fatalf("page lengths %d vs %d", len(scan), len(indexed))
			}
			for i, m := range scan {
				if m.ID != indexed[i].ID || m.Content != indexed[i].Content {
					t.Fatalf("page %d differs at %d", page, i)
				}
				if seen[m.ID] {
					t.Fatal("duplicate across pages")
				}
				seen[m.ID] = true
			}
			if len(scan) == 0 {
				break
			}
			// Alternate which execution path produces the continuation key.
			last := scan[len(scan)-1]
			if page%2 == 0 {
				last = indexed[len(indexed)-1]
			}
			k := messages.KeyForPage(last)
			after = &k
		}
		if len(seen) != 254 {
			t.Fatalf("%s got %d messages", order, len(seen))
		}
	}
}
