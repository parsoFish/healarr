# 🩺 Healarr

> **A two-node health agent for a split Plex/*arr media stack.**

Healarr watches a Pi + NAS split media stack (Radarr, Sonarr, Prowlarr, Overseerr, qBittorrent, Plex, Tautulli, Docker, host mounts), detects when work-in-progress is silently failing, and surfaces what to do about it — via a daily digest email and a small LAN-only decisions page. It is **not** a Plex dashboard; it's the layer that notices when the stack looks healthy from the outside but isn't actually doing its job.

Designed as a companion to [simplarr](https://github.com/parsoFish/simplarr) but works with any equivalent split stack.

Ships as a single Go binary — `internal/cli` for the uniform `healarr <service> <verb>` command surface, `internal/agent` for the long-running daemon on each node. See [docs/architecture.md](docs/architecture.md) for the full design and [docs/decisions.md](docs/decisions.md) for why it's built this way.

## Project status

| Phase | Scope | Status |
|---|---|---|
| **1** — CLI + clients | Uniform CLI, 9 service clients, fixtures, cross-compile CI | ✅ |
| **2** — Checks + store + report | Check registry, SQLite store, one-shot `report generate` | ⏳ |
| **3** — Daemon + peer + digest | Cron daemon, Pi↔NAS peer channel, first real digest email | ⏳ |
| **4** — Web UI + decisions | `/healarr/` dashboard/decisions page, cleanups, staleness scoring | ⏳ |
| **5** — LLM digest | Haiku 4.5 digest narrative, budget/cost log, `--no-llm` fallback | ⏳ |

## Build

Requires Go (see `go.mod` for the minimum version).

```bash
make build       # local dev build → dist/healarr
make build-pi    # CGO_ENABLED=0 GOOS=linux GOARCH=arm64 → dist/healarr-linux-arm64
make build-nas   # CGO_ENABLED=0 GOOS=linux GOARCH=amd64 → dist/healarr-linux-amd64
make test        # go test ./... -race -cover
make lint        # go vet + golangci-lint
```

Both `build-pi` and `build-nas` produce a static binary — nothing else needs to be installed on either host.

## Configuration and secrets

Config is TOML; a `[node]`-scoped `config.toml` plus a separate `secrets.toml` kept at file mode `0600`. Copy the examples and fill them in:

```bash
cp config.example.toml config.toml
cp secrets.example.toml secrets.toml
chmod 0600 secrets.toml
```

| Node | Config | Secrets | State |
|---|---|---|---|
| Pi | `/etc/healarr/config.toml` | `/etc/healarr/secrets.toml` (0600, owner `parso`) | `/var/lib/healarr/state.db` |
| NAS | `/volume1/docker/healarr/config.toml` | `/volume1/docker/healarr/secrets.toml` (0600) | `/volume1/docker/healarr/state.db` |

Each node gets its own copy of both files — `node = "pi"` or `node = "nas"` in `config.toml` decides which checks and responsibilities that instance runs (ADR-015). See [docs/runbook.md](docs/runbook.md) for first-run setup on each host.

Validate a config + secrets pair without starting anything:

```bash
healarr config validate --config /etc/healarr/config.toml
```

This loads both files, checks the secrets file's permission bits, and reports what it found — it never prints key values.

## CLI examples

Every command supports `--json` for machine-readable output and a global `--dry-run` that guarantees no mutating call is made, even on write verbs (marked `(W)` in each service's `--help`) — including `docker exec`, which honours `--dry-run` the same as every other write verb despite not mutating Docker's own state.

```bash
# Sonarr health check items, as JSON
healarr sonarr health --json

# qBittorrent torrents stuck in the "stalled" filter
healarr qbit list --filter stalled

# Kernel mount table for the host healarr is running on
healarr host mounts
```

Run `healarr --help` or `healarr <service> --help` for the full verb list per service.

## Documentation

- [docs/architecture.md](docs/architecture.md) — system design, components, data flow, phasing
- [docs/decisions.md](docs/decisions.md) — ADR-style log of design decisions
- [docs/tools.md](docs/tools.md) — future agent tool inventory by tier
- [docs/runbook.md](docs/runbook.md) — operations runbook: first-run, verification, updates

## Why a separate repo?

Healarr observes simplarr from the outside. Bundling them would conflate two surface areas:

- **simplarr** = "set up and run a media stack"
- **healarr** = "watch a media stack and fix what breaks"

Run simplarr without healarr; run healarr against any other equivalent stack. Both improve when used together.
