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

## Daily operations

- **Digest email**: once Phase 3 ships, a daily digest summarises both nodes' findings and links into `/healarr/` for anything that needs a decision.
- **Decisions page**: once Phase 4 ships, staleness candidates and gated Correct-tier actions are approved or rejected from `/healarr/` on the LAN — no email reply, no IMAP.

## Common scenarios

### "`config validate` says the secrets file is unsafe"

The secrets file must be `0600` and owned by the user the agent runs as. Fix with `chmod 0600 secrets.toml` and re-run validate.

### "NAS checks fail with a Docker permission error"

The agent's DSM user isn't in the `docker` group, or was added after the current session started. Re-check group membership and log the user out/in (or reboot).

### "Plex checks fail with a certificate or connection error"

The `plex.direct` hostname in `config.toml` is stale (Plex rotates the dashed-IP segment if the server's LAN IP changes) or points at plain HTTP. Re-fetch the current hostname from Plex Settings → Network and update `config.toml`.

## Disabling Healarr quickly

`systemctl stop healarr` (Pi) or stop the DSM Task Scheduler task and kill the process (NAS). Checks pause; nothing else on the stack is affected.

## Updating

Updating is replacing the binary: build the new version (`make build-pi` / `make build-nas`), copy it over the existing one at the path above, and restart the service (`systemctl restart healarr` on the Pi; re-run the DSM boot task, or restart the process directly, on the NAS). Schema migrations in `internal/store/` are applied automatically on start.
