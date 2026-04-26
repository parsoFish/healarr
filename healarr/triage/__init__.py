"""Triage — deterministic rule engine.

Reads recent observations, emits structured Events for the small subset
that look anomalous. This is the cost gate: most monitor cycles produce
zero events and the agent never runs.

Phase 1 lands:
  rules.py   — initial rule set (wrong_file_suspect, stuck_torrent,
               orphaned_request, disk_pressure, nfs_disconnect,
               indexer_degraded, plex_scan_stuck, service_unhealthy)
  events.py  — Event dataclass + event_type registry + dedup keys
"""
