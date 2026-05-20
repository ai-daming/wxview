package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"wxview/internal/key"
	"wxview/internal/webapi"
)

func TestRootHelpIsDescriptive(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview - Read local WeChat data",
		"Machine-readable usage",
		"wxview init",
		"wxview me",
		"wxview api",
		"wxview cache",
		"wxview me --help",
		"wxview api --help",
		"wxview api start --help",
		"wxview api status --help",
		"wxview api stop --help",
		"wxview api serve --help",
		"wxview contacts --help",
		"wxview members --help",
		"wxview sessions --help",
		"wxview unread --help",
		"wxview new-messages --help",
		"wxview messages --help",
		"wxview search --help",
		"wxview timeline --help",
		"wxview favorites --help",
		"wxview articles --help",
		"wxview sns --help",
		"wxview cache --help",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("root help missing %q:\n%s", want, text)
		}
	}
}

func TestMeHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"me", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview me - Show the current WeChat account identity",
		"--format table|json",
		"--detail paths",
		"data_dir_fingerprint",
		"identity_source",
		"wxview me --format json",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("me help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"api", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview api - Run the loopback-only local WebAPI",
		"wxview api start",
		"wxview api status",
		"wxview api stop",
		"wxview api serve",
		"127.0.0.1:38765",
		"GET /v1/me",
		"GET /v1/contacts",
		"POST /v1/cache/refresh",
		"GET endpoints are",
		"does not reuse the daemon",
		"one API instance",
		"start or serve",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("api help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIStartHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"api", "start", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview api start - Start the loopback-only local WebAPI in the background",
		"--addr HOST:PORT",
		"one API instance",
		"start or serve",
		"~/.wxview/api.json",
		"~/.wxview/api.log",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("api start help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIStatusHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"api", "status", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview api status - Show the single managed WebAPI status",
		"wxview api status",
		"does not take --addr",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("api status help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIStopHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"api", "stop", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview api stop - Stop the single managed WebAPI instance",
		"wxview api stop",
		"does not take --addr",
		"Foreground wxview api serve",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("api stop help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIServeHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"api", "serve", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview api serve - Run the loopback-only local WebAPI in the foreground",
		"--addr HOST:PORT",
		"loopback",
		"serve registers itself",
		"GET /health",
		"GET /v1/messages",
		"POST /v1/new-messages/poll",
		"kind",
		"limit",
		"refresh=true",
		"which local WebAPI endpoint",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("api serve help missing %q:\n%s", want, text)
		}
	}
}

func TestAPIStartRejectsExistingManagedInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveAPIState(apiState{
		Addr:      webapi.DefaultAddr,
		PID:       os.Getpid(),
		LogPath:   "/tmp/wxview-api-test.log",
		StartedAt: "2026-05-20T00:00:00Z",
		Version:   "test",
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"api", "start"}, &out, &out)
	if err == nil {
		t.Fatal("expected api start to reject an existing managed instance")
	}
	if !strings.Contains(err.Error(), "api is already running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIServeRejectsExistingManagedInstance(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveAPIState(apiState{
		Addr:      webapi.DefaultAddr,
		PID:       os.Getpid(),
		LogPath:   "/tmp/wxview-api-test.log",
		StartedAt: "2026-05-20T00:00:00Z",
		Version:   "test",
	}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"api", "serve", "--addr", "127.0.0.1:58964"}, &out, &out)
	if err == nil {
		t.Fatal("expected api serve to reject an existing managed instance")
	}
	if !strings.Contains(err.Error(), "api is already running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInitOutputDefaultsToSummary(t *testing.T) {
	results := []key.EnsureResult{
		{
			Entry:  key.Entry{Fingerprint: "abc"},
			Target: key.TargetDB{Account: "wxid_a", DataDir: "/tmp/db_storage", DBRelPath: "contact/contact.db"},
			Reused: false,
		},
		{
			Entry:  key.Entry{Fingerprint: "def"},
			Target: key.TargetDB{Account: "wxid_a", DataDir: "/tmp/db_storage", DBRelPath: "message/message_0.db"},
			Reused: true,
		},
	}
	warnings := []key.EnsureWarning{{
		DBRelPath: "message/message_revoke.db",
		Message:   "auxiliary key missing; skipped. Retry later with `wxview init` and process-memory permissions.",
	}}

	var out bytes.Buffer
	if err := writeInitOutput(&out, results, warnings, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"account: wxid_a",
		"data_dir: /tmp/db_storage",
		"keys_total: 2",
		"keys_scanned: 1",
		"keys_reused: 1",
		"warnings_total: 1",
		"message/message_revoke.db auxiliary key missing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("init summary missing %q:\n%s", want, text)
		}
	}
	for _, notWant := range []string{
		"\nkeys:\n",
		"fingerprint=",
		"contact/contact.db fingerprint",
	} {
		if strings.Contains(text, notWant) {
			t.Fatalf("init summary should not contain %q:\n%s", notWant, text)
		}
	}
}

