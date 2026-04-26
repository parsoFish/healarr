"""Notify — outbound email + IMAP polling for replies.

Phase 1 lands:
  email.py     — outbound via host msmtp (or native smtplib fallback);
                 builds approval / escalation / critical / digest emails
                 with stable Message-ID for IMAP correlation
  imap.py      — IMAP polling loop; correlates incoming replies via
                 In-Reply-To / References (subject token fallback);
                 records processed UIDs for idempotency
  approvals.py — proposal lifecycle: token issuance, reprompt schedule,
                 expiry, decision execution
"""
