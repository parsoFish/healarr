# Healarr Phase 3 — Daemon + peer + digest email Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Phase 2 one-shot check engine into a long-running agent on each node: cron-driven check cycles persisted to the store, a bearer-token HTTP peer channel so the NAS pushes its reports and heartbeats to the Pi, and a daily 07:00 digest email sent from the Pi via msmtp that merges both nodes' findings — all observe-only (no remediation executes).

**Architecture:** `internal/peer` defines the wire types and a small `Client` (5 s timeout, 2 retries) + `Server` (`net/http`, bearer auth, four `/v1/*` endpoints) backed by a `Handler` interface. `internal/agent` owns the daemon: a `robfig/cron/v3` scheduler with one job per cadence (5m/15m/hourly/daily) that runs that cadence's checks for this node through the Phase 2 runner, saves the report, and (NAS) pushes it to the Pi; a heartbeat job; a daily digest job (Pi) that builds a `notify.DigestInput` from own + peer findings and sends it through `notify.Sender`; and a nightly WAL checkpoint. `internal/notify` gains the msmtp `Sender` and a peer section in the digest. `internal/store` gains peer-message, email-outbox and checkpoint methods. The CLI gains `agent serve`, `notify test`, `peer ping`. `deploy/` gains the systemd unit and the DSM boot script.

**Tech Stack:** Go 1.25, `github.com/robfig/cron/v3`, `net/http` + `httptest`, `modernc.org/sqlite` (already present), `os/exec` for msmtp. No cgo.

**Spec:** `docs/superpowers/specs/2026-09-12-go-rewrite-design.md` — sections C2 (agent/peer/notify packages, deploy files), C3 (nightly checkpoint, email_outbox, peer_messages), C4 (two-node topology, peer protocol, daily digest), C8 phase 3, Deployment.

## Global Constraints

- Module path `github.com/parsoFish/healarr`; `go 1.25.0` in `go.mod` (Phase 2 bump).
- Every build must pass `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` and `GOARCH=amd64`.
- Files under 400 lines; one responsibility per file; packages organised by feature.
- Never mutate inputs — return new values. Errors are always returned, wrapped with `fmt.Errorf("...: %w", err)`; nothing is silently swallowed. Background jobs log errors (via `log/slog`) and continue; they never crash the daemon.
- No hardcoded hosts, ports, paths, addresses, cadences or credentials in Go code; defaults live in `internal/config/defaults.go`.
- **Observe-only:** Phase 3 executes no Nudge/Correct/Escalate action. `POST /v1/decision` only records the message and returns 202; nothing acts on it until Phase 4.
- Peer auth: `Authorization: Bearer <secrets.peer_token>`, compared with `crypto/subtle.ConstantTimeCompare`; the server refuses to start when the token is empty; every request without a valid token gets 401 and is not logged with its body.
- Peer client: 5 s per-request timeout, 2 retries with 500 ms → 1 s backoff on transport errors and 5xx only (never on 4xx); an unreachable peer is never fatal — callers log and continue.
- Email: msmtp shell-out only (`exec.CommandContext(ctx, cfg.Email.MsmtpPath, "--read-envelope-from", "-t")`, message on stdin with `From:`, `To:`, `Subject:`, `Date:`, `MIME-Version: 1.0`, `Content-Type: text/plain; charset=utf-8` headers). Only the Pi sends mail; the NAS never calls the sender.
- Timezone: schedules use `cfg.Agent.Timezone` (default `time.Local`); cron specs are `robfig/cron` standard 5-field.
- Tests: `go test ./... -race -cover`, ≥ 80 % per non-trivial package; peer client/server tested with `httptest`; agent tested with fakes for store/peer/sender and a fake clock; store against a temp-file DB.
- Fixtures and docs use RFC 5737 addresses (`192.0.2.0/24`); no real LAN IPs, plex.direct hashes, usernames or tokens anywhere in the repo.
- Commits: conventional, **no AI attribution trailers** (plain `git commit -m`).
- Lint: `go vet ./...` and `golangci-lint run ./...` clean (gocyclo ≥ 15 fails).

---

### Task 1: Config — `[agent]` section, `[email].digest_at`, defaults, example

**Files:**
- Modify: `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/load.go` (`validate`), `internal/config/load_test.go`, `config.example.toml`

