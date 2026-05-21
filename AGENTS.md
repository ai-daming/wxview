# Agent Notes for Wxview

Wxview is a local-first CLI for reading data from the user's own macOS or
Windows WeChat.
The V1 implementation is intentionally narrow: initialize supported DB keys,
decrypt `contact/contact.db`, `session/session.db`, message-related DBs, and
selected optional data DBs, run a local daemon for cache maintenance, list
contacts/groups from the decrypted contact cache, query recent/unread/incremental
sessions, and query history for an explicit username from decrypted
`message/message_[number].db` caches. It also supports bounded message-content
`search` and cross-conversation `timeline` queries for AI and script consumers.
Image, video, file, voice, and local avatar media should resolve usable local
paths automatically when the supporting caches are available.

## Collaboration Rules

- Do not run git operations such as commit, push, rebase, or branch changes
  unless the user explicitly asks for git work.
- Do not print full DB keys, raw secrets, or unnecessary WeChat data. Printing a
  short key fingerprint is acceptable.
- `wechat-decrypt/` is an external reference repo and is ignored by the root git
  repo. Read it for behavior, but do not mix it into the Go runtime path.
- Runtime state under `~/.wxview/` is local user data and must not be committed.

## Command Semantics

- `wxview init` is the first-time setup command. It detects WeChat, finds or
  reuses required DB keys, verifies each against page 1 HMAC, and saves them
  in `~/.wxview/cache/<account>/keys.json`. Normal use should run it once at
  the beginning. Auxiliary message DB keys are best-effort and should warn
  rather than block init.
- `wxview init` should be concise by default: print account, data_dir, key
  counts, and warnings. Per-DB fingerprints/status belong behind `--verbose`.
- `wxview me` reports the current WeChat identity Wxview will read. It supports
  `--format table|json` and `--detail paths`; default output must include a
  short `data_dir_fingerprint` but not the full source `data_dir`.
- On Windows, account discovery reads `%APPDATA%\Tencent\xwechat\config\*.ini`
  and searches `xwechat_files\<account>\db_storage`. `WXVIEW_WECHAT_DB_STORAGE`
  may point directly at a `db_storage` directory when auto-detection fails.
- Current account detection should prefer the account whose `db_storage` files
  are currently open by the running WeChat process. File mtime is only a
  fallback when open-file detection cannot identify an account.
- `wxview contacts` with no flags must show the same help as
  `wxview contacts --help`; it should not query data. Require an explicit
  output/filter flag such as `--format` or `--kind` to query.
- `wxview contact` is a supported alias for `wxview contacts`.
- `wxview contract --help` is accepted only as a typo-friendly help alias. Do
  not make `contract` an official command.
- `wxview contacts` is intended to be usable by other tools and AI agents. It
  supports `--format table|json|jsonl|csv`, `--kind`, `--query`, `--username`,
  `--limit`, `--offset`, `--sort username|name`, and `--count`. Prefer `json`,
  `jsonl`, or `csv` plus an explicit `--limit` for automated reads.
- `--count` reports the filtered total before pagination; `--limit` and
  `--offset` do not affect the count.
- `wxview api start` is the normal loopback-only local WebAPI entrypoint. It
  defaults to `--addr 127.0.0.1:38765`, runs in the background, and must return
  an error if a managed API instance is already running. V1 manages only one
  API instance: `wxview api status` and `wxview api stop` take no `--addr`.
  `wxview api serve` is retained as the foreground/debug form. Both forms must
  only allow loopback listen addresses. This is not a remote/authenticated API
  in V1.
- WebAPI business responses should include lightweight account/server identity
  metadata so callers can distinguish multiple local instances. Business query
  endpoints return a `{meta, items}` envelope; message-like endpoints reuse the
  same item schema as CLI `messages`, `timeline`, `search`, and
  `new-messages`.
- WebAPI `GET` endpoints are query-only. They parse only documented query
  parameters and ignore undeclared parameters, including `refresh=true`.
  Cache refresh is exposed as the single action `POST /v1/cache/refresh` with
  optional `scope=all|contacts|sessions|messages|favorites|articles|sns`.
  `POST /v1/new-messages/poll` advances the incremental checkpoint;
  `GET /v1/new-messages` is a read-only preview and must not advance it.
- WebAPI serves local media via `GET /v1/media?path=...`. It is an internal
  loopback helper, not a public file server: only absolute paths under the
  current account's WeChat data directory or `~/.wxview/cache/<account>` should
  be served. Message-like JSON responses should add `content_detail.media_url`
  and `content_detail.thumbnail_url` when `path` or `thumbnail_path` are
  available. They should also add URLs for nested media fields such as
  `refer_path`, `refer_thumbnail_path`, `avatar_path`, and media paths inside
  `record_items_json`.
