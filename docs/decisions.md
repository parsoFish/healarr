# Healarr — Architecture Decision Records

Captures the key design decisions made during the design phase. Each ADR records context, decision, and consequences so future-you (or contributors) can understand why something is the way it is — not just what it is.

Format is lightweight — short context, the decision, and its consequences. New decisions append; superseded ones get marked as such rather than deleted.

---

## ADR-001 — Separate repo from simplarr

**Context.** Healarr observes simplarr from outside. Bundling them would conflate two surface areas: simplarr is "set up and run a media stack"; Healarr is "watch a media stack and fix what breaks."

**Decision.** Healarr lives in its own repository. It depends on simplarr only at runtime via service APIs (Radarr/Sonarr/etc), not at build time.

**Consequences.** Each project ships independently. Users who want simplarr without Healarr aren't paying for the agent layer. Users who already have an *arr stack from another setup can adopt Healarr without re-deploying their stack.

---

## ADR-002 — Python as implementation language

**Context.** simplarr is Bash + PowerShell. Healarr needs HTTP clients, an LLM SDK, IMAP polling, SQLite, structured retry logic. Bash isn't the right tool.

**Decision.** Python 3.11+, with the official `anthropic` SDK and `httpx` for service calls.

**Consequences.** Different language from simplarr — that's fine because the projects have different concerns. Python chosen over Node/Go for: mature Anthropic SDK, batteries-included stdlib (smtplib, imaplib, sqlite3), and the largest ecosystem for ad-hoc data wrangling. Container size impact is negligible (slim Python image is ~50MB).

---

## ADR-003 — SQLite as state store

**Context.** Healarr needs persistent state for observations, events, proposals, action history, IMAP UID tracking. Single-host single-process workload.

**Decision.** SQLite, single file, mounted from host volume. No ORM — raw SQL via `sqlite3`. Schema migrations as numbered SQL files.

**Consequences.** Zero operational overhead vs Postgres. ACID for our access patterns. Migration story is "apply each unrun schema file in order." If we ever need multi-process write concurrency, this becomes a rewrite — but YAGNI for a single-home single-stack tool.

---

## ADR-004 — Email-only for user-facing notifications

**Context.** User already runs msmtp on the Pi for outbound mail (existing nas_monitor.log + .msmtprc setup). Adding Discord, Slack, ntfy, or push services would mean new credentials and integrations the user doesn't need.

**Decision.** Email for all user-facing notifications. Reuse the user's existing msmtp via mounted `~/.msmtprc` and the `msmtp` binary in the container. No SMTP creds in Healarr's `.env`.

**Consequences.** Faster MVP. Tightly coupled to msmtp specifically — switching to native Python smtplib would require new env vars. Users without msmtp will need to set up either msmtp or a Healarr-specific SMTP block (deferred until requested).

---

## ADR-005 — Email-reply approvals (no web UI)

**Context.** Original design proposed a web link (`http://healarr.local:8089/approve/<token>`) for approval. Requires Healarr to be reachable from wherever the user reads email — which means LAN-only unless a reverse proxy / VPN / public exposure is added. User vetoed the web UI on complexity grounds.

**Decision.** Approvals work by replying to the approval email. Healarr polls IMAP for replies, parses first non-quoted line for `approve|yes` / `reject|no`, and executes accordingly.

**Consequences.**
- **Pro:** zero hosting concerns, works from anywhere the user's email works (including offline-then-sync).
- **Con:** ~60s latency from reply to action (IMAP poll cadence).
- **Con:** new IMAP credential in `.env` (recommend app-specific password).
- **Con:** reply parsing is keyword-based — robust for ~95% of clients; edge cases (HTML-only mobile replies with weird quoting) may mis-parse, in which case the proposal expires unused.

Correlation uses `In-Reply-To` / `References` headers as primary, with `[APPROVAL-<token>]` in the subject as fallback.

---

## ADR-006 — Tiered tool permissions (Observe / Nudge / Correct / Escalate)

**Context.** Self-healing means write actions against user data — torrents, releases, library scans. Different actions have very different blast radii.

**Decision.** Four tiers, enforced by tool dispatch:

| Tier | Examples | Default policy |
|---|---|---|
| Observe (read) | get queue, get torrents, get history | always available |
| Nudge (reversible, no data loss) | trigger search, rescan, retry import | auto-execute |
| Correct (destructive but reversible) | blocklist release, delete torrent + files | email-approval gated |
| Escalate (high blast radius) | delete media, modify Plex library | agent can only propose, never execute |