**Interfaces:**
- Produces:
```go
// Agent configures the daemon's schedules.
type Agent struct {
	Timezone          string        `toml:"timezone"`           // IANA name; "" = time.Local
	HeartbeatInterval time.Duration `toml:"heartbeat_interval"` // default 5m
	CheckpointAt      string        `toml:"checkpoint_at"`      // "HH:MM" local, default "03:00"
	PeerStaleAfter    time.Duration `toml:"peer_stale_after"`   // default 15m (3 missed heartbeats)
}
// Email gains:
DigestAt string `toml:"digest_at"` // "HH:MM" local, default "07:00"
// Config gains: Agent Agent `toml:"agent"`
```
- `validate` additionally rejects: `Agent.Timezone` that `time.LoadLocation` cannot load; `DigestAt`/`CheckpointAt` not matching `^([01]\d|2[0-3]):[0-5]\d$`; `Agent.HeartbeatInterval <= 0`. Add `func (c Config) Location() (*time.Location, error)`.
- Defaults: `Agent{HeartbeatInterval: 5*time.Minute, CheckpointAt: "03:00", PeerStaleAfter: 15*time.Minute}`, `Email.DigestAt: "07:00"`.
- `config.example.toml`: add `[agent]` block (commented defaults) and `digest_at = "07:00"` under `[email]`.

- [ ] Step 1: failing tests `TestLoadAgentDefaults`, `TestLoadRejectsBadTimezone`, `TestLoadRejectsBadDigestAt`, `TestLocationDefaultsToLocal`.
- [ ] Step 2: FAIL → Step 3: implement → Step 4: `go test ./internal/config/ -race -cover` ≥ 90 % → Step 5: commit `feat(config): [agent] schedule section and email digest_at`.

---

### Task 2: Store — peer messages, email outbox, WAL checkpoint

**Files:**
- Create: `internal/store/peer.go`, `internal/store/email.go`, `internal/store/peer_test.go`, `internal/store/email_test.go`
- Modify: `internal/store/store.go` (add `Checkpoint`), `internal/store/store_test.go`

**Interfaces:**
- Produces:
```go
// PeerMessage mirrors one peer_messages row.
type PeerMessage struct { ID int64; Direction string; Kind string; Peer config.Node; Payload string; ReceivedAt time.Time }
// SavePeerMessage records one inbound ("in") or outbound ("out") message; kind ∈ report|heartbeat|decision.
func (s *Store) SavePeerMessage(ctx context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error)
// LastPeerMessageAt returns the newest received_at for (peer, kind, direction "in"); ok=false when none.
func (s *Store) LastPeerMessageAt(ctx context.Context, peer config.Node, kind string) (time.Time, bool, error)

// EnqueueEmail inserts a pending outbox row and returns its id.
func (s *Store) EnqueueEmail(ctx context.Context, to, subject, body string, at time.Time) (int64, error)
// MarkEmailSent / MarkEmailFailed update status ("sent"|"failed"), sent_at, error.
func (s *Store) MarkEmailSent(ctx context.Context, id int64, at time.Time) error
func (s *Store) MarkEmailFailed(ctx context.Context, id int64, at time.Time, cause error) error
// PendingEmails lists status="pending" rows oldest first (used by the retry job and `notify test`).
func (s *Store) PendingEmails(ctx context.Context) ([]OutboxEmail, error)
type OutboxEmail struct { ID int64; To, Subject, Body string; CreatedAt time.Time }

// Checkpoint runs PRAGMA wal_checkpoint(TRUNCATE) (spec C3 nightly checkpoint).
func (s *Store) Checkpoint(ctx context.Context) error
```
- Direction and kind are validated (`errors.New` sentinel `ErrBadPeerMessage`); unknown values are rejected, never stored.

- [ ] Step 1: failing tests: save/last-at round trip per kind, direction filter (an "out" row does not count for LastPeerMessageAt), bad kind rejected, enqueue → pending → mark sent/failed transitions, checkpoint on a store with data succeeds and is idempotent, closed-store error paths.
- [ ] Step 2–5: implement, `go test ./internal/store/ -race -cover` ≥ 80 %, commit `feat(store): peer message log, email outbox and WAL checkpoint`.

---

### Task 3: Peer wire types + client

**Files:**
- Create: `internal/peer/types.go`, `internal/peer/client.go`, `internal/peer/client_test.go`

