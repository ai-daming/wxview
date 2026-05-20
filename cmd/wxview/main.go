package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"wxview/internal/app"
	"wxview/internal/articles"
	"wxview/internal/buildinfo"
	"wxview/internal/contacts"
	"wxview/internal/daemon"
	"wxview/internal/favorites"
	"wxview/internal/identity"
	"wxview/internal/key"
	"wxview/internal/messages"
	"wxview/internal/msgindex"
	wxquery "wxview/internal/query"
	"wxview/internal/sessions"
	"wxview/internal/sns"
	"wxview/internal/webapi"
)

const messageEnvelopeSchemaVersion = 1

func main() {
	log.SetFlags(log.LstdFlags)
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}
	ctx := context.Background()
	switch args[0] {
	case "init":
		return runInit(ctx, args[1:], stdout)
	case "me":
		return runMe(ctx, args[1:], stdout)
	case "daemon":
		return runDaemon(args[1:], stdout)
	case "api":
		return runAPI(ctx, args[1:], stdout)
	case "cache":
		return runCache(args[1:], stdout)
	case "index":
		return runIndex(ctx, args[1:], stdout)
	case "contact", "contacts":
		return runContacts(ctx, args[1:], stdout)
	case "members":
		return runMembers(ctx, args[1:], stdout)
	case "sessions":
		return runSessions(ctx, args[1:], stdout)
	case "unread":
		return runUnread(ctx, args[1:], stdout)
	case "new-messages":
		return runNewMessages(ctx, args[1:], stdout)
	case "messages":
		return runMessages(ctx, args[1:], stdout, stderr)
	case "search":
		return runSearch(ctx, args[1:], stdout)
	case "timeline":
		return runTimeline(ctx, args[1:], stdout)
	case "favorites":
		return runFavorites(ctx, args[1:], stdout)
	case "articles":
		return runArticles(ctx, args[1:], stdout)
	case "sns":
		return runSNS(ctx, args[1:], stdout)
	case "contract":
		if len(args) > 1 && hasHelp(args[1:]) {
			contactsUsage(stdout)
			fmt.Fprintln(stdout, "\nNote: the official command is `contacts`; `contract --help` is accepted only as a typo-friendly help alias.")
			return nil
		}
		usage(stderr)
		return fmt.Errorf("unknown command: contract (did you mean contacts?)")
	case "help", "-h", "--help":
		if len(args) > 1 {
			return commandHelp(args[1], stdout, stderr)
		}
		usage(stdout)
		return nil
	default:
		usage(stderr)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `wxview - Read local WeChat data

Wxview is a local-first CLI for reading macOS and Windows WeChat 4.x data from the
user's own machine. It can obtain database keys, decrypt local WeChat
databases into ~/.wxview/cache, list contacts or contact-table groups, and
query message history for an explicit username.

Commands:
  wxview init       First-time setup: detect WeChat, get supported DB keys,
                    and save them locally. Usually run once at the beginning.
  wxview me         Show the current WeChat account identity Wxview will read.
  wxview daemon     Show daemon help.
  wxview api        Run the loopback-only local WebAPI.
  wxview cache      Inspect local decrypted cache freshness and key coverage.
  wxview index      Inspect and maintain the derived message index.
  wxview contacts   List contacts from the decrypted contact cache.
  wxview members    List members and owner for an explicit chatroom username.
  wxview sessions   List recent chats from the decrypted session cache.
  wxview unread     List unread chats from the decrypted session cache.
  wxview new-messages
                    Return messages newer than the account-scoped checkpoint.
  wxview messages   List messages for an explicit username.
  wxview search     Search message content across selected conversations.
  wxview timeline   List messages across selected conversations by time.
  wxview favorites  List WeChat favorites from the local favorite cache.
  wxview articles   List official-account articles and appmsg posts.
  wxview sns        Read local Moments feed, search results, or notifications.
  wxview help CMD   Show detailed help for a command.

Common examples:
  wxview init
  wxview me --format json
  wxview api start --addr 127.0.0.1:38765
  wxview contacts --format json
  wxview contacts --kind friend --format jsonl
  wxview contacts --kind friend --format csv
  wxview contacts --kind friend --query AI --limit 20 --format json
  wxview contacts --kind chatroom --format table
  wxview contacts --detail --username wxid_xxx --format json
  wxview members --username 123@chatroom --format json
  wxview sessions --limit 20 --format json
  wxview unread --limit 20 --format json
  wxview new-messages --limit 100 --format json
  wxview search --query "AI" --kind chatroom --date today --format json
  wxview contacts --refresh --format json
  wxview messages --username wxid_xxx --start "2026-05-01" --end "2026-05-14" --format json
  wxview messages --username wxid_xxx --date today --limit 100 --format json
  wxview messages --username wxid_xxx --after-seq 1773421286000 --limit 100 --format jsonl
  wxview timeline --kind chatroom --query AI --date today --limit 200 --format json
  wxview favorites --type article --limit 20 --format json
  wxview articles --query AI --date today --format json
  wxview sns feed --date today --limit 20 --format json
  wxview sns notifications --include-read --limit 20 --format json
  wxview cache status --format table
  wxview index status
  wxview index refresh
  wxview daemon start
  wxview daemon status

Machine-readable usage:
  Use --format json for machine-readable output. messages and timeline use a
  JSON envelope with meta and items; contacts uses a JSON array.
  Use --format jsonl for one JSON object per line.
  Use --format csv for spreadsheet/shell pipelines.
  Use --kind friend for ordinary private-chat contacts.
  Use --kind chatroom for groups present in the contact table.

Current scope:
  Supported: macOS and Windows WeChat 4.x contact/contact.db, message/message_*.db,
             selected optional data DBs such as favorite/favorite.db,
             sns/sns.db, and message/biz_message_*.db when keys are available.
             Message history reads by explicit username include local image/video
             path resolution when media files are available.
  Not included yet: WAL patching, remote Web API access, authenticated write APIs.

  Run:
  wxview init --help
  wxview me --help
  wxview api --help
  wxview api start --help
  wxview api status --help
  wxview api stop --help
  wxview api serve --help
  wxview contacts --help
  wxview members --help
  wxview sessions --help
  wxview unread --help
  wxview new-messages --help
  wxview messages --help
  wxview search --help
  wxview timeline --help
  wxview favorites --help
  wxview articles --help
  wxview sns --help
  wxview index --help
  wxview cache --help
  wxview daemon --help`)
}

func commandHelp(command string, stdout io.Writer, stderr io.Writer) error {
	switch command {
	case "init":
		initUsage(stdout)
	case "me":
		meUsage(stdout)
	case "daemon":
		daemonUsage(stdout)
	case "api":
		apiUsage(stdout)
	case "cache":
		cacheUsage(stdout)
	case "index":
		indexUsage(stdout)
	case "contact", "contacts":
		contactsUsage(stdout)
	case "members":
		membersUsage(stdout)
	case "sessions":
		sessionsUsage(stdout)
	case "unread":
		unreadUsage(stdout)
	case "new-messages":
		newMessagesUsage(stdout)
	case "messages":
		messagesUsage(stdout)
	case "search":
		searchUsage(stdout)
	case "timeline":
		timelineUsage(stdout)
	case "favorites":
		favoritesUsage(stdout)
	case "articles":
		articlesUsage(stdout)
	case "sns":
		snsUsage(stdout)
	default:
		usage(stderr)
		return fmt.Errorf("unknown command: %s", command)
	}
	return nil
}

func initUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview init - First-time setup for reading local WeChat data

Usage:
  wxview init [--verbose]
  go run ./cmd/wxview init [--verbose]

When to run:
  Run this at the beginning before using contacts/daemon.
  In normal use, run it once. Run it again only if WeChat changes account,
  contact/contact.db salt changes, the saved key becomes invalid, or WeChat is
  reinstalled/updated in a way that changes the database key.

What it does:
  1. Detects the current WeChat 4.x account under macOS:
     ~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/<account>/db_storage
     or Windows:
     <data-root>\xwechat_files\<account>\db_storage
  2. Finds required databases:
     contact/contact.db
     message/message_[number].db
     and best-effort auxiliary message databases:
     message/message_fts.db, message/message_resource.db, message/message_revoke.db
  3. Reads each discovered DB page 1 salt.
  4. Reuses existing valid keys from ~/.wxview/cache/<account>/keys.json, or
     scans the running WeChat process memory for missing SQLCipher raw keys.
  5. Verifies each key with page 1 HMAC.
  6. Saves version 1 account metadata and keys in
     ~/.wxview/cache/<account>/keys.json with mode 0600.

Required DB key failures stop init. Auxiliary message DB failures are reported
as warnings and can be retried later with wxview init. On macOS this usually
means running with sudo; on Windows run the terminal as Administrator.

Output fields:
  account       WeChat account directory name.
  data_dir      Source db_storage directory.
  keys_total    Number of DB keys prepared.
  keys_scanned  Number of keys found in this run.
  keys_reused   Number of saved keys reused.
  warnings      Optional DBs that could not be prepared in this run.

Flags:
  --verbose     Also print each DB path, short key fingerprint, and status.

Notes:
  The full key is never printed, even with --verbose.
  Key scanning needs WeChat running and permission to read its process memory.
  On macOS Hardened Runtime WeChat builds, sudo alone may not be enough; use a
  local GUI terminal with Developer Tools permission or ad-hoc re-sign WeChat.
  On Windows, run from an elevated terminal. If automatic path detection fails,
  set WXVIEW_WECHAT_DB_STORAGE to the account db_storage directory.`)
}

func meUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview me - Show the current WeChat account identity

Usage:
  wxview me [--format table|json] [--detail paths]
  go run ./cmd/wxview me [--format table|json] [--detail paths]

What it does:
  Detects the current WeChat db_storage directory, derives the account username,
  and enriches it from the decrypted contact cache when the user's own contact
  row is present.

Output fields:
  account               WeChat account directory name.
  username              Current account username Wxview will read as self.
  display_name          remark > nick_name > alias > username.
  alias                 WeChat ID / alias when present in contact cache.
  remark                User remark when present in contact cache.
  nick_name             Nickname when present in contact cache.
  identity_source       account or contact_cache.
  cache_dir             Account-scoped ~/.wxview/cache/<account> directory.
  data_dir_fingerprint  Short sha256 fingerprint of the source db_storage path.
  data_dir              Only included with --detail paths.

Flags:
  --format table|json   Output format. Default: table.
  --detail paths        Include the full source db_storage path for debugging.

Examples:
  wxview me
  wxview me --format json
  wxview me --format json --detail paths`)
}

func apiUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview api - Run the loopback-only local WebAPI

Usage:
  wxview api
  wxview api start [--addr 127.0.0.1:38765]
  wxview api status
  wxview api stop
  wxview api serve [--addr 127.0.0.1:38765]

Subcommands:
  start    Start the loopback-only HTTP server in the background.
  status   Show the single managed API instance status.
  stop     Stop the single managed API instance.
  serve    Run the HTTP server in the foreground for debugging.

Notes:
  The WebAPI is a separate local data entrypoint. It does not reuse the daemon
  socket and does not make the daemon a query service. GET endpoints are
  read-only queries; POST endpoints are local cache/new-messages actions.
  Only loopback addresses such as 127.0.0.1:38765, localhost:38765, or
  [::1]:38765 are accepted.
  V1 supports one API instance. Running start or serve while another API
  instance is already running returns an error.

Endpoints:
  GET /health
  GET /v1/me
  GET /v1/contacts?kind=chatroom&limit=5
  GET /v1/sessions?limit=5
  GET /v1/messages?username=wxid_xxx&limit=20
  GET /v1/timeline?kind=chatroom&date=today&limit=20
  GET /v1/search?query=合同&limit=20
  GET /v1/favorites?limit=20
  GET /v1/articles/accounts
  GET /v1/sns/feed?limit=20
  POST /v1/cache/refresh
  POST /v1/new-messages/poll
  POST /v1/new-messages/reset

Run:
  wxview api start --help
  wxview api status --help
  wxview api stop --help
  wxview api serve --help`)
}

func apiStartUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview api start - Start the loopback-only local WebAPI in the background

Usage:
  wxview api start [--addr 127.0.0.1:38765]

Flags:
  --addr HOST:PORT   loopback-only listen address. Default: 127.0.0.1:38765.

Notes:
  V1 supports one API instance. If an API instance started by start or serve is
  already running, start returns an error instead of starting a second process.
  Runtime files are:
    ~/.wxview/api.json
    ~/.wxview/api.log`)
}

func apiStatusUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview api status - Show the single managed WebAPI status

Usage:
  wxview api status

Notes:
  status does not take --addr in V1 because only one managed API instance is
  supported.`)
}

func apiStopUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview api stop - Stop the single managed WebAPI instance

Usage:
  wxview api stop

Notes:
  stop does not take --addr in V1 because only one managed API instance is
  supported. Foreground wxview api serve processes started by this version are
  also registered as the single API instance.`)
}

func apiServeUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview api serve - Run the loopback-only local WebAPI in the foreground

Usage:
  wxview api serve [--addr 127.0.0.1:38765]

Flags:
  --addr HOST:PORT   loopback-only listen address. Default: 127.0.0.1:38765.

Notes:
  V1 supports one API instance. serve registers itself while running, so start
  or another serve command will return an error until this process exits.

Endpoints:
  GET /health
  GET /v1/me
  GET /v1/me?detail=paths
  GET /v1/contacts?kind=chatroom&query=AI&limit=5
  GET /v1/contacts/detail?username=wxid_xxx
  GET /v1/members?username=123@chatroom
  GET /v1/sessions?kind=chatroom&limit=20
  GET /v1/unread?limit=20
  GET /v1/messages?username=wxid_xxx&date=today&limit=100
  GET /v1/search?query=合同&kind=all&date=today&limit=50
  GET /v1/timeline?kind=chatroom&query=AI&date=today&limit=200
  GET /v1/new-messages?limit=100
  GET /v1/favorites?type=article&limit=20
  GET /v1/articles/accounts
  GET /v1/articles?username=gh_xxx&limit=20
  GET /v1/sns/feed?limit=20
  GET /v1/sns/search?query=关键词&limit=20
  GET /v1/sns/notifications?include_read=true&limit=20
  POST /v1/cache/refresh
  POST /v1/new-messages/poll
  POST /v1/new-messages/reset