- WebAPI V1 does not use asynchronous jobs. Long local reads stay synchronous
  and should rely on the server's 30-minute read/write timeout.
- WebAPI V1 data endpoints are `/health`, `/v1/me`, `/v1/contacts`,
  `/v1/contacts/detail`, `/v1/members`, `/v1/sessions`, `/v1/unread`,
  `/v1/messages`, `/v1/search`, `/v1/timeline`, `/v1/new-messages`,
  `/v1/favorites`, `/v1/articles/accounts`, `/v1/articles`, `/v1/sns/feed`,
  `/v1/sns/search`, and `/v1/sns/notifications`, plus the POST actions above.
- CLI and WebAPI query paths should share `internal/query` use-case logic.
  `cmd/wxview` owns flags, help, CLI JSON/table/csv/jsonl formatting, and
  `internal/webapi` owns HTTP params, HTTP meta/envelopes, and error responses.
  Do not duplicate time parsing, index fallback, chat metadata enrichment,
  media enrichment, new-message checkpoint, or daemon-first refresh fallback
  in either presentation layer.
- `wxview messages` requires `--username`; without args it must show the same
  help as `wxview messages --help`. It supports `--format table|json|jsonl|csv`,
  `--date`, `--start`, `--end`, `--after-seq`, `--limit`, `--offset`,
  `--source`, and `--refresh`.
- `wxview messages --format json` returns a `{meta, items}` envelope. `meta`
  includes `schema_version` and `timezone`. Reliable machine pagination should
  follow `meta.next_args` / `meta.next_after_seq`; `--offset` is mainly for
  temporary human reads. `jsonl`, `csv`, and `table` remain item-only outputs.
- Treat the `--username` value as an ordinary chat target even when it matches
  the current account username. Do not add a special self-chat guard or require
  an extra override flag for that case.
- `wxview messages` returns records sorted by time ascending by default. Apply
  `--limit` and `--offset` after merging all matching message shards.
- V1 maintains a derived local message index when available. `messages` may use
  it as a fast path when the index is ready, covers the chat, and is either
  globally near-real-time or chat-level fresh according to `session/session.db`.
  It must fall back to direct decrypted message-cache scanning when the index is
  missing, building, incompatible, or stale for the requested chat.
- `--start` and `--end` are inclusive. Date-only `--end` includes the full day.
- `messages --date today|yesterday|YYYY-MM-DD` selects one local calendar day
  and is mutually exclusive with `--start`/`--end`.
- `--after-seq` is a cursor-style filter. It returns only rows whose `seq` is
  greater than the provided value, then global sorting and pagination still
  apply.
- `--source` is a debugging flag only. It may expose source DB/table/local row
  metadata for cache/shard diagnosis, but normal chat-history reads should not
  require it.
- `wxview timeline` selects one or more conversations and returns a bounded
  cross-conversation time range. It supports `--format table|json|jsonl|csv`,
  `--kind all|friend|chatroom|other`, `--query`, `--username`, `--date`,
  `--start`, `--end`, `--limit`, `--cursor`, `--source`, and `--refresh`.
- `timeline` may use the derived message index only when it covers every
  selected conversation and each selected chat is fresh enough. Otherwise it
  fans out across selected conversations and message shards, merges results
  in-process, then applies the global limit. Broad selectors or wide date ranges
  can be very slow; prefer narrow selectors and small time windows in examples
  and AI/tool usage.
- `wxview timeline` must have an explicit time range: either `--date
  today|yesterday|YYYY-MM-DD` or both `--start` and `--end`. `--date` is
  mutually exclusive with `--start`/`--end`.
- `wxview timeline` must have an explicit conversation selector: either
  `--username`, or `--kind`/`--query`. `--username` cannot be combined with
  `--kind` or `--query`.
- `timeline --query` filters conversation metadata (`username`, `alias`,
  `remark`, `nick_name`); it does not search message content.
- `wxview timeline --format json` returns a `{meta, items}` envelope with
  `meta.schema_version`, `meta.timezone`, `meta.next_cursor`, and
  `meta.next_args`. Reliable pagination consumers should follow
  `meta.next_args`; `jsonl`, `csv`, and `table` remain item-only outputs.
- `timeline` `matched_chats` is the number of conversations selected by the
  filter, not the number of conversations that have returned messages on the
  current page or within the requested time range.
- `wxview search` searches message content and parsed `content_detail` values.
  The initial implementation is a local scan over selected conversations; future
  FTS/index work should be treated as an accelerator, not as part of the item
  schema contract. JSON output uses the same `{meta, items}` envelope and message
  item schema as `messages` and `timeline`.