**Interfaces:**
- Produces:
```go
package peer

// ReportEnvelope is the body of POST /v1/report and GET /v1/report/latest.
type ReportEnvelope struct {
	Node    config.Node  `json:"node"`
	SentAt  time.Time    `json:"sentAt"`
	Version string       `json:"version"`
	Report  check.Report `json:"report"`
}
// Heartbeat is the body of POST /v1/heartbeat.
type Heartbeat struct { Node config.Node `json:"node"`; At time.Time `json:"at"`; Version string `json:"version"`; UptimeSeconds int64 `json:"uptimeSeconds"` }
// Decision is the body of POST /v1/decision (recorded only in Phase 3).
type Decision struct { ID int64 `json:"id"`; Kind string `json:"kind"`; EntityKey string `json:"entityKey"`; Payload map[string]any `json:"payload,omitempty"`; RequestedAt time.Time `json:"requestedAt"` }
// Ack is every POST's 2xx response.
type Ack struct { Status string `json:"status"`; ID int64 `json:"id,omitempty"` }

// Client talks to one peer.
type Client interface {
	PushReport(ctx context.Context, env ReportEnvelope) (Ack, error)
	FetchLatest(ctx context.Context) (ReportEnvelope, bool, error) // ok=false on 404 (peer has no report yet)
	SendDecision(ctx context.Context, d Decision) (Ack, error)
	Heartbeat(ctx context.Context, hb Heartbeat) (Ack, error)
}
// New builds an HTTP client for baseURL with bearer token; opts override timeout/retries (defaults 5 s, 2).
func New(baseURL, token string, opts ...Option) (*HTTPClient, error)
// ErrUnauthorized (401/403), ErrPeerUnavailable (transport error or 5xx after retries) are sentinel errors.
// Fake implements Client for tests: records calls, returns configured Ack/Envelope/Err.
```
- Implementation on `internal/clients/httpx` if it fits (bearer header via option); otherwise a small private `do()` with `net/http` — either is acceptable, but retries/backoff and the 4xx-no-retry rule must be unit-tested with `httptest` counting attempts.

- [ ] Step 1: failing tests: each verb sends the right method/path/auth header and decodes the body; 401 → `ErrUnauthorized` no retry; 503 twice then 200 → success with 3 attempts; 503 ×3 → `ErrPeerUnavailable`; `FetchLatest` 404 → ok=false nil error; context cancellation respected; bad baseURL → error from `New`.
- [ ] Step 2–5: implement; `go test ./internal/peer/ -race -cover` ≥ 85 %; commit `feat(peer): wire types and retrying bearer-token client`.

---

### Task 4: Peer server + handlers

**Files:**
- Create: `internal/peer/server.go`, `internal/peer/handlers.go`, `internal/peer/server_test.go`

**Interfaces:**
- Produces:
```go
// Handler is what the server needs from the agent/store.
type Handler interface {
	ReceiveReport(ctx context.Context, env ReportEnvelope) (int64, error)   // store as peer's report
	LatestOwnReport(ctx context.Context) (ReportEnvelope, bool, error)
	ReceiveDecision(ctx context.Context, d Decision) (int64, error)         // Phase 3: record only
	ReceiveHeartbeat(ctx context.Context, hb Heartbeat) error
}
// NewServer returns an http.Handler mounting /v1/report (POST), /v1/report/latest (GET), /v1/decision (POST), /v1/heartbeat (POST) behind bearer auth. token must be non-empty (error otherwise). Request bodies are capped at 4 MiB; JSON decode errors → 400; handler errors → 500 with a generic body (details only in slog).
func NewServer(token string, h Handler, logger *slog.Logger) (http.Handler, error)
// ListenAndServe runs the server on addr until ctx is done (graceful shutdown, 5 s).
func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error
```
- `FakeHandler` in `server_test.go` records inputs; tests use `httptest.NewServer` and the Task 3 client end-to-end (client ↔ server round trip for every verb).

- [ ] Step 1: failing tests: missing/wrong token → 401 for every route; wrong method → 405; oversized body → 413; bad JSON → 400; each verb round-trips through the real client; `LatestOwnReport` ok=false → 404; handler error → 500 without leaking the error text; empty token → `NewServer` error; `ListenAndServe` stops on ctx cancel.
- [ ] Step 2–5: implement; ≥ 85 %; commit `feat(peer): bearer-authenticated HTTP server for report, decision and heartbeat`.

---

### Task 5: Notify — msmtp sender, digest peer section, digest builder

