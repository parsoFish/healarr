"""Agent — Claude API + tool dispatch.

Invoked once per Triage event. Static system prompt + cached tool
definitions; per-call payload is the event + recent state snapshot.

Phase 2 lands:
  prompt.py  — system prompt + simplarr-stack runbook context
  tools.py   — Observe-tier tools + tier-aware dispatcher
  loop.py    — agent invocation, retry, conversation persistence

Phase 3 adds Nudge + Correct tool implementations + email-approval gate
for Correct tier.
"""
