-- Healarr state schema. Applied idempotently on every Healarr startup
-- via store.initialise(). New migrations append; existing tables aren't
-- modified destructively (use ALTER + numbered migration files when needed).

PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- ---------------------------------------------------------------------
-- observations: raw outputs from each Monitor cycle. Polled snapshots
-- of service state. Triage rules consume these; agent reads them via
-- Observe-tier tools.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS observations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    service       TEXT    NOT NULL,    -- 'radarr' | 'sonarr' | 'qbit' | 'plex' | ...
    kind          TEXT    NOT NULL,    -- 'queue' | 'history' | 'torrents' | 'health' | ...
    entity_id     TEXT,                -- service-local ID (movie_id, episode_id, hash, ...)
    payload_json  TEXT    NOT NULL     -- raw JSON snapshot
);

CREATE INDEX IF NOT EXISTS observations_service_kind_idx
    ON observations(service, kind, created_at DESC);
CREATE INDEX IF NOT EXISTS observations_entity_idx
    ON observations(service, entity_id, created_at DESC);

-- ---------------------------------------------------------------------
-- events: structured anomalies emitted by Triage rules. Each event
-- references the observations that justified it. Most observations
-- never produce events (cost gate before agent invocation).
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    event_type      TEXT    NOT NULL,  -- 'wrong_file_suspect' | 'stuck_torrent' | ...
    entity_key      TEXT    NOT NULL,  -- stable dedup key (service:kind:entity_id)
    severity        TEXT    NOT NULL,  -- 'info' | 'warning' | 'critical'
    context_json    TEXT    NOT NULL,  -- frozen context the agent consumes
    status          TEXT    NOT NULL DEFAULT 'open',
                                       -- 'open' | 'agent_running' | 'awaiting_approval'
                                       -- | 'resolved' | 'escalated' | 'expired'
    resolved_at     TEXT
);

CREATE INDEX IF NOT EXISTS events_status_idx ON events(status, created_at DESC);
CREATE INDEX IF NOT EXISTS events_entity_dedup_idx ON events(entity_key, status);

-- ---------------------------------------------------------------------
-- proposals: Correct-tier actions awaiting user approval via email
-- reply. Token is what gets embedded in the approval-email subject.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS proposals (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id        INTEGER NOT NULL REFERENCES events(id),
    created_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token           TEXT    NOT NULL UNIQUE,
    plan_json       TEXT    NOT NULL,  -- ordered list of tool calls the agent wants to run
    status          TEXT    NOT NULL DEFAULT 'pending',
                                       -- 'pending' | 'approved' | 'rejected' | 'expired'
    decided_at      TEXT,
    decided_by_uid  TEXT,               -- IMAP UID that decided (audit)
    reprompt_count  INTEGER NOT NULL DEFAULT 0,
    last_reprompt_at TEXT
);

CREATE INDEX IF NOT EXISTS proposals_status_idx ON proposals(status, created_at);
CREATE INDEX IF NOT EXISTS proposals_token_idx ON proposals(token);

-- ---------------------------------------------------------------------
-- actions: every tool dispatch (auto or post-approval), success or fail.
-- Audit trail. Also fuels the daily digest.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS actions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    event_id      INTEGER REFERENCES events(id),
    proposal_id   INTEGER REFERENCES proposals(id),
    tool_name     TEXT    NOT NULL,
    tier          TEXT    NOT NULL,    -- 'observe' | 'nudge' | 'correct' | 'escalate'
    args_json     TEXT    NOT NULL,
    outcome       TEXT    NOT NULL,    -- 'success' | 'failed' | 'refused'
    result_json   TEXT
);

CREATE INDEX IF NOT EXISTS actions_event_idx ON actions(event_id, created_at);
CREATE INDEX IF NOT EXISTS actions_tool_idx ON actions(tool_name, created_at DESC);

-- ---------------------------------------------------------------------
-- agent_conversations: full Claude turn-by-turn record per event.
-- Persisted for audit, debugging, and replay (running an event back
-- through the agent with newer prompts/tools).
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS agent_conversations (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id      INTEGER NOT NULL REFERENCES events(id),
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    model         TEXT    NOT NULL,
    messages_json TEXT    NOT NULL,    -- ordered list of {role, content, tool_uses, ...}
    usage_json    TEXT    NOT NULL,    -- {input_tokens, output_tokens, cache_*, cost_usd}
    final_status  TEXT    NOT NULL     -- 'fixed' | 'proposed' | 'escalated' | 'errored'
);

CREATE INDEX IF NOT EXISTS agent_conv_event_idx ON agent_conversations(event_id);

-- ---------------------------------------------------------------------
-- email_outbox: every email Healarr sends, keyed by Message-ID. Used
-- to correlate inbound replies via In-Reply-To / References.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS email_outbox (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    sent_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    message_id    TEXT    NOT NULL UNIQUE,
    purpose       TEXT    NOT NULL,    -- 'approval_request' | 'reprompt' | 'escalation' | 'critical' | 'digest'
    proposal_id   INTEGER REFERENCES proposals(id),
    event_id      INTEGER REFERENCES events(id),
    subject       TEXT    NOT NULL,
    body_preview  TEXT
);

CREATE INDEX IF NOT EXISTS email_outbox_msgid_idx ON email_outbox(message_id);
CREATE INDEX IF NOT EXISTS email_outbox_proposal_idx ON email_outbox(proposal_id);

-- ---------------------------------------------------------------------
-- email_inbox: idempotency record for IMAP UIDs we've already processed.
-- Prevents the same reply triggering the same approval twice.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS email_inbox (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    processed_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    imap_uid      TEXT    NOT NULL,
    mailbox       TEXT    NOT NULL,
    in_reply_to   TEXT,
    subject       TEXT,
    decision      TEXT,                -- 'approve' | 'reject' | 'unparseable'
    proposal_id   INTEGER REFERENCES proposals(id),
    UNIQUE(mailbox, imap_uid)
);

CREATE INDEX IF NOT EXISTS email_inbox_proposal_idx ON email_inbox(proposal_id);

-- ---------------------------------------------------------------------
-- schema_meta: track which migration version we're at. Phase 0 = 1.
-- ---------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS schema_meta (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_meta(version) VALUES (1);