**Files:**
- Create: `internal/notify/sender.go`, `internal/notify/sender_test.go`, `internal/notify/build.go`, `internal/notify/build_test.go`
- Modify: `internal/notify/digest.go`, `internal/notify/digest.tmpl`, `internal/notify/digest_test.go`

**Interfaces:**
- Produces:
```go
// Sender delivers one plain-text email.
type Sender interface { Send(ctx context.Context, to, subject, body string) error }
// MsmtpSender shells out to msmtp. Runner is exec.CommandContext by default; tests inject a fake.
type MsmtpSender struct { Path, From string; Runner func(ctx context.Context, name string, args ...string) *exec.Cmd }
func NewMsmtpSender(path, from string) *MsmtpSender
func (m *MsmtpSender) Send(ctx context.Context, to, subject, body string) error
// FakeSender records sends and returns Err.

// PeerSection is the peer node's contribution to the digest.
type PeerSection struct { Node config.Node; GeneratedAt time.Time; Findings []check.Finding; ChecksRun, ChecksFailed int; StaleSince *time.Time }
// DigestInput gains: Peer *PeerSection; Subject() string returns "healarr digest — <date> — <C> critical, <W> warn" (counts across both nodes).

// BuildDigest is the pure merge step the agent calls (tested without a store):
func BuildDigest(node config.Node, now time.Time, own check.Report, ownFindings []check.Finding, peer *PeerSection, baseURL string) DigestInput
```
- Template gains a `PEER (<node>)` block after the own-node sections: peer summary line, its CRITICAL/WARN/INFO lists (same format), or `peer <node> stale since <time>` when `StaleSince != nil`, or `peer <node>: no report received` when `Peer == nil`.
- Message built for msmtp (exact header order): `From: <from>`, `To: <to>`, `Subject: <subject>`, `Date: <RFC 1123Z now>`, `MIME-Version: 1.0`, `Content-Type: text/plain; charset=utf-8`, blank line, body. Subject and header values are sanitised: CR/LF stripped (header-injection guard) — test it. msmtp is invoked as `<path> --read-envelope-from -t`; non-zero exit → error including the trimmed stderr.

- [ ] Step 1: failing tests: sender builds the exact message and args (fake runner captures stdin), non-zero exit wraps stderr, CRLF in subject is stripped; `BuildDigest` merges counts, sets `StaleSince` only when passed, `Subject()` counts across both nodes; template renders peer block in all three states and keeps all existing digest tests green.
- [ ] Step 2–5: implement; `go test ./internal/notify/ -race -cover` ≥ 85 %; commit `feat(notify): msmtp sender, digest builder and peer section`.

---

### Task 6: Agent — cycles (checks by cadence → store → push) and the peer handler

**Files:**
- Create: `internal/agent/agent.go` (types + `New`), `internal/agent/cycle.go`, `internal/agent/handler.go`, `internal/agent/cycle_test.go`, `internal/agent/handler_test.go`, `internal/agent/fakes_test.go`

**Interfaces:**
- Produces:
```go
package agent

// Store is the subset of *store.Store the agent uses (satisfied by *store.Store; compile-time assertion in cli wiring).
type Store interface {
	SaveReport(ctx context.Context, rep check.Report) (int64, store.UpsertSummary, error)
	LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error)
	OpenFindings(ctx context.Context, node config.Node) ([]store.StoredFinding, error)
	SavePeerMessage(ctx context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error)
	LastPeerMessageAt(ctx context.Context, peer config.Node, kind string) (time.Time, bool, error)
	EnqueueEmail(ctx context.Context, to, subject, body string, at time.Time) (int64, error)
	MarkEmailSent(ctx context.Context, id int64, at time.Time) error
	MarkEmailFailed(ctx context.Context, id int64, at time.Time, cause error) error
	Checkpoint(ctx context.Context) error
}

// Options wires the daemon; every field is required except Peer/Sender (nil on the node that doesn't use them).
type Options struct {
	Cfg      config.Config
	Registry *check.Registry
	Deps     func(ctx context.Context) (check.Deps, error) // builds clients per cycle (fresh Previous)
	Store    Store
	Peer     peer.Client   // nil ⇒ no push/heartbeat
	Sender   notify.Sender // nil ⇒ no email (NAS)
	Logger   *slog.Logger
	Now      func() time.Time
	Version  string
}
type Agent struct { /* unexported */ }
func New(o Options) (*Agent, error)   // validates required fields

// RunCycle runs every check for this node whose Cadence == cadence, saves the report (Previous = latest stored report),
// and — when Peer != nil and node is NAS — pushes the envelope (error logged, recorded as failed push, never returned).
// It returns the report and the store id.
func (a *Agent) RunCycle(ctx context.Context, cadence time.Duration) (check.Report, int64, error)

// PeerHandler adapts the agent to peer.Handler: ReceiveReport → Store.SaveReport (node from envelope; rejects env.Node == own node) + SavePeerMessage("in","report"), LatestOwnReport → Store.LatestReport(own), ReceiveDecision → SavePeerMessage("in","decision") only (202, Phase 4 executes), ReceiveHeartbeat → SavePeerMessage("in","heartbeat").
func (a *Agent) PeerHandler() peer.Handler
```
- `Deps` is a function so each cycle gets fresh clients and the latest `Previous`; `RunCycle` sets `Deps.Previous` from `Store.LatestReport(own)` before running.
- Cadence selection: `registry.ForNode(node)` filtered by `Cadence == cadence`; unknown cadence (no checks) → returns an empty report and does not save.