Contact query parameters:
  kind       all, friend, chatroom, or other. Default: all.
  query      Contains search over username, alias, remark, and nick_name.
  username   Exact username lookup.
  limit      Maximum rows. Default: 50. Max: 500. 0 means no limit.
  offset     Rows to skip before returning results.
  sort       username or name. Default: username.

Response notes:
  Business endpoints return JSON and include account/server identity in meta,
  so callers can confirm which local WebAPI endpoint they are reading.
  GET query endpoints ignore undeclared parameters, including refresh=true.
  Refresh is exposed only as POST /v1/cache/refresh with optional scope:
  all, contacts, sessions, messages, favorites, articles, or sns.`)
}

const (
	daemonForegroundEnv   = "WXVIEW_DAEMON_FOREGROUND"
	apiManagedChildEnv    = "WXVIEW_API_MANAGED_CHILD"
	daemonSupportedForms  = "`wxview daemon`, `wxview daemon start`, `wxview daemon stop`, `wxview daemon status`"
	daemonStartWait       = 15 * time.Second
	daemonStopTimeout     = 35 * time.Second
	daemonStopWait        = 5 * time.Second
	daemonStatusPollEvery = 100 * time.Millisecond
	apiStartWait          = 10 * time.Second
	apiStopWait           = 5 * time.Second
	apiStatusPollEvery    = 100 * time.Millisecond
)

func daemonUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview daemon - Manage the local WeChat contact cache daemon

Usage:
  wxview daemon
  wxview daemon start
  wxview daemon stop
  wxview daemon status

Supported forms:
  wxview daemon         Show this help.
  wxview daemon start   Start the daemon in the background.
  wxview daemon stop    Stop the background daemon.
  wxview daemon status  Check whether ~/.wxview/wxview.sock responds to health.

Flags:
  No daemon flags are currently supported except -h/--help/help.

What it does:
  1. Uses keys prepared by wxview init.
  2. Decrypts contact/contact.db into:
     ~/.wxview/cache/<account>/contact/contact.db
  3. Decrypts supported message DBs into:
     ~/.wxview/cache/<account>/message/
  4. Opens an internal Unix socket:
     ~/.wxview/wxview.sock
  5. Watches contact, session, supported message/media, head_image, favorite,
     and sns DB files and
     refreshes affected caches after a debounce delay when they change.
     The current account is resolved from DB files opened by WeChat, so account
     switches while the daemon is running move to the new account cache.
     Unchanged DBs are skipped using ~/.wxview/cache/<account>/mtime.json.

Internal daemon actions:
  health
  refresh_contacts
  refresh_sessions
  refresh_messages
  refresh_index
  refresh_avatars
  refresh_favorites
  refresh_sns
  stop

Notes:
  This is an internal local transport, not the WebAPI. Use wxview api start or
  wxview api serve for the separate local HTTP data endpoint.
  daemon start writes daemon logs to ~/.wxview/wxview.log.
  V1 does not patch or stream .db-wal, so refresh is near-real-time after WeChat
  checkpoints/writes the main DB.`)
}

func cacheUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview cache - Inspect local decrypted cache status

Usage:
  wxview cache
  wxview cache status [--group GROUP] [--format table|json|jsonl|csv]
  wxview cache clean-tmp [--format table|json]
  wxview cache --help

Subcommands:
  status         Report source DB, decrypted cache, mtime metadata, and key status.
  clean-tmp      Remove expired decrypted-cache *.tmp files. Index tmp files
                 are handled by wxview index clean-tmp.

No-argument behavior:
  wxview cache is intentionally the same as wxview cache --help.
  It does not refresh, decrypt, scan process memory, or talk to the daemon.

Groups:
  all            Every discovered supported DB. This is the default.
  contacts       contact/contact.db.
  messages       message/message_*.db, biz_message_*.db, and message auxiliary DBs.
  media          message/media_*.db.
  sessions       session/session.db.
  avatars        head_image/head_image.db.
  favorites      favorite/favorite.db.
  sns            sns/sns.db.

Flags:
  --group all|contacts|messages|media|sessions|avatars|favorites|sns
  --format table   Human-readable table output.
  --format json    JSON object with count and items.
  --format jsonl   Newline-delimited JSON, one DB status item per line.
  --format csv     CSV with header row.

Output fields:
  group, account, db_rel_path, status, key_status, source_size, cache_size,
  source_mtime, cache_mtime, refreshed_at, and reason.
  JSON output also includes data_dir, source_path, cache_path, source_exists,
  and cache_exists.

Status values:
  fresh          Cache exists and mtime metadata matches the source DB.
  stale          Cache exists, but metadata is missing, unreadable, or mismatched.
  missing_cache  Source DB exists, but the decrypted cache file is missing.
  missing_source Source DB path disappeared after discovery.

Key status values:
  available      keys.json contains a valid key for the current source salt.
  missing        No saved key matches this DB and current salt.
  invalid        A saved key exists but no longer validates page 1 HMAC.
  unknown        Key store or source salt could not be inspected.

Examples:
  wxview cache status
  wxview cache status --group messages --format json
  wxview cache status --group media --format csv
  wxview cache clean-tmp`)
}

func indexUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview index - Inspect and maintain the derived message index

Usage:
  wxview index
  wxview index status [--format table|json|jsonl|csv]
  wxview index refresh [--format table|json]
  wxview index rebuild --reset [--format table|json]
  wxview index clean-tmp [--format table|json]
  wxview index --help

Subcommands:
  status         Report whether ~/.wxview/cache/<account>/index/messages.db is
                 missing, building, interrupted, ready, refreshing, stale, or
                 schema_mismatch.
  refresh        Default maintenance action. Refreshes message caches first,
                 resumes interrupted builds, builds missing indexes, and appends
                 new rows to a ready index.
  rebuild        Requires --reset. Deletes the current index/job/tmp files, then
                 rebuilds from zero.
  clean-tmp      Remove orphaned index tmp files from the index directory.

Notes:
  The index is a derived cache. It can be deleted and rebuilt at any time.
  It stores row references and decoded text for future search acceleration, but
  does not store DB keys or CDN AES keys. It accelerates messages and timeline
  when ready and near-realtime. Lag up to 60 seconds is treated as normal
  near-realtime delay; larger lag falls back to the original scan path.
  This does not replace wxview cache status, which observes decrypted cache
  freshness and key coverage.

Flags:
  --format table   Human-readable table output.
  --format json    JSON output.
  --format jsonl   Status only: one JSON object.
  --format csv     Status only: CSV with header row.

Examples:
  wxview index status
  wxview index status --format json
  wxview index refresh --format json
  wxview index rebuild --reset
  wxview index clean-tmp`)
}

func contactsUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview contacts - List WeChat contacts and contact-table groups

Usage:
  wxview contacts --format table|json|jsonl|csv [flags]
  wxview contacts --detail --username USERNAME --format json [flags]
  wxview contacts --count [flags]
  wxview contacts --help

Alias:
  wxview contact

No-argument behavior:
  wxview contacts is intentionally the same as wxview contacts --help.
  To query data, pass an explicit output/filter flag such as --format or --kind.

Flags:
  --format table   Human-readable table output.
  --format json    Machine-readable JSON array.
  --format jsonl   Machine-readable newline-delimited JSON, one contact per line.
  --format csv     Machine-readable CSV with header row.

  --kind all       Return every row selected from the contact table.
  --kind friend    Ordinary private-chat contacts.
  --kind chatroom  Group chats.
  --kind other     Official accounts, enterprise contacts, non-friend room members,
                   and special/system contacts.

  --query TEXT     Case-insensitive contains search over username, alias,
                   remark, and nick_name.
  --username TEXT  Exact username lookup, e.g. wxid_* or *@chatroom.
  --detail         With --username, return one rich contact detail object
                   instead of the default contact-list fields.
  --limit N        Return at most N rows. 0 means no limit.
  --offset N       Skip N rows before returning results. Requires stable sorting
                   for paging; default sort is username.
  --sort username  Sort by username. This is the default and best for paging.
  --sort name      Sort by display name: remark, then nick_name, then alias,
                   then username.
  --count          Output only the number of rows after filters. Pagination
                   flags --limit and --offset are ignored for counts.

  --refresh        Before listing, decrypt the source contact/contact.db into the
                   local cache again. Without --refresh, uses the existing cache
                   when available.

Output fields:
  username    Stable WeChat username, e.g. wxid_* or *@chatroom.
  alias       WeChat ID / alias when present.
  remark      Your remark for the contact.
  nick_name   Contact nickname from WeChat.
  head_url    Avatar image URL.
  kind        friend, chatroom, or other.

Detail-only fields:
  small_head_url, big_head_url, description, verify_flag, local_type,
  is_chatroom, and is_official are only returned by --detail.

Examples for humans:
  wxview contacts --format table
  wxview contacts --kind friend --format table
  wxview contacts --kind chatroom --format table
  wxview contacts --kind friend --query AI --limit 20 --format table

Examples for AI/tools:
  wxview contacts --format json
  wxview contacts --kind friend --format json
  wxview contacts --kind chatroom --format jsonl
  wxview contacts --kind friend --format csv
  wxview contacts --kind friend --query AI --limit 20 --offset 0 --sort username --format json
  wxview contacts --username wxid_xxx --format json
  wxview contacts --detail --username wxid_xxx --format json
  wxview contacts --kind friend --count
  wxview contacts --refresh --format json

Runtime behavior:
  This command always reads contacts from the local decrypted cache.
  If --refresh is used and the daemon is running, it asks the daemon to refresh
  the cache first; otherwise it refreshes the cache in this process.`)
}

func membersUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview members - List group members and owner from the contact cache

Usage:
  wxview members --username CHATROOM --format table|json|jsonl|csv [flags]
  wxview members --query TEXT --format json [flags]
  wxview members --help

Selection:
  --username TEXT  Exact chatroom username, e.g. 123@chatroom.
  --query TEXT     Search chatroom username, alias, remark, and nick_name.
                   The query must match exactly one chatroom; otherwise use
                   --username.

Flags:
  --format table   Human-readable table output.
  --format json    JSON object with owner, count, and members.
  --format jsonl   One member JSON object per line.
  --format csv     Member CSV with header row.
  --refresh        Refresh contact/contact.db cache before querying.

Output fields:
  username           Group chat username.
  display_name       Group display name.
  owner              Owner username when WeChat exposes it.
  owner_display_name Owner display name when present in member rows.
  members            Member rows with username, display_name, alias, remark,
                     nick_name, kind, and is_owner.

Examples:
  wxview members --username 123@chatroom --format json
  wxview members --query "AI交流群" --format table
  wxview members --username 123@chatroom --refresh --format csv`)
}

func sessionsUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview sessions - List recent WeChat sessions

Usage:
  wxview sessions --format table|json|jsonl|csv [flags]
  wxview sessions --help

Flags:
  --format table|json|jsonl|csv
  --kind all|friend|chatroom|other
                   Filter by conversation kind after contact metadata is applied.
  --query TEXT     Search username, display name, sender, or summary.
  --limit N        Return at most N rows. Default 20. 0 means no limit.
  --offset N       Skip N rows.
  --refresh        Refresh session/session.db cache before querying.

Output fields:
  username, chat_kind, chat_display_name, unread_count, summary,
  last_timestamp, time, last_msg_type, last_msg_sub_type, last_sender,
  and last_sender_display_name.

Examples:
  wxview sessions --limit 20 --format table
  wxview sessions --kind chatroom --query AI --format json
  wxview sessions --refresh --format jsonl`)
}

func unreadUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview unread - List unread WeChat sessions

Usage:
  wxview unread --format table|json|jsonl|csv [flags]
  wxview unread --help

Flags:
  --format table|json|jsonl|csv
  --kind all|friend|chatroom|other
  --query TEXT     Search username, display name, sender, or summary.
  --limit N        Return at most N rows. Default 20. 0 means no limit.
  --offset N       Skip N rows.
  --refresh        Refresh session/session.db cache before querying.

Examples:
  wxview unread --format json
  wxview unread --kind chatroom --limit 20 --format table`)
}

func newMessagesUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview new-messages - Return messages newer than the saved checkpoint

Usage:
  wxview new-messages --format table|json|jsonl|csv [flags]
  wxview new-messages --help

Flags:
  --format table|json|jsonl|csv
  --limit N        Return at most N messages. Default 100. 0 means no limit.
  --source         Include source DB/table/local row metadata.
  --reset          Ignore and overwrite the saved checkpoint.
  --refresh        Refresh session and message-related caches before querying.

Behavior:
  State is account-scoped under ~/.wxview/cache/<account>/state/.
  First run or --reset uses a 24-hour fallback window instead of returning all
  historical messages. Results share the same item schema as messages/timeline.