- `wxview sessions`, `wxview unread`, and `wxview new-messages` read
  `session/session.db` directly from the local decrypted cache. `new-messages`
  keeps account-scoped state under `~/.wxview/cache/<account>/state/`.
- Future performance work should be documented as optional TODOs unless the
  product direction explicitly changes. Likely directions are SQL pagination
  pushdown for scan fallbacks, `timeline --explain` / dry-run estimation,
  better reconcile scheduling, and batched timeline fan-out with stable cursor
  semantics. FTS should remain mainly a `search` accelerator; `messages` and
  `timeline` rely on ordinary chat/time index rows for correctness and speed.
- Message `items` from `messages`, `timeline`, `search`, and `new-messages` must
  share the same schema.
  Include chat metadata fields `chat_kind`, `chat_display_name`, `chat_alias`,
  `chat_remark`, and `chat_nick_name`. `chat_display_name` is `remark >
  nick_name > alias > username`; missing contacts fall back to `chat_username`
  and `chat_kind=unknown`.
- `content` must remain the raw decoded message body. Use `content_detail` for
  convenience parsing. For image and video messages, parse useful XML metadata
  such as md5, length/play length, thumbnail dimensions, and CDN file
  identifiers there. Do not copy CDN AES keys into `content_detail`.
- `wxview messages` resolves image, video, file, voice, rich-message covers,
  emoji/stickers, quoted media, forwarded chat-record item media, and card
  avatars automatically for returned rows when supporting caches are available.
  It should resolve local media files from WeChat storage and decode supported
  `.dat` media into `~/.wxview/cache/<account>/media/`.
  Voice media should be decoded from `VoiceInfo.voice_data` into playable WAV
  files in the same media cache without requiring a system ffmpeg install.
  Put the result in `content_detail`, not a separate top-level `media` object:
  include `media_status`, `path`, `source_path`, `decoded`, `thumbnail`,
  `thumbnail_path`, `thumbnail_source_path`, `thumbnail_decoded`, `width`,
  `height`, and `media_reason` when available.
- `content_detail` may include prefixed nested media fields such as
  `refer_media_status`, `refer_path`, `refer_thumbnail_path`, and per-item media
  fields inside `record_items_json`. Missing nested media should be reported as
  `not_found` with a reason, not as a query error.
- Do not copy CDN AES keys, file upload tokens, thumb keys, auth keys, or hashed
  internal usernames into `content_detail`. Safe remote locators such as md5,
  CDN file identifiers, ordinary URLs, titles, sizes, and app identifiers are
  acceptable when useful for lookup or display.
- `wxview messages` must not scan WeChat process memory. If a message key is
  missing or invalid, tell the user to run `wxview init` with process-memory
  permissions (`sudo` on macOS, Administrator terminal on Windows).

## Contact Output Contract

The current `contacts` output fields are:

- `username`: stable WeChat username, such as `wxid_*` or `*@chatroom`
- `alias`: WeChat ID / alias when present
- `remark`: user's remark for the contact
- `nick_name`: nickname from WeChat
- `head_url`: avatar image URL
- `kind`: one of `friend`, `chatroom`, or `other`

The `kind` classifier is intentionally pragmatic:

- `friend`: ordinary private-chat contacts
- `chatroom`: usernames ending in `@chatroom`
- `other`: official accounts, enterprise contacts, non-friend room members, and
  special/system contacts
- `all`: CLI filter value only, not an output kind

Avoid reintroducing `is_friend`; its semantics were too ambiguous for the
product goal.

## Daemon and Cache

- V1 does not patch WAL files. Refresh is near-real-time after WeChat writes or
  checkpoints the main DB.
- In V1, the daemon is a cache maintenance service, not a contacts query
  service. It should focus on cache refresh, source DB watching, `health`,
  `refresh_contacts`, `refresh_sessions`, `refresh_messages`,
  `refresh_avatars`, `refresh_favorites`, `refresh_sns`, and `stop`.
- The only supported daemon CLI forms are `wxview daemon`, `wxview daemon start`,
  `wxview daemon stop`, and `wxview daemon status`. Bare `wxview daemon` must
  show the same help as `wxview daemon --help` and must not start the daemon.
- `wxview contacts ...` should always read contacts directly from the local
  decrypted cache, then apply filtering, sorting, pagination, counts, and output
  formatting in the CLI path.
- If `contacts --refresh` is used and the daemon is running, the CLI may ask the
  daemon to refresh first, then still read the cache directly. If the daemon is
  not running, the CLI may refresh the cache in-process.
