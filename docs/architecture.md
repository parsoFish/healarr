# Healarr — Architecture

This document captures the full system design as agreed during the design phase. It is the source of truth for what Healarr is and isn't trying to do.

## Goals

1. **Detect work-in-progress failures** in a Plex/*arr stack that aren't visible from liveness checks alone.
2. **Reason about anomalies** using a Claude API agent with structured tools.
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
   │  │ staleness +   │  │ web/ (:8090, │                │  │ cleanup/     │
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
internal/check/                           Check interface, registry, Finding/Report types, dedup
internal/checks/{mounts,queue,indexers,
  qbit,plex,disk,updates,requests}/       one check family per dir, table-tested against fixtures
internal/staleness/                       score formula (pure function, ADR-016), candidate selection
internal/cleanup/                         recycle-bin / orphan / seeded-torrent / docker-prune actions (dry-run first)
internal/decision/                        keep/delete/approve/reject executor (arr delete-with-files, overseerr sync)
internal/store/                           modernc.org/sqlite, schema.sql, numbered migrations
internal/peer/                            HTTP server+client, bearer token, message types (ADR-015)
internal/agent/                           daemon: cron, orchestration, digest reconciliation, guards
internal/notify/                          msmtp shell-out, digest templates
internal/llm/                             single daily Haiku call, cost log, templated fallback
internal/web/                             html/template + minimal JS; dashboard, decisions, history (ADR-014)
internal/cli/                             cobra subcommands per service (`healarr <service> <verb>`)
deploy/systemd/healarr.service            Pi unit (User=parso, After=docker.service)
deploy/dsm/healarr-boot.sh                NAS Task Scheduler boot-up script (absolute paths, no $HOME)
deploy/nginx/healarr.conf.snippet         `location /healarr/ { proxy_pass http://<pi-ip>:8090/; }` for simplarr split.conf
```

`internal/clients/*` and `internal/cli/*` are what Phase 1 (this repo state) delivers: a `Client` interface per service with its own types (not a third-party client's), an adapter over the shared `httpx` client, an `httptest`-backed fake for unit tests, and golden JSON fixtures captured from the live stack. See ADR-013 for why these are hand-rolled rather than built on `golift.io/starr` / `go-qbittorrent`.

### Check tiers (unchanged concept from the Python design — ADR-006)

Every check still classifies its possible remediation by blast radius, same four tiers as before, now driven off the `auto tier` column of the check catalogue rather than an LLM tool call per event:

- **Observe** (read-only) — always runs, always safe
- **Nudge** (reversible, no data loss) — auto-executes (retry import, reannounce, trigger scan)
- **Correct** (destructive but reversible) — gated: dry-run by default until promoted, then executes or awaits a decision on `/healarr/`
- **Escalate** (high blast radius) — never auto-executes; surfaces as a decision for a human (staleness candidates, ADR-016)

### State store (`internal/store/`)

SQLite via `modernc.org/sqlite` (ADR-013), `PRAGMA journal_mode=WAL, synchronous=NORMAL`, batched writes per cycle — the Pi's SD card and the NAS's flash both make write amplification worth avoiding. Tables:

| Table | Purpose |
|---|---|
| `reports` | one row per node per check cycle |
| `findings` | Check outputs, deduped on `check_id + entity_key` while `status ∈ open\|snoozed` |
| `remediations` | executed tool calls + outcomes (was `actions`) |
| `decisions` | staleness keep/delete and other human decisions (`pending\|executed\|failed`, `snooze_until`) |
| `peer_messages` | inbound/outbound peer protocol log |
| `staleness_scores` | per-entity score + component breakdown (ADR-016) |
| `llm_calls` | daily digest LLM calls + cost |
| `email_outbox` | sent digest emails |
| `schema_meta` | applied migration versions |

Dropped from the Python design: `observations` (raw per-poll snapshots — too much write churn for an SD card), `proposals` and `email_inbox` (IMAP-era approval bookkeeping, gone with ADR-014).

### Notify (`internal/notify/`)

Outbound email via msmtp only (ADR-004 unchanged) — reuses the host's existing `~/.msmtprc`, zero new SMTP creds. No inbound channel: approvals happen on the web page (ADR-014), not by replying to mail. The daily digest email links into `/healarr/` for anything that needs a decision.

### Decisions (`internal/web/`, `internal/decision/`)

The `/healarr/` page (Pi, LAN-only, behind the existing simplarr nginx) replaces the old email-reply approval flow (ADR-014). It shows the latest reconciled digest, both nodes' heartbeats, pending Correct-tier actions, and staleness candidates (ADR-016). A keep/delete/approve/reject click writes a `decisions` row; delete routes through Sonarr/Radarr's delete-with-files endpoint so *arr state and disk stay consistent, and notifies the peer if the file lives on the other node.

### Cost + safety guardrails

- **LLM cost**: one Haiku 4.5 call per day for the digest narrative (not per-event as in the original agentic design) — `daily_budget_usd` in config caps spend; a failed or over-budget call falls back to a templated digest, never blocks the email.
- **Dry-run mode**: global `--dry-run` flag (and per-check promotion from dry-run to live) — Correct-tier cleanups ship dry-run first and are promoted once trusted (Phase 4).
- **Peer resilience**: unreachable peer never blocks the digest — it just reports "peer stale since …" (ADR-015).
- **Escalate tier**: the agent never auto-deletes library media; staleness candidates always require a human decision (ADR-016).

## Phasing

Go rewrite (ADR-013..016) replaces the earlier Python-era phase plan below. Each phase ships as its own PR: TDD, `go test ./... -cover` ≥80% on non-trivial packages, conventional commits.

### Phase 1 — CLI + clients ✅

- `go.mod`, cobra root, config + secrets loading, node identity (`pi` | `nas`)
- All nine client packages (`sonarr`, `radarr`, `prowlarr`, `overseerr`, `qbittorrent`, `plex`, `tautulli`, `docker`, `hostfs`) with `Client` interfaces, `httptest` fakes, and golden fixtures captured from the live stack
- Full `healarr <service> <verb>` CLI surface, `--json` everywhere
- CI: `go vet`, `golangci-lint`, tests, cross-compile matrix (`build-pi` / `build-nas`)
- Docs: ADR-013/014/015/016, this README/architecture/runbook rewrite

### Phase 2 — Checks + store + one-shot report

- SQLite schema + store (`internal/store/`)
- Check registry + every check from the C5 catalogue as a pure function with table tests
- `healarr check run --all [--dry-run]`, `healarr report generate --dry-run`

### Phase 3 — Daemon + peer + digest email

- `cron`-scheduled daemon (`healarr agent serve`)
- Peer HTTP server/client (ADR-015), report reconciliation
- msmtp notifier, `deploy/` systemd + DSM units installed on both nodes
- First real daily digest email

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