Examples:
  wxview new-messages --limit 100 --format json
  wxview new-messages --reset --format table`)
}

func messagesUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview messages - List WeChat messages for one username

Usage:
  wxview messages --username USERNAME --format table|json|jsonl|csv [flags]
  wxview messages --help

Required:
  --username TEXT  Exact WeChat username, e.g. wxid_* or *@chatroom.

Flags:
  --format table   Human-readable table output.
  --format json    JSON envelope with meta and items.
  --format jsonl   Machine-readable newline-delimited JSON, one message per line.
  --format csv     Machine-readable CSV with header row.

  --start TIME     Inclusive start time. Supports Unix seconds, YYYY-MM-DD,
                   YYYY-MM-DD HH:MM, YYYY-MM-DD HH:MM:SS, or RFC3339.
  --end TIME       Inclusive end time. Date-only values include the full day.
  --date today|yesterday|YYYY-MM-DD
                   Select one full local day. Cannot be combined with --start
                   or --end.
  --after-seq N    Return rows with seq greater than N. Use this for cursor-style
                   "next page after this message" reads.
  --limit N        Return at most N rows after global time sorting. 0 means no limit.
  --offset N       Skip N rows after global time sorting.
  --source         Include source DB/table/local row metadata for debugging.
  --no-index       Force the old scan path even when a ready index exists.
  --trace-time     Write per-stage timing trace to stderr. JSON stdout remains
                   valid, so use "2>trace.log" to capture it.
  --refresh        Decrypt message/message_*.db shards and supported
                   message-related DBs into the local cache before querying. Without --refresh,
                   uses the existing complete cache when available; otherwise
                   refreshes in this process.

Output fields:
  id                Stable local message id for this cache row.
  chat_username     Conversation username requested by --username.
  chat_kind         friend, chatroom, other, or unknown.
  chat_display_name Conversation display name: remark, nick_name, alias, then
                    username.
  chat_alias        Conversation alias when present.
  chat_remark       Your remark for the conversation when present.
  chat_nick_name    Conversation nickname when present.
  from_username     Actual sender username when known. "self" is only used as a
                    fallback when WeChat does not expose the local username.
  direction         out, in, or unknown.
  is_self           Whether this row is from the local user.
  is_chatroom       Whether chat_username is a group chat.
  seq               WeChat sort sequence; pass it to --after-seq for cursor reads.
  server_id         WeChat server message id when present.
  create_time       Unix timestamp from WeChat.
  time              Local formatted time.
  type              Lower 32 bits of WeChat local_type.
  sub_type          Upper 32 bits of WeChat local_type.
  content           Raw decoded message content. XML stays XML.
  content_detail    Parsed convenience fields such as type, text, title, url,
                    image/video metadata, and local media paths when available.
  content_encoding  text, zstd, zstd_error, or invalid_hex.

Source fields:
  Only shown when --source is passed. They are useful for debugging cache/shard
  behavior, but are not needed for normal chat-history reads.

JSON output:
  --format json returns {"meta": ..., "items": [...]}.
  meta.schema_version identifies the envelope contract, and meta.timezone shows
  the local timezone used to interpret --date, --start, --end, and date-only
  bounds.
  meta.next_after_seq and meta.next_args are included when another page exists.
  Reliable clients should continue with meta.next_args rather than --offset.
  jsonl, csv, and table outputs remain item-only.

Examples:
  wxview messages --username wxid_xxx --format table
  wxview messages --username wxid_xxx --date today --limit 100 --format json
  wxview messages --username wxid_xxx --start "2026-05-01" --end "2026-05-14" --format json
  wxview messages --username wxid_xxx --after-seq 1773421286000 --limit 100 --format jsonl
  wxview messages --username 123@chatroom --limit 100 --offset 0 --format jsonl
  wxview messages --username wxid_xxx --source --refresh --format json

Runtime behavior:
  This command reads message rows from local decrypted message caches. When the
  derived message index is ready and near-realtime, it uses the index to select
  page row refs and then reads full rows from the original decrypted caches. If
  the index is missing, building, interrupted, too stale, or unusable, it falls
  back to the old shard scan. Use --no-index to compare or debug the scan path.
  Results remain sorted by create_time ascending, then seq, source local id,
  and source shard.
  Image and video messages in the returned page are resolved to local files
  automatically. WeChat .dat images and recognizable .dat videos are decoded
  or normalized into ~/.wxview/cache/<account>/media/.
  If a required message DB key is missing, run wxview init with process-memory
  permissions first.`)
}

func searchUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview search - Search local WeChat message content

Usage:
  wxview search --query TEXT --format table|json|jsonl|csv [flags]
  wxview search --help

Required:
  --query TEXT     Message-content keyword. This searches decoded content and
                   parsed content_detail values.

Conversation selection:
  --username TEXT  Search one exact conversation username.
  --kind all|friend|chatroom|other
                   Select conversations from the contact cache. Default all.
  --chat-query TEXT
                   Filter conversation metadata before searching. Useful with
                   --kind chatroom. Cannot be combined with --username.

Time range:
  --date today|yesterday|YYYY-MM-DD
  --start TIME
  --end TIME

Other flags:
  --format table|json|jsonl|csv
  --limit N        Return at most N rows after global newest-first sorting.
                   Default 50. 0 means no limit.
  --offset N       Skip N rows after sorting.
  --source         Include source DB/table/local row metadata.
  --refresh        Refresh contact and message caches before querying.

JSON output:
  --format json returns {"meta": ..., "items": [...]} using the same message
  item schema as messages and timeline.

Examples:
  wxview search --query "AI" --date today --format json
  wxview search --query "开会" --kind chatroom --chat-query 项目 --limit 50 --format table
  wxview search --query "合同" --username wxid_xxx --start "2026-05-01" --format json`)
}

func timelineUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview timeline - List WeChat messages across selected conversations

Usage:
  wxview timeline --kind all|friend|chatroom|other --query TEXT --date today --format json [flags]
  wxview timeline --username USERNAME --start TIME --end TIME --format json [flags]
  wxview timeline --help

Conversation selection:
  --kind all       Select from all conversation rows in the contact cache.
  --kind friend    Select ordinary private-chat contacts.
  --kind chatroom  Select group chats.
  --kind other     Select official accounts, enterprise contacts, and other rows.
  --query TEXT     Case-insensitive conversation filter over username, alias,
                   remark, and nick_name. This does not search message content.
  --username TEXT  Exact conversation username. Cannot be combined with --kind
                   or --query.

Time range:
  --date today|yesterday|YYYY-MM-DD
                   Select one full local day. Cannot be combined with --start
                   or --end.
  --start TIME     Inclusive start time. Required with --end when --date is not used.
  --end TIME       Inclusive end time. Required with --start when --date is not used.

Pagination:
  --limit N        Return at most N rows. Default 200. Maximum 1000.
  --cursor TOKEN   Continue from meta.next_cursor. Keep the same query arguments.

Other flags:
  --format table   Human-readable table output.
  --format json    JSON envelope with meta and items. Use this for reliable paging.
  --format jsonl   Newline-delimited JSON, one message item per line.
  --format csv     CSV message items with header row.
  --source         Include source DB/table/local row metadata for debugging.
  --no-index       Force the old scan path even when a ready index exists.
  --refresh        Refresh contact and message caches before querying.

Examples:
  wxview timeline --kind chatroom --query AI --date today --limit 200 --format json
  wxview timeline --kind chatroom --query AI --start "2026-05-14" --end "2026-05-14" --format json
  wxview timeline --kind chatroom --query AI --start "2026-05-14 00:00:00" --end "2026-05-14 23:59:59" --limit 200 --cursor TOKEN --format json
  wxview timeline --username wxid_xxx --date yesterday --format jsonl

Runtime behavior:
  This command selects conversations from the local contact cache. When the
  derived message index is ready, near-realtime, and covers every selected
  conversation, it uses the index to select global timeline row refs and then
  reads full rows from the original decrypted caches. If the index is missing,
  building, interrupted, too stale, incomplete, or unusable, it falls back to the
  old fan-out scan.
  Prefer small time windows and narrow selectors such as --kind chatroom
  --query AI.
  JSON output includes meta.schema_version and meta.timezone. It also includes
  meta.next_args so AI/tool callers can continue paging without understanding
  the cursor internals.`)
}

func favoritesUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview favorites - List WeChat favorites from the local cache

Usage:
  wxview favorites --format table|json|jsonl|csv [flags]
  wxview favorites --help

Flags:
  --format table   Human-readable table output.
  --format json    JSON object with count and items.
  --format jsonl   Newline-delimited JSON, one favorite per line.
  --format csv     CSV with header row.
  --type TYPE      Filter by text, image, voice, video, article, location,
                   file, chat_history, note, card, or video_channel.
  --query TEXT     Search favorite XML/content text.
  --limit N        Return at most N rows. Default 20. 0 means no limit.
  --offset N       Skip N rows.
  --refresh        Refresh favorite/favorite.db cache before querying.

Output fields:
  JSON items include id, type, type_code, time, timestamp, summary, content,
  url, content_detail, content_items, from_username, and source_chat_username.
  content is the readable extracted body when available. content_detail and
  content_items include safe carrier metadata such as file format, size, local
  source path when present, ordinary http(s) URL when available, WeChat CDN
  remote_locator when only CDN metadata is available, md5, message UUID, and
  media_status. CDN keys are not copied to output.

Examples:
  wxview favorites --type article --limit 20 --format json
  wxview favorites --query "AI" --format table
  wxview favorites --refresh --format jsonl`)
}

func articlesUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview articles - List official-account articles and appmsg posts

Usage:
  wxview articles --list-accounts --format json [flags]
  wxview articles --username USERNAME --format table|json|jsonl|csv [flags]
  wxview articles --query TEXT --format json [flags]
  wxview articles --help

Selection:
  --list-accounts  List followed official accounts from contact/contact.db.
  --username TEXT  Exact official-account username.
  --query TEXT     Filter official-account username, alias, remark, nick_name,
                   or description. Without --username, matching accounts are
                   scanned for appmsg posts.

Time range:
  --date today|yesterday|YYYY-MM-DD
                   Select one full local day. Cannot be combined with --start
                   or --end.
  --start TIME     Inclusive start time.
  --end TIME       Inclusive end time.

Flags:
  --format table   Human-readable table output.
  --format json    JSON object with count and items.
  --format jsonl   Newline-delimited JSON, one article per line.
  --format csv     CSV with header row.
  --limit N        Return at most N rows after sorting newest first. Default 20.
  --offset N       Skip N rows.
  --refresh        Refresh contact and message/biz_message caches before querying.

Examples:
  wxview articles --list-accounts --format json
  wxview articles --username gh_xxx --limit 20 --format json
  wxview articles --query "AI" --date today --format table`)
}

func snsUsage(w io.Writer) {
	fmt.Fprintln(w, `wxview sns - Read local WeChat Moments data from sns/sns.db

Usage:
  wxview sns feed --format table|json|jsonl|csv [flags]
  wxview sns search KEYWORD --format json [flags]
  wxview sns notifications --format table|json|jsonl|csv [flags]
  wxview sns --help

Subcommands:
  feed           List local Moments posts cached by WeChat.
  search         Search Moments post content.
  notifications List like/comment notifications. Defaults to unread only.

Common flags:
  --format table|json|jsonl|csv
  --username TEXT  Filter feed/search by author username.
  --date today|yesterday|YYYY-MM-DD
  --start TIME
  --end TIME
  --limit N        Default 20. 0 means no limit.
  --offset N
  --refresh        Refresh sns/sns.db cache before querying.

notifications flags:
  --include-read   Include already-read notifications.

Output notes:
  SNS media output includes URLs, thumbnails, md5, size, and duration metadata
  when present. It intentionally does not expose CDN key/token fields.

Examples:
  wxview sns feed --date today --limit 20 --format json
  wxview sns search "AI" --start "2026-05-01" --format table
  wxview sns notifications --include-read --limit 20 --format json`)
}

func hasHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			return true
		}
	}
	return false
}

func runMe(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		meUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("me", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table or json")
	detail := fs.String("detail", "", "paths")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected me argument: %s", fs.Arg(0))
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("invalid format %q: use table or json", *format)
	}
	if *detail != "" && *detail != "paths" {
		return fmt.Errorf("invalid detail %q: use paths", *detail)
	}
	info, err := identity.ResolveCurrent(ctx, *detail == "paths")
	if err != nil {
		return err
	}
	return writeIdentity(stdout, info, *format)
}

func runAPI(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		apiUsage(stdout)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		apiUsage(stdout)
		return nil
	case "start":
		return runAPIStart(ctx, args[1:], stdout)
	case "status":
		return runAPIStatus(ctx, args[1:], stdout)
	case "stop":
		return runAPIStop(ctx, args[1:], stdout)
	case "serve":
		return runAPIServe(ctx, args[1:], stdout)
	default:
		apiUsage(stdout)
		return fmt.Errorf("unknown api command: %s", args[0])
	}
}

func runAPIStart(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		apiStartUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("api start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", webapi.DefaultAddr, "loopback listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected api start argument: %s", fs.Arg(0))
	}
	if err := webapi.ValidateLoopbackAddr(*addr); err != nil {
		return err
	}
	if err := ensureNoAPIAlreadyRunning(ctx, *addr); err != nil {
		return err
	}

	logPath, err := apiLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	if err := app.ChownForSudo(logPath); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "api", "serve", "--addr", *addr)
	cmd.Env = append(os.Environ(), apiManagedChildEnv+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	state := apiState{
		Addr:      *addr,
		PID:       cmd.Process.Pid,
		LogPath:   logPath,
		StartedAt: time.Now().Format(time.RFC3339),
		Version:   buildinfo.String(),
		Mode:      "background",
	}
	if err := saveAPIState(state); err != nil {
		_ = terminateProcess(cmd.Process.Pid)
		return err
	}
	fmt.Fprintf(stdout, "api addr: http://%s\n", *addr)
	fmt.Fprintf(stdout, "log: %s\n", logPath)
	fmt.Fprintf(stdout, "pid: %d\n", cmd.Process.Pid)

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	if ok, err := waitForAPIHealthy(ctx, *addr, apiStartWait, exitCh); err != nil {
		_ = removeAPIState()
		return err
	} else if ok {
		fmt.Fprintln(stdout, "status: running")
		return nil
	}
	fmt.Fprintln(stdout, "status: starting")
	fmt.Fprintln(stdout, "note: api has not responded to health yet; check status or the log file.")
	return nil
}

