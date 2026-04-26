"""Config loading from environment variables.

Single source of truth for what Healarr expects in its environment. The
shape mirrors `.env.example`. Missing required values raise at import
time rather than failing far from the cause.
"""
from __future__ import annotations

import os
from dataclasses import dataclass


def _env(name: str, default: str | None = None, *, required: bool = False) -> str:
    value = os.environ.get(name, default)
    if required and not value:
        raise RuntimeError(
            f"Missing required env var {name}. See .env.example for the full contract."
        )
    return value or ""


def _env_int(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        return int(raw)
    except ValueError as exc:
        raise RuntimeError(f"Env var {name}={raw!r} is not an integer") from exc


def _env_bool(name: str, default: bool) -> bool:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    return raw.lower() in {"1", "true", "yes", "on"}


def _env_float(name: str, default: float) -> float:
    raw = os.environ.get(name)
    if raw is None or raw == "":
        return default
    try:
        return float(raw)
    except ValueError as exc:
        raise RuntimeError(f"Env var {name}={raw!r} is not a number") from exc


@dataclass(frozen=True)
class ServiceConfig:
    """Connection details for one upstream service."""

    url: str
    api_key: str = ""


@dataclass(frozen=True)
class EmailConfig:
    """SMTP-out + IMAP-in configuration. Uses host msmtp by default."""

    via_msmtp: bool
    from_address: str
    to_address: str

    imap_host: str
    imap_port: int
    imap_user: str
    imap_pass: str
    imap_mailbox: str
    imap_poll_secs: int


@dataclass(frozen=True)
class AgentConfig:
    """Anthropic API + budget guardrails."""

    api_key: str
    model: str
    daily_soft_alert_usd: float
    daily_hard_cap_usd: float


@dataclass(frozen=True)
class HealarrConfig:
    radarr: ServiceConfig
    sonarr: ServiceConfig
    prowlarr: ServiceConfig
    overseerr: ServiceConfig
    qbittorrent: ServiceConfig
    plex: ServiceConfig

    state_db_path: str
    monitor_poll_secs: int
    dry_run: bool

    email: EmailConfig
    agent: AgentConfig


def load() -> HealarrConfig:
    """Read config from the environment. Call once at startup."""
    return HealarrConfig(
        radarr=ServiceConfig(
            url=_env("HEALARR_RADARR_URL", "http://localhost:7878"),
            api_key=_env("HEALARR_RADARR_API_KEY"),
        ),
        sonarr=ServiceConfig(
            url=_env("HEALARR_SONARR_URL", "http://localhost:8989"),
            api_key=_env("HEALARR_SONARR_API_KEY"),
        ),
        prowlarr=ServiceConfig(
            url=_env("HEALARR_PROWLARR_URL", "http://localhost:9696"),
            api_key=_env("HEALARR_PROWLARR_API_KEY"),
        ),
        overseerr=ServiceConfig(
            url=_env("HEALARR_OVERSEERR_URL", "http://localhost:5055"),
            api_key=_env("HEALARR_OVERSEERR_API_KEY"),
        ),
        qbittorrent=ServiceConfig(
            url=_env("HEALARR_QBITTORRENT_URL", "http://localhost:8080"),
        ),
        plex=ServiceConfig(
            url=_env("HEALARR_PLEX_URL", "http://localhost:32400"),
            api_key=_env("HEALARR_PLEX_TOKEN"),
        ),
        state_db_path=_env("HEALARR_STATE_DB", "/var/lib/healarr/state.db"),
        monitor_poll_secs=_env_int("HEALARR_MONITOR_POLL_SECS", 300),
        dry_run=_env_bool("HEALARR_DRY_RUN", False),
        email=EmailConfig(
            via_msmtp=_env_bool("HEALARR_SMTP_VIA_MSMTP", True),
            from_address=_env("HEALARR_FROM_EMAIL", required=False),
            to_address=_env("HEALARR_TO_EMAIL", required=False),
            imap_host=_env("HEALARR_IMAP_HOST"),
            imap_port=_env_int("HEALARR_IMAP_PORT", 993),
            imap_user=_env("HEALARR_IMAP_USER"),
            imap_pass=_env("HEALARR_IMAP_PASS"),
            imap_mailbox=_env("HEALARR_IMAP_MAILBOX", "INBOX"),
            imap_poll_secs=_env_int("HEALARR_IMAP_POLL_SECS", 60),
        ),
        agent=AgentConfig(
            api_key=_env("ANTHROPIC_API_KEY"),
            model=_env("HEALARR_AGENT_MODEL", "claude-sonnet-4-6"),
            daily_soft_alert_usd=_env_float("HEALARR_BUDGET_SOFT_USD", 3.0),
            daily_hard_cap_usd=_env_float("HEALARR_BUDGET_HARD_USD", 10.0),
        ),
    )