- `wxview messages ...` should always read messages directly from local
  decrypted message caches. If `--refresh` is used and the daemon is running,
  it may ask the daemon to refresh message caches first; otherwise it may
  refresh message caches in-process. Do not route message queries through the
  daemon in V1.
- `wxview sessions ...` and `wxview unread ...` should always read sessions
  directly from the local decrypted session cache. If `--refresh` is used and
  the daemon is running, they may ask the daemon to refresh session cache first;
  otherwise they may refresh session cache in-process.
- `wxview new-messages ...` should use `session/session.db` as the changed-chat
  index, then read matching message rows directly from local decrypted message
  caches. Do not route new-message queries through the daemon in V1.
- `wxview favorites ...` should always read favorites directly from the local
  decrypted favorite cache. If `--refresh` is used and the daemon is running,
  it may ask the daemon to refresh favorite cache first; otherwise it may
  refresh favorite cache in-process.
- `wxview sns ...` should always read SNS data directly from the local
  decrypted SNS cache. If `--refresh` is used and the daemon is running, it may
  ask the daemon to refresh SNS cache first; otherwise it may refresh SNS cache
  in-process.
- `wxview timeline ...` should select conversations from the local decrypted
  contact cache and read messages directly from local decrypted message caches.
  If `--refresh` is used, refresh both contact and message caches, preferring
  daemon refresh actions when available. Do not route timeline queries through
  the daemon in V1.
- Do not reintroduce daemon-side `list_contacts`; the WebAPI product direction
  is a separate `wxview api start` / `wxview api serve` process that reads
  local cache/query services directly.
- The daemon uses the internal Unix socket `~/.wxview/wxview.sock`. Treat this as
  internal transport, not the WebAPI.
- Decrypted contact cache path is:
  `~/.wxview/cache/<account>/contact/contact.db`
- Decrypted message cache paths are:
  `~/.wxview/cache/<account>/message/message_*.db`
- Decrypted session cache path is:
  `~/.wxview/cache/<account>/session/session.db`
- Decrypted head image cache path is:
  `~/.wxview/cache/<account>/head_image/head_image.db`
- Account key store path is:
  `~/.wxview/cache/<account>/keys.json`. Do not use a global
  `~/.wxview/keys.json`; keys are account-scoped.
- Cache refresh metadata path is:
  `~/.wxview/cache/<account>/mtime.json`. It records source DB path, size,
  mtime, salt, cache path, and refresh time. Refresh should skip decrypting a
  DB when metadata still matches and the cache file exists.
- Daemon watchers should resolve the current account dynamically, so logging out
  of one WeChat account and logging into another while the daemon is running
  switches to the new account's key/cache metadata.
- Supported message-related DBs include numeric正文分片
  `message/message_[number].db` plus `message/biz_message_[number].db`,
  `message/media_[number].db`, `message/message_fts.db`,
  `message/message_resource.db`, and `message/message_revoke.db`.
- Required DB keys are `contact/contact.db` plus numeric message shards.
  `session/session.db`, `head_image/head_image.db`, `message/media_*.db`,
  `message_fts.db`, `message_resource.db`, and `message_revoke.db` are
  auxiliary/optional and should not block init/cache refresh when their key is
  not yet present in WeChat process memory.
- Use readable account directory names, not base64, unless a future account name
  contains unsafe path characters. Unsafe characters should be replaced with `_`.
- When running under `sudo`, state written under `~/.wxview` should be chowned
  back to the original user from `SUDO_UID`/`SUDO_GID`.

## SQLite and Decryption Notes

- SQLite cache validation, cache querying, and derived index writes use
  `database/sql` with the CGO-free `modernc.org/sqlite` driver. Normal wxview
  usage does not require an external SQLite command-line tool.
- Open decrypted cache DBs with an immutable read-only URI:
  `file://...?mode=ro&immutable=1`
  This avoids SQLite `unable to open database file (14)` issues with read-only
  mode and WAL/SHM side files.
- SQLCipher 4 raw key validation uses page 1 HMAC. Do not overwrite the previous
  cache on decrypt or validation failure.

## Verification

For code changes, run:

```sh
GOCACHE=$(pwd)/.cache/go-build go test ./...
GOCACHE=$(pwd)/.cache/go-build go vet ./...
```

For help behavior, useful smoke checks are:

```sh
go run ./cmd/wxview --help
go run ./cmd/wxview init --help
go run ./cmd/wxview contacts
go run ./cmd/wxview contacts --help
go run ./cmd/wxview messages
go run ./cmd/wxview messages --help
go run ./cmd/wxview timeline
go run ./cmd/wxview timeline --help
```