func runAPIStatus(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		apiStatusUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("api status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected api status argument: %s", fs.Arg(0))
	}
	state, ok, err := loadAPIState()
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "version: %s\n", buildinfo.String())
	if !ok {
		if health, healthy := apiHealth(ctx, webapi.DefaultAddr); healthy {
			fmt.Fprintf(stdout, "api addr: http://%s\n", webapi.DefaultAddr)
			fmt.Fprintf(stdout, "server_id: %s\n", health.ServerID)
			if me, ok := apiMe(ctx, webapi.DefaultAddr); ok {
				fmt.Fprintf(stdout, "account: %s\n", me.Account.Username)
				fmt.Fprintf(stdout, "display_name: %s\n", me.Account.DisplayName)
				fmt.Fprintf(stdout, "identity_source: %s\n", me.Account.IdentitySource)
			}
			fmt.Fprintln(stdout, "status: running")
			fmt.Fprintln(stdout, "mode: foreground")
			return nil
		}
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	if state.Addr == "" {
		state.Addr = webapi.DefaultAddr
	}
	fmt.Fprintf(stdout, "api addr: http://%s\n", state.Addr)
	fmt.Fprintf(stdout, "pid: %d\n", state.PID)
	if state.LogPath != "" {
		fmt.Fprintf(stdout, "log: %s\n", state.LogPath)
	}
	if state.Version != "" && state.Version != buildinfo.String() {
		fmt.Fprintf(stdout, "api_version: %s\n", state.Version)
	}
	if state.Mode != "" {
		fmt.Fprintf(stdout, "mode: %s\n", state.Mode)
	}
	if health, healthy := apiHealth(ctx, state.Addr); healthy {
		fmt.Fprintf(stdout, "server_id: %s\n", health.ServerID)
		if me, ok := apiMe(ctx, state.Addr); ok {
			fmt.Fprintf(stdout, "account: %s\n", me.Account.Username)
			fmt.Fprintf(stdout, "display_name: %s\n", me.Account.DisplayName)
			fmt.Fprintf(stdout, "identity_source: %s\n", me.Account.IdentitySource)
		}
		fmt.Fprintln(stdout, "status: running")
		return nil
	}
	if state.PID > 0 && processAlive(state.PID) {
		fmt.Fprintln(stdout, "status: starting")
		fmt.Fprintln(stdout, "reason: health check failed")
		return nil
	}
	_ = removeAPIState()
	fmt.Fprintln(stdout, "status: stopped")
	fmt.Fprintln(stdout, "reason: stale pid")
	return nil
}

func runAPIStop(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		apiStopUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("api stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected api stop argument: %s", fs.Arg(0))
	}
	state, ok, err := loadAPIState()
	if err != nil {
		return err
	}
	if !ok {
		if _, healthy := apiHealth(ctx, webapi.DefaultAddr); healthy {
			return fmt.Errorf("api is running at http://%s but no managed pid was found; stop the foreground process with Ctrl+C", webapi.DefaultAddr)
		}
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	if state.Addr == "" {
		state.Addr = webapi.DefaultAddr
	}
	fmt.Fprintf(stdout, "api addr: http://%s\n", state.Addr)
	fmt.Fprintf(stdout, "pid: %d\n", state.PID)
	if state.PID <= 0 || !processAlive(state.PID) {
		_ = removeAPIState()
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	if state.PID == os.Getpid() {
		return fmt.Errorf("refusing to stop current process from api state")
	}
	if err := terminateProcess(state.PID); err != nil {
		return err
	}
	if waitForProcessStopped(ctx, state.PID, apiStopWait) {
		_ = removeAPIState()
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	fmt.Fprintln(stdout, "status: stopping")
	return nil
}

func runAPIServe(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		apiServeUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("api serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", webapi.DefaultAddr, "loopback listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected api serve argument: %s", fs.Arg(0))
	}
	if err := webapi.ValidateLoopbackAddr(*addr); err != nil {
		return err
	}
	managedChild := os.Getenv(apiManagedChildEnv) == "1"
	if !managedChild {
		if err := ensureNoAPIAlreadyRunning(ctx, *addr); err != nil {
			return err
		}
		if err := saveAPIState(apiState{
			Addr:      *addr,
			PID:       os.Getpid(),
			StartedAt: time.Now().Format(time.RFC3339),
			Version:   buildinfo.String(),
			Mode:      "foreground",
		}); err != nil {
			return err
		}
	}
	defer removeAPIStateForPID(os.Getpid())
	ctx, stop := signal.NotifyContext(ctx, daemonSignals()...)
	defer stop()
	fmt.Fprintf(stdout, "api addr: http://%s\n", *addr)
	fmt.Fprintln(stdout, "api mode: local")
	return webapi.Serve(ctx, webapi.Options{
		Addr:    *addr,
		Version: buildinfo.String(),
	})
}

func ensureNoAPIAlreadyRunning(ctx context.Context, addr string) error {
	state, ok, err := loadAPIState()
	if err != nil {
		return err
	}
	if ok && state.PID > 0 && processAlive(state.PID) {
		if strings.TrimSpace(state.Addr) == "" {
			state.Addr = webapi.DefaultAddr
		}
		return fmt.Errorf("api is already running at http://%s (pid %d)", state.Addr, state.PID)
	}
	if ok {
		_ = removeAPIState()
	}
	if addr != webapi.DefaultAddr {
		if _, healthy := apiHealth(ctx, webapi.DefaultAddr); healthy {
			return fmt.Errorf("api is already running at http://%s", webapi.DefaultAddr)
		}
	}
	if _, healthy := apiHealth(ctx, addr); healthy {
		return fmt.Errorf("api is already running at http://%s", addr)
	}
	return nil
}

func apiStatePath() (string, error) {
	base, err := app.EnsureBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "api.json"), nil
}

func apiLogPath() (string, error) {
	base, err := app.EnsureBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "api.log"), nil
}

func loadAPIState() (apiState, bool, error) {
	path, err := apiStatePath()
	if err != nil {
		return apiState{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return apiState{}, false, nil
		}
		return apiState{}, false, err
	}
	var state apiState
	if err := json.Unmarshal(data, &state); err != nil {
		return apiState{}, false, fmt.Errorf("parse api state: %w", err)
	}
	if state.Addr == "" {
		state.Addr = webapi.DefaultAddr
	}
	return state, true, nil
}

func saveAPIState(state apiState) error {
	path, err := apiStatePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return app.ChownForSudo(path)
}

func removeAPIState() error {
	path, err := apiStatePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeAPIStateForPID(pid int) {
	state, ok, err := loadAPIState()
	if err != nil || !ok || state.PID != pid {
		return
	}
	_ = removeAPIState()
}

func apiHealth(ctx context.Context, addr string) (webapi.HealthResponse, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		return webapi.HealthResponse{}, false
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return webapi.HealthResponse{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return webapi.HealthResponse{}, false
	}
	var health webapi.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return webapi.HealthResponse{}, false
	}
	return health, health.OK
}

func apiMe(ctx context.Context, addr string) (webapi.MeResponse, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/v1/me", nil)
	if err != nil {
		return webapi.MeResponse{}, false
	}
	client := http.Client{Timeout: time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return webapi.MeResponse{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return webapi.MeResponse{}, false
	}
	var me webapi.MeResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		return webapi.MeResponse{}, false
	}
	return me, true
}

func runInit(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		initUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	verbose := fs.Bool("verbose", false, "print per-database key fingerprints and statuses")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected init argument: %s", fs.Arg(0))
	}
	ensureResult, err := key.EnsureSupportedKeys(ctx)
	if err != nil {
		return err
	}
	results := ensureResult.Keys
	if len(results) == 0 {
		return fmt.Errorf("no supported database found")
	}
	return writeInitOutput(stdout, results, ensureResult.Warnings, *verbose)
}

func writeInitOutput(stdout io.Writer, results []key.EnsureResult, warnings []key.EnsureWarning, verbose bool) error {
	fmt.Fprintf(stdout, "account: %s\n", results[0].Target.Account)
	fmt.Fprintf(stdout, "data_dir: %s\n", results[0].Target.DataDir)
	fmt.Fprintf(stdout, "keys_total: %d\n", len(results))
	scanned := 0
	reused := 0
	for _, res := range results {
		if res.Reused {
			reused++
		} else {
			scanned++
		}
	}
	fmt.Fprintf(stdout, "keys_scanned: %d\n", scanned)
	fmt.Fprintf(stdout, "keys_reused: %d\n", reused)
	if verbose {
		fmt.Fprintln(stdout, "keys:")
		for _, res := range results {
			status := "scanned"
			if res.Reused {
				status = "reused"
			}
			fmt.Fprintf(stdout, "  %s fingerprint=%s status=%s\n", res.Target.DBRelPath, res.Entry.Fingerprint, status)
		}
	}
	if len(warnings) > 0 {
		fmt.Fprintf(stdout, "warnings_total: %d\n", len(warnings))
		fmt.Fprintln(stdout, "warnings:")
		for _, warning := range warnings {
			fmt.Fprintf(stdout, "  %s %s\n", warning.DBRelPath, warning.Message)
		}
	}
	return nil
}

func runDaemon(args []string, stdout io.Writer) error {
	if os.Getenv(daemonForegroundEnv) == "1" && len(args) == 0 {
		return runDaemonForeground(stdout)
	}
	if len(args) == 0 || hasHelp(args) {
		daemonUsage(stdout)
		return nil
	}
	switch args[0] {
	case "start":
		return runDaemonStart(args[1:], stdout)
	case "stop":
		return runDaemonStop(args[1:], stdout)
	case "status":
		return runDaemonStatus(args[1:], stdout)
	default:
		return fmt.Errorf("unknown daemon command: %s; supported forms are %s", args[0], daemonSupportedForms)
	}
}

func runDaemonForeground(stdout io.Writer) error {
	socketPath, err := app.SocketPath()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), daemonSignals()...)
	defer stop()

	fmt.Fprintf(stdout, "daemon socket: %s\n", socketPath)
	fmt.Fprintln(stdout, "starting cache maintenance...")
	server := &daemon.Server{SocketPath: socketPath, Shutdown: stop}
	if err := server.Run(ctx); err != nil {
		return err
	}
	return nil
}

func runDaemonStart(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected daemon start argument: %s", fs.Arg(0))
	}
	socketPath, err := app.SocketPath()
	if err != nil {
		return err
	}
	client := daemon.Client{SocketPath: socketPath, Timeout: 500 * time.Millisecond}
	fmt.Fprintf(stdout, "daemon socket: %s\n", socketPath)
	if client.Healthy(context.Background()) {
		fmt.Fprintln(stdout, "status: already running")
		return nil
	}

	logPath, err := app.LogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	if err := app.ChownForSudo(logPath); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Env = append(os.Environ(), daemonForegroundEnv+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "log: %s\n", logPath)
	fmt.Fprintf(stdout, "pid: %d\n", cmd.Process.Pid)

	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	if ok, err := waitForDaemonHealthy(context.Background(), client, daemonStartWait, exitCh); err != nil {
		return err
	} else if ok {
		fmt.Fprintln(stdout, "status: running")
		return nil
	}
	fmt.Fprintln(stdout, "status: starting")
	fmt.Fprintln(stdout, "note: daemon has not responded to health yet; check status or the log file.")
	return nil
}

func runDaemonStop(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected daemon stop argument: %s", fs.Arg(0))
	}
	socketPath, err := app.SocketPath()
	if err != nil {
		return err
	}
	client := daemon.Client{SocketPath: socketPath, Timeout: daemonStopTimeout}
	fmt.Fprintf(stdout, "daemon socket: %s\n", socketPath)
	if !client.Healthy(context.Background()) {
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	if _, err := client.Call(context.Background(), daemon.ActionStop); err != nil {
		return err
	}
	if waitForDaemonStopped(context.Background(), client, daemonStopWait) {
		fmt.Fprintln(stdout, "status: stopped")
		return nil
	}
	fmt.Fprintln(stdout, "status: stopping")
	return nil
}

func runDaemonStatus(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected daemon status argument: %s", fs.Arg(0))
	}
	socketPath, err := app.SocketPath()
	if err != nil {
		return err
	}
	client := daemon.Client{SocketPath: socketPath, Timeout: 500 * time.Millisecond}
	fmt.Fprintf(stdout, "daemon socket: %s\n", socketPath)
	fmt.Fprintf(stdout, "version: %s\n", buildinfo.String())
	if resp, err := client.Call(context.Background(), daemon.ActionHealth); err == nil && resp.OK {
		if strings.TrimSpace(resp.Version) == "" {
			fmt.Fprintln(stdout, "daemon_version: unknown")
		} else if resp.Version != buildinfo.String() {
			fmt.Fprintf(stdout, "daemon_version: %s\n", resp.Version)
		}
		fmt.Fprintln(stdout, "status: running")
		return nil
	}
	fmt.Fprintln(stdout, "status: stopped")
	return nil
}

func runCache(args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		cacheUsage(stdout)
		return nil
	}
	switch args[0] {
	case "status":
		return runCacheStatus(args[1:], stdout)
	case "clean-tmp":
		return runCacheCleanTmp(args[1:], stdout)
	default:
		return fmt.Errorf("unknown cache command: %s; supported forms are `wxview cache status` and `wxview cache clean-tmp`", args[0])
	}
}