- [ ] Step 1: failing tests (fake store/peer/deps, fixed Now): 5m cycle on pi runs only 5m pi checks and saves; Previous is passed through; NAS cycle pushes an envelope with the saved report and records an "out"/"report" peer message; push failure is logged + recorded, error nil; handler rejects own-node envelope; decision is recorded and not executed (no client call); heartbeat recorded.
- [ ] Step 2–5: implement; `go test ./internal/agent/ -race -cover` ≥ 85 %; commit `feat(agent): check cycles with store persistence, peer push and inbound peer handler`.

---

### Task 7: Agent — scheduler, heartbeat, digest, checkpoint, Run

**Files:**
- Create: `internal/agent/schedule.go`, `internal/agent/digest.go`, `internal/agent/run.go`, `internal/agent/schedule_test.go`, `internal/agent/digest_test.go`, `internal/agent/run_test.go`
- Modify: `go.mod` (`go get github.com/robfig/cron/v3@v3.0.1`)

**Interfaces:**
- Produces:
```go
// Schedule builds the cron entries for this node in cfg's location:
//   every 5m / 15m / hourly / daily → RunCycle(cadence)   (cron specs "*/5 * * * *", "*/15 * * * *", "0 * * * *", "10 0 * * *")
//   every Agent.HeartbeatInterval → SendHeartbeat (only when Peer != nil)
//   Email.DigestAt daily → SendDigest (Pi only: Sender != nil)
//   Agent.CheckpointAt daily → Store.Checkpoint
// Returns the *cron.Cron (not started) so tests can inspect entries.
func (a *Agent) Schedule() (*cron.Cron, error)
// SendHeartbeat posts a Heartbeat to the peer; failure is logged, never returned as fatal.
func (a *Agent) SendHeartbeat(ctx context.Context) error
// SendDigest builds the digest from own open findings + latest own report + peer latest report/open findings (peer stale when LastPeerMessageAt(peer, "report") older than Agent.PeerStaleAfter or absent), enqueues it, sends it, marks sent/failed. Returns the outbox id.
func (a *Agent) SendDigest(ctx context.Context) (int64, error)
// Run starts the peer server (cfg.Peer.ListenAddr) and the cron scheduler, runs an immediate 5m cycle on start, and blocks until ctx is done; shutdown is graceful (cron.Stop waits for running jobs, server 5 s).
func (a *Agent) Run(ctx context.Context) error
```
- The cron uses `cron.New(cron.WithLocation(loc), cron.WithChain(cron.Recover(logger), cron.SkipIfStillRunning(logger)))` so a slow cycle never overlaps itself.
- Daily cycle runs at 00:10 so it precedes the 07:00 digest by hours and never coincides with the checkpoint (03:00).

- [ ] Step 1: failing tests: `Schedule()` on pi has exactly the expected entry specs (5m, 15m, hourly, daily, heartbeat, digest, checkpoint) and on nas omits digest; `SendDigest` enqueues then marks sent (fake sender), marks failed and returns error when the sender fails, marks peer stale when last report is older than `PeerStaleAfter`, includes peer findings when fresh; `SendHeartbeat` records an "out"/"heartbeat" message; `Run` with a cancelled context returns promptly and closes the listener (use `127.0.0.1:0`).
- [ ] Step 2–5: implement; ≥ 85 %; commit `feat(agent): cron schedule, heartbeat, daily digest, checkpoint and Run loop`.

