# Healarr — Operations Runbook

Populated incrementally as features ship. Keep this short and operational.

## First-run setup

1. Clone the repo.
2. `cp .env.example .env` and fill in:
   - Service URLs + API keys for Radarr / Sonarr / Prowlarr / Overseerr / qBittorrent / Plex
   - SMTP block (default reuses host msmtp; alt: native smtplib creds)
   - IMAP block (host, port, user, pass, mailbox, poll cadence)
   - `ANTHROPIC_API_KEY`
   - Optional overrides: poll cadence, budget caps, model
3. Verify host msmtp works: `echo "test" | msmtp <your-email>` from the Pi shell. If you get a bounce, fix msmtp before deploying Healarr.
4. `docker compose up -d`
5. Watch logs: `docker compose logs -f healarr`. Healarr will create the SQLite database on first start.

## Daily operations

- **Approval emails**: reply with `approve` / `yes` to authorise, `reject` / `no` to decline. First non-quoted line is what's parsed. Anything else → ignored, proposal stays pending until next re-prompt or expiry.
- **Escalation emails**: agent couldn't fix something automatically. Human follow-up required.
- **Critical-self emails**: Healarr itself is sick. Check container logs first.

## Common scenarios (will be filled in as we hit them)

### "I got an approval email but I'm not sure what to do"

The body of the email includes the action, target, reasoning, and reversibility. If still unclear, ignore the email — proposal expires in 24h with no harm done. Check the dashboard if you want full context.

### "Healarr fixed something I didn't want fixed"

Until Phase 4, every Correct-tier action requires email approval. If it happened anyway, check `actions` table in SQLite — only Nudge tier should be auto. If a Nudge action was unwanted, you can disable that tool individually in `.env` (e.g. `HEALARR_NUDGE_RESCAN_PLEX_LIBRARY=false`).

### "Budget cap hit"

Healarr is paused. Check the action log — if a real bug caused a loop, fix and re-enable. If it was just a busy day, raise the cap or let it auto-reset at midnight UTC.

### "Reply to approval email isn't being processed"

Check IMAP polling logs. Common causes:
- Reply landed in a folder Healarr isn't watching (configure `HEALARR_IMAP_MAILBOX`)
- Reply has odd quoting and the first non-quoted line wasn't `approve` / `yes` / `reject` / `no`
- IMAP credentials expired

## Disabling Healarr quickly

`docker compose stop healarr` — services keep running, observation pauses. Resume with `docker compose start healarr`.

For a full stop including state purge: `docker compose down && rm healarr-state.db`.

## Updating

Healarr is versioned via git tags. Pull the latest tag, rebuild the container, restart. Schema migrations are auto-applied on start.