func runCacheStatus(args []string, stdout io.Writer) error {
	if hasHelp(args) {
		cacheUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("cache status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	group := fs.String("group", key.CacheGroupAll, "all, contacts, messages, media, sessions, avatars, favorites, or sns")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected cache status argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if _, err := key.NormalizeCacheStatusGroup(*group); err != nil {
		return err
	}
	items, err := key.CacheStatuses(*group)
	if err != nil {
		return err
	}
	return writeCacheStatuses(stdout, items, *format)
}

func runCacheCleanTmp(args []string, stdout io.Writer) error {
	if hasHelp(args) {
		cacheUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("cache clean-tmp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected cache clean-tmp argument: %s", fs.Arg(0))
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("invalid format %q: use table or json", *format)
	}
	result, err := key.CleanTmp()
	if err != nil {
		return err
	}
	return writeCleanTmpResult(stdout, result.Path, result.Removed, result.RemovedBytes, result.Kept, *format)
}

func runIndex(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		indexUsage(stdout)
		return nil
	}
	switch args[0] {
	case "status":
		return runIndexStatus(ctx, args[1:], stdout)
	case "refresh":
		return runIndexRefresh(ctx, args[1:], stdout)
	case "rebuild":
		return runIndexRebuild(ctx, args[1:], stdout)
	case "clean-tmp":
		return runIndexCleanTmp(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown index command: %s; supported forms are `wxview index status`, `wxview index refresh`, `wxview index rebuild --reset`, and `wxview index clean-tmp`", args[0])
	}
}

func runIndexStatus(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		indexUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("index status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected index status argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	localTarget, ok, err := localIndexStatusTarget()
	if err != nil {
		return err
	}
	if ok {
		cachePaths, err := localMessageCachePathsReadOnly(localTarget.account)
		if err != nil {
			return err
		}
		status, err := msgindex.StatusFor(ctx, localTarget.indexPath, cachePaths)
		if err != nil {
			return err
		}
		return writeIndexStatus(stdout, status, *format)
	}
	target, err := key.DiscoverContactDB()
	if err != nil {
		return err
	}
	indexPath, err := msgindex.LocalPath(target.Account)
	if err != nil {
		return err
	}
	cachePaths, err := messageCachePathsReadOnly(target.Account)
	if err != nil {
		return err
	}
	status, err := msgindex.StatusFor(ctx, indexPath, cachePaths)
	if err != nil {
		return err
	}
	return writeIndexStatus(stdout, status, *format)
}

type indexStatusTarget struct {
	account   string
	indexPath string
	modTime   time.Time
}

func localIndexStatusTarget() (indexStatusTarget, bool, error) {
	base, err := app.BaseDir()
	if err != nil {
		return indexStatusTarget{}, false, err
	}
	cacheRoot := filepath.Join(base, "cache")
	if target, ok, err := localIndexStatusTargetFromGlob(filepath.Join(cacheRoot, "*", "index", "messages.db")); err != nil || ok {
		return target, ok, err
	}
	contacts, err := filepath.Glob(filepath.Join(cacheRoot, "*", "contact", "contact.db"))
	if err != nil {
		return indexStatusTarget{}, false, err
	}
	targets := make([]indexStatusTarget, 0, len(contacts))
	for _, contactPath := range contacts {
		info, err := os.Stat(contactPath)
		if err != nil || info.IsDir() {
			continue
		}
		accountDir := filepath.Dir(filepath.Dir(contactPath))
		account := filepath.Base(accountDir)
		targets = append(targets, indexStatusTarget{
			account:   account,
			indexPath: filepath.Join(accountDir, "index", "messages.db"),
			modTime:   info.ModTime(),
		})
	}
	if len(targets) == 0 {
		return indexStatusTarget{}, false, nil
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if !targets[i].modTime.Equal(targets[j].modTime) {
			return targets[i].modTime.After(targets[j].modTime)
		}
		return targets[i].account < targets[j].account
	})
	return targets[0], true, nil
}

func localIndexStatusTargetFromGlob(pattern string) (indexStatusTarget, bool, error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return indexStatusTarget{}, false, err
	}
	targets := make([]indexStatusTarget, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		accountDir := filepath.Dir(filepath.Dir(path))
		targets = append(targets, indexStatusTarget{
			account:   filepath.Base(accountDir),
			indexPath: path,
			modTime:   info.ModTime(),
		})
	}
	if len(targets) == 0 {
		return indexStatusTarget{}, false, nil
	}
	sort.SliceStable(targets, func(i, j int) bool {
		if !targets[i].modTime.Equal(targets[j].modTime) {
			return targets[i].modTime.After(targets[j].modTime)
		}
		return targets[i].account < targets[j].account
	})
	return targets[0], true, nil
}

func runIndexRefresh(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		indexUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("index refresh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected index refresh argument: %s", fs.Arg(0))
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("invalid format %q: use table or json", *format)
	}
	result, err := buildMessageIndex(ctx, true, false)
	if err != nil {
		return err
	}
	return writeIndexBuildResult(stdout, result, *format)
}

func runIndexRebuild(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		indexUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("index rebuild", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table or json")
	reset := fs.Bool("reset", false, "delete the current index and rebuild from zero")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected index rebuild argument: %s", fs.Arg(0))
	}
	if !*reset {
		return fmt.Errorf("wxview index rebuild requires --reset")
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("invalid format %q: use table or json", *format)
	}
	result, err := buildMessageIndex(ctx, true, true)
	if err != nil {
		return err
	}
	return writeIndexBuildResult(stdout, result, *format)
}

func runIndexCleanTmp(ctx context.Context, args []string, stdout io.Writer) error {
	if hasHelp(args) {
		indexUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("index clean-tmp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table or json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected index clean-tmp argument: %s", fs.Arg(0))
	}
	if *format != "table" && *format != "json" {
		return fmt.Errorf("invalid format %q: use table or json", *format)
	}
	target, err := key.DiscoverContactDB()
	if err != nil {
		return err
	}
	indexPath, err := msgindex.LocalPath(target.Account)
	if err != nil {
		return err
	}
	result, err := msgindex.CleanIndexTmpResult(indexPath)
	if err != nil {
		return err
	}
	return writeCleanTmpResult(stdout, result.Path, result.Removed, result.RemovedBytes, result.Kept, *format)
}

