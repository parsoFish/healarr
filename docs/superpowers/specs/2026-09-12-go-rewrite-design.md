# Healarr Go rewrite — design spec (2026-09-12)

Status: approved by the maintainer in the 2026-09-12 planning session. Supersedes ADR-002 (Python) and ADR-005 (email-reply approvals); see `docs/decisions.md` ADR-013..016.

## Problem

The simplarr split stack (Pi runs the *arr apps, NAS runs Plex + qBittorrent, media over NFS) fails in ways liveness checks cannot see. Verified on 2026-09-12: after a power event the Pi's NFS mounts came up 21 s after Docker, so every container bind-mounted the empty local directories under the mountpoints and ran that way for ten days — host mounts looked healthy, Sonarr reported 937 missing episodes and every import failed. The NAS also accumulated ~470 GB of junk (a stray archive, a full recycle bin, fake `.exe`/`.scr` "episodes" from low-quality indexers) with nothing watching. Nothing tracked which shows nobody watches any more.

## Goals

1. One `healarr` binary that is both a uniform CLI over every service API and a long-running agent.
2. One agent per node (Pi, NAS). Each runs the checks relevant to its host, exchanges reports with its peer, and the pair produce one reconciled daily digest.
3. Deterministic checks and rules. A single Claude Haiku 4.5 call per day writes the digest narrative, reconciles the two nodes' views, and explains staleness picks. No per-event agentic loop in this iteration (kept as a later phase; tool tiers, budget caps and loop detection from `docs/architecture.md` stay the model for it).
4. NAS space management: scheduled cleanups (some automatic, some approval-gated) and a staleness score per series/movie that surfaces keep/delete decisions to the user.
5. Decisions and approvals through a LAN-only web page on the Pi, linked from the digest email. No IMAP.

## Non-goals

Unchanged from `docs/architecture.md`: not a replacement for the *arr apps, not a chat UI, not multi-tenant, not a defence against hostile releases beyond detecting and removing them.

Repo: local clone `~/sideProjects/projects/healarr` (clean, on `main` = origin). Work on branch `feat/go-rewrite`. Delete Python package + `pyproject.toml` + `Dockerfile`/`docker-compose.yml` (replace with Go build + `deploy/`); keep and amend `docs/`.

### C1. Libraries (research done — all active, pushed Sep 2026)
| Need | Lib |
|---|---|
| CLI | `github.com/spf13/cobra` |
| Sonarr/Radarr/Prowlarr | `golift.io/starr` (wrap behind own interfaces) |
| qBittorrent | `github.com/autobrr/go-qbittorrent` |
| Plex/Tautulli/Overseerr/Docker socket | raw `net/http` (Docker: `http.Transport{DialContext→unix socket}`; avoid `docker/docker` dep) |
| SQLite | `modernc.org/sqlite` v1.58 (pure Go → `CGO_ENABLED=0` cross-compile) |
| Scheduler | `github.com/robfig/cron/v3` |
| LLM | `github.com/anthropics/anthropic-sdk-go`, model `claude-haiku-4-5-20251001` |
| Config | TOML via `github.com/BurntSushi/toml`; secrets file mode 0600 validated at load |