Every tool call logged to state. Per-tool rate limits + per-event dedup prevent runaway loops.

**Consequences.** Users get a clear mental model of what Healarr is allowed to do without explicit consent. Adding new tools means classifying them — friction is intentional. "Off by default for Escalate" means even with budget + approvals fully open, Healarr cannot destroy the library.

---

## ADR-007 — Phased trust progression (dry-run → nudge auto → correct gated)

**Context.** Trust in the agent is earned, not granted. Day-1 full-auto is a recipe for "agent did something I didn't expect" stories.

**Decision.** Four-phase build:

- **Phase 1**: Monitor + Triage + email digest. No agent. Rules-only visibility.
- **Phase 2**: Agent invoked, but only Observe tools. Drafts proposed remediations as emails for the user to manually act on.
- **Phase 3**: Nudge auto-execute, Correct gated by email approval. Circuit breakers active.
- **Phase 4**: Polish (webhooks, model routing, etc).

**Consequences.** User builds intuition for what the agent proposes before it has authority to act. Each phase is independently useful — the value of Phase 1 is "noisy stack observability"; Phase 2 adds "diagnosis-ready alerts"; Phase 3 is the actual self-healing.

---

## ADR-008 — Sonnet 4.6 default, Haiku 4.5 in Phase 4

**Context.** Anthropic's pricing tiers Haiku < Sonnet < Opus. Most Triage events are routine; some require multi-service correlation.

**Decision.** Default agent model is Sonnet 4.6. Phase 4 introduces a model-routing layer where Haiku 4.5 handles "obvious" events (single-service, well-known patterns) and Sonnet handles complex correlation. Opus reserved for cases where agent reasoning visibly fails on Sonnet.

**Consequences.** Costs predictable from day one. Routing complexity deferred until we have real data on event mix. Switching default models later is a config change, not an architectural one.

---

## ADR-009 — 24h approval expiry with re-prompts at 4h and 12h

**Context.** Pending approvals can't sit forever. They also can't disappear silently the moment the user is on a flight.

**Decision.** Each pending Correct-tier proposal:

- Sends approval email at T+0
- Re-sends at T+4h if no reply
- Re-sends at T+12h if no reply
- Auto-escalates (separate escalation email, proposal canceled) at T+24h

**Consequences.** Two re-prompts is a useful "did you see this" without being spammy. 24h hard cap means an issue never stays in agent-limbo more than a day — by then it's either fixed or the user is consciously deciding to handle it later.

---

## ADR-010 — Daily budget: $3 soft alert, $10 hard cap

**Context.** Anthropic API costs are real money. A runaway loop could rack up $$$ overnight before the user notices.

**Decision.** Two thresholds:

- **$3/day soft alert** — email user "you've crossed $3 today, here's what was spent on"
- **$10/day hard cap** — agent disabled, escalation email sent. Re-enables at midnight UTC.

Both configurable in `.env`.

**Consequences.** Worst-case daily spend bounded at $10, a value chosen as "annoying to lose, not catastrophic." Soft alert at $3 catches the ramp before it hits the cap.

---

## ADR-011 — Hybrid polling + webhooks

**Context.** Polling has 5-minute floor latency for any event. Webhooks are instant but require Healarr to expose a port reachable by the *arr services. Pure-webhook misses drift events that don't fire webhooks (stuck torrents, disk pressure).

**Decision.** Phase 1-3 are polling-only. Phase 4 adds a webhook receiver for the events Radarr/Sonarr/Overseerr already emit (import-failed, download-failed, request-available). Polling stays for the drift cases webhooks don't cover.

**Consequences.** Phase 1-3 ship faster. Webhook complexity is paid for only when polling cadence becomes a felt limitation. Hybrid means we always have a fallback if a webhook is dropped.

---

## ADR-012 — `parsoFish/healarr` GitHub repo, public

**Context.** simplarr is public. Healarr is meant to elevate simplarr. Publishing means others can discover and contribute.

**Decision.** Public repo at `github.com/parsoFish/healarr`. MIT license (same posture as simplarr's intent).

**Consequences.** Secrets must never be committed (.env in .gitignore from day 1). Contributions could come from outside; that's a feature.
