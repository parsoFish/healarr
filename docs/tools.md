# Healarr — Agent Tool Inventory

Reference for the tools exposed to the Claude agent. Each tool entry lists tier, target service, and behaviour.

Tools are grouped by tier. The dispatcher in `healarr/agent/tools.py` enforces tier policy: Observe is always available, Nudge auto-executes, Correct routes through the email-approval flow, Escalate is propose-only.

## Tier 1 — Observe (read-only, always available)

### Radarr

| Tool | Description |
|---|---|
| `get_radarr_queue()` | Current download/import queue with status per item |
| `get_radarr_history(movie_id?, limit=50)` | Recent events: grabs, downloads, imports, failures |
| `get_radarr_movie(movie_id)` | Movie details + monitored status + quality profile |
| `get_radarr_system_status()` | Version, app data, disk usage as Radarr sees it |

### Sonarr

| Tool | Description |
|---|---|
| `get_sonarr_queue()` | Current download/import queue per episode |
| `get_sonarr_history(series_id?, episode_id?, limit=50)` | Recent events |
| `get_sonarr_episode(episode_id)` | Episode details + monitored status |
| `get_sonarr_series(series_id)` | Series details |
| `get_sonarr_system_status()` | Version, app data, disk usage |

### Prowlarr

| Tool | Description |
|---|---|
| `get_prowlarr_indexers()` | All configured indexers + health flags |
| `get_prowlarr_indexer_stats(indexer_id, hours=24)` | Success/failure rate, query counts, average response time |

### Overseerr

| Tool | Description |
|---|---|
| `get_overseerr_requests(status?, limit=50)` | All / pending / available requests |
| `get_overseerr_request(request_id)` | Single-request detail with media + user info |

### qBittorrent

| Tool | Description |
|---|---|
| `get_qbit_torrents()` | All torrents with state, progress, ratio |
| `get_qbit_torrent_files(hash)` | File list inside a torrent (used to detect wrong-file-type) |
| `get_qbit_torrent_trackers(hash)` | Tracker list + status (debug stalled torrents) |

### Plex

| Tool | Description |
|---|---|
| `get_plex_libraries()` | All libraries + last-scan times |
| `get_plex_library_stats(library_id)` | Item counts, recently-added |
| `get_plex_sessions()` | Active streams (rare to need but cheap) |
| `get_plex_server_identity()` | Server hostname, version, online status |

### System

| Tool | Description |
|---|---|
| `get_disk_usage(path)` | Used/free/percent for a host path |
| `get_nfs_mount_status(mount)` | Whether the NFS mount is responsive |
| `get_container_status(name)` | Docker container state + restart count |
| `get_recent_logs(container_name, lines=100)` | Tail of a container's logs |

---

## Tier 2 — Nudge (reversible, no data loss; auto-execute)

### Radarr

| Tool | Description |
|---|---|
| `trigger_radarr_search(movie_id)` | Force a fresh search for releases |
| `refresh_radarr_movie(movie_id)` | Re-fetch metadata |
| `retry_radarr_import(queue_id)` | Re-attempt importing a stuck queue item |

### Sonarr

| Tool | Description |
|---|---|
| `trigger_sonarr_search(episode_id)` | Force a fresh search for the episode |
| `trigger_sonarr_season_search(series_id, season)` | Search a whole season |
| `refresh_sonarr_series(series_id)` | Re-fetch metadata |
| `retry_sonarr_import(queue_id)` | Re-attempt importing a stuck queue item |

### Plex

| Tool | Description |
|---|---|
| `rescan_plex_library(library_id)` | Trigger Plex to scan for new files |
| `refresh_plex_metadata(item_id)` | Update metadata on a single item |

---

## Tier 3 — Correct (destructive but reversible; email-approval gated)

### Radarr

| Tool | Description | Reversibility |
|---|---|---|
| `blocklist_radarr_release(release_id)` | Mark release as bad so Radarr won't grab it again | Manual unblocklist via UI |
| `manual_import_radarr(file_path, movie_id)` | Force-import a file Radarr otherwise wouldn't | Reverse via delete + re-search |

### Sonarr

| Tool | Description | Reversibility |
|---|---|---|
| `blocklist_sonarr_release(release_id)` | Mark release as bad | Manual unblocklist via UI |
| `manual_import_sonarr(file_path, episode_id)` | Force-import | Reverse via delete + re-search |

### qBittorrent

| Tool | Description | Reversibility |
|---|---|---|
| `delete_qbit_torrent(hash, delete_files=False)` | Remove from qBit, optionally delete on-disk files | Files unrecoverable if `delete_files=True` |
| `pause_qbit_torrent(hash)` | Stop a torrent (downloads + uploads) | Reverse via resume |
| `resume_qbit_torrent(hash)` | Start a paused torrent | n/a |

### Overseerr

| Tool | Description | Reversibility |
|---|---|---|
| `decline_overseerr_request(request_id, reason)` | Mark a request declined | Manually re-request |

---

## Tier 4 — Escalate (high blast radius; propose-only, never auto-execute)

These tools exist as schema in the agent's prompt so the model knows what shape an escalation should take, but the dispatcher refuses to execute them. Any agent reasoning that lands here results in an escalation email rather than an action.

| Tool | Why it can't auto-execute |
|---|---|
| `delete_radarr_movie(movie_id, delete_files=true)` | Removes media + metadata. User irrecoverable. |
| `delete_sonarr_series(series_id, delete_files=true)` | Same. |
| `remove_plex_library(library_id)` | Hours of scan/curation work erased. |
| `modify_arr_indexer_config(...)` | Subtle settings changes can break the whole stack. |
| `modify_qbit_settings(...)` | Same. |

---

## Adding a new tool

1. Pick a tier. If unsure, default to Correct (gated) or Escalate (propose-only).
2. Add the tool function in `healarr/agent/tools.py`. Pure function over an injected service client; returns a dict the agent can read.
3. Register it in the tool registry with the correct tier.
4. Add a unit test that exercises happy path + at least one error case.
5. Update this doc.

Tier reclassification (e.g. moving a Correct-tier tool to Nudge after operational confidence) is a config change in the registry — no code change needed.