func waitForDaemonHealthy(ctx context.Context, client daemon.Client, timeout time.Duration, exitCh <-chan error) (bool, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(daemonStatusPollEvery)
	defer ticker.Stop()
	for {
		if client.Healthy(ctx) {
			return true, nil
		}
		select {
		case err := <-exitCh:
			if err != nil {
				return false, fmt.Errorf("daemon exited before becoming healthy: %w", err)
			}
			return false, fmt.Errorf("daemon exited before becoming healthy")
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func waitForDaemonStopped(ctx context.Context, client daemon.Client, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(daemonStatusPollEvery)
	defer ticker.Stop()
	for {
		if !client.Healthy(ctx) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		case <-ctx.Done():
			return false
		}
	}
}

func waitForAPIHealthy(ctx context.Context, addr string, timeout time.Duration, exitCh <-chan error) (bool, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(apiStatusPollEvery)
	defer ticker.Stop()
	for {
		if _, ok := apiHealth(ctx, addr); ok {
			return true, nil
		}
		select {
		case err := <-exitCh:
			if err != nil {
				return false, fmt.Errorf("api exited before becoming healthy: %w", err)
			}
			return false, fmt.Errorf("api exited before becoming healthy")
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func waitForProcessStopped(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(apiStatusPollEvery)
	defer ticker.Stop()
	for {
		if !processAlive(pid) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		case <-ctx.Done():
			return false
		}
	}
}

func runContacts(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		contactsUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("contacts", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	kind := fs.String("kind", contacts.KindAll, "all, friend, chatroom, or other")
	query := fs.String("query", "", "contains search over username, alias, remark, and nick_name")
	username := fs.String("username", "", "exact username lookup")
	sortBy := fs.String("sort", "username", "username or name")
	limit := fs.Int("limit", 0, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip before returning results")
	countOnly := fs.Bool("count", false, "output only count after filters")
	detail := fs.Bool("detail", false, "return rich detail for --username")
	refresh := fs.Bool("refresh", false, "refresh decrypted contact cache before listing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected contacts argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if !validKind(*kind) {
		return fmt.Errorf("invalid kind %q: use all, friend, chatroom, or other", *kind)
	}
	if !validSort(*sortBy) {
		return fmt.Errorf("invalid sort %q: use username or name", *sortBy)
	}
	if *limit < 0 {
		return fmt.Errorf("invalid limit %d: must be >= 0", *limit)
	}
	if *offset < 0 {
		return fmt.Errorf("invalid offset %d: must be >= 0", *offset)
	}

	rt := wxquery.NewRuntime()
	if *detail {
		if strings.TrimSpace(*username) == "" {
			return fmt.Errorf("--detail requires --username")
		}
		result, err := rt.ContactDetail(ctx, wxquery.ContactDetailRequest{
			Username:      *username,
			ResolveAvatar: true,
			CacheMode:     wxquery.CacheMode{Refresh: *refresh},
		})
		if err != nil {
			return err
		}
		return writeContactDetail(stdout, result.Item, *format)
	}
	req := wxquery.ContactsRequest{
		Kind:     *kind,
		Query:    *query,
		Username: *username,
		Sort:     *sortBy,
		Limit:    *limit,
		Offset:   *offset,
	}
	if *countOnly {
		req.Limit = 0
		req.Offset = 0
	}
	req.CacheMode = wxquery.CacheMode{Refresh: *refresh}
	result, err := rt.Contacts(ctx, req)
	if err != nil {
		return err
	}
	if *countOnly {
		return writeCount(stdout, result.Total, *format)
	}
	return writeContacts(stdout, result.Items, *format)
}

func runMembers(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		membersUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("members", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	username := fs.String("username", "", "exact chatroom username")
	query := fs.String("query", "", "chatroom search query")
	refresh := fs.Bool("refresh", false, "refresh decrypted contact cache before listing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected members argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	usernameValue := strings.TrimSpace(*username)
	queryValue := strings.TrimSpace(*query)
	if usernameValue == "" && queryValue == "" {
		return fmt.Errorf("members requires --username or --query")
	}
	if usernameValue != "" && queryValue != "" {
		return fmt.Errorf("--username cannot be combined with --query")
	}
	result, err := wxquery.NewRuntime().Members(ctx, wxquery.MembersRequest{
		Username:  usernameValue,
		Query:     queryValue,
		CacheMode: wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeGroupMembers(stdout, result.Group, *format)
}

func runSessions(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		sessionsUsage(stdout)
		return nil
	}
	return runSessionList(ctx, args, stdout, false)
}

func runUnread(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		unreadUsage(stdout)
		return nil
	}
	return runSessionList(ctx, args, stdout, true)
}

func runSessionList(ctx context.Context, args []string, stdout io.Writer, unreadOnly bool) error {
	name := "sessions"
	if unreadOnly {
		name = "unread"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	kind := fs.String("kind", contacts.KindAll, "all, friend, chatroom, or other")
	query := fs.String("query", "", "search username, display name, sender, or summary")
	limit := fs.Int("limit", 20, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip")
	refresh := fs.Bool("refresh", false, "refresh decrypted session cache before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected %s argument: %s", name, fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if !validKind(*kind) {
		return fmt.Errorf("invalid kind %q: use all, friend, chatroom, or other", *kind)
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	result, err := wxquery.NewRuntime().Sessions(ctx, wxquery.SessionsRequest{
		Kind:       *kind,
		Query:      *query,
		UnreadOnly: unreadOnly,
		Limit:      *limit,
		Offset:     *offset,
		CacheMode:  wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeSessions(stdout, result.Items, *format)
}

func runNewMessages(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		newMessagesUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("new-messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	limit := fs.Int("limit", 100, "maximum messages to return; 0 means no limit")
	includeSource := fs.Bool("source", false, "include source DB/table/local row metadata")
	reset := fs.Bool("reset", false, "ignore and overwrite the saved checkpoint")
	refresh := fs.Bool("refresh", false, "refresh session and message caches before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected new-messages argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if *limit < 0 {
		return fmt.Errorf("invalid limit %d: must be >= 0", *limit)
	}
	result, err := wxquery.NewRuntime().NewMessages(ctx, wxquery.NewMessagesRequest{
		Limit:         *limit,
		IncludeSource: *includeSource,
		Commit:        true,
		Reset:         *reset,
		CacheMode:     wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	if *format == "json" {
		meta := newMessagesMeta{
			SchemaVersion: messageEnvelopeSchemaVersion,
			Timezone:      wxquery.LocalTimezoneName(),
			Mode:          "new_messages",
			FirstRun:      result.FirstRun,
			Reset:         *reset,
			Limit:         *limit,
			Returned:      len(result.Items),
			ChangedChats:  result.ChangedChats,
			StatePath:     result.StatePath,
		}
		return writeMessageEnvelope(stdout, meta, result.Items)
	}
	return writeMessages(stdout, result.Items, *format, *includeSource)
}

func runMessages(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		messagesUsage(stdout)
		return nil
	}
	trace := newTimingTrace(flagProvided(args, "trace-time"), stderr, "messages")
	fs := flag.NewFlagSet("messages", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	username := fs.String("username", "", "exact WeChat username")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	afterSeq := fs.Int64("after-seq", 0, "return rows with seq greater than this value")
	limit := fs.Int("limit", 0, "maximum rows to return after sorting; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip after sorting")
	includeSource := fs.Bool("source", false, "include source DB/table/local row metadata")
	noIndex := fs.Bool("no-index", false, "force old message-cache scan path")
	refresh := fs.Bool("refresh", false, "refresh decrypted message caches before listing")
	traceTime := fs.Bool("trace-time", false, "write per-stage timing trace to stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}
	trace.Enabled = *traceTime
	trace.Mark("parse_args")
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected search argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if strings.TrimSpace(*username) == "" {
		return fmt.Errorf("--username is required")
	}
	if *limit < 0 {
		return fmt.Errorf("invalid limit %d: must be >= 0", *limit)
	}
	if *offset < 0 {
		return fmt.Errorf("invalid offset %d: must be >= 0", *offset)
	}
	if *afterSeq < 0 {
		return fmt.Errorf("invalid after-seq %d: must be >= 0", *afterSeq)
	}
	rangeValue, err := wxquery.ParseMessageRange(*dateText, *startText, *endText, time.Now())
	if err != nil {
		return err
	}
	trace.Mark("parse_range", traceBool("has_start", rangeValue.HasStart), traceBool("has_end", rangeValue.HasEnd))
	usernameValue := strings.TrimSpace(*username)
	result, err := wxquery.NewRuntime().Messages(ctx, wxquery.MessagesRequest{
		Username:      usernameValue,
		AfterSeq:      *afterSeq,
		Date:          *dateText,
		StartText:     *startText,
		EndText:       *endText,
		IncludeSource: *includeSource,
		Limit:         *limit,
		Offset:        *offset,
		NoIndex:       *noIndex,
		WantHasMore:   *format == "json",
		CacheMode:     wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	trace.Mark("query_rows", traceInt("rows", len(result.Items)), "query_mode="+result.Execution.QueryMode, "index_status="+result.Execution.IndexStatus, "index_reason="+result.Execution.IndexReason)
	if *format == "json" {
		meta := messagesMeta{
			SchemaVersion:      messageEnvelopeSchemaVersion,
			Timezone:           wxquery.LocalTimezoneName(),
			Mode:               "messages",
			queryExecutionMeta: result.Execution,
			Username:           usernameValue,
			Start:              wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart),
			End:                wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd),
			Limit:              *limit,
			Returned:           len(result.Items),
			HasMore:            result.HasMore,
		}
		if result.HasMore && result.NextAfterSeq > 0 {
			meta.NextAfterSeq = result.NextAfterSeq
			meta.NextArgs = buildMessagesNextArgs(usernameValue, result.Range.Start, result.Range.HasStart, result.Range.End, result.Range.HasEnd, meta.NextAfterSeq, *limit, *includeSource, *noIndex)
		}
		err := writeMessageEnvelope(stdout, meta, result.Items)
		trace.Mark("write_output", "format=json", traceInt("returned", len(result.Items)))
		trace.Mark("total")
		return err
	}
	err = writeMessages(stdout, result.Items, *format, *includeSource)
	trace.Mark("write_output", "format="+*format, traceInt("returned", len(result.Items)))
	trace.Mark("total")
	return err
}

func runSearch(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		searchUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	query := fs.String("query", "", "message-content keyword")
	kind := fs.String("kind", contacts.KindAll, "all, friend, chatroom, or other")
	chatQuery := fs.String("chat-query", "", "conversation metadata filter")
	username := fs.String("username", "", "exact WeChat username")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	limit := fs.Int("limit", 50, "maximum rows to return after sorting; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip after sorting")
	includeSource := fs.Bool("source", false, "include source DB/table/local row metadata")
	refresh := fs.Bool("refresh", false, "refresh contact and message caches before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if !validKind(*kind) {
		return fmt.Errorf("invalid kind %q: use all, friend, chatroom, or other", *kind)
	}
	if strings.TrimSpace(*query) == "" {
		return fmt.Errorf("--query is required")
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	usernameValue := strings.TrimSpace(*username)
	chatQueryValue := strings.TrimSpace(*chatQuery)
	if usernameValue != "" && chatQueryValue != "" {
		return fmt.Errorf("--username cannot be combined with --chat-query")
	}
	result, err := wxquery.NewRuntime().Search(ctx, wxquery.SearchRequest{
		Query:         strings.TrimSpace(*query),
		Kind:          *kind,
		ChatQuery:     chatQueryValue,
		Username:      usernameValue,
		Date:          *dateText,
		StartText:     *startText,
		EndText:       *endText,
		IncludeSource: *includeSource,
		Limit:         *limit,
		Offset:        *offset,
		CacheMode:     wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	if *format == "json" {
		meta := searchMeta{
			SchemaVersion:      messageEnvelopeSchemaVersion,
			Timezone:           wxquery.LocalTimezoneName(),
			Mode:               "search",
			queryExecutionMeta: result.Execution,
			Query:              strings.TrimSpace(*query),
			Kind:               *kind,
			ChatQuery:          chatQueryValue,
			Username:           usernameValue,
			Start:              wxquery.FormatMetaTime(result.Range.Start, result.Range.HasStart),
			End:                wxquery.FormatMetaTime(result.Range.End, result.Range.HasEnd),
			Limit:              *limit,
			Offset:             *offset,
			Returned:           len(result.Items),
			TotalMatches:       result.TotalMatches,
			MatchedChats:       result.MatchedChats,
		}
		return writeMessageEnvelope(stdout, meta, result.Items)
	}
	return writeMessages(stdout, result.Items, *format, *includeSource)
}

func runTimeline(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		timelineUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("timeline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	kind := fs.String("kind", contacts.KindAll, "all, friend, chatroom, or other")
	query := fs.String("query", "", "conversation contains search over username, alias, remark, and nick_name")
	username := fs.String("username", "", "exact WeChat username")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	limit := fs.Int("limit", 200, "maximum rows to return; max 1000")
	cursor := fs.String("cursor", "", "opaque cursor from meta.next_cursor")
	includeSource := fs.Bool("source", false, "include source DB/table/local row metadata")
	noIndex := fs.Bool("no-index", false, "force old message-cache scan path")
	refresh := fs.Bool("refresh", false, "refresh contact and message caches before listing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected timeline argument: %s", fs.Arg(0))
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if !validKind(*kind) {
		return fmt.Errorf("invalid kind %q: use all, friend, chatroom, or other", *kind)
	}
	if *limit <= 0 {
		return fmt.Errorf("invalid limit %d: must be > 0", *limit)
	}
	if *limit > 1000 {
		return fmt.Errorf("invalid limit %d: must be <= 1000", *limit)
	}
	usernameValue := strings.TrimSpace(*username)
	queryValue := strings.TrimSpace(*query)
	kindProvided := flagProvided(args, "kind")
	if usernameValue != "" && (kindProvided || queryValue != "") {
		return fmt.Errorf("--username cannot be combined with --kind or --query")
	}
	if usernameValue == "" && !kindProvided && queryValue == "" {
		return fmt.Errorf("timeline requires --username, --kind, or --query")
	}
	result, err := wxquery.NewRuntime().Timeline(ctx, wxquery.TimelineRequest{
		Username:      usernameValue,
		Kind:          *kind,
		KindProvided:  kindProvided,
		Query:         queryValue,
		Date:          *dateText,
		StartText:     *startText,
		EndText:       *endText,
		Limit:         *limit,
		Cursor:        *cursor,
		IncludeSource: *includeSource,
		NoIndex:       *noIndex,
		CacheMode:     wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	if *format == "json" {
		meta := timelineMeta{
			SchemaVersion:      messageEnvelopeSchemaVersion,
			Timezone:           wxquery.LocalTimezoneName(),
			Mode:               "timeline",
			queryExecutionMeta: result.Execution,
			Kind:               "",
			Query:              "",
			Username:           usernameValue,
			Start:              result.Range.StartLabel,
			End:                result.Range.EndLabel,
			Limit:              *limit,
			Returned:           len(result.Items),
			MatchedChats:       result.MatchedChats,
			HasMore:            result.HasMore,
		}
		if usernameValue == "" {
			meta.Kind = *kind
			meta.Query = queryValue
		}
		if result.HasMore {
			meta.NextCursor = result.NextCursor
			meta.NextArgs = buildTimelineNextArgs(usernameValue, *kind, queryValue, result.Range.StartLabel, result.Range.EndLabel, *limit, result.NextCursor, *includeSource, *noIndex)
		}
		return writeMessageEnvelope(stdout, meta, result.Items)
	}
	return writeMessages(stdout, result.Items, *format, *includeSource)
}

func runFavorites(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		favoritesUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("favorites", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	favType := fs.String("type", "", "text, image, article, card, or video")
	query := fs.String("query", "", "contains search over favorite content")
	limit := fs.Int("limit", 20, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip before returning results")
	refresh := fs.Bool("refresh", false, "refresh decrypted favorite cache before listing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	result, err := wxquery.NewRuntime().Favorites(ctx, wxquery.FavoritesRequest{
		Type:      *favType,
		Query:     *query,
		Limit:     *limit,
		Offset:    *offset,
		CacheMode: wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeFavorites(stdout, result.Items, *format)
}

func runArticles(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		articlesUsage(stdout)
		return nil
	}
	fs := flag.NewFlagSet("articles", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	listAccounts := fs.Bool("list-accounts", false, "list official accounts")
	username := fs.String("username", "", "exact official-account username")
	query := fs.String("query", "", "account metadata search query")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	limit := fs.Int("limit", 20, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip before returning results")
	refresh := fs.Bool("refresh", false, "refresh contact and message caches before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	rt := wxquery.NewRuntime()
	if *listAccounts {
		result, err := rt.ArticleAccounts(ctx, wxquery.ArticleAccountsRequest{
			Query:     *query,
			CacheMode: wxquery.CacheMode{Refresh: *refresh},
		})
		if err != nil {
			return err
		}
		return writeArticleAccounts(stdout, result.Items, *format)
	}
	result, err := rt.Articles(ctx, wxquery.ArticlesRequest{
		Username:  strings.TrimSpace(*username),
		Query:     strings.TrimSpace(*query),
		Date:      *dateText,
		StartText: *startText,
		EndText:   *endText,
		Limit:     *limit,
		Offset:    *offset,
		CacheMode: wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeArticles(stdout, result.Items, *format)
}

func runSNS(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || hasHelp(args) {
		snsUsage(stdout)
		return nil
	}
	switch args[0] {
	case "feed":
		return runSNSFeed(ctx, args[1:], stdout, "")
	case "search":
		query := ""
		rest := args[1:]
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			query = rest[0]
			rest = rest[1:]
		}
		return runSNSFeed(ctx, rest, stdout, query)
	case "notifications":
		return runSNSNotifications(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown sns command: %s", args[0])
	}
}

func runSNSFeed(ctx context.Context, args []string, stdout io.Writer, positionalQuery string) error {
	fs := flag.NewFlagSet("sns feed", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	username := fs.String("username", "", "author username")
	query := fs.String("query", positionalQuery, "post content search query")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	limit := fs.Int("limit", 20, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip before returning results")
	refresh := fs.Bool("refresh", false, "refresh decrypted sns cache before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	result, err := wxquery.NewRuntime().SNSFeed(ctx, wxquery.SNSFeedRequest{
		Username:  strings.TrimSpace(*username),
		Query:     strings.TrimSpace(*query),
		Date:      *dateText,
		StartText: *startText,
		EndText:   *endText,
		Limit:     *limit,
		Offset:    *offset,
		CacheMode: wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeSNSPosts(stdout, result.Items, *format)
}

func runSNSNotifications(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sns notifications", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", "table", "table, json, jsonl, or csv")
	dateText := fs.String("date", "", "today, yesterday, or YYYY-MM-DD")
	startText := fs.String("start", "", "inclusive start time")
	endText := fs.String("end", "", "inclusive end time")
	limit := fs.Int("limit", 20, "maximum rows to return; 0 means no limit")
	offset := fs.Int("offset", 0, "rows to skip before returning results")
	includeRead := fs.Bool("include-read", false, "include already-read notifications")
	refresh := fs.Bool("refresh", false, "refresh decrypted sns cache before querying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !validFormat(*format) {
		return fmt.Errorf("invalid format %q: use table, json, jsonl, or csv", *format)
	}
	if *limit < 0 || *offset < 0 {
		return fmt.Errorf("limit and offset must be >= 0")
	}
	result, err := wxquery.NewRuntime().SNSNotifications(ctx, wxquery.SNSNotificationsRequest{
		Date:        *dateText,
		StartText:   *startText,
		EndText:     *endText,
		Limit:       *limit,
		Offset:      *offset,
		IncludeRead: *includeRead,
		CacheMode:   wxquery.CacheMode{Refresh: *refresh},
	})
	if err != nil {
		return err
	}
	return writeSNSNotifications(stdout, result.Items, *format)
}

type messagesMeta struct {
	SchemaVersion int    `json:"schema_version"`
	Timezone      string `json:"timezone"`
	Mode          string `json:"mode"`
	queryExecutionMeta
	Username     string   `json:"username"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	Limit        int      `json:"limit"`
	Returned     int      `json:"returned"`
	HasMore      bool     `json:"has_more"`
	NextAfterSeq int64    `json:"next_after_seq,omitempty"`
	NextArgs     []string `json:"next_args,omitempty"`
}

type timelineMeta struct {
	SchemaVersion int    `json:"schema_version"`
	Timezone      string `json:"timezone"`
	Mode          string `json:"mode"`
	queryExecutionMeta
	Kind         string   `json:"kind,omitempty"`
	Query        string   `json:"query,omitempty"`
	Username     string   `json:"username,omitempty"`
	Start        string   `json:"start"`
	End          string   `json:"end"`
	Limit        int      `json:"limit"`
	Returned     int      `json:"returned"`
	MatchedChats int      `json:"matched_chats"`
	HasMore      bool     `json:"has_more"`
	NextCursor   string   `json:"next_cursor,omitempty"`
	NextArgs     []string `json:"next_args,omitempty"`
}

type searchMeta struct {
	SchemaVersion int    `json:"schema_version"`
	Timezone      string `json:"timezone"`
	Mode          string `json:"mode"`
	queryExecutionMeta
	Query        string `json:"query"`
	Kind         string `json:"kind,omitempty"`
	ChatQuery    string `json:"chat_query,omitempty"`
	Username     string `json:"username,omitempty"`
	Start        string `json:"start"`
	End          string `json:"end"`
	Limit        int    `json:"limit"`
	Offset       int    `json:"offset"`
	Returned     int    `json:"returned"`
	TotalMatches int    `json:"total_matches"`
	MatchedChats int    `json:"matched_chats"`
}

type queryExecutionMeta = wxquery.ExecutionMeta

type newMessagesMeta struct {
	SchemaVersion int    `json:"schema_version"`
	Timezone      string `json:"timezone"`
	Mode          string `json:"mode"`
	FirstRun      bool   `json:"first_run"`
	Reset         bool   `json:"reset"`
	Limit         int    `json:"limit"`
	Returned      int    `json:"returned"`
	ChangedChats  int    `json:"changed_chats"`
	StatePath     string `json:"state_path"`
}

type messageEnvelope struct {
	Meta  any                `json:"meta"`
	Items []messages.Message `json:"items"`
}

func buildMessageIndex(ctx context.Context, refreshCaches bool, reset bool) (msgindex.BuildResult, error) {
	if refreshCaches {
		socketPath, err := app.SocketPath()
		if err != nil {
			return msgindex.BuildResult{}, err
		}
		client := daemon.Client{SocketPath: socketPath, Timeout: 10 * time.Minute}
		if client.Healthy(ctx) && !reset {
			if _, err := client.Call(ctx, daemon.ActionRefreshIndex); err != nil {
				if !isUnknownDaemonAction(err) {
					return msgindex.BuildResult{}, err
				}
			} else {
				target, err := key.DiscoverContactDB()
				if err != nil {
					return msgindex.BuildResult{}, err
				}
				indexPath, err := msgindex.LocalPath(target.Account)
				if err != nil {
					return msgindex.BuildResult{}, err
				}
				cachePaths, err := messageCachePathsReadOnly(target.Account)
				if err != nil {
					return msgindex.BuildResult{}, err
				}
				status, err := msgindex.StatusFor(ctx, indexPath, cachePaths)
				if err != nil {
					return msgindex.BuildResult{}, err
				}
				return msgindex.BuildResult{Status: status}, nil
			}
		}
		if _, err := wxquery.NewRuntime().CacheRefresh(ctx, wxquery.RefreshRequest{Scope: "messages"}); err != nil {
			return msgindex.BuildResult{}, err
		}
	}
	target, contactPath, ok := key.HasContactCache()
	if !ok {
		return msgindex.BuildResult{}, fmt.Errorf("contact cache does not exist: run `wxview init` or `wxview contacts --refresh --format json` first")
	}
	paths, allExist, err := key.MessageCachePaths()
	if err != nil {
		return msgindex.BuildResult{}, err
	}
	if !allExist {
		return msgindex.BuildResult{}, fmt.Errorf("message cache does not exist: run `wxview init` or `wxview messages --refresh --username USERNAME` first")
	}
	sessionPath := ""
	if _, path, err := wxquery.NewRuntime().SessionCacheTargetPath(ctx, wxquery.CacheMode{Refresh: refreshCaches}); err == nil {
		sessionPath = path
	}
	opts := msgindex.BuildOptions{
		Account:           target.Account,
		ContactCachePath:  contactPath,
		SessionCachePath:  sessionPath,
		MessageCachePaths: paths,
	}
	if reset {
		return msgindex.Build(ctx, opts)
	}
	return msgindex.Refresh(ctx, opts)
}

func messageCachePathsReadOnly(account string) ([]string, error) {
	targets, err := key.DiscoverMessageDBs()
	if err != nil {
		return nil, err
	}
	base, err := app.BaseDir()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		targetAccount := target.Account
		if targetAccount == "" {
			targetAccount = account
		}
		paths = append(paths, filepath.Join(base, "cache", app.SafeAccountDir(targetAccount), filepath.FromSlash(target.DBRelPath)))
	}
	return paths, nil
}

func localMessageCachePathsReadOnly(account string) ([]string, error) {
	base, err := app.BaseDir()
	if err != nil {
		return nil, err
	}
	pattern := filepath.Join(base, "cache", app.SafeAccountDir(account), "message", "message_*.db")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if _, ok := localMessageShardIndex(filepath.Base(path)); !ok {
			continue
		}
		paths = append(paths, path)
	}
	sort.SliceStable(paths, func(i, j int) bool {
		left, _ := localMessageShardIndex(filepath.Base(paths[i]))
		right, _ := localMessageShardIndex(filepath.Base(paths[j]))
		return left < right
	})
	return paths, nil
}

func localMessageShardIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "message_") || !strings.HasSuffix(name, ".db") {
		return 0, false
	}
	text := strings.TrimSuffix(strings.TrimPrefix(name, "message_"), ".db")
	index, err := strconv.Atoi(text)
	return index, err == nil
}

func isUnknownDaemonAction(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown daemon action")
}

func validFormat(format string) bool {
	switch format {
	case "table", "json", "jsonl", "csv":
		return true
	default:
		return false
	}
}

func validKind(kind string) bool {
	switch kind {
	case contacts.KindAll, contacts.KindFriend, contacts.KindChatroom, contacts.KindOther:
		return true
	default:
		return false
	}
}

func validSort(sortBy string) bool {
	switch sortBy {
	case "username", "name":
		return true
	default:
		return false
	}
}

func flagProvided(args []string, name string) bool {
	long := "--" + name
	for _, arg := range args {
		if arg == long || strings.HasPrefix(arg, long+"=") {
			return true
		}
	}
	return false
}

type timingTrace struct {
	Enabled bool
	command string
	w       io.Writer
	start   time.Time
	last    time.Time
}

func newTimingTrace(enabled bool, w io.Writer, command string) *timingTrace {
	if w == nil {
		w = io.Discard
	}
	now := time.Now()
	return &timingTrace{
		Enabled: enabled,
		command: command,
		w:       w,
		start:   now,
		last:    now,
	}
}

func (t *timingTrace) Mark(phase string, fields ...string) {
	if t == nil || !t.Enabled {
		return
	}
	now := time.Now()
	duration := now.Sub(t.last)
	total := now.Sub(t.start)
	t.last = now
	fmt.Fprintf(t.w, "trace_time command=%s phase=%s duration_ms=%.3f total_ms=%.3f", t.command, phase, float64(duration.Microseconds())/1000, float64(total.Microseconds())/1000)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		fmt.Fprintf(t.w, " %s", field)
	}
	fmt.Fprintln(t.w)
}

func traceInt(name string, value int) string {
	return name + "=" + strconv.Itoa(value)
}

func traceBool(name string, value bool) string {
	return name + "=" + strconv.FormatBool(value)
}

func buildMessagesNextArgs(username string, start int64, hasStart bool, end int64, hasEnd bool, nextAfterSeq int64, limit int, includeSource bool, noIndex bool) []string {
	args := []string{"messages", "--username", username}
	if hasStart {
		args = append(args, "--start", wxquery.FormatMetaTime(start, true))
	}
	if hasEnd {
		args = append(args, "--end", wxquery.FormatMetaTime(end, true))
	}
	args = append(args, "--after-seq", strconv.FormatInt(nextAfterSeq, 10))
	if limit > 0 {
		args = append(args, "--limit", strconv.Itoa(limit))
	}
	if includeSource {
		args = append(args, "--source")
	}
	if noIndex {
		args = append(args, "--no-index")
	}
	args = append(args, "--format", "json")
	return args
}

func buildTimelineNextArgs(username string, kind string, query string, start string, end string, limit int, cursor string, includeSource bool, noIndex bool) []string {
	args := []string{"timeline"}
	if username != "" {
		args = append(args, "--username", username)
	} else {
		args = append(args, "--kind", kind)
		if query != "" {
			args = append(args, "--query", query)
		}
	}
	args = append(args, "--start", start, "--end", end, "--limit", strconv.Itoa(limit), "--cursor", cursor)
	if includeSource {
		args = append(args, "--source")
	}
	if noIndex {
		args = append(args, "--no-index")
	}
	args = append(args, "--format", "json")
	return args
}

type apiState struct {
	Addr      string `json:"addr"`
	PID       int    `json:"pid"`
	LogPath   string `json:"log_path"`
	StartedAt string `json:"started_at"`
	Version   string `json:"version"`
	Mode      string `json:"mode,omitempty"`
}

var defaultContactFields = []string{"username", "alias", "remark", "nick_name", "head_url", "kind"}
var contactDetailFields = []string{"username", "alias", "remark", "nick_name", "head_url", "small_head_url", "big_head_url", "description", "verify_flag", "local_type", "kind", "is_chatroom", "is_official", "avatar_status", "avatar_path", "avatar_reason"}
var memberFields = []string{"username", "display_name", "alias", "remark", "nick_name", "kind", "is_owner"}
var sessionFields = []string{"username", "chat_kind", "chat_display_name", "unread_count", "summary", "last_timestamp", "time", "last_msg_type", "last_msg_sub_type", "last_sender", "last_sender_display_name"}
var defaultMessageFields = []string{"id", "chat_username", "chat_kind", "chat_display_name", "chat_alias", "chat_remark", "chat_nick_name", "from_username", "direction", "is_self", "is_chatroom", "seq", "server_id", "create_time", "time", "type", "sub_type", "content", "content_detail", "content_encoding"}
var sourceMessageFields = []string{"source_db", "source_table", "source_local_id", "source_raw_type", "source_status", "source_real_sender_id"}
var favoriteFields = []string{"id", "type", "type_code", "time", "timestamp", "summary", "content", "url", "content_detail", "from_username", "source_chat_username"}
var articleAccountFields = []string{"username", "alias", "remark", "nick_name", "description", "head_url"}
var articleFields = []string{"id", "account_username", "account_display_name", "time", "timestamp", "type", "text", "title", "desc", "url", "source_username", "source_display_name", "nickname", "message_id"}
var snsPostFields = []string{"id", "author_username", "time", "timestamp", "content", "location", "media_count"}
var snsNotificationFields = []string{"id", "type", "from_username", "from_nick_name", "content", "feed_id", "feed_author_username", "feed_preview", "time", "timestamp"}
var cacheStatusFields = []string{"group", "account", "db_rel_path", "status", "key_status", "source_size", "cache_size", "source_mtime", "cache_mtime", "refreshed_at", "reason"}
var indexStatusFields = []string{"status", "query_mode", "schema_version", "source_db_count", "indexed_rows", "source_rows", "covered_chats", "built_at", "refreshed_at", "job_state", "lag_seconds", "session_lag_seconds", "lag_policy", "reason", "refresh_mode", "active_chats_checked", "active_chats_refreshed", "reconcile_sources_pending", "reconcile_tables_checked", "path", "progress_percent", "progress_rows", "progress_total_rows", "checked_tables", "covered_tables", "progress_total_tables", "progress_current", "progress_updated_at"}
var indexBuildFields = append(append([]string{}, indexStatusFields...), "duration_ms")
var cleanTmpFields = []string{"path", "removed", "removed_bytes", "kept"}

func writeIdentity(w io.Writer, info identity.Info, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	case "table":
		return writeKeyValues(w, identityFields(info.Account), identityValues(info.Account))
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func identityFields(account identity.Account) []string {
	fields := []string{"account", "username", "display_name", "alias", "remark", "nick_name", "identity_source", "cache_dir", "data_dir_fingerprint"}
	if account.DataDir != "" {
		fields = append(fields, "data_dir")
	}
	return fields
}

func identityValues(account identity.Account) []string {
	values := []string{
		account.Account,
		account.Username,
		account.DisplayName,
		account.Alias,
		account.Remark,
		account.NickName,
		account.IdentitySource,
		account.CacheDir,
		account.DataDirFingerprint,
	}
	if account.DataDir != "" {
		values = append(values, account.DataDir)
	}
	return values
}

func writeContacts(w io.Writer, list []contacts.Contact, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	case "jsonl":
		enc := jsonEncoder(w)
		for _, contact := range list {
			if err := enc.Encode(contact); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(defaultContactFields); err != nil {
			return err
		}
		for _, contact := range list {
			if err := cw.Write(contactValues(contact)); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.ToUpper(strings.Join(defaultContactFields, "\t")))
		for _, contact := range list {
			values := contactValues(contact)
			for i := range values {
				values[i] = cleanCell(values[i])
			}
			fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeContactDetail(w io.Writer, item contacts.Detail, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(item)
	case "jsonl":
		return jsonEncoder(w).Encode(item)
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(contactDetailFields); err != nil {
			return err
		}
		if err := cw.Write(contactDetailValues(item)); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	case "table":
		return writeKeyValues(w, contactDetailFields, contactDetailValues(item))
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeGroupMembers(w io.Writer, result contacts.GroupMembers, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case "jsonl":
		enc := jsonEncoder(w)
		for _, member := range result.Members {
			if err := enc.Encode(member); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(memberFields); err != nil {
			return err
		}
		for _, member := range result.Members {
			if err := cw.Write(memberValues(member)); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "table":
		fmt.Fprintf(w, "group: %s\n", result.DisplayName)
		fmt.Fprintf(w, "username: %s\n", result.Username)
		fmt.Fprintf(w, "owner: %s\n", result.OwnerDisplayName)
		fmt.Fprintf(w, "count: %d\n", result.Count)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.ToUpper(strings.Join(memberFields, "\t")))
		for _, member := range result.Members {
			values := memberValues(member)
			for i := range values {
				values[i] = cleanCell(values[i])
			}
			fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeSessions(w io.Writer, list []sessions.Session, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, sessionFields, len(list), func(i int) []string { return sessionValues(list[i]) })
	case "table":
		return writeTable(w, sessionFields, len(list), func(i int) []string { return sessionValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeMessages(w io.Writer, list []messages.Message, format string, includeSource bool) error {
	fields := messageFields(includeSource)
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	case "jsonl":
		enc := jsonEncoder(w)
		for _, message := range list {
			if err := enc.Encode(message); err != nil {
				return err
			}
		}
		return nil
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write(fields); err != nil {
			return err
		}
		for _, message := range list {
			if err := cw.Write(messageValues(message, fields)); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, strings.ToUpper(strings.Join(fields, "\t")))
		for _, message := range list {
			values := messageValues(message, fields)
			for i := range values {
				values[i] = cleanCell(values[i])
			}
			fmt.Fprintln(tw, strings.Join(values, "\t"))
		}
		return tw.Flush()
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeMessageEnvelope(w io.Writer, meta any, list []messages.Message) error {
	enc := jsonEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(messageEnvelope{Meta: meta, Items: list})
}

func writeFavorites(w io.Writer, list []favorites.Item, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, favoriteFields, len(list), func(i int) []string { return favoriteValues(list[i]) })
	case "table":
		return writeTable(w, favoriteFields, len(list), func(i int) []string { return favoriteValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeArticleAccounts(w io.Writer, list []articles.Account, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, articleAccountFields, len(list), func(i int) []string { return articleAccountValues(list[i]) })
	case "table":
		return writeTable(w, articleAccountFields, len(list), func(i int) []string { return articleAccountValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeArticles(w io.Writer, list []articles.Article, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, articleFields, len(list), func(i int) []string { return articleValues(list[i]) })
	case "table":
		return writeTable(w, articleFields, len(list), func(i int) []string { return articleValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeSNSPosts(w io.Writer, list []sns.Post, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, snsPostFields, len(list), func(i int) []string { return snsPostValues(list[i]) })
	case "table":
		return writeTable(w, snsPostFields, len(list), func(i int) []string { return snsPostValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeSNSNotifications(w io.Writer, list []sns.Notification, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, snsNotificationFields, len(list), func(i int) []string { return snsNotificationValues(list[i]) })
	case "table":
		return writeTable(w, snsNotificationFields, len(list), func(i int) []string { return snsNotificationValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeCacheStatuses(w io.Writer, list []key.CacheStatusItem, format string) error {
	switch format {
	case "json":
		return writeItemsObject(w, list)
	case "jsonl":
		return writeJSONLines(w, list)
	case "csv":
		return writeRows(w, cacheStatusFields, len(list), func(i int) []string { return cacheStatusValues(list[i]) })
	case "table":
		return writeTable(w, cacheStatusFields, len(list), func(i int) []string { return cacheStatusValues(list[i]) })
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeIndexStatus(w io.Writer, status msgindex.Status, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	case "jsonl":
		return jsonEncoder(w).Encode(status)
	case "csv":
		return writeRows(w, indexStatusFields, 1, func(i int) []string { return indexStatusValues(status) })
	case "table":
		return writeKeyValues(w, indexStatusFields, indexStatusValues(status))
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeIndexBuildResult(w io.Writer, result msgindex.BuildResult, format string) error {
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	case "table":
		return writeKeyValues(w, indexBuildFields, indexBuildValues(result))
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeCleanTmpResult(w io.Writer, path string, removed int, removedBytes int64, kept int, format string) error {
	values := []string{path, strconv.Itoa(removed), strconv.FormatInt(removedBytes, 10), strconv.Itoa(kept)}
	switch format {
	case "json":
		enc := jsonEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"path":          path,
			"removed":       removed,
			"removed_bytes": removedBytes,
			"kept":          kept,
		})
	case "table":
		return writeKeyValues(w, cleanTmpFields, values)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func writeItemsObject[T any](w io.Writer, list []T) error {
	enc := jsonEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"count": len(list), "items": list})
}

func writeJSONLines[T any](w io.Writer, list []T) error {
	enc := jsonEncoder(w)
	for _, item := range list {
		if err := enc.Encode(item); err != nil {
			return err
		}
	}
	return nil
}

func writeRows(w io.Writer, fields []string, n int, valueAt func(int) []string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(fields); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		if err := cw.Write(valueAt(i)); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeTable(w io.Writer, fields []string, n int, valueAt func(int) []string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper(strings.Join(fields, "\t")))
	for i := 0; i < n; i++ {
		values := valueAt(i)
		for i := range values {
			values[i] = cleanCell(values[i])
		}
		fmt.Fprintln(tw, strings.Join(values, "\t"))
	}
	return tw.Flush()
}

func messageFields(includeSource bool) []string {
	fields := append([]string{}, defaultMessageFields...)
	if includeSource {
		fields = append(fields, sourceMessageFields...)
	}
	return fields
}

func writeCount(w io.Writer, count int, format string) error {
	switch format {
	case "json", "jsonl":
		return jsonEncoder(w).Encode(map[string]int{"count": count})
	case "csv":
		cw := csv.NewWriter(w)
		if err := cw.Write([]string{"count"}); err != nil {
			return err
		}
		if err := cw.Write([]string{fmt.Sprintf("%d", count)}); err != nil {
			return err
		}
		cw.Flush()
		return cw.Error()
	case "table":
		_, err := fmt.Fprintf(w, "%d\n", count)
		return err
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

func contactValues(contact contacts.Contact) []string {
	values := make([]string, 0, len(defaultContactFields))
	for _, field := range defaultContactFields {
		values = append(values, contactValue(contact, field))
	}
	return values
}

func contactDetailValues(item contacts.Detail) []string {
	values := make([]string, 0, len(contactDetailFields))
	for _, field := range contactDetailFields {
		switch field {
		case "username":
			values = append(values, item.Username)
		case "alias":
			values = append(values, item.Alias)
		case "remark":
			values = append(values, item.Remark)
		case "nick_name":
			values = append(values, item.NickName)
		case "head_url":
			values = append(values, item.HeadURL)
		case "small_head_url":
			values = append(values, item.SmallHeadURL)
		case "big_head_url":
			values = append(values, item.BigHeadURL)
		case "description":
			values = append(values, item.Description)
		case "verify_flag":
			values = append(values, strconv.Itoa(item.VerifyFlag))
		case "local_type":
			values = append(values, strconv.Itoa(item.LocalType))
		case "kind":
			values = append(values, item.Kind)
		case "is_chatroom":
			values = append(values, strconv.FormatBool(item.IsChatroom))
		case "is_official":
			values = append(values, strconv.FormatBool(item.IsOfficial))
		case "avatar_status":
			values = append(values, item.AvatarStatus)
		case "avatar_path":
			values = append(values, item.AvatarPath)
		case "avatar_reason":
			values = append(values, item.AvatarReason)
		default:
			values = append(values, "")
		}
	}
	return values
}

func memberValues(member contacts.Member) []string {
	values := make([]string, 0, len(memberFields))
	for _, field := range memberFields {
		switch field {
		case "username":
			values = append(values, member.Username)
		case "display_name":
			values = append(values, member.DisplayName)
		case "alias":
			values = append(values, member.Alias)
		case "remark":
			values = append(values, member.Remark)
		case "nick_name":
			values = append(values, member.NickName)
		case "kind":
			values = append(values, member.Kind)
		case "is_owner":
			values = append(values, strconv.FormatBool(member.IsOwner))
		default:
			values = append(values, "")
		}
	}
	return values
}

func sessionValues(item sessions.Session) []string {
	return []string{
		item.Username,
		item.ChatKind,
		item.ChatDisplayName,
		strconv.FormatInt(item.UnreadCount, 10),
		item.Summary,
		strconv.FormatInt(item.LastTimestamp, 10),
		item.Time,
		strconv.FormatInt(item.LastMsgType, 10),
		strconv.FormatInt(item.LastMsgSubType, 10),
		item.LastSender,
		item.LastSenderDisplayName,
	}
}

func favoriteValues(item favorites.Item) []string {
	return []string{
		strconv.FormatInt(item.ID, 10),
		item.Type,
		strconv.FormatInt(item.TypeCode, 10),
		item.Time,
		strconv.FormatInt(item.Timestamp, 10),
		item.Summary,
		item.Content,
		item.URL,
		favoriteDetailText(item),
		item.From,
		item.SourceChat,
	}
}

func favoriteDetailText(item favorites.Item) string {
	if item.ContentDetail == nil {
		return ""
	}
	if status := item.ContentDetail["media_status"]; status != "" {
		parts := compactCells(status, item.ContentDetail["format"], item.ContentDetail["size"], item.ContentDetail["path"], item.ContentDetail["url"], item.ContentDetail["remote_locator"])
		return strings.Join(parts, " | ")
	}
	return item.ContentDetail["type"]
}

func compactCells(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func articleAccountValues(item articles.Account) []string {
	return []string{item.Username, item.Alias, item.Remark, item.NickName, item.Description, item.HeadURL}
}

func articleValues(item articles.Article) []string {
	return []string{
		item.ID,
		item.AccountUsername,
		item.AccountDisplayName,
		item.Time,
		strconv.FormatInt(item.Timestamp, 10),
		item.Type,
		item.Text,
		item.Title,
		item.Desc,
		item.URL,
		item.SourceUsername,
		item.SourceDisplayName,
		item.Nickname,
		item.MessageID,
	}
}

func snsPostValues(item sns.Post) []string {
	return []string{
		strconv.FormatInt(item.ID, 10),
		item.AuthorUsername,
		item.Time,
		strconv.FormatInt(item.Timestamp, 10),
		item.Content,
		item.Location,
		strconv.Itoa(item.MediaCount),
	}
}

func snsNotificationValues(item sns.Notification) []string {
	return []string{
		strconv.FormatInt(item.ID, 10),
		item.Type,
		item.FromUsername,
		item.FromNickName,
		item.Content,
		strconv.FormatInt(item.FeedID, 10),
		item.FeedAuthorUsername,
		item.FeedPreview,
		item.Time,
		strconv.FormatInt(item.Timestamp, 10),
	}
}

func cacheStatusValues(item key.CacheStatusItem) []string {
	return []string{
		item.Group,
		item.Account,
		item.DBRelPath,
		item.Status,
		item.KeyStatus,
		strconv.FormatInt(item.SourceSize, 10),
		strconv.FormatInt(item.CacheSize, 10),
		item.SourceMTime,
		item.CacheMTime,
		item.RefreshedAt,
		item.Reason,
	}
}

func indexStatusValues(status msgindex.Status) []string {
	return []string{
		status.Status,
		status.QueryMode,
		strconv.Itoa(status.SchemaVersion),
		strconv.Itoa(status.SourceDBCount),
		strconv.FormatInt(status.IndexedRows, 10),
		strconv.FormatInt(status.SourceRows, 10),
		strconv.Itoa(status.CoveredChats),
		status.BuiltAt,
		status.RefreshedAt,
		status.JobState,
		strconv.FormatInt(status.LagSeconds, 10),
		strconv.FormatInt(status.SessionLagSeconds, 10),
		status.LagPolicy,
		status.Reason,
		status.RefreshMode,
		strconv.Itoa(status.ActiveChatsChecked),
		strconv.Itoa(status.ActiveChatsRefreshed),
		strconv.Itoa(status.ReconcileSourcesPending),
		strconv.Itoa(status.ReconcileTablesChecked),
		status.Path,
		fmt.Sprintf("%.2f", status.ProgressPercent),
		strconv.FormatInt(status.ProgressRows, 10),
		strconv.FormatInt(status.ProgressTotalRows, 10),
		strconv.Itoa(status.CheckedTables),
		strconv.Itoa(status.CoveredTables),
		strconv.Itoa(status.ProgressTotalTables),
		status.ProgressCurrent,
		status.ProgressUpdatedAt,
	}
}

func indexBuildValues(result msgindex.BuildResult) []string {
	values := indexStatusValues(result.Status)
	values = append(values, strconv.FormatInt(result.DurationMS, 10))
	return values
}

func writeKeyValues(w io.Writer, fields []string, values []string) error {
	for i, field := range fields {
		value := ""
		if i < len(values) {
			value = cleanCell(values[i])
		}
		if _, err := fmt.Fprintf(w, "%s: %s\n", field, value); err != nil {
			return err
		}
	}
	return nil
}

func contactValue(contact contacts.Contact, field string) string {
	switch field {
	case "username":
		return contact.Username
	case "alias":
		return contact.Alias
	case "remark":
		return contact.Remark
	case "nick_name":
		return contact.NickName
	case "head_url":
		return contact.HeadURL
	case "kind":
		return contact.Kind
	default:
		return ""
	}
}

func messageValues(message messages.Message, fields []string) []string {
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		values = append(values, messageValue(message, field))
	}
	return values
}

func messageValue(message messages.Message, field string) string {
	switch field {
	case "id":
		return message.ID
	case "chat_username":
		return message.ChatUsername
	case "chat_kind":
		return message.ChatKind
	case "chat_display_name":
		return message.ChatDisplayName
	case "chat_alias":
		return message.ChatAlias
	case "chat_remark":
		return message.ChatRemark
	case "chat_nick_name":
		return message.ChatNickName
	case "from_username":
		return message.FromUsername
	case "direction":
		return message.Direction
	case "is_self":
		return strconv.FormatBool(message.IsSelf)
	case "is_chatroom":
		return strconv.FormatBool(message.IsChatroom)
	case "seq":
		return fmt.Sprintf("%d", message.Seq)
	case "server_id":
		return fmt.Sprintf("%d", message.ServerID)
	case "create_time":
		return fmt.Sprintf("%d", message.CreateTime)
	case "time":
		return message.Time
	case "type":
		return fmt.Sprintf("%d", message.Type)
	case "sub_type":
		return fmt.Sprintf("%d", message.SubType)
	case "content":
		return message.Content
	case "content_detail":
		if message.ContentDetail == nil {
			return ""
		}
		if text := message.ContentDetail["text"]; text != "" {
			return text
		}
		return message.ContentDetail["type"]
	case "content_encoding":
		return message.ContentEncoding
	case "source_db":
		if message.Source != nil {
			return message.Source.DB
		}
		return message.SourceDB
	case "source_table":
		if message.Source != nil {
			return message.Source.Table
		}
		return message.TableName
	case "source_local_id":
		return fmt.Sprintf("%d", message.LocalID)
	case "source_raw_type":
		return fmt.Sprintf("%d", message.RawType)
	case "source_status":
		return fmt.Sprintf("%d", message.Status)
	case "source_real_sender_id":
		return fmt.Sprintf("%d", message.RealSenderID)
	default:
		return ""
	}
}

func jsonEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func cleanCell(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
