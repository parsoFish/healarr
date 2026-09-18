# Healarr — Operations Runbook

Populated incrementally as features ship. Keep this short and operational.

Healarr ships as a single static binary — no interpreter, no container, nothing to install beyond the file itself (ADR-013). Each node (Pi, NAS) runs its own copy with its own `config.toml` and `secrets.toml`.

## First-run setup — Pi

1. Build or download `healarr-linux-arm64` and copy it to the Pi as `/usr/local/bin/healarr`; `chmod +x`.
2. Create `/etc/healarr/config.toml` from `config.example.toml` and `/etc/healarr/secrets.toml` from `secrets.example.toml`. Set `node = "pi"`.
3. `chmod 0600 /etc/healarr/secrets.toml` — the config loader refuses to start if the secrets file is group- or world-readable.
4. Fill in the Pi-side service blocks: Sonarr/Radarr/Prowlarr/Overseerr/Tautulli URLs, API keys (either inline in `secrets.toml` or via `api_key_file` pointing at simplarr's existing service config), the Docker socket path, and the mount list under `[[mounts]]`.
5. Verify: `healarr config validate --config /etc/healarr/config.toml`.
6. Verify service reachability directly, e.g. `healarr sonarr health --json`, `healarr docker ps`, `healarr host mounts`.
7. Install the systemd unit: copy `deploy/systemd/healarr.service` to `/etc/systemd/system/`, `systemctl daemon-reload`, `systemctl enable --now healarr`. The unit runs as `User=parso` and starts `After=docker.service network-online.target`.
8. Add the nginx snippet (`deploy/nginx/healarr.conf.snippet`) to simplarr's `split.conf` so `/healarr/` proxies to the agent's web port once Phase 4 ships.

## First-run setup — NAS (Synology DSM)

1. **Prerequisite**: add the agent's DSM user to the `docker` group (Control Panel → User & Group → the account → Group tab). DSM does not apply group membership to already-open sessions — log the user out and back in (or reboot) before the agent will have access to the Docker socket.
2. Copy `healarr-linux-amd64` to `/volume1/docker/healarr/healarr`; `chmod +x`.
3. Create `/volume1/docker/healarr/config.toml` and `/volume1/docker/healarr/secrets.toml` from the examples. Set `node = "nas"`.
4. `chmod 0600 /volume1/docker/healarr/secrets.toml`.
5. Fill in the NAS-side blocks:
   - **qBittorrent**: `qbit_user` / `qbit_pass` go in `secrets.toml`, never in `config.toml`.
   - **Plex**: the `url` must be Plex's own HTTPS `plex.direct` hostname (the one Plex issues per-server, e.g. `https://<dashed-ip>.<hash>.plex.direct:32400`), not a plain `http://` LAN address — Plex refuses unencrypted requests from outside `localhost` and a bare IP won't match the certificate Plex presents. Find it under Plex Settings → Network, or `healarr plex identity` once a token is set.
   - `plex_token` goes in `secrets.toml`.
6. Verify: `healarr config validate --config /volume1/docker/healarr/config.toml`.
7. Verify service reachability: `healarr qbit list`, `healarr plex identity`, `healarr host mounts` (confirms the NFS/shared volume the NAS side cares about is actually mounted, not just present in fstab).
8. Add a DSM Task Scheduler "boot-up" task running as the same user, executing `deploy/dsm/healarr-boot.sh`. The script uses absolute paths throughout — DSM boot-time tasks run with a minimal environment and no `$HOME`.

### Docker API note

Healarr's Docker checks call the Engine API directly over the Unix socket using unversioned paths (`/containers/json`, not `/v1.41/containers/json`) so they work across the range of daemon versions DSM and Raspberry Pi OS ship. The Pi daemon in this deployment supports API ≤1.41; if a check starts failing after a host OS upgrade, check the daemon's negotiated API version first (`docker version --format '{{.Server.APIVersion}}'`).

## Verifying a running install

- `healarr config validate --config <path>` — confirms both files parse, secrets permissions are correct, and lists which services/mounts were found (never prints key values).
- `healarr docker ps` — confirms the Docker socket is reachable and shows what's actually running, independent of what simplarr's own compose state thinks.
- `healarr host mounts` — confirms the host's mount table looks like what's expected; this is the check that would have caught the 2026-09-12 mount-race incident (see `docs/superpowers/specs/2026-09-12-go-rewrite-design.md`).

## Running checks by hand

Phase 2 shipped the check engine (`internal/check` + `internal/checks/*`) and the SQLite store (`internal/store`), and the CLI verbs below. There is no scheduler yet — that's Phase 3's cron daemon; until it ships, run checks and generate the digest by hand (e.g. from cron or a terminal).

### The catalogue

`healarr check list [--json]` prints every registered check: id, node(s), tier, and cadence (the interval Phase 3's daemon will use — the CLI itself does not schedule anything). The catalogue is 21 checks, each applying to `pi`, `nas`, or both:

| Check ID | Node(s) | Cadence |
|---|---|---|
| `mount_race` | pi | 5m |
| `host_mount_health` | pi, nas | 5m |
| `arr_health` | pi | 5m |
| `arr_queue_stuck` | pi | 15m |
| `arr_wanted_missing_spike` | pi | 24h |
| `indexer_failures` | pi | 15m |
| `qbit_stalled_errored` | nas | 15m |
| `qbit_completed_not_imported` | nas | 15m |
| `wrong_file_type` | nas | 15m |
| `plex_reachability` | nas | 5m |
| `plex_scan_freshness` | nas | 24h |
| `tautulli_reachability` | pi | 24h |
| `overseerr_stuck_processing` | pi | 24h |
| `disk_pressure_nas_volume` | nas | 1h |
| `disk_pressure_pi_sd` | pi | 24h |
| `docker_image_bloat` | pi | 24h |
| `log_size` | pi | 24h |
| `recycle_bin_size` | nas | 24h |
| `orphan_downloads` | nas | 24h |
| `seeded_done` | nas | 24h |
| `service_update_available` | pi, nas | 24h |

(`staleness_scan` from the spec's C5 table is intentionally not here — it's Phase 4 scope, alongside the staleness scorer of ADR-016.)

### Running checks and generating the digest

- `healarr check run (--all | --id <check-id>) [--dry-run] [--json]` — exactly one of `--all`/`--id` is required. `--all` runs every check that applies to this node (from `config.toml`'s `node = "pi"|"nas"`); `--id` runs one, and errors if that check doesn't apply to this node.
- `healarr report generate [--dry-run] [--json]` — renders the plain-text digest (or, with `--json`, the digest's structured input).

A check that errors or is skipped (its dependency isn't configured) is data, not a CLI failure — `check run`'s exit code stays 0 and the run continues; only a config-load or store failure returns non-zero.

```bash
# see the catalogue
healarr check list --json

# run everything for this node, look but don't touch the store
healarr check run --all --dry-run --json

# run one check for real (persists to the store)
healarr check run --id qbit_stalled_errored

# render the digest from the store's currently-open findings
healarr report generate
```

### `--dry-run` semantics

`--dry-run` is a global flag (`healarr --dry-run check run --all` and `healarr check run --all --dry-run` are equivalent) and for `check run`/`report generate` it means **the run never opens or writes the state database**:

- `check run --dry-run` never calls `store.Open`, never reads a previous report, and never persists this one. `check.Deps.Previous` stays `nil`, so delta checks that compare against yesterday's number (e.g. `arr_wanted_missing_spike`, which reads `Deps.PreviousMetric`) see no baseline and simply don't fire. `--json` output always shows `"persisted": false` and an all-zero `"upsert": {"new": 0, "updated": 0, "resolved": 0}`.
- `report generate --dry-run` runs every check for the node in memory and renders the digest straight from that in-process report — it never calls `LatestReport` or `OpenFindings`.

### State DB location, dedup, and resolve

The store is one SQLite file at `[state] db_path` (see the per-node paths in the README's configuration table; default `/var/lib/healarr/state.db`). `store.Open` creates the file if missing, sets `PRAGMA journal_mode=WAL`, `synchronous=NORMAL`, a 5s `busy_timeout`, and applies any pending embedded migration — `internal/store/migrations/0001_init.sql` creates every table Phase 2's design calls for (`schema_meta`, `reports`, `findings`, `remediations`, `decisions`, `peer_messages`, `staleness_scores`, `llm_calls`, `email_outbox`) up front, so later phases add columns, not tables. Only one connection is ever opened, so there's no multi-writer contention to reason about on the Pi's SD card.

Each non-dry-run `check run` commits one transaction:

1. Inserts the run's `reports` row.
2. For every finding the run produced, upserts on `(node, check_id, entity_key)`: an existing `open`/`snoozed` row gets `last_seen`, `severity`, `summary`, `detail`, `data` refreshed and `seen_count` bumped; otherwise a new `open` row is inserted.
3. For every check id that **ran successfully** this cycle, resolves (`status = 'resolved'`) any `open`/`snoozed` finding for that `(node, check_id)` whose `entity_key` wasn't reported this run.

A check that errored or was skipped never resolves anything under its own id — a transient failure (Sonarr briefly unreachable) can't make that check's existing findings disappear just because it didn't run. `check run --json`'s `"upsert"` block reports the `{new, updated, resolved}` counts from that transaction. `report generate` (no `--dry-run`) reads back `store.OpenFindings` (status `open`/`snoozed`, sorted severity desc, check id, entity key) plus the node's `LatestReport` for the summary counts, and renders the same plain-text template Phase 3's cron job will eventually mail.

## Daily operations

- **Digest email**: since Phase 3, a daily digest at `email.digest_at` (default `07:00` local) summarises both nodes' findings; see "Daemon operations" below. Ad hoc, `healarr report generate` still produces the same digest text by hand for this node alone (see above).
- **Decisions page**: once Phase 4 ships, staleness candidates and gated Correct-tier actions are approved or rejected from `/healarr/` on the LAN — no email reply, no IMAP. Phase 3's peer channel already carries a `decisions` endpoint, but nothing acts on it yet (ADR-018) — it only records what's received.

## Daemon operations

Phase 3 added the long-running daemon, `healarr agent serve`, which both nodes run instead of
invoking `check run`/`report generate` by hand. It is **observe-only**: every check cycle,
heartbeat, and peer push it runs persists a report or a peer message and (on the Pi) mails a
digest, but nothing in this phase blocks a torrent, deletes a file, or otherwise remediates
anything — that's Phase 4.

### Lifecycle

- **Pi (systemd)**: `sudo systemctl enable --now healarr` to start at boot; `sudo systemctl stop healarr` / `sudo systemctl restart healarr` day to day. See [`deploy/README.md`](../deploy/README.md) for the unit file and update procedure.
- **NAS (DSM Task Scheduler)**: a "Boot-up" task runs `deploy/dsm/healarr-boot.sh`, which is idempotent — it checks `agent.pid` via `kill -0` and logs + exits 0 rather than starting a second copy if the agent is already up. It also verifies the copy it just started: a daemon that exits within a second (bad config, missing binary) makes the task fail with `healarr failed to start; see /volume1/docker/healarr/agent.log` and writes no PID file, so DSM's task history shows the failure instead of a clean run. Stop with `ssh nas 'kill "$(cat /volume1/docker/healarr/agent.pid)"'`; restart by re-running the Task Scheduler task by hand (Task Scheduler → select the task → Run) — that's exactly what a reboot does.
- Both launchers ultimately run `healarr agent serve --config <path>`. `agent serve` rejects `--dry-run` outright with `"agent serve does not support --dry-run; the daemon is observe-only in this phase"` — every other verb's `--dry-run` means "run in memory," but the daemon's ordinary job is to persist reports, push peer traffic, and send mail, so honouring `--dry-run` there would mean silently running a daemon that never does its job.
- On start, `agent serve` runs one check cycle immediately (the 5-minute cadence's checks) before the cron scheduler's own first tick fires, so a restart doesn't leave the node's data stale for up to 5 minutes.
- Shutdown is graceful on SIGINT/SIGTERM: the cron scheduler waits for any in-flight job to finish, and the peer HTTP server (when `peer.listen_addr` is set) waits up to 5s for in-flight requests before its listener closes.

### Logs

- **Pi**: `journalctl -u healarr -f` (add `--since today` to scope it).
- **NAS**: `ssh nas tail -f /volume1/docker/healarr/agent.log` — there's no `journalctl` on DSM; this is the boot script's own `nohup` redirect.
- Normal operation is logged, not just failures: one `agent: starting` line per start (node, version, timezone, peer listen address, whether the peer/heartbeat/digest are wired, `digest_at`, `checkpoint_at`, and the number of cron entries registered), then one line per completed check cycle (cadence, checks run/failed, findings, report id, whether the push to the peer succeeded), per digest sent (outbox id, subject) and per nightly checkpoint (rows pruned). A node that is up but silent between these lines is a node whose scheduler isn't firing.
- Every scheduled job logs its own failures through the daemon's structured logger rather than crashing it: a panicking job is recovered and logged (`cron.Recover`), and an overlapping run of the same job is skipped and logged rather than left to pile up (`cron.SkipIfStillRunning`).

### Schedule

Every job below runs in `agent.timezone` (an IANA name; `""`, the default, uses the host's own local timezone):

| Job | Spec | Notes |
|---|---|---|
| 5-minute check cycle | `*/5 * * * *` | also runs once immediately when `agent serve` starts |
| 15-minute check cycle | `*/15 * * * *` | |
| Hourly check cycle | `0 * * * *` | |
| Daily check cycle | `10 0 * * *` (00:10 local) | deliberately ahead of both the checkpoint and the digest |
| Heartbeat | every `agent.heartbeat_interval` (default `5m`) | only when this node has a peer configured |
| Daily digest | `email.digest_at` (default `07:00` local) | Pi only — the node with a mail sender configured |
| Nightly checkpoint | `agent.checkpoint_at` (default `03:00` local) | both nodes; prunes `peer_messages` older than `agent.peer_message_retention` (default `720h`) and then checkpoints the WAL. `ErrCheckpointBusy` (another connection pinning the WAL) is logged at Warn and just retried the next night, not treated as a failure; a failed prune is logged and never costs the night its checkpoint |

00:10, 03:00, and 07:00 are ordered on purpose: the daily checks run first, the checkpoint runs against a database that isn't mid-write from them, and the digest mails a report the checkpoint has already run against.

### Digest email

Only the Pi sends mail — it's the only node with `email.to`/`from`/`msmtp_path` configured and a sender wired in. The digest merges this node's own latest report and open findings with the peer's contribution, and the peer half comes entirely from what the NAS has already **pushed** into the Pi's own store (`peer_messages` + `findings` under `node = "nas"`) — there is no read-time pull baked into the digest itself. Three states, depending on what the Pi's store holds for the peer:

- **Fresh**: the NAS pushed a report within `agent.peer_stale_after` (default `15m`) — rendered the same as this node's own section.
- **Stale**: `PEER (nas)` / `peer nas stale since <timestamp>` — the last pushed report is older than `agent.peer_stale_after`.
- **Never received**: `PEER (nas)` / `peer nas: no report received` — this node has never received a report from the peer channel at all (e.g. a brand-new install, or the NAS has never come up).

A digest whose send fails is marked `failed` in `email_outbox` (with the error text) and is **not** retried until the next day's `email.digest_at` — there is no outbox drain loop in this phase, so a morning with no digest means checking `journalctl -u healarr` for the failure, or the `email_outbox` table for the row that recorded it. A Pi that cannot send at all (no `email.to`, or no `msmtp_path`/`from`) logs `digest disabled: …` at Warn once at start-up and registers no digest job.

`healarr notify test [--to <addr>]` sends one short test email through the Pi's real sender immediately, without waiting for `email.digest_at` — useful for confirming msmtp/`~/.msmtprc` works before trusting the schedule. `--dry-run` sends without touching the outbox and prints `"sent (not recorded)"`; without it, the message is enqueued, sent, and marked sent/failed in the outbox (printing `"outbox id: <n>"`), the same bookkeeping the scheduled digest itself does. On a node with no sender configured (the NAS), it fails fast with `"email is only configured on the pi node"` rather than silently doing nothing.

### Peer channel troubleshooting

`healarr peer ping [--json]` heartbeats the configured peer and fetches its latest pushed report — a real heartbeat, **including under `--dry-run`**: the whole point of the verb is to prove the channel works, and a ping that skipped the request would only prove the flag was honoured. (Nothing local is written either way: `peer ping` never touches the store.) It always exits 0 — it's a diagnostic, not a health gate — printing reachable/unreachable, any error text, and, when a report exists, its age in seconds plus checks-run/checks-failed/findings counts. With no `peer.peer_url` configured it fails fast (non-zero exit) with `"peer_url is not configured"` before attempting a request; the peer client itself retries a transport error or 5xx twice (500ms then 1s backoff) before giving up, so a `peer ping` can take a few seconds to report "unreachable" against a genuinely dead peer.

- **401 from the peer** → the two nodes' `peer_token` values (each node's own `secrets.toml`) don't match. The peer server compares the bearer token in constant time and rejects anything else with a bare 401 — there's no partial-credit response to read tea leaves from. Fix by copying the same token into both `secrets.toml` files and restarting both daemons; per ADR-018, a token rotation always needs a restart on each side, since both the server and the client capture it once at startup.
- **Digest says `"peer nas stale since …"`** (or `pi`, from the NAS's own digest, once it sends one) → the other node hasn't pushed a **report** inside `agent.peer_stale_after` (a heartbeat alone doesn't reset this — only an inbound report does). Check, in order: is its daemon actually running (`systemctl status healarr` on the Pi; `kill -0 "$(cat agent.pid)"` on the NAS)? Did its DSM boot task fire on the last reboot (Task Scheduler → the task → check run history; re-run it by hand if it didn't)? Is its `peer.listen_addr` reachable from this node at all (`peer ping` from the other side, or a plain `curl` to it)? This is a status message, never a failure — an unreachable peer never blocks the digest from sending.
- **`peer ping` reports "unreachable"** with a transport-style error → the peer's listener isn't bound (process down) or something on the LAN between the two hosts is blocking it. A 401/403 in the same error text specifically means a token mismatch (see above), not a network problem — don't chase a firewall rule for that one.

### Phase 2 leftover: the `wrong_file_type` inspect-errors finding

Unrelated to the daemon, but worth knowing before it shows up in a daemon-driven digest: `wrong_file_type` (nas, 15m) rolls every torrent whose files qBittorrent couldn't describe this run (a magnet whose metadata never arrived, a torrent removed mid-run) into a single warn/observe finding at entity key `qbit:wrong_file_type:inspect-errors`, rather than emitting one per torrent or letting one bad torrent hide fake releases in the rest of the run's findings. It's informational — check qBittorrent if it persists across cycles — and resolves itself once a later cycle can describe those torrents again (or they've been removed).

## Common scenarios

### "`config validate` says the secrets file is unsafe"

The loader checks the secrets file's permission bits and refuses to start if it is group- or world-readable (anything but `0600`); it does not check file ownership. Fix with `chmod 0600 secrets.toml` and re-run validate.

### "NAS checks fail with a Docker permission error"

The agent's DSM user isn't in the `docker` group, or was added after the current session started. Re-check group membership and log the user out/in (or reboot).

### "Plex checks fail with a certificate or connection error"

The `plex.direct` hostname in `config.toml` is stale (Plex rotates the dashed-IP segment if the server's LAN IP changes) or points at plain HTTP. Re-fetch the current hostname from Plex Settings → Network and update `config.toml`.

## Disabling Healarr quickly

`systemctl stop healarr` (Pi) or stop the DSM Task Scheduler task and kill the process (NAS). Checks pause; nothing else on the stack is affected.

## Updating

Updating is replacing the binary: build the new version (`make build-pi` / `make build-nas`), copy it over the existing one at the path above, and restart the service (`systemctl restart healarr` on the Pi; re-run the DSM boot task, or restart the process directly, on the NAS). Schema migrations in `internal/store/` are applied automatically on start.