func TestInitOutputVerboseIncludesKeyDetails(t *testing.T) {
	results := []key.EnsureResult{{
		Entry:  key.Entry{Fingerprint: "abc"},
		Target: key.TargetDB{Account: "wxid_a", DataDir: "/tmp/db_storage", DBRelPath: "contact/contact.db"},
		Reused: false,
	}}

	var out bytes.Buffer
	if err := writeInitOutput(&out, results, nil, true); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"keys:",
		"contact/contact.db fingerprint=abc status=scanned",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("verbose init output missing %q:\n%s", want, text)
		}
	}
}

func TestMessagesHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"messages", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview messages - List WeChat messages",
		"--username TEXT",
		"JSON envelope with meta and items",
		"--start TIME",
		"--end TIME",
		"--date today|yesterday|YYYY-MM-DD",
		"--after-seq N",
		"--limit N",
		"--offset N",
		"--source",
		"--trace-time",
		"meta.schema_version",
		"meta.timezone",
		"meta.next_args",
		"create_time ascending",
		"message/message_*.db",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("messages help missing %q:\n%s", want, text)
		}
	}
}

func TestMessagesWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"messages"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview messages - List WeChat messages",
		"Required:",
		"--username TEXT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("messages no-arg help missing %q:\n%s", want, text)
		}
	}
}

func TestTimelineHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"timeline", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview timeline - List WeChat messages",
		"--kind all",
		"--query TEXT",
		"--username TEXT",
		"--date today|yesterday|YYYY-MM-DD",
		"--cursor TOKEN",
		"meta.schema_version",
		"meta.timezone",
		"meta.next_args",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeline help missing %q:\n%s", want, text)
		}
	}
}

func TestTimelineWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"timeline"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview timeline - List WeChat messages",
		"Conversation selection:",
		"--date today|yesterday|YYYY-MM-DD",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeline no-arg help missing %q:\n%s", want, text)
		}
	}
}

func TestContactsHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"contacts", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"--format json",
		"--format csv",
		"--kind friend",
		"--query TEXT",
		"--detail",
		"--limit N",
		"--count",
		"Output fields",
		"Detail-only fields",
		"Examples for AI/tools",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("contacts help missing %q:\n%s", want, text)
		}
	}
}

func TestDaemonHelpShowsStatusAndNoQueryAction(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"daemon", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Supported forms",
		"wxview daemon         Show this help.",
		"wxview daemon start",
		"wxview daemon stop",
		"wxview daemon status",
		"No daemon flags are currently supported",
		"health",
		"refresh_contacts",
		"refresh_sessions",
		"refresh_messages",
		"refresh_avatars",
		"refresh_favorites",
		"refresh_sns",
		"stop",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daemon help missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "list_contacts") {
		t.Fatalf("daemon help should not advertise list_contacts:\n%s", text)
	}
}

func TestSessionsHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"sessions", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview sessions - List recent WeChat sessions",
		"session/session.db",
		"--kind all|friend|chatroom|other",
		"--query TEXT",
		"unread_count",
		"last_msg_type",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sessions help missing %q:\n%s", want, text)
		}
	}
}

func TestUnreadHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"unread", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview unread - List unread WeChat sessions",
		"--limit N",
		"--refresh",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("unread help missing %q:\n%s", want, text)
		}
	}
}

func TestNewMessagesHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"new-messages", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview new-messages - Return messages newer than the saved checkpoint",
		"--reset",
		"24-hour fallback window",
		"same item schema as messages/timeline",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("new-messages help missing %q:\n%s", want, text)
		}
	}
}

func TestSearchHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"search", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview search - Search local WeChat message content",
		"--query TEXT",
		"--chat-query TEXT",
		"same message",
		"newest-first",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("search help missing %q:\n%s", want, text)
		}
	}
}

