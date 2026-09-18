# Healarr — Architecture

This document captures the full system design as agreed during the design phase. It is the source of truth for what Healarr is and isn't trying to do.

## Goals

1. **Detect work-in-progress failures** in a Plex/*arr stack that aren't visible from liveness checks alone.
2. **Reason about anomalies with deterministic checks**, with a single daily LLM call producing the digest narrative; per-event agent reasoning with structured tools is deferred to a later phase.
3. **Self-heal within bounded permissions** — auto-execute reversible fixes, gate destructive ones behind a human decision on the `/healarr/` web page (ADR-014).
4. **Stay cheap** — most monitoring is rule-driven; the agent is invoked only when reasoning is required.
5. **Stay safe** — every destructive action is logged, dedup'd, and rate-limited.

## Non-goals

- Replacing *arr functionality (Radarr/Sonarr already handle indexing, importing, etc. well)
- Being a chat UI ("ask Healarr what's wrong") — interaction is digest email + a small decisions page, not a conversation
- Running as multi-tenant SaaS — this is self-hosted, two nodes, one household
- Defending against malicious or adversarial releases — Healarr cleans up after legitimate-but-bad downloads, not actively-hostile ones
- A full application UI beyond the LAN-only `/healarr/` decisions page (ADR-014) — no accounts, no mobile app, no public exposure

## Architecture

```
   ┌─────────────────────────────────────┐        ┌─────────────────────────────────────┐
   │              Pi (primary)            │        │              NAS (secondary)         │
   │                                       │        │                                       │
   │  Radarr/Sonarr/Prowlarr/Overseerr/    │        │  qBittorrent, Plex                    │
   │  Tautulli, Docker, host mounts        │        │                                       │
   │        │                              │        │        │                              │
   │        ▼                              │        │        ▼                              │
   │  ┌──────────────┐                     │        │  ┌──────────────┐                     │
   │  │   internal/   │  cron-scheduled,   │        │  │   internal/   │  cron-scheduled,   │
   │  │   checks/*    │  one family per    │        │  │   checks/*    │  one family per    │
   │  │  (Pi checks)  │  check dir         │        │  │ (NAS checks)  │  check dir         │
   │  └──────┬────────┘                     │        │  └──────┬────────┘                     │
   │         ▼                              │        │         ▼                              │
   │  ┌──────────────┐   internal/peer   ┌──┴──────┐ │  ┌──────────────┐   internal/peer   ┌──┴──────┐
   │  │ internal/store│◀─────HTTP/LAN───▶│  peer   │◀┼─▶│ internal/store│◀─────HTTP/LAN───▶│  peer   │
   │  │   (SQLite)    │  bearer token     │ server  │ │  │   (SQLite)    │  bearer token     │ client  │
   │  └──────┬────────┘                   └─────────┘ │  └──────┬────────┘                   └─────────┘
   │         │                                          │         │
   │         ▼                                          │         ▼ (on Pi's POST /v1/decision)
   │  ┌──────────────┐  ┌──────────────┐                │  ┌──────────────┐
   │  │ internal/     │  │ internal/    │                │  │ internal/    │
   │  │ staleness +   │  │ web/ (:8091, │                │  │ cleanup/     │
   │  │ decision/     │  │  LAN-only)   │                │  │ decision/    │
   │  └──────┬────────┘  └──────┬───────┘                │  └──────────────┘
   │         │                  │ nginx /healarr/         │
   │         ▼                  ▼                         │
   │  ┌──────────────┐  ┌──────────────┐                  │
   │  │ internal/llm/ │  │ internal/    │                  │
   │  │ (1 Haiku call │─▶│ notify/      │──▶ email (msmtp) │
   │  │  per day)     │  │ (digest)     │                  │
   │  └──────────────┘  └──────────────┘                  │
   └───────────────────────────────────────────────────────┘
```

Both nodes run the same `healarr` binary (`internal/agent/` as the daemon entry point); node identity (`pi` | `nas`) comes from config and decides which check families and responsibilities are active. See ADR-015.

## Components

Go package layout (one feature per directory, files kept under ~400 lines):

```
cmd/healarr/main.go                       cobra root — CLI + `agent serve` entry point
internal/config/                          TOML + env override; secrets file; node identity (pi|nas)
internal/clients/{sonarr,radarr,prowlarr,
  qbittorrent,plex,tautulli,overseerr,
  docker,hostfs}/                         Client interface + adapter + httptest fake + golden fixtures (Tasks 4-10)
internal/check/                           Check struct, Registry + Run (the runner), Finding/Report/Result types,
                                             Deps (incl. Deps.Previous for delta checks) — pure, store-agnostic (ADR-017)
internal/checks/{arr,disk,indexers,mounts,
  plex,qbit,requests}/                    one check family per dir, 21 checks total, table-tested against fixtures
internal/staleness/                       score formula (pure function, ADR-016), candidate selection
internal/cleanup/                         recycle-bin / orphan / seeded-torrent / docker-prune actions (dry-run first)
internal/decision/                        keep/delete/approve/reject executor (arr delete-with-files, overseerr sync)
internal/store/                           modernc.org/sqlite, embedded migrations, dedup/resolve on SaveReport (ADR-017)
internal/peer/                            HTTP server+client, bearer token, message types (ADR-015)
internal/agent/                           daemon: cron, orchestration, digest reconciliation, guards
internal/notify/                          msmtp shell-out, digest templates
internal/llm/                             single daily Haiku call, cost log, templated fallback
internal/web/                             html/template + minimal JS; dashboard, decisions, history (ADR-014)
internal/cli/                             cobra subcommands per service (`healarr <service> <verb>`)
deploy/systemd/healarr.service            Pi unit (User=parso, After=docker.service)
deploy/dsm/healarr-boot.sh                NAS Task Scheduler boot-up script (absolute paths, no $HOME)
deploy/nginx/healarr.conf.snippet         `location /healarr/ { proxy_pass http://<pi-ip>:8091/; }` for simplarr split.conf
```

`internal/clients/*` and `internal/cli/*` are what Phase 1 (this repo state) delivers: a `Client` interface per service with its own types (not a third-party client's), an adapter over the shared `httpx` client, an `httptest`-backed fake for unit tests, and golden JSON fixtures captured from the live stack. See ADR-013 for why these are hand-rolled rather than built on `golift.io/starr` / `go-qbittorrent`.

`internal/check` (the runner: `Registry`, `Run`, `Deps`, `Finding`/`Report`/`Result`) and `internal/checks/*` (the 21 check implementations, one family per client) are what Phase 2 delivers, together with `internal/store` and the `healarr check`/`report` CLI verbs — see ADR-017 and the "State store" section below for the store side, and `docs/runbook.md`'s "Running checks by hand" for the CLI.

### Check tiers (unchanged concept from the Python design — ADR-006)

Every check still classifies its possible remediation by blast radius, same four tiers as before, now driven off the `auto tier` column of the check catalogue rather than an LLM tool call per event:

- **Observe** (read-only) — always runs, always safe
- **Nudge** (reversible, no data loss) — auto-executes (retry import, reannounce, trigger scan)
- **Correct** (destructive but reversible) — gated: dry-run by default until promoted, then executes or awaits a decision on `/healarr/`
- **Escalate** (high blast radius) — never auto-executes; surfaces as a decision for a human (staleness candidates, ADR-016)

### State store (`internal/store/`)

SQLite via `modernc.org/sqlite` (ADR-013). `store.Open` sets `PRAGMA journal_mode=WAL`, `synchronous=NORMAL`, and a 5s `busy_timeout`, pins the connection pool to one connection, and applies any pending embedded migration — the Pi's SD card and the NAS's flash both make write amplification and multi-writer contention worth avoiding. `internal/store/migrations/0001_init.sql` creates every table below in one shot (Phase 2 only reads/writes `reports` and `findings`; later phases add columns, not tables):

| Table | Purpose | Phase 2 status |
|---|---|---|
| `reports` | one row per node per check run (`ran`/`skipped`/`errors`/`metrics` as JSON columns) | written by `SaveReport` |
| `findings` | check outputs, deduped on `(node, check_id, entity_key)` while `status ∈ open\|snoozed`, auto-resolved when a successful check stops emitting them (ADR-017) | written by `SaveReport` |
| `remediations` | executed tool calls + outcomes (was `actions`) | unused |
| `decisions` | staleness keep/delete and other human decisions (`pending\|executed\|failed`, `snooze_until`) | unused |
| `peer_messages` | inbound/outbound peer protocol log | unused |
| `staleness_scores` | per-entity score + component breakdown (ADR-016) | unused |
| `llm_calls` | daily digest LLM calls + cost | unused |
| `email_outbox` | sent digest emails | unused |
| `schema_meta` | applied migration version | written by `migrate` |

`SaveReport(ctx, check.Report)` inserts the report row and upserts its findings in one transaction; `LatestReport` and `OpenFindings` are the two reads `check run` (delta checks' baseline) and `report generate` (digest input) use. See `docs/runbook.md`'s "Running checks by hand" for the exact dedup/resolve mechanics and CLI usage.

Dropped from the Python design: `observations` (raw per-poll snapshots — too much write churn for an SD card), `proposals` and `email_inbox` (IMAP-era approval bookkeeping, gone with ADR-014).

### Notify (`internal/notify/`)

Outbound email via msmtp only (ADR-004 unchanged) — reuses the host's existing `~/.msmtprc`, zero new SMTP creds. No inbound channel: approvals happen on the web page (ADR-014), not by replying to mail. The daily digest email links into `/healarr/` for anything that needs a decision.

### Daemon (`internal/agent/`)

Both nodes run one long-running process, `healarr agent serve`, wired to this node's own check registry, store, and — on the Pi only — a mail sender. `Run` starts the peer HTTP server (when `peer.listen_addr` is configured), builds the cron scheduler, runs one check cycle immediately (so a restart isn't stale for up to 5 minutes), then blocks until SIGINT/SIGTERM or the peer server fails to bind. Shutdown is graceful: the scheduler waits for any in-flight job, and the peer server waits up to 5s for in-flight requests before its listener closes. The daemon is **observe-only** in Phase 3 — every job persists a report, a peer message, or a digest, but nothing it does blocks, deletes, or otherwise remediates anything; Phase 4 adds the executor.

The scheduler (`schedule.go`) registers five kinds of job, every one wrapped with `cron.Recover` (a panicking job is logged, never fatal) and `cron.SkipIfStillRunning` (an overlapping run is skipped and logged, never queued):

| Job | Cadence |
|---|---|
| 5-minute / 15-minute / hourly / daily check cycles | fixed cron specs — `*/5`, `*/15`, hourly, and `10 0 * * *` (00:10 local) for daily |
| Heartbeat | `agent.heartbeat_interval` (default `5m`), only when this node has a peer configured |
| Daily digest | `email.digest_at` (default `07:00` local), Pi only |
| Nightly WAL checkpoint | `agent.checkpoint_at` (default `03:00` local), both nodes |

00:10/03:00/07:00 are staggered on purpose: the daily checks land before the checkpoint, and the checkpoint lands before the digest that reports on them.

Peer traffic (`internal/peer/`) is four bearer-token-authenticated HTTP routes: `POST /v1/report` (the NAS pushes every cycle's report to the Pi), `GET /v1/report/latest` (pull-on-demand, and what `peer ping` calls), `POST /v1/heartbeat`, and `POST /v1/decision` (recorded only in Phase 3, per ADR-006 — nothing executes on it until Phase 4). The client retries a transport error or 5xx twice with 500ms→1s backoff before surfacing `ErrPeerUnavailable`; the server caps every body at 4 MiB and sets explicit read/write/idle timeouts so a slow or hostile peer can never hold a connection open indefinitely. ADR-018 covers the push-based reconciliation model this rests on.

The daily digest (`agent.SendDigest`, rendered by `internal/notify/`) merges this node's own latest report and open findings with the peer's contribution — built entirely from what the peer has already **pushed** into this node's own store (`peer_messages`/`findings` under the peer's node), never a read-time pull. An unreachable or silent peer never blocks the digest: it renders "peer stale since …" (last pushed report older than `agent.peer_stale_after`, default `15m`) or "no report received" (nothing pushed, ever) in place of that section instead.

### Decisions (`internal/web/`, `internal/decision/`)

The `/healarr/` page (Pi, LAN-only, behind the existing simplarr nginx) replaces the old email-reply approval flow (ADR-014). It shows the latest reconciled digest, both nodes' heartbeats, pending Correct-tier actions, and staleness candidates (ADR-016). A keep/delete/approve/reject click writes a `decisions` row; delete routes through Sonarr/Radarr's delete-with-files endpoint so *arr state and disk stay consistent, and notifies the peer if the file lives on the other node.

### Cost + safety guardrails

- **LLM cost**: one Haiku 4.5 call per day for the digest narrative (not per-event as in the original agentic design) — `daily_budget_usd` in config caps spend; a failed or over-budget call falls back to a templated digest, never blocks the email.
- **Dry-run mode**: global `--dry-run` flag (and per-check promotion from dry-run to live) — Correct-tier cleanups ship dry-run first and are promoted once trusted (Phase 4).
- **Peer resilience**: unreachable peer never blocks the digest — it just reports "peer stale since …" (ADR-015; push-based reconciliation mechanics in ADR-018).
- **Escalate tier**: the agent never auto-deletes library media; staleness candidates always require a human decision (ADR-016).

## Phasing

Go rewrite (ADR-013..016) replaces the earlier Python-era phase plan below. Each phase ships as its own PR: TDD, `go test ./... -cover` ≥80% on non-trivial packages, conventional commits.

### Phase 1 — CLI + clients ✅

- `go.mod`, cobra root, config + secrets loading, node identity (`pi` | `nas`)
- All nine client packages (`sonarr`, `radarr`, `prowlarr`, `overseerr`, `qbittorrent`, `plex`, `tautulli`, `docker`, `hostfs`) with `Client` interfaces, `httptest` fakes, and golden fixtures captured from the live stack
- Full `healarr <service> <verb>` CLI surface, `--json` everywhere
- CI: `go vet`, `golangci-lint`, tests, cross-compile matrix (`build-pi` / `build-nas`)
- Docs: ADR-013/014/015/016, this README/architecture/runbook rewrite

### Phase 2 — Checks + store + one-shot report ✅

- SQLite schema + store (`internal/store/`): embedded migrations, `SaveReport` dedup/resolve, `LatestReport`, `OpenFindings` (ADR-017)
- Check registry + all 21 checks from the C5 catalogue (every row except `staleness_scan`, which is Phase 4) as pure functions with table tests
- `healarr check list [--json]`, `healarr check run (--all | --id <id>) [--dry-run] [--json]`, `healarr report generate [--dry-run] [--json]`

### Phase 3 — Daemon + peer + digest email ✅

- `cron`-scheduled daemon (`healarr agent serve`): four fixed check-cycle cadences, a heartbeat, a daily digest (Pi only), and a nightly WAL checkpoint — exact specs under "Daemon" above
- Peer HTTP server/client (ADR-015), push-based report reconciliation on the Pi's own store (ADR-018)
- msmtp notifier (`internal/notify/`) wired into the daemon; `deploy/systemd/healarr.service` (Pi) and `deploy/dsm/healarr-boot.sh` (NAS) installed and verified on both nodes
- `healarr agent serve` (the daemon), `healarr notify test`, `healarr peer ping` (diagnostics) CLI verbs
- First real daily digest email, merging both nodes' findings
- Observe-only throughout (ADR-018): decisions are recorded, never executed, until Phase 4

### Phase 4 — Web UI + decisions + cleanups + staleness

- `/healarr/` pages: dashboard, decisions, history (ADR-014)
- Decision executor; cleanup actions promoted from dry-run to live
- Staleness scorer (ADR-016) wired into the decisions page
- nginx snippet PR against simplarr's `split.conf`

### Phase 5 — LLM digest

- Haiku 4.5 wrapper for the daily digest narrative
- Budget/cost log (`llm_calls` table), templated fallback on failure or over-budget
- `--no-llm` flag for a fully deterministic digest

## Worked example — the .exe episode

Historical illustration of the Triage-rule concept from the original design; the approval step now happens on the `/healarr/` web page (ADR-014) and per-event agent reasoning is a later phase.

| Step | What happens |
|---|---|
| T+0 | qBit completes `The.Boys.S04E05.exe` |
| T+1m | Sonarr import fails (not a video file) |
| T+5m | Next Monitor poll records: torrent completed, import failed, history event logged |
| T+30m | Triage rule fires `wrong_file_suspect` event |
| T+30m | Agent invoked. Sees event + recent state. Calls `get_sonarr_history(episode_id)` → confirms rejection. Calls `get_qbit_torrent_files(hash)` → single .exe. |
| T+30m | Agent decides: blocklist + delete + research. Emits proposal to state store. Approval email sent. |
| T+30m + reply | User replies "approve" from phone. IMAP polling picks up reply within 60s. |
| T+31m | Healarr executes the proposal: `blocklist_sonarr_release`, `delete_qbit_torrent(with_files=True)`, `trigger_sonarr_search(episode_id)`. |
| T+90m | Monitor confirms a new (different) torrent completed cleanly + import succeeded + Plex has the episode. Event closed. |
| If no resolution by T+4h | Escalation email — agent's plan didn't pan out, human takes over |

## Repository layout

```
healarr/
├── README.md
├── go.mod / go.sum
├── Makefile
├── config.example.toml
├── secrets.example.toml
├── .gitignore
├── docs/
│   ├── architecture.md          # this file
│   ├── decisions.md             # ADR log
│   ├── tools.md                 # tool tier inventory
│   └── runbook.md               # operations runbook
├── cmd/
│   └── healarr/                 # main.go — cobra root
├── internal/
│   ├── cli/                     # cobra subcommands per service
│   ├── config/                  # TOML + env override, secrets, node identity
│   ├── version/                 # build-time version/commit
│   ├── clients/                 # sonarr, radarr, prowlarr, qbittorrent,
│   │                             #   plex, tautulli, overseerr, docker, hostfs
│   ├── check/                   # Check interface, registry, Finding/Report (Phase 2)
│   ├── checks/                  # per-family check implementations (Phase 2)
│   ├── staleness/                # score formula (Phase 4)
│   ├── cleanup/                  # recycle-bin / orphan / prune actions (Phase 4)
│   ├── decision/                 # keep/delete/approve/reject executor (Phase 4)
│   ├── store/                    # modernc.org/sqlite, schema, migrations (Phase 2)
│   ├── peer/                     # HTTP peer server/client (Phase 3)
│   ├── agent/                    # daemon: cron, orchestration (Phase 3)
│   ├── notify/                   # msmtp shell-out, digest templates (Phase 3)
│   ├── llm/                      # Haiku digest call, cost log (Phase 5)
│   └── web/                      # html/template dashboard + decisions (Phase 4)
├── deploy/
│   ├── systemd/healarr.service   # Pi unit
│   ├── dsm/healarr-boot.sh       # NAS Task Scheduler boot script
│   └── nginx/healarr.conf.snippet
└── .github/workflows/ci.yml
```
