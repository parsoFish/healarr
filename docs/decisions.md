# Healarr — Architecture Decision Records

Captures the key design decisions made during the design phase. Each ADR records context, decision, and consequences so future-you (or contributors) can understand why something is the way it is — not just what it is.

Format is lightweight — short context, the decision, and its consequences. New decisions append; superseded ones get marked as such rather than deleted.

---

## ADR-001 — Separate repo from simplarr

**Context.** Healarr observes simplarr from outside. Bundling them would conflate two surface areas: simplarr is "set up and run a media stack"; Healarr is "watch a media stack and fix what breaks."

**Decision.** Healarr lives in its own repository. It depends on simplarr only at runtime via service APIs (Radarr/Sonarr/etc), not at build time.

**Consequences.** Each project ships independently. Users who want simplarr without Healarr aren't paying for the agent layer. Users who already have an *arr stack from another setup can adopt Healarr without re-deploying their stack.

---

## ADR-002 — Python as implementation language (Superseded by ADR-013)

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

## ADR-005 — Email-reply approvals (no web UI) (Superseded by ADR-014)

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

---

## ADR-013 — Go, single static binary (supersedes ADR-002)

**Context.** The NAS (Synology DSM 7.2) ships only Python 3.8 — too old for the `anthropic` SDK's minimum and for modern type-hint syntax used elsewhere in the codebase — and the agent user on the NAS has no docker-socket access, so a Python approach would need either an upgraded interpreter installed out-of-band or a container runtime the NAS side doesn't have. The Pi side is fine either way, but the two nodes need to run the same artifact for the design to hold together (ADR-015).

**Decision.** Rewrite Healarr in Go as one binary, built with `CGO_ENABLED=0` and cross-compiled per node (`GOOS=linux GOARCH=arm64` for the Pi, `GOARCH=amd64` for the NAS). The binary needs nothing installed on either host beyond the file itself — no interpreter, no virtualenv, no system packages. State storage keeps ADR-003 (SQLite) via `modernc.org/sqlite`, a pure-Go driver, so the CGO-free build doesn't lose the state store.

Two library candidates were evaluated and **not** adopted: `golift.io/starr` (Sonarr/Radarr/Prowlarr client) and `github.com/autobrr/go-qbittorrent` (qBittorrent client). The surface area Healarr actually calls against each service is a handful of endpoints (queue, history, health, series/movie CRUD, torrent list/delete/reannounce) — small enough to implement directly on a shared internal `httpx` client and verify with `httptest` fakes and golden fixtures captured from the live stack. Two fewer third-party dependencies to track for breaking changes, in exchange for owning slightly more HTTP-shape code; that trade was judged worth it for a project with this few call sites per service.

**Consequences.** Deployment on both nodes is "copy one file, run it" — no interpreter version skew between Pi and NAS to debug. Cross-compilation and static linking are a solved, standard Go workflow (`make build-pi` / `make build-nas`). Losing `starr` and `go-qbittorrent` means Healarr owns request/response shapes for those services directly; that surface is fixture-tested per Task 4–10 golden JSON, so drift shows up as a failing test rather than a silent runtime break. If a future service integration needs a much larger surface than the current handful of endpoints, revisit adopting a client library for that specific service.

---

## ADR-014 — LAN web approvals, no IMAP (supersedes ADR-005)

**Context.** The original design (ADR-005) approved Correct-tier actions by replying to email, parsed over IMAP. In practice this meant a second mailbox credential, reply-parsing heuristics that could misfire on HTML-only mobile replies, and ~60s of poll latency between a reply and the action executing. The Pi already runs nginx in front of the whole simplarr stack, so exposing one more page behind it is not new operational surface.

**Decision.** Approvals move to a LAN-only web page served by the Pi's agent (`internal/web/`) at `/healarr/`, reachable through the existing nginx reverse proxy. Digest emails link into this page instead of asking for a reply. No IMAP polling, no second mailbox credential, no reply-parsing.

**Consequences.** Approving or rejecting a decision is immediate (a click, not a poll cycle) and removes an entire subsystem (IMAP client, UID tracking, reply-correlation heuristics) along with its failure modes. The trade-off is that decisions can now only be made from the LAN or over VPN — there is no "approve from anywhere your email works" path any more. Digest emails (ADR-004, still via msmtp) remain the out-of-band notification channel; they just carry links into the web page instead of expecting a reply.

---

## ADR-015 — Two nodes, Pi primary

**Context.** The split stack already runs the *arr apps and Overseerr/Tautulli on the Pi and qBittorrent + Plex on the NAS, with media shared over NFS. A single-process agent on one host either can't see the other host's services directly or has to reach across the network for every check, and — critically — the NFS mount between the two hosts is itself one of the failure modes Healarr exists to detect (see the mount-race incident in the design spec), so state coordination can't depend on it.

**Decision.** Each host runs its own instance of the same `healarr` binary as a long-running agent. The Pi's agent owns the *arr/Overseerr/Tautulli/docker/host-mount checks, the web UI, outbound email, and reconciliation of both nodes' reports into one daily digest — it is the primary. The NAS's agent owns qBittorrent, Plex, NAS-volume, recycle-bin, and orphan-file checks, and executes NAS-side actions when told to by the Pi. The two exchange reports and decisions over an HTTP peer channel on the LAN, authenticated with a shared bearer token from the secrets file (`POST /v1/report`, `GET /v1/report/latest`, `POST /v1/decision`, `POST /v1/heartbeat`).

