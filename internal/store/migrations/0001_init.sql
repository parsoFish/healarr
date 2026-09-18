CREATE TABLE schema_meta (version INTEGER NOT NULL);
INSERT INTO schema_meta (version) VALUES (0);
CREATE TABLE reports (
  id INTEGER PRIMARY KEY, node TEXT NOT NULL, generated_at TEXT NOT NULL,
  checks_run INTEGER NOT NULL, checks_failed INTEGER NOT NULL, findings_count INTEGER NOT NULL,
  ran TEXT NOT NULL DEFAULT '[]', skipped TEXT NOT NULL DEFAULT '[]', errors TEXT NOT NULL DEFAULT '[]',
  metrics TEXT NOT NULL DEFAULT '{}');
CREATE INDEX reports_node_time ON reports (node, generated_at DESC);
CREATE TABLE findings (
  id INTEGER PRIMARY KEY, check_id TEXT NOT NULL, node TEXT NOT NULL, entity_key TEXT NOT NULL,
  severity TEXT NOT NULL, tier TEXT NOT NULL, summary TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
  data TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL DEFAULT 'open',
  first_seen TEXT NOT NULL, last_seen TEXT NOT NULL, resolved_at TEXT, snooze_until TEXT,
  seen_count INTEGER NOT NULL DEFAULT 1);
CREATE UNIQUE INDEX findings_open_key ON findings (node, check_id, entity_key) WHERE status IN ('open','snoozed');
CREATE INDEX findings_node_status ON findings (node, status);
CREATE TABLE remediations (id INTEGER PRIMARY KEY, finding_id INTEGER REFERENCES findings(id), node TEXT NOT NULL,
  action TEXT NOT NULL, tier TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT);
CREATE TABLE decisions (id INTEGER PRIMARY KEY, entity_key TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
  snooze_until TEXT, requested_at TEXT NOT NULL, executed_at TEXT, error TEXT NOT NULL DEFAULT '');
CREATE TABLE peer_messages (id INTEGER PRIMARY KEY, direction TEXT NOT NULL, kind TEXT NOT NULL, peer TEXT NOT NULL,
  payload TEXT NOT NULL, received_at TEXT NOT NULL);
CREATE INDEX peer_messages_lookup ON peer_messages (peer, kind, direction, received_at DESC);
CREATE TABLE staleness_scores (entity_key TEXT PRIMARY KEY, score REAL NOT NULL, components TEXT NOT NULL,
  computed_at TEXT NOT NULL);
CREATE TABLE llm_calls (id INTEGER PRIMARY KEY, called_at TEXT NOT NULL, model TEXT NOT NULL, input_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL, cost_usd REAL NOT NULL, ok INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '');
CREATE TABLE email_outbox (id INTEGER PRIMARY KEY, to_addr TEXT NOT NULL, subject TEXT NOT NULL, body TEXT NOT NULL,
  status TEXT NOT NULL, created_at TEXT NOT NULL, sent_at TEXT, error TEXT NOT NULL DEFAULT '');