func TestDaemonWithoutArgsShowsHelp(t *testing.T) {
	t.Setenv(daemonForegroundEnv, "")
	var out bytes.Buffer
	if err := run([]string{"daemon"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	if err := run([]string{"daemon", "--help"}, &help, &help); err != nil {
		t.Fatal(err)
	}
	if out.String() != help.String() {
		t.Fatalf("daemon without args should match daemon --help\nwithout args:\n%s\nhelp:\n%s", out.String(), help.String())
	}
}

func TestCacheHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"cache", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview cache - Inspect local decrypted cache status",
		"wxview cache status",
		"wxview cache clean-tmp",
		"--group all|contacts|messages|media|sessions|avatars|favorites|sns",
		"fresh",
		"stale",
		"missing_cache",
		"key_status",
		"does not refresh, decrypt, scan process memory, or talk to the daemon",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cache help missing %q:\n%s", want, text)
		}
	}
}

func TestIndexHelpExplainsRefreshResetAndLag(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"index", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview index - Inspect and maintain the derived message index",
		"wxview index refresh",
		"wxview index rebuild --reset",
		"wxview index clean-tmp",
		"ready and near-realtime",
		"Lag up to 60 seconds",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("index help missing %q:\n%s", want, text)
		}
	}
}

func TestCacheWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"cache"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	var help bytes.Buffer
	if err := run([]string{"cache", "--help"}, &help, &help); err != nil {
		t.Fatal(err)
	}
	if out.String() != help.String() {
		t.Fatalf("cache without args should match cache --help\nwithout args:\n%s\nhelp:\n%s", out.String(), help.String())
	}
}

func TestCacheStatusRejectsUnknownGroupBeforeDiscovery(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"cache", "status", "--group", "indexes"}, &out, &out)
	if err == nil {
		t.Fatal("expected invalid cache status group to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid cache group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnknownDaemonSubcommandIsRejected(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"daemon", "run"}, &out, &out)
	if err == nil {
		t.Fatal("expected unknown daemon subcommand to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown daemon command: run") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnexpectedDaemonStartArgumentIsRejected(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"daemon", "start", "extra"}, &out, &out)
	if err == nil {
		t.Fatal("expected extra daemon start argument to be rejected")
	}
	if !strings.Contains(err.Error(), "unexpected daemon start argument: extra") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnexpectedDaemonStopArgumentIsRejected(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"daemon", "stop", "extra"}, &out, &out)
	if err == nil {
		t.Fatal("expected extra daemon stop argument to be rejected")
	}
	if !strings.Contains(err.Error(), "unexpected daemon stop argument: extra") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUnexpectedDaemonStatusArgumentIsRejected(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"daemon", "status", "extra"}, &out, &out)
	if err == nil {
		t.Fatal("expected extra daemon status argument to be rejected")
	}
	if !strings.Contains(err.Error(), "unexpected daemon status argument: extra") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMembersHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"members", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview members - List group members and owner",
		"--username CHATROOM",
		"--query TEXT",
		"owner",
		"is_owner",
		"wxview members --username 123@chatroom --format json",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("members help missing %q:\n%s", want, text)
		}
	}
}

func TestFavoritesHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"favorites", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview favorites - List WeChat favorites",
		"--type TYPE",
		"text, image, voice, video, article, location",
		"favorite/favorite.db",
		"source_chat_username",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("favorites help missing %q:\n%s", want, text)
		}
	}
}

func TestArticlesHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"articles", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview articles - List official-account articles and appmsg posts",
		"--list-accounts",
		"--username TEXT",
		"--query TEXT",
		"message/biz_message caches",
		"wxview articles --query \"AI\" --date today",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("articles help missing %q:\n%s", want, text)
		}
	}
}

func TestSNSHelpIsActionable(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"sns", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview sns - Read local WeChat Moments data",
		"sns feed",
		"sns search KEYWORD",
		"sns notifications",
		"--include-read",
		"does not expose CDN key/token fields",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sns help missing %q:\n%s", want, text)
		}
	}
}

func TestContactsWithoutArgsShowsHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"contacts"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"wxview contacts - List WeChat contacts",
		"No-argument behavior",
		"wxview contacts is intentionally the same as wxview contacts --help",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("contacts no-arg help missing %q:\n%s", want, text)
		}
	}
}

func TestContractHelpTypoAlias(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"contract", "--help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "official command is `contacts`") {
		t.Fatalf("expected typo alias note:\n%s", out.String())
	}
}

func TestKeyCommandIsNotAccepted(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"key"}, &out, &out)
	if err == nil {
		t.Fatal("expected key command to be rejected")
	}
	if strings.Contains(out.String(), "sudo wxview key") {
		t.Fatalf("key command should not be advertised:\n%s", out.String())
	}
}