---

### Task 8: CLI — `agent serve`, `notify test`, `peer ping`; Deps wiring

**Files:**
- Create: `internal/cli/cmd_agent.go`, `internal/cli/cmd_notify.go`, `internal/cli/cmd_peer.go`, tests for each
- Modify: `internal/cli/deps.go`, `internal/cli/checkdeps.go` (reuse `buildCheckDeps`), `internal/cli/root.go`, `internal/cli/cmd_test.go`

**Interfaces:**
- `Deps` gains `PeerClient func(cfg, sec) (peer.Client, error)` (nil when `cfg.Peer.PeerURL` empty), `Sender func(cfg) notify.Sender` (returns `nil` on NAS or when `cfg.Email.To` empty), `NewAgent func(agent.Options) (*agent.Agent, error)`.
- `healarr agent serve`: loads config, opens the store (the store dir must exist — error otherwise), builds `agent.Options` (`Deps` = closure over `buildCheckDeps`, `Version` = `version.Version`), installs `signal.NotifyContext(SIGINT, SIGTERM)`, calls `Run`. Refuses to start with `--dry-run`? No — `--dry-run` is honoured by making `Deps` nil-out nothing; the daemon is observe-only regardless. `--dry-run` with `agent serve` returns an error "agent serve does not support --dry-run; the daemon is observe-only in this phase" (avoid a silently non-persisting daemon).
- `healarr notify test [--to <addr>]`: sends ONE short test email (`Subject: healarr test email from <node>`, body with version + time) via the sender; prints the outbox id or the error; NAS/no sender → error "email is only configured on the pi node".
- `healarr peer ping`: sends a heartbeat + `FetchLatest`, prints peer reachability, latest report age and counts (`--json` supported).
- Tests through the cobra tree with fakes: `agent serve --dry-run` errors; `notify test` calls the fake sender once with the right subject and enqueues/marks in the fake store; `peer ping` prints the fake envelope's age; missing peer_url → clear error.

- [ ] Step 1–5 per TDD; `go test ./internal/cli/ -race -cover` ≥ 80 %; commit `feat(cli): agent serve, notify test and peer ping verbs`.

---

### Task 9: Deploy files — systemd unit, DSM boot script, nginx snippet placeholder

**Files:**
- Create: `deploy/systemd/healarr.service`, `deploy/dsm/healarr-boot.sh`, `deploy/README.md`

`deploy/systemd/healarr.service`:
```ini
[Unit]
Description=healarr health agent (pi node)
After=network-online.target docker.service
Wants=network-online.target
RequiresMountsFor=/mnt/nas/tv /mnt/nas/movies /mnt/nas/downloads

[Service]
Type=simple
User=parso
ExecStart=/usr/local/bin/healarr agent serve --config /etc/healarr/config.toml
Restart=on-failure
RestartSec=10
Environment=TZ=Australia/Brisbane
NoNewPrivileges=true
ProtectSystem=full
ReadWritePaths=/var/lib/healarr
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```
(Mount paths and TZ are the simplarr defaults; the README says to edit them.)

`deploy/dsm/healarr-boot.sh` (absolute paths, no `$HOME`, DSM Task Scheduler "boot-up" task run as the agent user):
```sh
#!/bin/sh
# healarr NAS daemon launcher for DSM Task Scheduler (boot-up, user = agent user).
set -eu
HEALARR_DIR=/volume1/docker/healarr
export TZ=Australia/Brisbane
cd "$HEALARR_DIR"
if [ -f "$HEALARR_DIR/agent.pid" ] && kill -0 "$(cat "$HEALARR_DIR/agent.pid")" 2>/dev/null; then
  echo "healarr already running (pid $(cat "$HEALARR_DIR/agent.pid"))"; exit 0
fi
nohup setsid "$HEALARR_DIR/healarr" agent serve --config "$HEALARR_DIR/config.toml" >> "$HEALARR_DIR/agent.log" 2>&1 &
echo $! > "$HEALARR_DIR/agent.pid"
echo "healarr started (pid $(cat "$HEALARR_DIR/agent.pid"))"
```
`deploy/README.md`: install steps for both (copy, `systemctl enable --now healarr`, DSM Task Scheduler boot-up task), log locations (`journalctl -u healarr`, `agent.log`), stop/restart, where the nginx snippet lands in Phase 4. Add `shellcheck`-clean note; test: `sh -n deploy/dsm/healarr-boot.sh` in CI? Keep CI unchanged; the implementer runs `sh -n` locally and notes it.