NFS-shared state (e.g. both agents reading/writing one SQLite file across the mount) was rejected: the mount is one of the things being monitored, so using it as the coordination substrate would make the health-checking system depend on the health of the thing it checks. SSH between the nodes was also rejected: it would need key management on both sides for a machine account and gives no structured RPC — every call would be shell-command construction and stdout scraping, which is worse for testing and worse for failure handling than a small typed HTTP API.

**Consequences.** Either node can be down without losing the other's checks; an unreachable peer just makes the digest say "peer stale since …" rather than blocking. The peer protocol is a small, versioned surface (`internal/peer/`) that's easy to fixture-test independently of the real network. The cost is running and keeping in sync two deployments of the same binary with two config files, and a bearer token that has to be provisioned and rotated on both sides.

---

## ADR-016 — Deterministic staleness score with human decision

**Context.** The NAS accumulates media nobody watches, and nothing tracked that before this project — the design spec's motivating incident found ~470GB of junk and no visibility into which shows were dead weight. Deciding what's actually stale enough to remove needs signal from several services (Tautulli watch history, Sonarr/Radarr monitored status, file size, who requested it via Overseerr, how long it's sat there) and healarr must never take the decision to delete library media out of the user's hands.

**Decision.** A pure, deterministic formula (`internal/staleness/`) scores each series/movie 0–100 from: days since last Tautulli watch (0–40, linear, with an extra penalty for never-watched-and-added->30-days), watch completion (fully watched +15 / partially watched −10), Sonarr/Radarr status (ended and nothing upcoming +10 / continuing and monitored −15), size in GB/10 capped at 15, Overseerr requester (someone other than the owner and still unwatched +10 / owner −5), and age since added (0.05/day, capped at 10). All weights live in config, not code, so tuning them doesn't need a rebuild. A score ≥70 surfaces the item as a delete candidate on the `/healarr/` web page (50–69 is a watch-list entry, shown but not flagged; <50 is suppressed entirely).

The agent never auto-deletes library media. A human clicking "delete" on the web page is what executes the action, and it executes through Sonarr/Radarr's delete-with-files endpoint (plus an import-list exclusion and a best-effort Overseerr request decline) rather than healarr touching files directly — so the *arr apps' own state (monitored flags, history, root-folder bookkeeping) stays consistent with what's actually on disk. "Keep" snoozes the candidate for 60 days rather than dismissing it permanently.

**Consequences.** The score is fully explainable — every point is traceable to a rule and a config weight, so a human can see why something surfaced instead of trusting an opaque model. Because deletion always routes through the *arr apps, healarr can't end up with orphaned files an *arr app doesn't know about, or vice versa. The formula won't be perfect for every household's viewing habits on day one; that's why weights are config, not code, and why nothing crosses the ≥70 line into an actual delete without a person clicking it.

---

## ADR-017 — Stateless checks, stateful store

**Context.** Checks must be table-testable against fakes without a database in the loop, but some rules genuinely need yesterday's number (a *arr's wanted/missing count jumping is only meaningful relative to its last value). At the same time, the SD card on the Pi and the NAS's flash shouldn't see a write per check — the store needs to be the one place state actually lands, not something every check family reaches into on its own.

**Decision.** A check is a `check.Check` struct with a pure `Run(ctx context.Context, d Deps) (Result, error)` — no check imports `internal/store`, and no check performs I/O against the state database. Anything a check needs from "last time" comes from `Deps.Previous`, the last persisted `*Report` for this node (`Deps.PreviousMetric` reads `Previous.Metrics`); `Deps.Previous` is `nil` on a first run or under `--dry-run`, and a check that needs a baseline it doesn't have simply emits nothing rather than erroring. The store is the only writer: `SaveReport` dedups incoming findings on `(node, check_id, entity_key)` while `status ∈ {open, snoozed}` (updating `last_seen`/`severity`/`summary`/`detail`/`data`/`seen_count` in place) and auto-resolves any open/snoozed finding a check *ran successfully* this cycle but stopped emitting; a check that errored or was skipped never resolves anything under its own id, so a transient outage can't make real findings disappear. `--dry-run` on `check run`/`report generate` means exactly "never open or write the store" — no `Deps.Previous`, no persistence, an all-zero upsert summary. Every threshold the catalogue reads (disk percentages, staleness windows, spike percentages, …) lives in `config.Checks`, not in the check code; Phase 2 records each check's suggested remediation `Tier` and schedule `Cadence` but nothing executes on either yet.

**Consequences.** Checks are trivially testable — a table test constructs `Deps` by hand, no fixture database required — and deterministic given the same `Deps`. The cost is that a stale `Previous` after a long outage (the daemon down for days) produces one spurious spike finding on the next run, since the delta looks larger than it really was; this is accepted rather than engineered around, since it self-corrects on the following cycle. The store's schema carries every C3 table from migration `0001_init.sql` up front (`reports`, `findings`, `remediations`, `decisions`, `peer_messages`, `staleness_scores`, `llm_calls`, `email_outbox`, `schema_meta`) even though Phase 2 only writes to `reports` and `findings` — later phases add columns to existing tables, not new tables, keeping migration `0001` the schema's foundation rather than something later migrations have to work around.
