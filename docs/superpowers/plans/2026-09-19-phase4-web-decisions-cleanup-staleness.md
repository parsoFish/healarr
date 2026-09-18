# Healarr Phase 4 — Web UI + decisions + cleanups + staleness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Pi a LAN-only web page at `/healarr/` (dashboard, decisions, history) backed by the store; a deterministic staleness score (spec C6, weights in config) that surfaces keep/delete candidates; cleanup planners for recycle bins, orphans, docker images and seeded torrents; and a decision executor — all shipped behind an `[actions] enabled = false` gate so nothing mutates the stack until the user flips it.

**Architecture:** `internal/staleness` is a pure scorer plus a collector that assembles `Item`s from the Sonarr/Radarr/Tautulli/Overseerr clients; the `staleness_scan` check (pi, daily) emits findings whose `Data` carries the score components, so the web page and digest read them from the store like any other finding. `internal/cleanup` exposes one planner per kind (`Plan(ctx, Deps) (Plan, error)`) and one executor (`Execute(ctx, Deps, Plan) (Result, error)`); the agent runs planners daily and records them in `remediations` with `dry_run = 1`; execution only happens through the CLI/web when `cfg.Actions.Enabled`. `internal/decision` executes keep (snooze 60 d) / delete (arr delete-with-files + import-list exclusion, Overseerr decline best-effort, peer `SendDecision` so the NAS can drop the torrent) behind the same gate. `internal/web` is `net/http` + `html/template` (embedded), bound to `web.listen_addr`, served under `web.base_path`, protected by a session cookie minted from `secrets.web_token`; nginx on the Pi proxies `/healarr/` to it. The agent starts the web server on the Pi and adds staleness candidates, cleanup plan counts and pending decisions to the digest.