- [ ] Commit `feat(deploy): systemd unit and DSM boot script for the agent`.

---

### Task 10: Host deployment (operator task — controller session)

Preconditions: PR merged, `main` fast-forwarded, probe `ALL OK`.

- [ ] `make build-pi build-nas`.
- [ ] Pi: keep `healarr.prev`; `sudo install` new binary; update `/etc/healarr/config.toml` (`[peer] peer_url = "http://<nas-ip>:8090"`, `[email] to/from = user address`, `digest_at = "07:00"`, `[agent] timezone = "Australia/Brisbane"`, `[llm] enabled = false`); `healarr config validate`; install `deploy/systemd/healarr.service` to `/etc/systemd/system/`, `daemon-reload`, `enable --now`; `systemctl status healarr`; `journalctl -u healarr -n 30`.
- [ ] NAS: copy binary + `deploy/dsm/healarr-boot.sh` to `/volume1/docker/healarr/`; config `[peer] peer_url = "http://<pi-ip>:8090"`, `[llm] enabled = false`; `config validate`; start with the boot script (same nohup/setsid command); `tail agent.log`. **DSM Task Scheduler boot-up task is the user's job — note in report.**
- [ ] Smoke: from the Pi `healarr peer ping --config /etc/healarr/config.toml`; from the NAS the same; `curl -s -o /dev/null -w '%{http_code}' http://<pi-ip>:8090/v1/report/latest` without a token → 401.
- [ ] `healarr notify test --config /etc/healarr/config.toml` on the Pi → ONE email to the user's address.
- [ ] `ssh pi ~/healthcheck.sh` → `ALL OK`.

---

### Task 11: Docs — runbook daemon ops, README status, architecture, ADR-018

**Files:**
- Modify: `README.md` (Phase 3 ✅, `agent serve`/`notify test`/`peer ping` examples), `docs/runbook.md` (daemon lifecycle on both nodes, logs, digest schedule, peer troubleshooting: 401 → token mismatch, "peer stale" → check NAS process/boot task, DSM boot-task steps), `docs/architecture.md` (Phase 3 ✅; peer flow; schedules), `docs/decisions.md` (ADR-018).

**ADR-018 — Push-based peer reporting, Pi-side merge**: Context (two stores, one digest, NFS not a substrate — ADR-015); Decision (NAS pushes every cycle's report + a heartbeat to the Pi; the Pi stores peer reports in its own DB under `node = nas` using the node-scoped dedup from Phase 2; the digest is built from the Pi's DB only; `/v1/report/latest` exists for pull-on-demand and `peer ping`; decisions are recorded, not executed, until Phase 4); Consequences (NAS outage ⇒ digest says "peer stale since …"; Pi outage ⇒ NAS keeps its own history and re-pushes on the next cycle (no replay queue — accepted); token rotation requires restarting both).

- [ ] Commit `docs: phase 3 daemon/peer/digest runbook, ADR-018`.

---

## Self-review notes

- **Spec coverage:** C4 endpoints (4) ✓ T3/T4; timeouts 5 s + 2 retries ✓ T3; "peer stale, never blocks" ✓ T7; daily digest 07:00 local merging own + NAS ✓ T7/T5 (LLM narrative is Phase 5; staleness candidates/cleanup counts/pending decisions are Phase 4 — the digest template's peer/own sections are what exist now); C3 nightly checkpoint ✓ T2/T7; `email_outbox`/`peer_messages` used ✓ T2; C2 deploy files ✓ T9 (nginx snippet is Phase 4); C8 phase 3 "install both nodes, first real digest" ✓ T10 (a test email is sent; the first scheduled digest arrives at 07:00 — the report says so).
- **Deviations:** decisions endpoint records only (spec assigns execution to Phase 4's executor); no Pi→NAS pull loop (push + on-demand fetch suffice); `agent serve` rejects `--dry-run` rather than silently not persisting.
- **Type consistency:** `peer.ReportEnvelope.Report` is `check.Report`; `agent.Store` is satisfied by `*store.Store` (methods added in T2); `notify.DigestInput.Peer *PeerSection` consumed by T7's `SendDigest`; `Deps func(ctx) (check.Deps, error)` mirrors T11 (Phase 2) `buildCheckDeps`.
