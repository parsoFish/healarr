# Healarr — Architecture

This document captures the full system design as agreed during the design phase. It is the source of truth for what Healarr is and isn't trying to do.

## Goals

1. **Detect work-in-progress failures** in a Plex/*arr stack that aren't visible from liveness checks alone.
2. **Reason about anomalies** using a Claude API agent with structured tools.
3. **Self-heal within bounded permissions** — auto-execute reversible fixes, gate destructive ones behind email approval.
4. **Stay cheap** — most monitoring is rule-driven; the agent is invoked only when reasoning is required.
5. **Stay safe** — every destructive action is logged, dedup'd, and rate-limited.

## Non-goals

- Replacing *arr functionality (Radarr/Sonarr already handle indexing, importing, etc. well)
- Being a chat UI ("ask Healarr what's wrong") — interaction is one-directional via email
- Running as multi-tenant SaaS — this is self-hosted, single-user, single-stack
- Defending against malicious or adversarial releases — Healarr cleans up after legitimate-but-bad downloads, not actively-hostile ones
- Hosting a web UI for approvals — replaced by email-reply approvals (ADR-005)

## Architecture

```
                      ┌──────────────────────────────────────────┐
                      │               Healarr                    │
                      │                                          │
   ┌──────────┐       │   ┌──────────────┐                       │
   │  Radarr  ├──────▶│   │   Monitor    │ (every N min +        │
   │  Sonarr  ├──────▶│   │  (poll loop) │  webhook receiver)    │
   │ Prowlarr ├──────▶│   └──────┬───────┘                       │
   │Overseerr ├──────▶│          ▼                               │
   │  qBit    ├──────▶│   ┌──────────────┐                       │
   │   Plex   ├──────▶│   │    Triage    │ (pure rules;          │
   │ system   ├──────▶│   │ (rule engine)│  most events stop     │
   └──────────┘       │   └──────┬───────┘  here — no LLM)       │
                      │          │                               │
                      │          ▼ (only anomalies)              │
                      │   ┌──────────────┐                       │
                      │   │    Agent     │ (Claude API + tools,  │
                      │   │ (Sonnet 4.6) │  tiered permissions)  │
                      │   └──────┬───────┘                       │
                      │          │                               │
                      │   ┌──────┴────────────────────────┐      │
                      │   ▼                               ▼      │
                      │ ┌──────────┐               ┌────────────┐│
                      │ │  State   │               │  Notify    ││
                      │ │  SQLite  │               │  email +   ││
                      │ │          │               │  IMAP poll ││
                      │ └──────────┘               │  (replies) ││
                      │                            └────────────┘│
                      └──────────────────────────────────────────┘
```

Single container alongside the existing simplarr Pi stack.

## Components

### 1. Monitor (`healarr/monitor/`)

Polling loop at a configurable cadence (default 5 min). Each module polls its service and writes raw observations to SQLite. The Monitor knows nothing about "issues" — it just records facts.

| Module | Service | What it observes |
|---|---|---|
| `radarr.py` | Radarr | queue, history, system status |
| `sonarr.py` | Sonarr | queue, history, system status |
| `prowlarr.py` | Prowlarr | indexer list + health stats |
| `overseerr.py` | Overseerr | requests + status |
| `qbit.py` | qBittorrent | torrent list, stalls, completion ratios |
| `plex.py` | Plex | library scan times, sessions, server identity |
| `system.py` | host | disk usage, NFS mount probes, container restart counts |

Phase 4 adds a webhook receiver so Radarr/Sonarr/Overseerr can push fast-path events instead of waiting for the next poll.

### 2. Triage (`healarr/triage/`)

Deterministic rule engine over recent observations. Each rule is a small pure function over the state store; matching produces a structured `Event` with type + context.

Initial rule set:

| Event type | Rule |
|---|---|
| `wrong_file_suspect` | torrent completed + arr import failed + ≥30 min elapsed |
| `stuck_torrent` | torrent at 0% progress for ≥2h |
| `orphaned_request` | Overseerr request "Processing" ≥48h with no arr queue entry |
| `disk_pressure` | `/mnt/nas/downloads` usage ≥90% |
| `nfs_disconnect` | NFS probe from a Pi container fails twice in a row |
| `indexer_degraded` | Prowlarr indexer fail rate ≥50% over 1h |
| `plex_scan_stuck` | Plex library scan ≥24h despite new files in mounted dirs |
| `service_unhealthy` | service liveness fails 3 polls in a row |

The Triage layer is what enables Healarr to be cheap — most monitor cycles produce zero events, so the agent never runs.

### 3. Agent (`healarr/agent/`)

A Claude conversation per Triage event. System prompt is static (cached): describes Healarr, the simplarr stack topology, the tool tiers, the user's deployment specifics. Per-call payload is the event + recent state snapshot.

Default model: **Sonnet 4.6**. Phase 4 adds Haiku 4.5 for routine events.

The agent's job:

1. Use Observe-tier tools to confirm the diagnosis
2. Choose a remediation plan from Nudge or Correct tiers
3. For Nudge actions: execute directly
4. For Correct actions: emit a `proposal` to the State store, which triggers an approval email
5. Return a structured report (observed, diagnosed, acted, deferred)

### 4. Tools (`healarr/agent/tools.py`)

See [docs/tools.md](tools.md) for the full inventory. Tools are organised into four tiers:

- **Observe** (read-only) — always available
- **Nudge** (reversible, no data loss) — auto-execute
- **Correct** (destructive but reversible) — email approval gate
- **Escalate** (high blast radius) — agent can only propose, never execute

Tool dispatch enforces the policy. Every call is logged with `tool_name`, `args`, `result`, `agent_conversation_id`.

### 5. State store (`healarr/state/`)

SQLite. Tables:

| Table | Purpose |
|---|---|
| `observations` | raw monitor outputs, with timestamps |
| `events` | Triage outputs (event type + correlated observations) |
| `proposals` | Correct-tier actions awaiting email approval |
| `actions` | executed tool calls + outcomes |
| `agent_conversations` | full Claude turns for audit + replay |
| `email_outbox` | sent emails + their message IDs (for IMAP correlation) |
| `email_inbox` | processed reply UIDs (idempotency) |

Schema in [`healarr/state/schema.sql`](../healarr/state/schema.sql).

### 6. Notify (`healarr/notify/`)

**Outbound email via msmtp** (reuses the user's existing `~/.msmtprc` — zero new SMTP creds).

**Inbound IMAP polling** for approval replies. See [Approval flow](#approval-flow).

Notification policy — Healarr emails the user **only** in these cases:

1. **Approval request** — a Correct-tier action needs approval
2. **Escalation** — agent identified an issue but can't fix it autonomously
3. **Critical self-failure** — Healarr itself is broken (agent unreachable, polling hard-failing for ≥1h, budget exhausted)

It does **not** email for:
- Routine self-healed events (visible in dashboard / state log only)
- Healthy-state heartbeats
- Daily noise

### 7. Approval flow

#### Request email

```
Subject: [Healarr] Approve: blocklist The Boys S04E05 [APPROVAL-a3f7b9c2]

Healarr proposes a Correct-tier action:

  Action:  blocklist Sonarr release + delete qBit torrent + new search
  Target:  The Boys — S04E05 (release: "The.Boys.S04E05.exe ...")
  Why:     Single-file torrent contains an executable, not video. Sonarr
           rejected the import 38 minutes ago.
  Risk:    Medium — reversible.

Reply with "approve" or "yes" as the first line to authorise.
Reply with "reject" or "no" to decline.
```

#### IMAP polling

Healarr maintains an IMAP connection (poll cadence configurable, default 60s). For each new message in the configured mailbox:

1. Match `In-Reply-To` / `References` header against `email_outbox` (primary)
2. Fall back to extracting `[APPROVAL-<token>]` from the subject line
3. If a match is found and the token is still pending: parse the first non-quoted line of the plain-text body for `approve|yes|approved|y` or `reject|no|rejected|n`
4. Execute or cancel the proposal accordingly; mark the token consumed; mark the IMAP UID processed

#### Re-prompts and timeouts

| Time since proposal | Action |
|---|---|
| 4h | Re-send approval email if not yet replied to |
| 12h | Re-send again |
| 24h | Auto-escalate — proposal expires, separate escalation email |

### 8. Cost + safety guardrails

- **Daily budget**: $3 soft alert, $10 hard cap (agent paused, escalation email sent). Tokens metered via the Anthropic SDK.
- **Prompt caching**: system prompt + tool definitions are static and cached. Each agent invocation is mostly cache hits.
- **Loop detection**: if the same event type fires for the same entity (movie/episode/torrent) >3 times in 24h, a circuit breaker trips and the issue escalates instead of triggering more agent calls.
- **Per-tool circuit breakers**: e.g. `delete_qbit_torrent` rate limited globally to N/hr.
- **Dry-run mode**: launch flag that disables ALL Nudge + Correct execution; agent only emits proposals as it would in approval mode.

## Phasing

### Phase 0 — Scaffolding ✅

Repo layout, design docs, Dockerfile, docker-compose, SQLite schema, .env contract, stub CLI.

### Phase 1 — Monitor + Triage + email digest

- All seven Monitor modules wired up
- Triage rule engine with the initial rule set
- Daily email digest summarising the past 24h's observations + events
- No agent yet — pure rules-driven visibility

### Phase 2 — Read-only agent

- Agent invoked on Triage events
- Tools restricted to Observe tier
- Agent drafts a proposed remediation, sends as email (no auto-execute)
- User can manually act based on the proposal — builds trust before granting write access

### Phase 3 — Nudge auto + Correct gated

- Nudge tier flips to auto-execute
- Correct tier executes only on email-reply approval
- Loop detection + per-tool circuit breakers + budget caps active
- This is the first "actually self-healing" phase

### Phase 4 — Polish

- Webhook receiver replaces some polling
- Haiku 4.5 routing for routine events to drop costs
- Optional: simplarr homepage tile showing Healarr status
- Richer rules (resolution mismatch, codec issues, etc.)
- Possible: revert links in action digests

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
├── pyproject.toml
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── .gitignore
├── docs/
│   ├── architecture.md          # this file
│   ├── decisions.md             # ADR log
│   ├── tools.md                 # tool tier inventory
│   └── runbook.md               # operations runbook (populated incrementally)
├── healarr/
│   ├── __init__.py
│   ├── __main__.py
│   ├── cli.py
│   ├── config.py
│   ├── state/
│   │   ├── __init__.py
│   │   ├── schema.sql
│   │   └── store.py
│   ├── monitor/                 # one module per service
│   ├── triage/                  # rule engine
│   ├── agent/                   # Claude wiring + tools
│   └── notify/                  # email out + IMAP in
├── tests/
│   └── test_smoke.py
└── .github/workflows/ci.yml
```
