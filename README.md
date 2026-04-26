# 🩺 Healarr

> **A self-healing agent for Plex/*arr media stacks.**

Healarr watches your Plex + Radarr + Sonarr + Prowlarr + Overseerr + qBittorrent + NAS, detects when work-in-progress is silently failing, and uses a Claude API agent to remediate within bounded permissions.

It is **not** a Plex monitoring dashboard. It's the layer that does something when monitoring goes red.

Designed as a companion to [simplarr](https://github.com/parsoFish/simplarr) but works with any equivalent stack.

---

## What it monitors

| Service | Examples of "work going wrong" |
|---|---|
| **Radarr / Sonarr** | Import-failed states, queue items stuck for hours, history showing repeated rejection |
| **Prowlarr** | Indexer health flapping or completely down |
| **Overseerr** | Requests stuck in "Processing" with no downstream pickup |
| **qBittorrent** | Torrents stalled at 0%, completed torrents that never imported, wrong file types (your `.exe` episode) |
| **Plex** | Library scans not firing despite new files, server unreachable beyond just the HTTP port |
| **System** | Disk pressure on NAS, NFS mount disconnects, container restart loops |

## What it does about it

Two-stage pipeline:

1. **Triage** — deterministic rule engine over recent observations. Emits structured events for the small fraction of observations that actually look wrong. Cheap, fast, no LLM.
2. **Agent** — a Claude conversation invoked per event, equipped with tools scoped by risk tier:

   | Tier | Examples | Default policy |
   |---|---|---|
   | **Observe** (read-only) | get queue, get history, get torrents | Always on |
   | **Nudge** (reversible, no data loss) | trigger search, rescan library, retry import | Auto |
   | **Correct** (destructive but reversible) | blocklist release, delete torrent + files, manual import | **Email approval gated** |
   | **Escalate** (high blast radius) | delete media, modify Plex library | **Off — agent can only propose** |

Approvals work via email reply ("approve" / "yes" or "reject" / "no" as the first line). No web UI required. See [docs/architecture.md](docs/architecture.md) for the full flow.

## Quickstart (Phase 0 — scaffolding only)

```bash
git clone https://github.com/parsoFish/healarr
cd healarr
cp .env.example .env
# fill in IMAP/SMTP creds + service API URLs/keys

# Local install for development
pip install -e .[dev]
healarr --version
healarr init-db

# Or via Docker (recommended for production deploys)
docker compose up -d
docker compose logs -f
```

> **Phase 0 status**: scaffolding only. The CLI runs and the SQLite schema initialises, but the Monitor/Triage/Agent loops are stubs. See [docs/architecture.md § Phasing](docs/architecture.md#phasing) for the build plan.

## Project status

| Phase | Scope | Status |
|---|---|---|
| **0** — Scaffolding | Repo, package layout, Docker, schema, design docs | ✅ this commit |
| **1** — Monitor + Triage | Polling, rules, dashboard, email digests | ⏳ next |
| **2** — Read-only agent | Agent invoked, drafts proposals, email approvals | ⏳ |
| **3** — Write tools | Nudge auto-execute, Correct gated by approval | ⏳ |
| **4** — Polish | Webhooks, Haiku routing, simplarr homepage tile | ⏳ |

## Documentation

- [docs/architecture.md](docs/architecture.md) — system design, components, data flow, phasing
- [docs/decisions.md](docs/decisions.md) — ADR-style log of design decisions
- [docs/tools.md](docs/tools.md) — agent tool inventory by tier
- [docs/runbook.md](docs/runbook.md) — operations runbook (stub — populated as features land)

## Why a separate repo?

Healarr observes simplarr from the outside. Bundling them would conflate two surface areas:

- **simplarr** = "set up and run a media stack"
- **healarr** = "watch a media stack and fix what breaks"

Run simplarr without healarr; run healarr against any other equivalent stack. Both improve when used together.