### C2. Package layout (by feature, files <400 lines)
```
cmd/healarr/main.go                      cobra root
internal/config/                         TOML + env override; secrets file; node identity (pi|nas)
internal/clients/{sonarr,radarr,prowlarr,qbittorrent,plex,tautulli,overseerr,docker,hostfs}/
                                         Client interface + adapter + httptest fake + golden fixtures
internal/check/                          Check interface, registry, Finding/Report types, dedup
internal/checks/{mounts,queue,indexers,qbit,plex,disk,updates,requests}/  one check family per dir
internal/staleness/                      score formula (pure), candidate selection
internal/cleanup/                        recycle-bin / orphan / seeded-torrent / docker-prune actions (dry-run first)
internal/decision/                       keep/delete/approve/reject executor (arr delete-with-files, overseerr sync)
internal/store/                          modernc sqlite, schema.sql, numbered migrations
internal/peer/                           HTTP server+client, bearer token, message types
internal/agent/                          daemon: cron, orchestration, digest reconciliation, guards
internal/notify/                         msmtp shell-out, digest templates
internal/llm/                            single daily Haiku call, cost log, templated fallback
internal/web/                            html/template + minimal JS; dashboard, decisions, history
deploy/systemd/healarr.service           Pi unit (User=parso, After=docker.service)
deploy/dsm/healarr-boot.sh               NAS Task Scheduler boot-up script (absolute paths, no $HOME)
deploy/nginx/healarr.conf.snippet        `location /healarr/ { proxy_pass http://<pi-ip>:8090/; }` for simplarr split.conf
```
Interfaces: `Client` per service (own types, not starr's); `Check{ID, Node, Tier, Cadence, Run(ctx, Deps) ([]Finding, error)}`; `Finding{ID, CheckID, Node, EntityKey, Severity, Tier, Summary, Detail, Data, FirstSeen, LastSeen}`; `Report{Node, GeneratedAt, Findings, ChecksRun, ChecksFailed}`; `Store`, `Notifier`, `Peer{PushReport, FetchLatest, SendDecision, Heartbeat}`.

CLI surface (`--json` everywhere): `healarr <service> <verb>` (e.g. `sonarr queue|health|wanted|series|delete-series`, `qbit list|files|delete`, `plex identity|libraries|recently-added`, `tautulli history`, `overseerr requests`, `docker ps|logs|df`, `host mounts|disk`), `healarr check run [--all|--id] [--dry-run]`, `healarr report generate`, `healarr staleness score`, `healarr cleanup <kind> [--dry-run]`, `healarr decide keep|delete <entity> [--snooze 60d]`, `healarr agent serve`, `healarr config validate`.

### C3. Store (SQLite; SD-card-wear aware)
- Keep `remediations` (was `actions`), `email_outbox`, `schema_meta`. Drop `observations` (raw snapshots = write churn), `proposals`, `email_inbox`, IMAP bits.
- Add `reports`, `findings` (dedup on `check_id+entity_key` while `status ∈ open|snoozed`), `decisions` (`pending|executed|failed`, `snooze_until`), `peer_messages`, `staleness_scores`, `llm_calls`.
- `PRAGMA journal_mode=WAL, synchronous=NORMAL`; batch writes per cycle; nightly checkpoint. Pi DB at `/var/lib/healarr/state.db`, NAS at `/volume1/docker/healarr/state.db`.

### C4. Two-node topology + peer protocol
- **Pi = primary**: hosts web UI (`:8090`, LAN bind), sends email, owns staleness + decisions, reconciles both reports. **NAS = secondary**: runs NAS checks, executes NAS-side actions on Pi's command.
- **HTTP over LAN + shared bearer token** (secrets file). NFS-shared-file rejected (coordination over the mount whose failure we detect); SSH rejected (key mgmt, no structured RPC).
- Endpoints: `POST /v1/report` (idempotent upsert), `GET /v1/report/latest`, `POST /v1/decision` (Pi→NAS execute e.g. qbit delete), `POST /v1/heartbeat`. Timeouts 5 s, 2 retries; unreachable peer ⇒ digest says "peer stale since …", never blocks.
- Daily digest at 07:00 local: Pi merges own + NAS findings, staleness candidates, cleanup dry-run counts, pending decisions → LLM narrative → email with `/healarr/` links.

### C5. Check catalogue
| id | node | auto tier | cadence |
|---|---|---|---|
| `mount_race` — container-side `stat -c %d` / file count vs host mount | pi | Nudge: alert; **Correct**: compose restart (gated until trusted) | 5 min |
| `host_mount_health` | both | Observe | 5 min |
| `arr_health` (`/api/v3/health`) | pi | Observe | 5 min |
| `arr_queue_stuck` / failed import | pi | Nudge: retry import | 15 min |
| `arr_wanted_missing_spike` (Δ vs yesterday) | pi | Observe | daily |
| `indexer_failures` (Prowlarr status) | pi | Observe | 15 min |
| `qbit_stalled_errored` | nas | Nudge: reannounce/resume | 15 min |
| `qbit_completed_not_imported` (done in qbit, absent in arr history) | nas | Correct | 15 min |
| `wrong_file_type` (exe/scr/bat/lnk/msi/iso-in-tv) | nas | Correct: delete torrent+files | 15 min |
| `plex_reachability` (HTTPS plex.direct + token) | nas | Observe | 5 min |
| `plex_scan_freshness` (recently-added vs newest file) | nas | Nudge: trigger scan | daily |
| `tautulli_reachability`, `overseerr_stuck_processing` | pi | Observe | daily |
| `disk_pressure_nas_volume` (>85 % warn, >92 % crit) | nas | Correct: cleanup run | hourly |
| `disk_pressure_pi_sd`, `docker_image_bloat`, `log_size` | pi | Nudge: prune dangling, vacuum journal | daily |
| `recycle_bin_size` | nas | Correct: empty | daily |
| `orphan_downloads` (not in qbit, not in arr history, > N days) | nas | Correct: delete | daily |
| `seeded_done` (ratio/time met, imported) | nas | Nudge: remove torrent keep files → then orphan rule | daily |
| `service_update_available` | both | Observe | daily |
| `staleness_scan` | pi | Escalate → web decision | daily |

### C6. Staleness score (0–100, ≥70 candidate, 50–69 watch-list, <50 suppressed)
| Component | Rule | Points |
|---|---|---|
| Days since last watch (Tautulli) | linear 0→40 over 0–180 d; never watched & added >30 d | ≤40 |
| Watch completion | fully watched +15; partially watched −10 |  |
| Arr status | ended / nothing monitored upcoming +10; continuing & monitored −15 |  |
| Size | GB/10 capped | ≤15 |
| Overseerr requester | other user & unwatched +10; owner −5 |  |
| Age since added | 0.05/day capped | ≤10 |
Delete path: web click → `decisions.pending` → Sonarr/Radarr delete `deleteFiles=true` (+ `addImportListExclusion`) → Overseerr request decline (best effort) → peer notified → `executed`. Keep → `snooze_until = now+60d`. Weights live in config, not code.

### C7. Web UI (Pi, LAN-only)
`html/template`, no build step. Pages: `/` dashboard (latest digest, node heartbeats, disk gauges), `/decisions` (staleness candidates + gated Correct actions: keep/delete/approve/reject), `/history`. Auth: session cookie from a token in secrets; nginx `location /healarr/` added to `simplarr/nginx/split.conf`.

### C8. Implementation phases (each: TDD, `go test ./... -cover ≥80 %`, conventional commits, PR per phase)
1. **Skeleton + clients**: go.mod, cobra, config, all 9 client packages with fakes + golden fixtures captured from the live stack (`healarr sonarr queue --json` against Pi). CI: `go vet`, `golangci-lint`, test, cross-compile matrix. Docs: ADR-013/014/015, README rewrite, `.env.example` → `config.example.toml`.
2. **Checks + store + one-shot report**: schema, store, check registry, every C5 check as pure function with table tests (mount-race fixture from A1), `healarr check run --all`, `healarr report generate --dry-run`.
3. **Daemon + peer + digest email**: cron, peer server/client, reconciler, msmtp notifier, `deploy/` units, install both nodes, first real digest.
4. **Web UI + decisions + cleanups + staleness**: pages, executor, cleanup actions promoted from dry-run, staleness scorer (ADR-016), nginx snippet PR to simplarr.
5. **LLM digest**: Haiku wrapper, budget/cost log, templated fallback, `--no-llm`.

### C9. Risks
Plex HTTPS quirk (probe via plex.direct + token, `InsecureSkipVerify=false` with SNI); qBit session refresh on 403 or subnet whitelist for 192.0.2.11; DSM Task Scheduler minimal env; docker-group grant needs re-login; SD wear (no raw snapshots, WAL, batched writes); Overseerr eventual consistency; capture fixtures before version bumps.

---


## Deployment

- **Pi**: `/usr/local/bin/healarr`, config `/etc/healarr/config.toml`, secrets `/etc/healarr/secrets.toml` (0600, owner parso), state `/var/lib/healarr/`. systemd unit `healarr.service` (`User=parso`, `After=docker.service network-online.target`). Web UI on `:8090` bound to the LAN address; simplarr nginx proxies `/healarr/`.
- **NAS**: `/volume1/docker/healarr/{healarr,config.toml,secrets.toml,state.db}`. DSM Task Scheduler "boot-up" task as `lyndor` runs `deploy/dsm/healarr-boot.sh`, which execs the daemon with absolute paths. Peer listener on `:8090` LAN-only. Requires `lyndor` in the DSM `docker` group.
- (Ports per config defaults: peer :8090, web :8091.)
- Cross-compiled from the dev machine: `GOOS=linux GOARCH=arm64|amd64 CGO_ENABLED=0`. GitHub Actions builds both on every PR; releases attach binaries.

## Testing

- Unit tests per package with `httptest` fakes; golden JSON fixtures captured from the live stack under `internal/clients/<svc>/testdata/`.
- Rule engine: table tests per check, including a fixture reproducing the 2026-09-12 mount race (container reports the root device and zero files while the host mount is healthy).
- Store: in-memory SQLite per test.
- Integration smoke (manual, documented in runbook): `healarr check run --all --dry-run` on each node; peer heartbeat via curl; forced LLM failure must still produce a templated digest.
- Coverage target ≥ 80 % on non-trivial packages.
