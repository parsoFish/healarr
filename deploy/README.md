# Deploy

Install files for the two nodes healarr runs on. Both run the same binary as a long-running
daemon (`healarr agent serve --config <path>`, added in Phase 3) — only the launcher differs,
because the Pi has systemd and the NAS (Synology DSM) does not.

For first-run setup (config, secrets, mounts, Docker group membership) see
[`docs/runbook.md`](../docs/runbook.md). This README only covers installing and operating the
daemon launcher itself.

## Pi — systemd

`systemd/healarr.service` runs `healarr agent serve` as `User=parso`, after the network and
Docker are up, and refuses to start until the NAS mounts it depends on
(`/mnt/nas/{tv,movies,downloads}`) are present (`RequiresMountsFor`).

```bash
sudo cp deploy/systemd/healarr.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now healarr
```

`StateDirectory=healarr` has systemd create `/var/lib/healarr` (owned by `User=`) before the
daemon starts, so a first run on a fresh host doesn't fail on a missing state directory —
`store.Open` creates the database file but never its parent.

The mount paths, `User`, and `Environment=TZ=...` are the simplarr defaults for this host. If
your Pi's mounts, user, or timezone differ, edit `/etc/systemd/system/healarr.service` (not the
copy in this repo) before enabling it, then `sudo systemctl daemon-reload`.

**Logs**: `journalctl -u healarr -f` (add `--since today` to scope it). **Status**:
`systemctl status healarr`.

**Stop / restart**:

```bash
sudo systemctl stop healarr        # pause checks; nothing else on the stack is affected
sudo systemctl restart healarr     # e.g. after editing config.toml
```

**Updating the binary**:

```bash
sudo systemctl stop healarr
sudo cp /usr/local/bin/healarr /usr/local/bin/healarr.prev   # keep the last-known-good copy
sudo cp dist/healarr-linux-arm64 /usr/local/bin/healarr
sudo systemctl start healarr
```

Roll back by copying `healarr.prev` back over `healarr` and restarting. Schema migrations in
`internal/store/` apply automatically on start, so an update never needs a separate migration
step. `RestartSec=10` means a crashing binary retries every 10s rather than hammering the host or
giving up.

## NAS — DSM Task Scheduler

DSM has no systemd, so `dsm/healarr-boot.sh` is a plain shell launcher: it backgrounds the agent
with `nohup`, waits a second and checks (`kill -0`) that the process it started is still alive
before recording its PID in `agent.pid`, and is idempotent — running it while the agent is already
up (checked the same way on the recorded PID) logs that and exits 0 instead of starting a second
copy. A daemon that exits immediately (bad config, missing binary) therefore fails the task with
`healarr failed to start; see /volume1/docker/healarr/agent.log` and leaves no stale PID file,
rather than being reported as a clean boot. All paths inside it are absolute; DSM boot-time tasks
run with a minimal environment and no `$HOME`.

```bash
scp dist/healarr-linux-amd64 nas:/volume1/docker/healarr/healarr
scp deploy/dsm/healarr-boot.sh nas:/volume1/docker/healarr/healarr-boot.sh
ssh nas chmod +x /volume1/docker/healarr/healarr /volume1/docker/healarr/healarr-boot.sh
```

**The DSM Task Scheduler task itself has to be created by the operator in the DSM UI** — there is
no CLI or file-based way to register it:

> Control Panel → Task Scheduler → Create → Triggered Task → User-defined script
> Event: **Boot-up**
> User: the agent user (same one that owns `/volume1/docker/healarr` and is in the `docker` group)
> Script: `sh /volume1/docker/healarr/healarr-boot.sh`

Run the task once by hand after creating it (Task Scheduler → select it → Run) to confirm it
starts cleanly before relying on the next reboot.

**Logs**: `/volume1/docker/healarr/agent.log` (the script's own `nohup` redirect — there is no
`journalctl` on DSM). Tail it with `ssh nas tail -f /volume1/docker/healarr/agent.log`.

**Stop**:

```bash
ssh nas 'kill "$(cat /volume1/docker/healarr/agent.pid)"'
```

**Restart**: stop it as above, then re-run the Task Scheduler task by hand (Task Scheduler →
select the task → Run), which is exactly what a reboot does.

**Updating the binary**:

```bash
ssh nas 'cp /volume1/docker/healarr/healarr /volume1/docker/healarr/healarr.prev'
scp dist/healarr-linux-amd64 nas:/volume1/docker/healarr/healarr
ssh nas 'kill "$(cat /volume1/docker/healarr/agent.pid)"'
```

then re-run the Task Scheduler task. `healarr.prev` is kept alongside the live binary so a bad
update can be rolled back by copying it back over `healarr` and restarting.

## nginx (`/healarr/`)

`deploy/nginx/healarr.conf.snippet` proxies simplarr's nginx to healarr's web UI on the Pi. The
location block goes **inside the existing `server {}` block** of simplarr's split config
(e.g. `docker/simplarr/split.conf`).

**Setup**:

1. Back up the config: `sudo cp docker/simplarr/split.conf docker/simplarr/split.conf.bak`
2. Open `docker/simplarr/split.conf` and add the snippet from `deploy/nginx/healarr.conf.snippet`
   inside the `server {}` block (e.g. at the end, before the closing `}`), replacing `192.0.2.20`
   with your Pi's LAN address.
3. Test the config: `docker exec simplarr_nginx_1 nginx -t` (adjust container name if needed).
   If valid, it prints `nginx: the configuration file ... is OK`.
4. Reload nginx: `docker exec simplarr_nginx_1 nginx -s reload` (or full restart:
   `docker restart simplarr_nginx_1`).
5. Re-run the stack health probe to confirm healarr is reachable via `/healarr/`.

**Verify access**:

```bash
# Without token → 302 redirect to login
curl -s -o /dev/null -w '%{http_code}' http://<pi>/healarr/

# With token → 200 OK
curl -s -o /dev/null -w '%{http_code}' http://<pi>/healarr/?token=<web_token>
```

The same snippet should be contributed to the simplarr repository as a PR, so other users of the
stack can pull it in a future simplarr release.

## Local verification

Checked when these files were added:

```bash
sh -n deploy/dsm/healarr-boot.sh          # syntax check — passed
shellcheck deploy/dsm/healarr-boot.sh     # passed clean, no warnings
systemd-analyze verify deploy/systemd/healarr.service   # unit syntax valid; only complains
                                                          # that /usr/local/bin/healarr doesn't
                                                          # exist on this dev machine, which is
                                                          # expected off-target
```
