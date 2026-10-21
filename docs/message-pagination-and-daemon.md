# Message pagination and daemon optimization

Stable message pagination and daemon discovery changes. The daemon changes address issue #3.

## Protocol

First page:

```sh
bin/wxview messages --username synthetic-chat --order desc --limit 100 --format json
```

Continue with the argument array in `meta.next_args`, using an executable plus argument
array, never shell evaluation. Allowlist `--username`, `--order`, `--cursor`, `--limit`,
`--start`, `--end`, `--format`, and optionally `--source` / `--no-index`.
Validate the expected account, conversation, direction and fixed bounds in the consuming
application. Cursor identity includes account/data-directory hash; no secrets or content.
A client may change limit or index selection between pages. Account/range/order changes
require restarting from page one. Explicit order requires positive limit.

Synthetic shape (cursor shown as a placeholder, not executable):

```json
{
  "meta": {
    "schema_version": 1,
    "timezone": "Asia/Shanghai",
    "mode": "messages",
    "username": "synthetic-chat",
    "order": "desc",
    "limit": 100,
    "returned": 100,
    "has_more": true,
    "next_cursor": "<opaque-token>",
    "next_args": ["messages", "--username", "synthetic-chat", "--order", "desc", "--cursor", "<opaque-token>", "--limit", "100", "--format", "json", "--end", "1792512000"]
  },
  "items": ["<100 unchanged-schema message items>"]
}
```

HTTP uses order/cursor and next_url with normalized Unix start/end, removing date,
after_seq and offset. Legacy calls without order preserve their seq-only protocol;
that protocol is not guaranteed to traverse non-monotonic or duplicate seq values.

This is keyset traversal of changing caches, not a database snapshot. Inserts after end
are excluded. Backfills after the cursor may appear, while backfills into already traversed
key ranges are missed until restart. Deletes shorten results. Stable source names and local
IDs are required across cache replacement. No schema migration or source DB indexes are created.

## Query cost

`BenchmarkMessagePages`: 100,000 synthetic text rows across two SQLite shards, page size
101 including has_more lookahead. macOS amd64, Intel i9-9880H, Go benchmark reports 16 logical
CPUs. One iteration per case: illustrative local measurements, not a production latency SLA.

| Source SQL index | First page | Later keyset page | Full-read reference | Go bytes/page |
| --- | ---: | ---: | ---: | ---: |
| absent | 707 ms | 373 ms | 1,881 ms | ~293 KB |
| `(create_time, sort_seq, local_id)` | 5.6 ms | 9.1 ms | 1,389 ms | ~289 KB |

The full-read reference uses the unlimited read/normalize path on the same synthetic data,
not a separately built historical binary. It allocates about 208 MB. The measurements are
Go allocated bytes per operation, not peak RSS; SQLite temporary disk/memory use is not
bounded by the Go allocation result. EXPLAIN shows a table scan and TEMP B-TREE without the
source index, and index traversal without the temporary sort when indexed.

Reproduce:

```sh
GOCACHE=$(pwd)/.cache/go-build go test ./internal/messages -run '^$' -bench BenchmarkMessagePages -benchtime=1x -benchmem -v
```

## Daemon behavior

Shared snapshot: 20s account probe, 5s shard enumeration, 1s known-file checks; debounce is
1s after changes. These are scheduling intervals, not end-to-end delivery guarantees.
macOS uses open-file evidence for account selection, caching failure for 20s rather than
selecting a fallback account by mtime. A missing message directory retries independently.
All daemon cache-writing entrypoints are serialized; watcher callbacks have one pending
slot, and existing index refresh coalescing remains. Windows retains its platform account
discovery policy; no new process probing is introduced there.

This change does not patch WAL, change key initialization, advance checkpoints or restart
services. A running old daemon continues old behavior until a later restart. Real daemon
CPU, lsof concurrency and message-update latency have NOT been measured before/after under
an identical workload; the previous issue samples are not an optimized-runtime benchmark.

## Validation

- `GOCACHE=$(pwd)/.cache/go-build go test ./...`: passed.
- `GOCACHE=$(pwd)/.cache/go-build go vet ./...`: passed.
- Windows amd64 cross-compilation: passed (not a Windows runtime test).
- Race tests: daemon, messages, msgindex and query passed; daemon/query rerun after
  final synchronization and calendar-range changes. macOS linker emitted LC_DYSYMTAB
  warnings, but test executables completed successfully with no race reports.
- Composite pagination tests cover 0/1/99/100/101/251 records, ties across shards,
  both directions, index/scan interchange, inclusive ranges and empty ranges.
- Mutation tests cover insertion beyond end, backfill on both sides of the cursor,
  and deletion of the cursor row. Cursor tests reject corruption, unsupported versions,
  account/directory/chat/range/order mismatches and ambiguous legacy combinations.
- Date tests include equivalent RFC3339 offsets and 23/25-hour DST calendar days.
- CLI argument / HTTP next URL tests preserve fixed bounds and filters. No-args/help
  comparison covers contacts, messages, timeline and daemon without querying chat data.
- Simulated six-watcher scheduling over 60 seconds: 3 account probes, 12 directory scans.
  Concurrent probe expiry/failure and long-callback trigger coalescing are tested.
- `daemon status` can display account probe count, failures, cumulative probe milliseconds
  and directory scan count from the updated daemon. Counters are process-lifetime values;
  reading them does not wait for in-progress lsof discovery.

The source release version is unchanged. Existing daemon processes need a restart after
installing an updated binary; replacing a binary alone does not update a running process.
