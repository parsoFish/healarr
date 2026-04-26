"""Healarr command-line entry point.

Phase 0: --version + init-db are real; monitor / agent are stubs that
print "not implemented in Phase 0" and exit 0. They take their final
shape in Phase 1+.
"""
from __future__ import annotations

import sys

import click

from healarr import __version__
from healarr.state import store


@click.group()
@click.version_option(__version__, prog_name="healarr")
def main() -> None:
    """Healarr — self-healing agent for Plex/*arr media stacks."""


@main.command("init-db")
@click.option(
    "--db",
    "db_path",
    default=None,
    help="Override the SQLite path (else read from HEALARR_STATE_DB / default).",
)
def init_db(db_path: str | None) -> None:
    """Create the SQLite database and apply the schema. Idempotent."""
    from healarr.config import load

    config = load() if db_path is None else None
    target = db_path or (config.state_db_path if config else "state.db")
    store.initialise(target)
    click.echo(f"Initialised state database at {target}")


@main.command("monitor")
def monitor_cmd() -> None:
    """Run the polling Monitor loop. (Phase 1 — not yet implemented.)"""
    click.echo(
        "monitor: not implemented in Phase 0. The Monitor + Triage loops "
        "land in Phase 1. See docs/architecture.md § Phasing."
    )
    sys.exit(0)


@main.command("agent")
def agent_cmd() -> None:
    """Run the Agent on a triage event. (Phase 2 — not yet implemented.)"""
    click.echo(
        "agent: not implemented in Phase 0. The Claude agent lands in "
        "Phase 2. See docs/architecture.md § Phasing."
    )
    sys.exit(0)


@main.command("doctor")
def doctor_cmd() -> None:
    """Sanity-check config + DB + service reachability. (Phase 1.)"""
    click.echo(
        "doctor: not implemented in Phase 0. Lands with the Monitor "
        "module in Phase 1."
    )
    sys.exit(0)


if __name__ == "__main__":
    main()
