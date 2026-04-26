"""Smoke tests for Phase 0 scaffolding.

These verify the bare-minimum invariants: package imports, CLI runs,
SQLite schema applies. They run in CI and locally without any external
services or credentials.
"""
from __future__ import annotations

import sqlite3
import subprocess
import sys
from pathlib import Path

import pytest

import healarr
from healarr.state import store


def test_package_version_is_set() -> None:
    assert isinstance(healarr.__version__, str)
    assert healarr.__version__  # non-empty


def test_subpackages_import_cleanly() -> None:
    """Each subpackage must import without raising."""
    import importlib

    for name in (
        "healarr.agent",
        "healarr.cli",
        "healarr.config",
        "healarr.monitor",
        "healarr.notify",
        "healarr.state",
        "healarr.triage",
    ):
        assert importlib.import_module(name) is not None, f"failed to import {name}"


def test_cli_version_runs() -> None:
    result = subprocess.run(
        [sys.executable, "-m", "healarr", "--version"],
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert result.returncode == 0, result.stderr
    assert "healarr" in result.stdout.lower()


def test_cli_help_lists_phase0_commands() -> None:
    result = subprocess.run(
        [sys.executable, "-m", "healarr", "--help"],
        capture_output=True,
        text=True,
        timeout=15,
    )
    assert result.returncode == 0
    for cmd in ("init-db", "monitor", "agent", "doctor"):
        assert cmd in result.stdout, f"missing CLI command: {cmd}"


def test_init_db_creates_schema(tmp_path: Path) -> None:
    db_path = tmp_path / "healarr-test.db"
    store.initialise(str(db_path))

    assert db_path.exists()
    with sqlite3.connect(db_path) as conn:
        rows = conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"
        ).fetchall()
    table_names = {row[0] for row in rows}

    expected = {
        "actions",
        "agent_conversations",
        "email_inbox",
        "email_outbox",
        "events",
        "observations",
        "proposals",
        "schema_meta",
    }
    missing = expected - table_names
    assert not missing, f"missing tables: {missing}"


def test_init_db_is_idempotent(tmp_path: Path) -> None:
    db_path = tmp_path / "healarr-test.db"
    store.initialise(str(db_path))
    store.initialise(str(db_path))  # second call must not raise


def test_config_load_with_minimal_env(monkeypatch: pytest.MonkeyPatch) -> None:
    """Config loads with all-defaults (every required field has a default)."""
    # Clear any inherited HEALARR_* env so the test is hermetic.
    for key in list(__import__("os").environ):
        if key.startswith("HEALARR_") or key == "ANTHROPIC_API_KEY":
            monkeypatch.delenv(key, raising=False)

    from healarr.config import load

    config = load()
    assert config.agent.model == "claude-sonnet-4-6"
    assert config.monitor_poll_secs == 300
    assert config.email.imap_poll_secs == 60
    assert config.agent.daily_soft_alert_usd == 3.0
    assert config.agent.daily_hard_cap_usd == 10.0
