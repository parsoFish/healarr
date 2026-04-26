"""Monitor — the polling layer.

One module per upstream service, each emitting raw observations to the
state store. Knows nothing about "issues" — purely fact-recording.

Phase 1 lands:
  radarr.py    — queue + history + system status
  sonarr.py    — queue + history + system status
  prowlarr.py  — indexer list + per-indexer health
  overseerr.py — requests + statuses
  qbit.py      — torrents + files + trackers
  plex.py      — libraries + scan times + sessions
  system.py    — disk usage, NFS probes, container status

Phase 4 adds a webhook receiver for fast-path events from arr services.
"""