**Tech Stack:** Go 1.25, `net/http`, `html/template`, `embed`, existing store/agent/peer packages. No JS build, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-12-go-rewrite-design.md` — C2 (staleness/cleanup/decision/web packages, nginx snippet), C3 (decisions, remediations, staleness_scores), C4 (`/v1/decision`), C5 (`staleness_scan`, Correct-tier rows), C6, C7, C8 phase 4; ADR-016.

## Global Constraints

- Module path `github.com/parsoFish/healarr`; `go 1.25.0`.
- Every build passes `CGO_ENABLED=0 GOOS=linux GOARCH=arm64|amd64 go build ./...`.
- Files under 400 lines; one responsibility per file; packages by feature.
- Never mutate inputs; errors wrapped with `%w`, nothing swallowed; background failures logged and recorded, never fatal.
- No hardcoded values: weights, thresholds, ports, paths and the actions gate live in config with defaults in `internal/config/defaults.go`.
- **Actions gate:** every mutating path (`decision.Execute` delete branch, `cleanup.Execute`, NAS-side `qbit_delete`) checks `cfg.Actions.Enabled` first and returns `ErrActionsDisabled` (recorded in `remediations`/`decisions` as `status = "blocked"`) when false. Default false. Keep (snooze) is not a stack mutation and is allowed.
- Web: LAN-only bind from config; every page and POST requires the session cookie; POSTs also require the `X-Healarr-Token` header or a hidden form field equal to the session token (CSRF); cookie `HttpOnly`, `SameSite=Strict`; the token is compared with `crypto/subtle.ConstantTimeCompare`; no secrets in templates or logs; all template output auto-escaped (`html/template`).
- Staleness weights: every point in C6 maps to a `[staleness]` config key; the scorer is pure and table-tested against the C6 table.
- Tests: `go test ./... -race -cover` ≥ 80 % per non-trivial package; web handlers via `httptest`; store via temp DB.
- Fixtures/docs: RFC 5737 addresses, no real names/ids/tokens.
- Commits conventional, no AI attribution trailers; lint clean (`go vet`, `golangci-lint`, gocyclo < 15).

---

### Task 1: Config — `[staleness]`, `[cleanup]`, `[actions]`; defaults; validation; example

**Files:** modify `internal/config/config.go`, `defaults.go`, `load.go`, `config.example.toml`; test `internal/config/phase4_test.go`.

```go
type Staleness struct {
	DaysMaxPoints        float64 `toml:"days_max_points"`         // 40
	DaysHorizon          int     `toml:"days_horizon"`            // 180
	NeverWatchedAfterDays int    `toml:"never_watched_after_days"`// 30 (never watched & added > this ⇒ full days points)
	WatchedFullPoints    float64 `toml:"watched_full_points"`     // +15
	WatchedPartialPoints float64 `toml:"watched_partial_points"`  // -10
	EndedPoints          float64 `toml:"ended_points"`            // +10
	ContinuingPoints     float64 `toml:"continuing_points"`       // -15
	SizePointsPerGB      float64 `toml:"size_points_per_gb"`      // 0.1 (GB/10)
	SizeMaxPoints        float64 `toml:"size_max_points"`         // 15
	OtherRequesterPoints float64 `toml:"other_requester_points"`  // +10
	OwnerRequesterPoints float64 `toml:"owner_requester_points"`  // -5
	AgePointsPerDay      float64 `toml:"age_points_per_day"`      // 0.05
	AgeMaxPoints         float64 `toml:"age_max_points"`          // 10
	CandidateThreshold   float64 `toml:"candidate_threshold"`     // 70
	WatchlistThreshold   float64 `toml:"watchlist_threshold"`     // 50
	Owner                string  `toml:"owner"`                   // Overseerr username/email of the owner ("" = unknown)
	SnoozeDays           int     `toml:"snooze_days"`             // 60
}
type Cleanup struct {
	DryRun          bool          `toml:"dry_run"`           // true
	OrphanMinAge    time.Duration `toml:"orphan_min_age"`    // 168h
	RecycleMinAge   time.Duration `toml:"recycle_min_age"`   // 24h
	DockerDangling  bool          `toml:"docker_dangling"`   // true (prune dangling only)
}
type Actions struct { Enabled bool `toml:"enabled"` } // false
```
Validation: thresholds `0 < watchlist < candidate ≤ 100`; all points finite; `SnoozeDays > 0`; durations ≥ 0. `config validate` prints `actions_enabled`, `cleanup_dry_run`, `staleness thresholds` so the operator can eyeball the gate. Tests: defaults, overrides, each rejection.

- [ ] TDD → commit `feat(config): staleness weights, cleanup and actions gate sections`.

---

### Task 2: Store — decisions, remediations, finding history

**Files:** create `internal/store/decisions.go`, `remediations.go`, `history.go` + tests. No new migration (tables exist in 0001; add `CREATE INDEX decisions_status ON decisions (status, requested_at DESC)` and `remediations_created ON remediations (created_at DESC)` to 0001 — no host DB exists).

```go
type Decision struct { ID int64; EntityKey, Kind, Status string; SnoozeUntil *time.Time; RequestedAt time.Time; ExecutedAt *time.Time; Error string }
func (s *Store) CreateDecision(ctx, entityKey, kind string, at time.Time) (int64, error)      // status pending
func (s *Store) PendingDecisions(ctx) ([]Decision, error)
func (s *Store) DecisionByID(ctx, id int64) (Decision, bool, error)
func (s *Store) MarkDecision(ctx, id int64, status string, at time.Time, cause error) error   // executed|failed|blocked
func (s *Store) SnoozeEntity(ctx, entityKey string, until time.Time) error                    // decision kind keep → snooze; also sets findings.snooze_until + status snoozed for that entity
func (s *Store) SnoozedUntil(ctx, entityKey string) (time.Time, bool, error)
type Remediation struct { ID int64; FindingID *int64; Node config.Node; Action, Tier, Status, Detail string; DryRun bool; CreatedAt time.Time; FinishedAt *time.Time }
func (s *Store) RecordRemediation(ctx, r Remediation) (int64, error)
func (s *Store) RecentRemediations(ctx, since time.Time) ([]Remediation, error)
func (s *Store) FindingHistory(ctx, node config.Node, since time.Time, limit int) ([]StoredFinding, error) // resolved+open, newest last_seen first
```
Dedup note: `SaveReport` must respect `snooze_until` — a finding whose entity is snoozed stays `snoozed` (not reopened) until the snooze passes; add that to `upsertFindings` with a test.

- [ ] TDD → commit `feat(store): decisions, remediations, snooze and finding history`.

---

### Task 3: Staleness — pure scorer + collector + `staleness_scan` check + CLI `staleness score`

**Files:** create `internal/staleness/{score.go,collect.go,check.go}` + tests; modify `internal/checks/all.go` (+ all_test 22 ids), `internal/cli/cmd_staleness.go` + test, `internal/cli/root.go`.

```go
type Item struct { EntityKey, Title, Kind string /* series|movie */; LastWatched time.Time; WatchedFully, WatchedPartially bool; Ended, Monitored bool; SizeBytes int64; RequestedBy string; Added time.Time }
type Score struct { Total float64; Components map[string]float64 } // keys: days, completion, arr, size, requester, age
func ScoreItem(it Item, w config.Staleness, now time.Time) Score      // pure; clamp Total to [0,100]
func Band(total float64, w config.Staleness) string                    // "candidate" | "watchlist" | "suppressed"
func Collect(ctx context.Context, d check.Deps) ([]Item, error)        // Sonarr.Series + Radarr.Movies joined with Tautulli.History (grandparent/rating keys matched by title when keys unknown — document the heuristic) and Overseerr.Requests (tvdb/tmdb ids)
```
`staleness_scan` check (pi, escalate tier, daily): requires Sonarr or Radarr and Tautulli; for each item with `Band != suppressed` emit a finding — `warn` for candidate, `info` for watchlist — key = `Item.EntityKey`, summary `"<title> stale (score N): <top two components>"`, Data `{score, components, sizeBytes, lastWatched, requestedBy}`; metric `staleness_candidates`, `staleness_watchlist`. Table tests: each C6 row in isolation (e.g. never watched + added 200 d ago ⇒ 40 days points; fully watched +15; continuing monitored −15; 200 GB ⇒ capped 15; other requester unwatched +10; age 365 d ⇒ capped 10), band thresholds, clamping, not configured, client errors. CLI `healarr staleness score [--json] [--min <score>]` runs Collect+ScoreItem and prints a table sorted by score.

- [ ] TDD → commit `feat(staleness): C6 scorer, collector, staleness_scan check and CLI`.

---

### Task 4: Cleanup planners/executors + CLI `cleanup <kind>`

**Files:** create `internal/cleanup/{cleanup.go,recycle.go,orphans.go,docker.go,seeded.go}` + tests; `internal/cli/cmd_cleanup.go` + test; root registration.

```go
type Kind string // recycle | orphans | docker | seeded
type Plan struct { Kind Kind; Node config.Node; Items []PlanItem; Bytes int64 }
type PlanItem struct { Key, Detail string; Bytes int64 }
type Result struct { Kind Kind; Executed int; Bytes int64; Errors []string }
var ErrActionsDisabled = errors.New("cleanup: actions are disabled in config")
type Planner interface { Kind() Kind; Nodes() []config.Node; Plan(ctx context.Context, d check.Deps) (Plan, error) }
func Execute(ctx context.Context, d check.Deps, p Plan) (Result, error) // returns ErrActionsDisabled when !d.Cfg.Actions.Enabled OR d.Cfg.Cleanup.DryRun
func All() []Planner
```
Planners reuse the Phase 2 rules: recycle (`Cleanup.RecycleDirs`, entries older than `RecycleMinAge`), orphans (same logic as the `orphan_downloads` check — extract a shared helper into `internal/checks/disk` if needed, without changing that check's behaviour), docker (dangling images via `DiskUsage`), seeded (torrents meeting ratio/time and imported — same helper as `seeded_done`). Executors: recycle → `hostfs` delete (add `Remove(ctx, path) error` to the hostfs Client + Fake + OS impl, refusing paths outside configured dirs), orphans → same, docker → `PruneImages(dangling)`, seeded → `QBit.Delete(hashes, false)`. CLI `healarr cleanup <kind> [--dry-run] [--json]`: plans, prints; executes only when `--dry-run` is false AND config allows; records a `remediations` row (`dry_run` true/false, status planned|executed|blocked|failed).

- [ ] TDD → commit `feat(cleanup): recycle, orphan, docker-image and seeded-torrent planners behind the actions gate`.

---

### Task 5: Decision executor + peer `qbit_delete` + CLI `decide`

**Files:** create `internal/decision/{decision.go,execute.go}` + tests; modify `internal/agent/handler.go` (`ReceiveDecision` executes `qbit_delete` on the NAS when actions enabled, else records blocked), `internal/cli/cmd_decide.go` + test, root.

```go
type Kind string // keep | delete
var ErrActionsDisabled = errors.New("decision: actions are disabled in config")
type Deps struct { check.Deps; Store DecisionStore; Peer peer.Client }
func Execute(ctx context.Context, d Deps, id int64) (store.Decision, error)
```
keep: `SnoozeEntity(key, now + SnoozeDays)`, mark executed. delete: gate → parse key (`sonarr:<id>` / `radarr:<id>`) → `DeleteSeries(id, true, true)` / `DeleteMovie(id, true, true)` → Overseerr `DeclineRequest` for matching requests (best effort, logged) → `Peer.SendDecision(Decision{Kind:"qbit_delete", EntityKey, Payload{"hashes": …}})` when the item's download ids are known from arr history (best effort) → mark executed; any hard failure → mark failed with cause. CLI `healarr decide keep|delete <entity-key> [--snooze 60d]` creates the decision and executes it (respecting `--dry-run` → prints what would happen).

- [ ] TDD → commit `feat(decision): keep/delete executor, NAS qbit_delete handling, decide verb`.

---

### Task 6: Web UI

**Files:** create `internal/web/{server.go,auth.go,handlers.go,templates.go,templates/*.html,static.css}` + tests (`httptest`).

- `New(cfg config.Config, token string, st WebStore, dec DecisionRunner, logger) (http.Handler, error)`; `WebStore` = subset of store (open findings both nodes, latest reports both nodes, pending decisions, recent remediations, finding history); mounted under `cfg.Web.BasePath`.
- Auth: `GET <base>/login?token=…` → constant-time compare with `web_token` → set cookie `healarr_session` (random 32-byte id kept in memory map → expires 30 d), redirect to dashboard; every other route 302 → login when cookie missing/invalid; `POST <base>/logout`.
- Pages: `/` dashboard (both nodes' latest report time/counts, heartbeat age, disk gauges from `disk_used_percent:*` metrics, open findings by severity), `/decisions` (staleness findings ≥ watchlist with score/components/size/last watched, buttons Keep/Delete → `POST /decisions` with `entity_key`, `kind`, hidden CSRF token; shows "actions disabled by config — recorded as pending" when gated; pending/blocked/executed decisions list; cleanup plans from `remediations dry_run=1` with Execute buttons, same gate), `/history` (finding history 7 d, remediations).
- Templates embedded; one base layout; no external assets; ≤ 20 KB CSS inline.
- `ListenAndServe(ctx, addr, handler)` mirrors `peer.ListenAndServe` (timeouts, graceful shutdown).
- Tests: unauthenticated → redirect; bad token → 401; login sets cookie; each page 200 with fixture data rendered (assert key strings); POST without CSRF → 403; POST keep creates decision and calls runner; gated delete shows the disabled message and records blocked.

- [ ] TDD → commit `feat(web): LAN dashboard, decisions and history pages with token-cookie auth`.

---

### Task 7: Agent + digest wiring

**Files:** modify `internal/agent/{run.go,schedule.go,digest.go,agent.go}`, `internal/notify/{build.go,digest.tmpl}`, `internal/cli/cmd_agent.go` (+ tests).

- `Options` gains `Web http.Handler` (nil on NAS) and `WebAddr`; `Run` starts it like the peer server (fail fast on bind).
- Daily job (after the 00:10 cycle): run every `cleanup.Planner` for this node, record each plan as a `remediations` row (`dry_run = true`, status planned) — never execute.
- Digest: `DigestInput` gains `Staleness []check.Finding` (candidate+watchlist), `CleanupPlans []notify.CleanupSummary{Kind, Items, Bytes}`, `PendingDecisions int`, `BaseURL` = `http://<pi-ip-or-host>/healarr` from new `web.public_url` config key (default ""), links `…/decisions`.
- CLI `agent serve` builds the web handler on the Pi with `sec.WebToken` (error if empty on the Pi).

- [ ] TDD → commit `feat(agent): serve the web UI, plan cleanups daily, enrich the digest`.

---

### Task 8: nginx snippet + deploy docs

**Files:** create `deploy/nginx/healarr.conf.snippet`; modify `deploy/README.md`.
```nginx
# simplarr split.conf — proxy healarr's LAN web UI
location /healarr/ {
    proxy_pass http://192.0.2.20:8091/healarr/;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_read_timeout 30s;
}
```
README: how to add it to the Pi's nginx config (backup, `docker exec nginx nginx -t`, `docker restart nginx`, probe), and that a simplarr PR should carry the same snippet.

- [ ] Commit `feat(deploy): nginx location snippet for /healarr/`.

---

### Task 9: Host deployment (operator task — controller session)

Preconditions: merged, probe ALL OK. Steps: build; install binaries (keep `.prev`); config deltas (`[staleness] owner = <user email>`, `[cleanup] dry_run = true`, `[actions] enabled = false`, `web.public_url`); `config validate`; restart Pi unit / NAS daemon; nginx: back up `~/simplarr/nginx/split.conf` (find the real path), append the snippet with the Pi's LAN IP, `docker exec nginx nginx -t`, `docker restart nginx`, probe; `curl -s -o /dev/null -w '%{http_code}' http://<pi>/healarr/` → 302 (login) and with `?token=` → 200; `healarr staleness score` on the Pi (read-only); `healarr cleanup recycle --dry-run` on the NAS; probe ALL OK.

---

### Task 10: Docs — runbook (web, decisions, cleanup, actions gate), README, architecture, ADR-019 (actions gate; staleness findings instead of `staleness_scores` table in Phase 4)

- [ ] Commit `docs: phase 4 web/decisions/cleanup runbook, ADR-019 actions gate`.

## Self-review notes
- Spec: C6 scorer ✓ T3; C7 pages + auth + nginx ✓ T6/T8; C5 `staleness_scan` ✓ T3 (catalogue → 22 ids); C3 decisions/remediations ✓ T2 (`staleness_scores` deliberately unused — ADR-019); C4 `/v1/decision` executes `qbit_delete` ✓ T5; "cleanup actions promoted from dry-run" — deliberately NOT promoted overnight: the actions gate (default off) is the promotion switch; ADR-019 records it.
- Type consistency: `staleness.Item`/`Score` used by T3 check + CLI; `cleanup.Plan` recorded via `store.Remediation` (T2) by T7 and CLI (T4); `decision.Execute` used by T5 CLI, T6 web (`DecisionRunner` interface), T5 handler; `DigestInput` extension consumed by T7.
