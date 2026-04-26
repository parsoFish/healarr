"""SQLite store: open + initialise + cursor helpers.

Phase 0 covers schema initialisation only. Higher-level CRUD wrappers
(insert_observation, fetch_open_events, etc.) are added per-table as
the consuming modules land in subsequent phases.
"""
from __future__ import annotations

import sqlite3
from importlib import resources
from pathlib import Path


def _schema_sql() -> str:
    """Return the bundled schema.sql file contents."""
    return resources.files("healarr.state").joinpath("schema.sql").read_text()


def initialise(db_path: str) -> None:
    """Create the SQLite database (if missing) and apply the schema.

    Idempotent: schema.sql uses CREATE TABLE IF NOT EXISTS throughout,
    so safe to call on every Healarr startup.
    """
    target = Path(db_path)
    target.parent.mkdir(parents=True, exist_ok=True)

    with connect(str(target)) as conn:
        conn.executescript(_schema_sql())
        conn.commit()


def connect(db_path: str) -> sqlite3.Connection:
    """Open a SQLite connection with sensible defaults for Healarr.

    - Foreign keys on (referenced from schema for future-proofing).
    - Row factory set to sqlite3.Row so callers get dict-like access.
    - Returns a context-manager-compatible connection.
    """
    conn = sqlite3.connect(db_path, isolation_level=None)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON")
    return conn
