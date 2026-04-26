"""SQLite-backed state store for Healarr.

Tables (see schema.sql):
  observations          raw monitor outputs
  events                triage outputs (correlated observations)
  proposals             Correct-tier actions awaiting email approval
  actions               executed tool calls + outcomes
  agent_conversations   full Claude turns for audit + replay
  email_outbox          sent emails + message IDs (for IMAP correlation)
  email_inbox           processed reply UIDs (idempotency)
"""
