# Daemon/API Start TTY Confirmation Gate — Design

Date: 2026-11-18
Status: approved (方案 A), implementation pending

## Problem

`wxview daemon start` and `wxview api start` spawn persistent background
processes. No code path starts them implicitly (verified: the only self-spawn
sites are `runDaemonStart` and `runAPIStart`), but any agent or script that
runs these commands directly gets a background daemon with no user consent.
The user wants an interactive confirmation gate: only the user, at a real
terminal, may approve a background start; everything else is rejected.

## Approved Approach (方案 A: pure TTY confirmation gate)

- `wxview daemon start` / `wxview api start` check whether stdin is a
  terminal before spawning anything.
  - TTY: print a `y/N` prompt describing what will be started. Only `y`/`yes`
    proceeds; empty line, `n`, garbage, or EOF denies. Default is deny.
  - Not a TTY (agents, pipes, `go test`): reject with a non-zero exit and a
    clear message: the command requires interactive confirmation and must be
    run from the user's own terminal.
- Already-running checks happen before the prompt, so "already running"
  responses never prompt.
- No bypass: no `--yes` flag, no environment-variable override, no config
  switch. A bypass usable by a caller is a bypass usable by an agent.
- Hidden foreground path (`WXVIEW_DAEMON_FOREGROUND=1 wxview daemon`) is
  closed with a one-time spawn token:
  - `daemon start`, after confirmation, generates 32 random bytes
    (crypto/rand, hex), writes them to `~/.wxview/daemon-spawn-token`
    (0600, `ChownForSudo` under sudo), and passes the value to the child
    via env `WXVIEW_DAEMON_SPAWN_TOKEN`.
  - The foreground entry consumes the token: read file, delete it
    unconditionally, then compare with the env value. Missing file, missing
    env, or mismatch rejects the run, even from a TTY. The foreground path is
    internal plumbing; humans use `wxview daemon start`.
- `wxview api serve` stays ungated: it is the documented foreground/debug
  form, attached to the caller's session, and is also the child of the
  (now-gated) `api start`. Detaching it via `nohup ... &` is deliberate
  adversarial behavior, covered by the AGENTS.md soft rule, not by this gate.

## Components

| Component | Location | Responsibility |
|---|---|---|
| `startgate.go` | `cmd/wxview/` (new) | TTY check, y/N prompt, deny errors, token issue/consume |
| `app.DaemonSpawnTokenPath()` | `internal/app/paths.go` | token file path (`~/.wxview/daemon-spawn-token`) |
| `daemonSpawnTokenEnv` const | `cmd/wxview/main.go` const block | `WXVIEW_DAEMON_SPAWN_TOKEN` |
| help text | `daemonUsage`, `apiStartUsage` | document the confirmation requirement |
| AGENTS.md | repo root | soft rule: agents must not run start commands or spawn wxview background processes |

The gate is CLI presentation logic, so it lives in `cmd/wxview`; no changes to
`internal/daemon`, `internal/webapi`, or `internal/query`.

## Flow

```
wxview daemon start
  ├─ socket health: already running → print, return nil (no prompt, no spawn)
  ├─ confirmStartAction("Start wxview cache daemon in the background")
  │    ├─ stdin not a terminal → error: requires interactive confirmation
  │    └─ terminal → [y/N]; only y/yes proceeds
  ├─ issueSpawnToken() → token file + env value
  └─ spawn child (unchanged): env += WXVIEW_DAEMON_FOREGROUND=1,
       WXVIEW_DAEMON_SPAWN_TOKEN=<token>

WXVIEW_DAEMON_FOREGROUND=1 wxview daemon   (child or forger)
  └─ consumeSpawnToken(env value)
       ├─ read token file, delete it unconditionally
       ├─ missing file / missing env / mismatch → reject
       └─ match → run foreground daemon (unchanged)

wxview api start [--addr A]
  ├─ ensureNoAPIAlreadyRunning (unchanged)
  ├─ confirmStartAction("Start wxview WebAPI at http://A in the background")
  └─ spawn `api serve` child (unchanged)
```

## Error handling

- All denials return an error (non-zero exit) with a one-line reason on
  stderr; agents see why the command failed in their tool output.
- TTY detection: `os.Stdin.Stat()` mode has `os.ModeCharDevice`. `< /dev/null`
  passes this check but then reads EOF, which denies. Cross-platform
  (macOS/Windows), no cgo, no new dependencies.
- Stale token files (spawn failed, child crashed before consuming) are
  harmless: the next `daemon start` overwrites the file; a stale file alone
  cannot pass validation without the matching env value.
- `waitForDaemonHealthy` and all post-spawn logic are unchanged.

## Testing

- `confirmFrom` (pure part): `y`/`Y`/`yes` allow; empty/`n`/garbage/EOF deny.
- Token round trip: issue → consume(same) ok, file removed after consume.
- Token rejection: wrong value, missing env, missing file; file always
  consumed (deleted) even on failure.
- Gate wiring (stubbed `confirmStartAction`): `daemon start` and `api start`
  fail before spawning when the gate denies; `daemon` foreground entry with
  `WXVIEW_DAEMON_FOREGROUND=1` and no token is rejected.
- Help text asserts the confirmation note in `daemon --help` and
  `api start --help`.
- Full verification per repo standard:
  `GOCACHE=$(pwd)/.cache/go-build go test ./...` and `go vet ./...`.

## Out of scope

- Any bypass mechanism (`--yes`, env, config) for automation; add later only
  if a real need appears.
- Gating `api serve` or other foreground commands.
- macOS native dialog confirmation (user chose deny-when-non-TTY instead).
