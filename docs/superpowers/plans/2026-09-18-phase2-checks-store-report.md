# Healarr Phase 2 — Checks + store + one-shot report Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Phase 1 CLI into a health engine: every row of the spec's check catalogue (C5, except `staleness_scan`) implemented as a pure function over the client interfaces, a SQLite store that persists reports and de-duplicates findings, and two new verbs — `healarr check run` and `healarr report generate` — that run on both hosts in dry-run mode.

**Architecture:** `internal/check` defines the `Check` struct, `Finding`/`Report` types, a `Deps` bundle of client interfaces, a `Registry`, and a `Runner` that executes checks with per-check timeouts and collects errors instead of aborting. Each check family lives in `internal/checks/<family>` and exposes `Checks(cfg) []check.Check`; `internal/checks/all.go` aggregates them into one registry. `internal/store` wraps `modernc.org/sqlite` with embedded numbered migrations; findings are upserted on `(check_id, entity_key)` while open. Checks that need a delta (e.g. wanted-missing spike) read `Deps.Previous`, the last persisted `Report` for this node, so the checks themselves stay stateless. `internal/notify/digest.go` renders the templated plain-text digest that Phase 3 emails and Phase 5 uses as the LLM fallback.

**Tech Stack:** Go 1.24, `modernc.org/sqlite` (pure Go, CGO_ENABLED=0), `github.com/BurntSushi/toml` (durations decode from strings), `text/template`, `github.com/spf13/cobra`. No cgo.

**Spec:** `docs/superpowers/specs/2026-09-12-go-rewrite-design.md` — sections C2 (package layout), C3 (store), C5 (check catalogue), C8 phase 2, Testing.

## Global Constraints

- Module path `github.com/parsoFish/healarr`; `go 1.24` in `go.mod`.
- Every build must pass `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` and `GOARCH=amd64` (`make build-pi`, `make build-nas`).
- Files under 400 lines; one responsibility per file; packages organised by feature.
- Never mutate inputs — return new values. Errors are always returned, wrapped with `fmt.Errorf("...: %w", err)`; nothing is silently swallowed.
- No hardcoded hosts, ports, paths, thresholds or credentials in Go code; defaults live in `internal/config/defaults.go` and are overridable from `config.toml`.
- Checks are pure: a check reads only from `check.Deps` and returns findings; it never writes to any service, never touches the store, never sleeps. Phase 2 executes **no** Nudge/Correct actions — tiers are recorded on findings only.
- `--dry-run` on `check run` / `report generate` means "do not open or write the store". Both verbs must succeed with `--dry-run` on a host that has no state directory yet.
- Dedup key is `check_id + entity_key`; a check must emit a stable `EntityKey` per entity (never an index, never a timestamp).
- Tests: `go test ./... -race -cover`, target ≥ 80 % per non-trivial package; every check has a table test; the store is tested against a temp-file SQLite DB per test.
- Fixtures and docs use RFC 5737 addresses (`192.0.2.0/24`), example usernames, no real device ids.
- Commits: conventional (`feat:`, `test:`, `chore:`, `docs:`), **no AI attribution lines** (user rule; use plain `git commit -m`).
- Lint: `go vet ./...` and `golangci-lint run ./...` (gocyclo ≥15 fails) must be clean.

---

### Task 1: `internal/check` core — types, registry, runner

**Files:**
- Create: `internal/check/types.go`, `internal/check/deps.go`, `internal/check/registry.go`, `internal/check/runner.go`
- Test: `internal/check/registry_test.go`, `internal/check/runner_test.go`, `internal/check/types_test.go`

**Interfaces:**
- Produces: everything below. Later tasks import `check` only.

```go
// internal/check/types.go
// Package check defines the check engine: the Check descriptor, the
// Finding and Report value types every check family produces, and the
// registry/runner that execute checks against a Deps bundle.
package check

import (
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// Severity orders findings for display and digest grouping.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// Tier is the remediation tier a finding *suggests* (ADR-006). Phase 2
// records it; nothing acts on it.
type Tier string

const (
	TierObserve  Tier = "observe"
	TierNudge    Tier = "nudge"
	TierCorrect  Tier = "correct"
	TierEscalate Tier = "escalate"
)

// Finding is one detected condition on one entity.
type Finding struct {
	CheckID   string         `json:"checkId"`
	Node      config.Node    `json:"node"`
	EntityKey string         `json:"entityKey"`
	Severity  Severity       `json:"severity"`
	Tier      Tier           `json:"tier"`
	Summary   string         `json:"summary"`
	Detail    string         `json:"detail,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	FirstSeen time.Time      `json:"firstSeen"`
	LastSeen  time.Time      `json:"lastSeen"`
}

// Key is the dedup identity: check_id + entity_key.
func (f Finding) Key() string { return f.CheckID + ":" + f.EntityKey }

// CheckError records a check that returned an error (the run continues).
type CheckError struct {
	CheckID string `json:"checkId"`
	Error   string `json:"error"`
}

// Report is the outcome of one run of a set of checks on one node.
type Report struct {
	Node         config.Node        `json:"node"`
	GeneratedAt  time.Time          `json:"generatedAt"`
	Findings     []Finding          `json:"findings"`
	Ran          []string           `json:"ran"`          // check ids that completed (with or without findings)
	Skipped      []string           `json:"skipped"`      // check ids skipped because a dependency isn't configured
	Errors       []CheckError       `json:"errors"`       // check ids that failed
	Metrics      map[string]float64 `json:"metrics"`      // scalar observations for delta checks (e.g. sonarr_wanted_missing)
	ChecksRun    int                `json:"checksRun"`    // len(Ran)+len(Errors)
	ChecksFailed int                `json:"checksFailed"` // len(Errors)
}

// Check describes one catalogue row. Run is a pure function over Deps.
type Check struct {
	ID      string
	Nodes   []config.Node // which node(s) run it
	Tier    Tier          // suggested remediation tier
	Cadence time.Duration // how often the Phase 3 daemon schedules it
	Run     func(ctx context.Context, d Deps) (Result, error)
}

// Result is what one check returns: zero or more findings plus optional
// metrics that the next run can compare against via Deps.Previous.
type Result struct {
	Findings []Finding
	Metrics  map[string]float64
}

// ErrNotConfigured is returned by a check whose dependency (a client, a
// path list) is absent from config. The runner records it as Skipped, not
// as an error.
var ErrNotConfigured = errors.New("check: dependency not configured")

// AppliesTo reports whether c runs on node.
func (c Check) AppliesTo(node config.Node) bool {
	for _, n := range c.Nodes { if n == node { return true } }
	return false
}
```
(Add `"context"` and `"errors"` imports.)

```go
// internal/check/deps.go
package check

// Deps is everything a check may read. Any client may be nil when the
// service is not configured on this node; a check that needs a nil client
// returns ErrNotConfigured.
type Deps struct {
	Node     config.Node
	Cfg      config.Config
	Now      func() time.Time
	Previous *Report // last persisted report for this node; nil on first run or --dry-run without store

	Sonarr    sonarr.Client
	Radarr    radarr.Client
	Prowlarr  prowlarr.Client
	QBit      qbittorrent.Client
	Plex      plex.Client
	Tautulli  tautulli.Client
	Overseerr overseerr.Client
	Docker    docker.Client
	Host      hostfs.Client
}

// PreviousMetric returns the named metric from the previous report.
func (d Deps) PreviousMetric(name string) (float64, bool) {
	if d.Previous == nil || d.Previous.Metrics == nil { return 0, false }
	v, ok := d.Previous.Metrics[name]
	return v, ok
}

// NewFinding builds a Finding stamped with d.Node and d.Now() on both
// FirstSeen and LastSeen (the store adjusts FirstSeen on upsert).
func (d Deps) NewFinding(checkID, entityKey string, sev Severity, tier Tier, summary string) Finding {
	now := d.Now()
	return Finding{CheckID: checkID, Node: d.Node, EntityKey: entityKey, Severity: sev, Tier: tier, Summary: summary, FirstSeen: now, LastSeen: now}
}
```

```go
// internal/check/registry.go
package check

// Registry holds every known check keyed by ID. Registration order is
// preserved for deterministic output.
type Registry struct {
	order []string
	byID  map[string]Check
}

func NewRegistry() *Registry { return &Registry{byID: map[string]Check{}} }

// Register adds c; a duplicate ID is a programming error and returns an error.
func (r *Registry) Register(c Check) error {
	if c.ID == "" { return errors.New("check: empty id") }
	if c.Run == nil { return fmt.Errorf("check %s: nil Run", c.ID) }
	if len(c.Nodes) == 0 { return fmt.Errorf("check %s: no nodes", c.ID) }
	if _, dup := r.byID[c.ID]; dup { return fmt.Errorf("check %s: already registered", c.ID) }
	r.byID[c.ID] = c
	r.order = append(r.order, c.ID)
	return nil
}

// All returns every check in registration order (a copy).
func (r *Registry) All() []Check
// ForNode returns the checks that apply to node, in registration order.
func (r *Registry) ForNode(node config.Node) []Check
// ByID looks one up.
func (r *Registry) ByID(id string) (Check, bool)
```

```go
// internal/check/runner.go
package check

// Run executes checks sequentially against d, giving each check its own
// context with timeout. A check error never aborts the run: it is recorded
// in Report.Errors. ErrNotConfigured is recorded in Report.Skipped.
// Findings are sorted by (Severity desc, CheckID, EntityKey) for stable
// output; Metrics from every check are merged (later checks win on
// duplicate keys, which the all.go test forbids).
func Run(ctx context.Context, checks []Check, d Deps, timeout time.Duration) Report {
	now := d.Now()
	rep := Report{Node: d.Node, GeneratedAt: now, Findings: []Finding{}, Ran: []string{}, Skipped: []string{}, Errors: []CheckError{}, Metrics: map[string]float64{}}
	for _, c := range checks {
		res, err := runOne(ctx, c, d, timeout)
		switch {
		case errors.Is(err, ErrNotConfigured):
			rep.Skipped = append(rep.Skipped, c.ID)
		case err != nil:
			rep.Errors = append(rep.Errors, CheckError{CheckID: c.ID, Error: err.Error()})
		default:
			rep.Ran = append(rep.Ran, c.ID)
			rep.Findings = append(rep.Findings, res.Findings...)
			for k, v := range res.Metrics { rep.Metrics[k] = v }
		}
	}
	sortFindings(rep.Findings)
	rep.ChecksRun = len(rep.Ran) + len(rep.Errors)
	rep.ChecksFailed = len(rep.Errors)
	return rep
}

// runOne applies the timeout and converts a panic inside a check into an
// error so one bad check cannot take the daemon down.
func runOne(ctx context.Context, c Check, d Deps, timeout time.Duration) (res Result, err error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil { err = fmt.Errorf("check %s panicked: %v", c.ID, r) }
	}()
	res, err = c.Run(cctx, d)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return Result{}, fmt.Errorf("check %s: %w", c.ID, err)
	}
	return res, err
}

func severityRank(s Severity) int { switch s { case SeverityCritical: return 0; case SeverityWarn: return 1; default: return 2 } }
```

- [ ] **Step 1: Write failing tests**

`internal/check/registry_test.go`:
```go
package check

import (
	"context"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

func noop(context.Context, Deps) (Result, error) { return Result{}, nil }

func TestRegistryRegisterAndFilter(t *testing.T) {
	r := NewRegistry()
	must := func(c Check) { t.Helper(); if err := r.Register(c); err != nil { t.Fatal(err) } }
	must(Check{ID: "a", Nodes: []config.Node{config.NodePi}, Run: noop})
	must(Check{ID: "b", Nodes: []config.Node{config.NodePi, config.NodeNAS}, Run: noop})
	must(Check{ID: "c", Nodes: []config.Node{config.NodeNAS}, Run: noop})
	if got := ids(r.All()); len(got) != 3 || got[0] != "a" || got[2] != "c" { t.Fatalf("All order: %v", got) }
	if got := ids(r.ForNode(config.NodeNAS)); len(got) != 2 || got[0] != "b" || got[1] != "c" { t.Fatalf("ForNode nas: %v", got) }
	if _, ok := r.ByID("b"); !ok { t.Fatal("ByID b missing") }
	if _, ok := r.ByID("zzz"); ok { t.Fatal("ByID zzz should miss") }
}

func TestRegistryRejectsBadChecks(t *testing.T) {
	r := NewRegistry()
	cases := map[string]Check{
		"empty id":  {Nodes: []config.Node{config.NodePi}, Run: noop},
		"nil run":   {ID: "x", Nodes: []config.Node{config.NodePi}},
		"no nodes":  {ID: "y", Run: noop},
	}
	for name, c := range cases {
		if err := r.Register(c); err == nil { t.Errorf("%s: expected error", name) }
	}
	if err := r.Register(Check{ID: "dup", Nodes: []config.Node{config.NodePi}, Run: noop}); err != nil { t.Fatal(err) }
	if err := r.Register(Check{ID: "dup", Nodes: []config.Node{config.NodePi}, Run: noop}); err == nil { t.Fatal("duplicate id accepted") }
}

func ids(cs []Check) []string { out := make([]string, 0, len(cs)); for _, c := range cs { out = append(out, c.ID) }; return out }
```

`internal/check/runner_test.go`:
```go
package check

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

func testDeps() Deps { return Deps{Node: config.NodePi, Now: func() time.Time { return fixedNow }} }

func TestRunCollectsFindingsErrorsAndSkips(t *testing.T) {
	checks := []Check{
		{ID: "ok", Nodes: []config.Node{config.NodePi}, Run: func(_ context.Context, d Deps) (Result, error) {
			return Result{Findings: []Finding{d.NewFinding("ok", "e1", SeverityWarn, TierObserve, "warn one")}, Metrics: map[string]float64{"m": 3}}, nil
		}},
		{ID: "crit", Nodes: []config.Node{config.NodePi}, Run: func(_ context.Context, d Deps) (Result, error) {
			return Result{Findings: []Finding{d.NewFinding("crit", "e2", SeverityCritical, TierCorrect, "crit")}}, nil
		}},
		{ID: "bad", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { return Result{}, errors.New("boom") }},
		{ID: "skip", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { return Result{}, ErrNotConfigured }},
	}
	rep := Run(context.Background(), checks, testDeps(), time.Second)
	if rep.ChecksRun != 3 || rep.ChecksFailed != 1 { t.Fatalf("run/failed = %d/%d", rep.ChecksRun, rep.ChecksFailed) }
	if len(rep.Skipped) != 1 || rep.Skipped[0] != "skip" { t.Fatalf("skipped: %v", rep.Skipped) }
	if len(rep.Errors) != 1 || rep.Errors[0].CheckID != "bad" || rep.Errors[0].Error != "check bad: boom" { t.Fatalf("errors: %+v", rep.Errors) }
	if len(rep.Findings) != 2 || rep.Findings[0].Severity != SeverityCritical { t.Fatalf("findings not sorted critical-first: %+v", rep.Findings) }
	if rep.Metrics["m"] != 3 { t.Fatalf("metrics: %v", rep.Metrics) }
	if !rep.GeneratedAt.Equal(fixedNow) || rep.Findings[0].FirstSeen != fixedNow { t.Fatal("timestamps not from Deps.Now") }
}

func TestRunTimesOutSlowCheck(t *testing.T) {
	slow := Check{ID: "slow", Nodes: []config.Node{config.NodePi}, Run: func(ctx context.Context, _ Deps) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	}}
	rep := Run(context.Background(), []Check{slow}, testDeps(), 10*time.Millisecond)
	if rep.ChecksFailed != 1 || !contains(rep.Errors[0].Error, "deadline") { t.Fatalf("expected deadline error, got %+v", rep.Errors) }
}

func TestRunRecoversPanic(t *testing.T) {
	p := Check{ID: "p", Nodes: []config.Node{config.NodePi}, Run: func(context.Context, Deps) (Result, error) { panic("oops") }}
	rep := Run(context.Background(), []Check{p}, testDeps(), time.Second)
	if rep.ChecksFailed != 1 || !contains(rep.Errors[0].Error, "panicked") { t.Fatalf("got %+v", rep.Errors) }
}

func TestPreviousMetric(t *testing.T) {
	d := testDeps()
	if _, ok := d.PreviousMetric("x"); ok { t.Fatal("nil previous should miss") }
	d.Previous = &Report{Metrics: map[string]float64{"x": 1}}
	if v, ok := d.PreviousMetric("x"); !ok || v != 1 { t.Fatal("previous metric lookup") }
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int { for i := 0; i+len(sub) <= len(s); i++ { if s[i:i+len(sub)] == sub { return i } }; return -1 }
```
(Use `strings.Contains` instead of the helper if you prefer — either is fine.)

`internal/check/types_test.go`: `TestFindingKey` (`Finding{CheckID:"a",EntityKey:"b"}.Key()=="a:b"`), `TestCheckAppliesTo`.

- [ ] **Step 2: Run tests, verify FAIL** — `go test ./internal/check/` → undefined symbols.
- [ ] **Step 3: Implement** the four files exactly per the interface block; `sortFindings` uses `sort.SliceStable` on (severityRank, CheckID, EntityKey).
- [ ] **Step 4: Run** `go test ./internal/check/ -race -cover` → PASS, ≥85 %.
- [ ] **Step 5: Commit** `feat(check): check descriptor, finding/report types, registry and runner`

---

### Task 2: `[checks]` config section, defaults, docker Fake per-command exec output

**Files:**
- Modify: `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/load_test.go`, `config.example.toml`
- Modify: `internal/clients/docker/fake.go`, `internal/clients/docker/fake_test.go`

**Interfaces:**
- Produces: `config.Checks` (below) reachable as `cfg.Checks`; `docker.Fake.ExecOutByCmd map[string]string` keyed by `container + " " + strings.Join(args, " ")`, consulted before `ExecOut[container]`.

```go
// append to internal/config/config.go
// PlexLibrary maps a Plex library title to the host directory its files live in.
type PlexLibrary struct {
	Title string `toml:"title"`
	Path  string `toml:"path"`
}

// Checks holds every threshold the check catalogue reads. Durations are
// TOML strings ("24h", "30m"); BurntSushi/toml decodes them.
type Checks struct {
	Timeout                   time.Duration `toml:"timeout"`                       // per-check timeout
	DiskPaths                 []string      `toml:"disk_paths"`                    // filesystems to watch for pressure
	DiskWarnPercent           float64       `toml:"disk_warn_percent"`
	DiskCritPercent           float64       `toml:"disk_crit_percent"`
	QueueStuckAfter           time.Duration `toml:"queue_stuck_after"`
	QBitStalledAfter          time.Duration `toml:"qbit_stalled_after"`
	CompletedNotImportedAfter time.Duration `toml:"completed_not_imported_after"`
	ArrHistoryWindow          time.Duration `toml:"arr_history_window"`
	WrongFileExts             []string      `toml:"wrong_file_exts"`
	TVCategories              []string      `toml:"tv_categories"`               // qBit categories where .iso is also wrong
	DownloadsDirs             []string      `toml:"downloads_dirs"`
	OrphanAfter               time.Duration `toml:"orphan_after"`
	RecycleDirs               []string      `toml:"recycle_dirs"`
	RecycleWarnGB             float64       `toml:"recycle_warn_gb"`
	ImageBloatWarnGB          float64       `toml:"image_bloat_warn_gb"`
	LogDirs                   []string      `toml:"log_dirs"`
	LogWarnGB                 float64       `toml:"log_warn_gb"`
	WantedSpikePercent        float64       `toml:"wanted_spike_percent"`
	WantedSpikeMin            int           `toml:"wanted_spike_min"`
	IndexerFailureWindow      time.Duration `toml:"indexer_failure_window"`
	OverseerrStuckAfter       time.Duration `toml:"overseerr_stuck_after"`
	PlexScanStaleAfter        time.Duration `toml:"plex_scan_stale_after"`
	PlexLibraries             []PlexLibrary `toml:"plex_libraries"`
	SeededMinAge              time.Duration `toml:"seeded_min_age"`
}
```
Add `Checks Checks \`toml:"checks"\`` to `Config`. Defaults (in `Defaults()`):
```go
Checks: Checks{
	Timeout: 60 * time.Second,
	DiskPaths: []string{"/"}, DiskWarnPercent: 85, DiskCritPercent: 92,
	QueueStuckAfter: 24 * time.Hour, QBitStalledAfter: time.Hour,
	CompletedNotImportedAfter: 30 * time.Minute, ArrHistoryWindow: 7 * 24 * time.Hour,
	WrongFileExts: []string{".exe", ".scr", ".bat", ".lnk", ".msi"}, TVCategories: []string{"tv"},
	OrphanAfter: 7 * 24 * time.Hour, RecycleWarnGB: 20, ImageBloatWarnGB: 2,
	LogDirs: []string{"/var/log"}, LogWarnGB: 1,
	WantedSpikePercent: 20, WantedSpikeMin: 10,
	IndexerFailureWindow: 24 * time.Hour, OverseerrStuckAfter: 7 * 24 * time.Hour,
	PlexScanStaleAfter: 2 * time.Hour, SeededMinAge: 24 * time.Hour,
},
```
`config.example.toml` gains a commented `[checks]` block with every key and a NAS-flavoured comment (`disk_paths = ["/volume1"]`, `downloads_dirs = ["/volume1/downloads/tv", "/volume1/downloads/movies"]`, `recycle_dirs = ["/volume1/#recycle", "/volume1/downloads/#recycle"]`, `plex_libraries = [{ title = "TV Shows", path = "/volume1/tv" }, { title = "Movies", path = "/volume1/movies" }]`).

Docker fake:
```go
// ExecOutByCmd, when set, overrides ExecOut for an exact command. Key is
// container + " " + strings.Join(args, " ").
func (f *Fake) Exec(_ context.Context, container string, args ...string) (string, error) {
	key := container + " " + strings.Join(args, " ")
	if out, ok := f.ExecOutByCmd[key]; ok {
		return out, f.record("Exec(%s, %s)", container, strings.Join(args, " "))
	}
	return f.ExecOut[container], f.record("Exec(%s, %s)", container, strings.Join(args, " "))
}
```

- [ ] **Step 1: Failing tests** — in `load_test.go` add `TestLoadChecksDefaultsAndOverride`: write a config with `[checks]\nqueue_stuck_after = "6h"\ndisk_paths = ["/volume1"]` and assert `cfg.Checks.QueueStuckAfter == 6*time.Hour`, `cfg.Checks.DiskPaths == []string{"/volume1"}`, and untouched `cfg.Checks.DiskWarnPercent == 85`. In `docker/fake_test.go` add `TestFakeExecOutByCmd` (per-command key wins, fallback to ExecOut).
- [ ] **Step 2: Run, verify FAIL.**
- [ ] **Step 3: Implement** config + defaults + example TOML + fake.
- [ ] **Step 4: Run** `go test ./internal/config/ ./internal/clients/docker/ -race` → PASS. Run `go run ./cmd/healarr config validate --config config.example.toml` is NOT required (example has placeholder api_key_file paths).
- [ ] **Step 5: Commit** `feat(config): [checks] thresholds section; docker fake per-command exec output`

---

### Task 3: `internal/store` — SQLite open, migrations, reports and findings

**Files:**
- Create: `internal/store/store.go`, `internal/store/migrate.go`, `internal/store/migrations/0001_init.sql`, `internal/store/reports.go`, `internal/store/findings.go`
- Test: `internal/store/store_test.go`, `internal/store/findings_test.go`
- Modify: `go.mod` (`go get modernc.org/sqlite@latest` — record the resolved version in the commit body)

**Interfaces:**
- Produces:
```go
package store

type Store struct { db *sql.DB }

// Open opens (creating if needed) the SQLite file at path, applies
// PRAGMA journal_mode=WAL and synchronous=NORMAL, busy_timeout 5000 ms,
// and runs pending migrations. The parent directory must exist.
func Open(ctx context.Context, path string) (*Store, error)
func (s *Store) Close() error
// SchemaVersion returns the applied migration number.
func (s *Store) SchemaVersion(ctx context.Context) (int, error)

// SaveReport inserts the report row and upserts its findings in one
// transaction. It returns the stored report id and an UpsertSummary.
func (s *Store) SaveReport(ctx context.Context, rep check.Report) (int64, UpsertSummary, error)
// LatestReport returns the most recent report for node (findings not
// loaded; Metrics loaded). ok=false when none.
func (s *Store) LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error)
// OpenFindings returns findings with status open or snoozed for node,
// sorted severity desc, check_id, entity_key.
func (s *Store) OpenFindings(ctx context.Context, node config.Node) ([]StoredFinding, error)

type UpsertSummary struct { New, Updated, Resolved int }

type StoredFinding struct {
	ID         int64
	check.Finding
	Status     string     // open|snoozed|resolved
	SeenCount  int
	ResolvedAt *time.Time
}
```
Dedup rule (`findings.go`, inside the SaveReport tx):
1. For each finding in `rep.Findings`: if a row exists with same `check_id`+`entity_key` and `status IN ('open','snoozed')` → `UPDATE last_seen=?, seen_count=seen_count+1, severity=?, summary=?, detail=?, data=?` (FirstSeen kept) → Updated++. Else INSERT with `first_seen=last_seen=rep.GeneratedAt` → New++.
2. For every check id in `rep.Ran` (successful checks only — never for `Errors`/`Skipped`): open findings for `(node, check_id)` whose `entity_key` is not in this report → `UPDATE status='resolved', resolved_at=?` → Resolved++.

Migration mechanism: `//go:embed migrations/*.sql`; files sorted by name; `schema_meta(version INTEGER NOT NULL)` single row; each migration applied in its own tx then version updated; idempotent on rerun.

`0001_init.sql` (all C3 tables so later phases only add columns):
```sql
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
CREATE UNIQUE INDEX findings_open_key ON findings (check_id, entity_key) WHERE status IN ('open','snoozed');
CREATE INDEX findings_node_status ON findings (node, status);
CREATE TABLE remediations (id INTEGER PRIMARY KEY, finding_id INTEGER REFERENCES findings(id), node TEXT NOT NULL,
  action TEXT NOT NULL, tier TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 1, status TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, finished_at TEXT);
CREATE TABLE decisions (id INTEGER PRIMARY KEY, entity_key TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
  snooze_until TEXT, requested_at TEXT NOT NULL, executed_at TEXT, error TEXT NOT NULL DEFAULT '');
CREATE TABLE peer_messages (id INTEGER PRIMARY KEY, direction TEXT NOT NULL, kind TEXT NOT NULL, peer TEXT NOT NULL,
  payload TEXT NOT NULL, received_at TEXT NOT NULL);
CREATE TABLE staleness_scores (entity_key TEXT PRIMARY KEY, score REAL NOT NULL, components TEXT NOT NULL,
  computed_at TEXT NOT NULL);
CREATE TABLE llm_calls (id INTEGER PRIMARY KEY, called_at TEXT NOT NULL, model TEXT NOT NULL, input_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL, cost_usd REAL NOT NULL, ok INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '');
CREATE TABLE email_outbox (id INTEGER PRIMARY KEY, to_addr TEXT NOT NULL, subject TEXT NOT NULL, body TEXT NOT NULL,
  status TEXT NOT NULL, created_at TEXT NOT NULL, sent_at TEXT, error TEXT NOT NULL DEFAULT '');
```
Timestamps are stored as RFC 3339 UTC strings. JSON columns via `encoding/json`. DSN: `file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)`; driver name `"sqlite"` (`_ "modernc.org/sqlite"`); `db.SetMaxOpenConns(1)` (single writer, SD-card friendly).

- [ ] **Step 1: Failing tests** (`store_test.go`):
```go
func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	s := openTemp(t)
	v, err := s.SchemaVersion(context.Background())
	if err != nil || v != 1 { t.Fatalf("version=%d err=%v", v, err) }
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" { t.Fatalf("journal_mode=%q err=%v", mode, err) }
}
func TestOpenIsIdempotent(t *testing.T) { /* Open twice on same path; version stays 1 */ }
func TestOpenFailsWhenDirMissing(t *testing.T) { /* Open(filepath.Join(t.TempDir(),"nope","x.db")) returns error */ }
```
`findings_test.go`:
```go
func TestSaveReportDedupsAndResolves(t *testing.T) {
	s := openTemp(t); ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	f := func(check, key string, sev check.Severity, at time.Time) check.Finding { /* build finding with FirstSeen=LastSeen=at, Node pi */ }
	r1 := check.Report{Node: config.NodePi, GeneratedAt: t0, Ran: []string{"a", "b"}, Metrics: map[string]float64{"m": 1},
		Findings: []check.Finding{f("a", "k1", check.SeverityWarn, t0), f("b", "k2", check.SeverityCritical, t0)}}
	_, sum, err := s.SaveReport(ctx, r1)
	// sum == {New:2}
	t1 := t0.Add(time.Hour)
	r2 := check.Report{Node: config.NodePi, GeneratedAt: t1, Ran: []string{"a"}, Errors: []check.CheckError{{CheckID: "b", Error: "x"}},
		Findings: []check.Finding{f("a", "k1", check.SeverityCritical, t1)}}
	_, sum, err = s.SaveReport(ctx, r2)
	// sum == {Updated:1} — k1 updated (severity now critical, first_seen still t0, seen_count 2); k2 NOT resolved because b errored
	open, _ := s.OpenFindings(ctx, config.NodePi)
	// len(open)==2; open[0] is a:k1 critical with FirstSeen t0, LastSeen t1, SeenCount 2
	r3 := check.Report{Node: config.NodePi, GeneratedAt: t1.Add(time.Hour), Ran: []string{"a", "b"}}
	_, sum, err = s.SaveReport(ctx, r3)
	// sum == {Resolved:2}; OpenFindings empty
	rep, ok, _ := s.LatestReport(ctx, config.NodePi)
	// ok, rep.GeneratedAt == r3.GeneratedAt, and LatestReport for r1 earlier had Metrics["m"]==1 (assert on a LatestReport call after r1)
}
func TestLatestReportNoneForNode(t *testing.T) { /* ok=false for nas on empty store */ }
func TestOpenFindingsOtherNodeIsolated(t *testing.T) { /* pi findings don't show under nas */ }
```
- [ ] **Step 2: Run, verify FAIL.**
- [ ] **Step 3: Implement** the five files (each <400 lines).
- [ ] **Step 4: Run** `go test ./internal/store/ -race -cover` → PASS ≥80 %; `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` → OK (proves modernc is cgo-free).
- [ ] **Step 5: Commit** `feat(store): sqlite store with embedded migrations, report and finding upsert`

---

## Check family conventions (apply to Tasks 4–9)

Every family package `internal/checks/<family>` exports exactly one constructor:

```go
// Checks returns this family's catalogue rows. cfg is read for thresholds
// only; clients come from check.Deps at run time.
func Checks(cfg config.Config) []check.Check
```

Rules every check follows:
- Entity keys are stable identifiers: container+path for mounts, `sonarr:<seriesId>` / `radarr:<movieId>` / queue `sonarr:queue:<id>`, torrent `qbit:<hash>`, indexer `prowlarr:<indexerId>`, path strings for disk/dirs, `plex:<libraryKey>`. A service-level condition (unreachable, auth) uses the service name as key (`sonarr`, `plex`).
- A nil client the check needs → `return check.Result{}, check.ErrNotConfigured`. An empty path list the check needs → same.
- A client call error → `return check.Result{}, fmt.Errorf("<call>: %w", err)`. Reachability checks are the exception: they convert the error into a critical finding (that is their purpose) and return nil error.
- Findings are built with `d.NewFinding(...)`; `Detail` carries the human explanation, `Data` carries numbers the digest may format (sizes in bytes, counts, percentages).
- Table tests: one `[]struct{name string; deps func() check.Deps; want []wantFinding; wantErr error}` per check, where `wantFinding{key string; sev check.Severity}`; a helper `run(t, c, deps)` asserts the finding keys+severities set-equal `want` and error `errors.Is` `wantErr`. Every check has at least: happy/no-finding, one finding per rule branch, not-configured, client error.
- `Now` in tests is fixed: `time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)`.
- Each file <400 lines: one file per check plus `checks.go` with `Checks()`; tests mirror.

Node constants: `pi := []config.Node{config.NodePi}`, `nas := []config.Node{config.NodeNAS}`, `both := []config.Node{config.NodePi, config.NodeNAS}` — define them in `internal/check/nodes.go` as `check.PiOnly`, `check.NASOnly`, `check.BothNodes` (add in Task 4 with a one-line test).

Cadence constants (add to `internal/check/nodes.go` too): `check.Every5m = 5*time.Minute`, `Every15m`, `Hourly`, `Daily = 24*time.Hour`.

---

### Task 4: `internal/checks/mounts` — `mount_race`, `host_mount_health`

**Files:**
- Create: `internal/check/nodes.go` (+ `nodes_test.go`), `internal/checks/mounts/checks.go`, `internal/checks/mounts/mount_race.go`, `internal/checks/mounts/host_mount_health.go`
- Test: `internal/checks/mounts/mount_race_test.go`, `internal/checks/mounts/host_mount_health_test.go`

**Interfaces:**
- Consumes: `check.Deps.Docker.Exec`, `check.Deps.Host.{IsMountpoint,DeviceID,Usage}`, `cfg.Mounts`.
- Produces: `mounts.Checks(cfg) []check.Check` with ids `mount_race` (pi, `TierCorrect`, `Every5m`) and `host_mount_health` (both, `TierObserve`, `Every5m`).

**`mount_race` rule** (pi). Requires `d.Docker` and `d.Host` non-nil and `len(cfg.Mounts) > 0`, else `ErrNotConfigured`. For each `m := cfg.Mounts[i]`:
1. `out, err := d.Docker.Exec(ctx, m.Container, "sh", "-c", "stat -c %d / "+shellQuote(m.ContainerPath)+"; ls -A "+shellQuote(m.ContainerPath)+" | wc -l")` — one exec, three lines: root device, path device, entry count. Exec error → **finding** `critical`, key `m.Container+":"+m.ContainerPath`, summary `"cannot inspect mount inside container"` (a container that can't be exec'd is itself a symptom), continue.
2. Parse three integers (trim, split on newline); parse failure → return error `fmt.Errorf("parse exec output for %s: %w", ...)`.
3. `hostMounted, err := d.Host.IsMountpoint(ctx, m.Host)`; error → return error.
4. Decide:
   - `pathDev == rootDev` → **critical**, `TierCorrect`, summary `"<container>:<path> is bound to the container root filesystem (mount race)"`, Data `{"rootDevice":..,"pathDevice":..,"entries":n,"hostMounted":hostMounted}`.
   - else if `entries == 0 && hostMounted` → **warn**, summary `"<container>:<path> is empty while host mount is healthy"`.
   - else no finding.
   `shellQuote` wraps in single quotes with `'\''` escaping (put it in `mount_race.go`, test it).

**Incident fixture** (must be the first table row, named `"2026-09-12 mount race"`): `cfg.Mounts = [{Host:"/mnt/nas/tv", Container:"sonarr", ContainerPath:"/tv"}]`; `docker.Fake{ExecOutByCmd: {"sonarr sh -c stat -c %d / '/tv'; ls -A '/tv' | wc -l": "2049\n2049\n0\n"}}`; `hostfs.Fake{Devices: {"/mnt/nas/tv": 41, "/mnt/nas": 2049}}` (host path IS a mountpoint: differs from parent). Expect exactly one finding `mount_race`, key `sonarr:/tv`, `critical`. Second row `"healthy"`: output `"2049\n41\n120\n"` → no findings. Third `"empty but mounted"`: `"2049\n41\n0\n"` → warn. Fourth `"exec fails"`: `Fake.Err = errors.New("no such container")` → critical finding, nil error. Fifth `"garbage output"`: `"x\n"` → error. Sixth `"not configured"`: no mounts → `ErrNotConfigured`.

**`host_mount_health` rule** (both). Requires `d.Host` and `len(cfg.Mounts)>0`. Dedupe `cfg.Mounts[i].Host` (several containers share one host path); for each unique host path: `IsMountpoint` false → **critical**, key = host path, summary `"<path> is not a mount point"`, tier observe. Else `Usage(path)` error → **critical** `"<path> unreadable: <err>"` (nil error returned — unreadable NFS is the condition we detect). Else no finding; emit metric `"mount_used_percent:<path>"` = `UsedPercent`. Tests: mounted+usage ok (no finding, metric present), not mountpoint, usage error, IsMountpoint error → returned error, not configured, dedupe (two mounts same host path → one IsMountpoint call in `Fake.Calls`).

- [ ] Step 1 write both table tests + `nodes_test.go` → Step 2 FAIL → Step 3 implement → Step 4 `go test ./internal/checks/mounts/ ./internal/check/ -race -cover` PASS ≥85 % → Step 5 commit `feat(checks): mount_race and host_mount_health with the 2026-09-12 incident fixture`

---

### Task 5: `internal/checks/arr` — `arr_health`, `arr_queue_stuck`, `arr_wanted_missing_spike`, `service_update_available`

**Files:**
- Create: `internal/checks/arr/checks.go`, `health.go`, `queue.go`, `wanted.go`, `updates.go`; tests alongside.

**Interfaces:**
- Consumes: `d.Sonarr`, `d.Radarr` (`Health`, `Queue`, `WantedMissingCount`), `d.Prowlarr.Health`, `cfg.Checks.{QueueStuckAfter,WantedSpikePercent,WantedSpikeMin}`, `d.PreviousMetric`.
- Produces: `arr.Checks(cfg)` with `arr_health` (pi, observe, 5m), `arr_queue_stuck` (pi, nudge, 15m), `arr_wanted_missing_spike` (pi, observe, daily), `service_update_available` (both, observe, daily).

Shared helper `health.go`: `type app struct{ name string; health func(context.Context) ([]sonarr.HealthItem, error) }` is awkward across three packages' distinct `HealthItem` types — instead define a local `type healthItem struct{ Type, Source, Message string }` and three tiny adapters (`sonarrHealth(d) ([]healthItem, error)` etc.) that return `nil, nil` when the client is nil. `arr_health` and `service_update_available` both iterate `apps := []struct{name string; items []healthItem}` for sonarr, radarr, prowlarr; if **all three** clients are nil → `ErrNotConfigured`.

**`arr_health`**: for each item with `Source != "UpdateCheck"`: severity `critical` if `strings.EqualFold(Type,"error")`, else `warn`; key `"<app>:"+Source+":"+shortHash(Message)` where `shortHash` = first 8 hex of sha256 (put in `health.go`); summary `"<app>: <Message>"`; tier observe. Client error → finding critical key `<app>` summary `"<app> unreachable: <err>"` (reachability semantics), nil error.

**`service_update_available`**: items with `Source == "UpdateCheck"` → `info` finding key `<app>:update`, summary `"<app>: <Message>"`. Client error → returned error (health already reported reachability).

**`arr_queue_stuck`**: requires Sonarr or Radarr. For each queue item (both apps): 
- `TrackedDownloadStatus` in {`warning`,`error`} (case-insensitive) or `TrackedDownloadState` in {`importFailed`,`failedPending`,`importBlocked`} → **warn**, tier nudge, key `"<app>:queue:<id>"`, summary `"<app> queue: <Title> — <first Messages entry or TrackedDownloadState>"`, Data `{"downloadId":..., "state":..., "status":...}`.
- else if `!Added.IsZero() && d.Now().Sub(Added) > cfg.Checks.QueueStuckAfter && SizeLeft > 0` → **warn**, tier nudge, summary `"<app> queue: <Title> has been downloading for <duration>h"`.
- Metrics: `sonarr_queue_len`, `radarr_queue_len`.
Tests: warning status, importFailed state, slow-old item, fresh healthy item, radarr-only, client error → error, not configured.

**`arr_wanted_missing_spike`**: requires Sonarr or Radarr. For each app: `n, err := WantedMissingCount`; error → returned error. Metric `"<app>_wanted_missing" = n`. If `prev, ok := d.PreviousMetric(...)` and `n-prev >= WantedSpikeMin` and `prev == 0 || (n-prev)/prev*100 >= WantedSpikePercent` → **warn**, key `<app>:wanted`, summary `"<app>: wanted/missing jumped from <prev> to <n>"`, Data `{"previous":prev,"current":n}`. No previous → metric only, no finding. Tests: first run, spike, small delta below min, big percent but below min, decrease.

- [ ] Steps 1–5 as per convention; commit `feat(checks): arr health, queue, wanted-missing spike and update-available checks`

---

### Task 6: `internal/checks/indexers` + `internal/checks/requests`

**Files:**
- Create: `internal/checks/indexers/{checks.go,failures.go,failures_test.go}`, `internal/checks/requests/{checks.go,tautulli.go,overseerr.go,tautulli_test.go,overseerr_test.go}`

**`indexer_failures`** (pi, observe, 15m): requires `d.Prowlarr`. `idx, err := Indexers()`; `st, err := IndexerStatus()` (errors returned). Build name map by ID. For each status: `disabled := !DisabledTill.IsZero() && DisabledTill.After(d.Now())`; `recent := !MostRecentFailure.IsZero() && d.Now().Sub(MostRecentFailure) <= cfg.Checks.IndexerFailureWindow`; if `disabled` → **warn** summary `"indexer <name> disabled until <RFC3339>"`; else if `recent` → **info** summary `"indexer <name> failed at <RFC3339>"`; key `prowlarr:<indexerId>`. Unknown id → name `"#<id>"`. Metric `prowlarr_indexers_enabled` = count of `Enable`. Tests: disabled, recent-failure, old failure (none), unknown id name, error, not configured.

**`tautulli_reachability`** (pi, observe, daily): requires `d.Tautulli`. `Ping` error → **warn** key `tautulli` summary `"tautulli unreachable: <err>"` (nil error). Else none, plus `ActivityCount` → metric `tautulli_active_streams` (ActivityCount error → returned error).

**`overseerr_stuck_processing`** (pi, observe, daily): requires `d.Overseerr`. `Requests(ctx, "processing")` error → returned error. For each with `!UpdatedAt.IsZero() && d.Now().Sub(UpdatedAt) > cfg.Checks.OverseerrStuckAfter` → **warn** key `overseerr:<id>` summary `"overseerr request #<id> (<mediaType> tmdb:<tmdbId>) processing since <date> for <RequestedBy>"`. Metric `overseerr_processing` = len. Tests: stuck, fresh, error, not configured.

- [ ] Steps 1–5; commit `feat(checks): prowlarr indexer failures, tautulli reachability, overseerr stuck requests`

---

### Task 7: `internal/checks/qbit` — `qbit_stalled_errored`, `qbit_completed_not_imported`, `wrong_file_type`, `seeded_done`

**Files:**
- Create: `internal/checks/qbit/{checks.go,stalled.go,not_imported.go,wrong_file.go,seeded.go,history.go}` + tests.

**Interfaces:**
- Consumes: `d.QBit.{Torrents,Files,Preferences}`, `d.Sonarr.History`, `d.Radarr.History` (nil-tolerant: if both nil the two history-based checks return `ErrNotConfigured`), `cfg.Checks.{QBitStalledAfter,CompletedNotImportedAfter,ArrHistoryWindow,WrongFileExts,TVCategories,SeededMinAge}`.
- Produces: `qbit.Checks(cfg)` ids `qbit_stalled_errored` (nas, nudge, 15m), `qbit_completed_not_imported` (nas, correct, 15m), `wrong_file_type` (nas, correct, 15m), `seeded_done` (nas, nudge, daily).

`history.go`: `importedDownloadIDs(ctx, d, since) (map[string]bool, error)` — union of Sonarr and Radarr `History(ctx, since, "downloadFolderImported")` `DownloadID`s upper-cased; skips nil clients; any error returned wrapped. Torrent hashes compare upper-cased.

**`qbit_stalled_errored`**: `Torrents(ctx, "", "")`. State in {`error`, `missingFiles`} → **critical**, tier nudge, summary `"<name> is in state <state>"`. State in {`stalledDL`, `metaDL`} and `!AddedOn.IsZero() && d.Now().Sub(AddedOn) > QBitStalledAfter` → **warn**, summary `"<name> stalled (<seeds> seeds, added <RFC3339>)"`. Key `qbit:<hash>`. Metrics `qbit_torrents`, `qbit_stalled`. Note qBittorrent 5 uses `stoppedDL`/`stoppedUP` for paused — do **not** flag those.

**`qbit_completed_not_imported`**: torrents with `Progress >= 1`, `Category` non-empty, `!CompletionOn.IsZero() && d.Now().Sub(CompletionOn) > CompletedNotImportedAfter`; `imported := importedDownloadIDs(ctx, d, d.Now().Add(-ArrHistoryWindow))`; hash not in `imported` → **warn**, tier correct, summary `"<name> completed <duration> ago but no arr import recorded"`, Data `{"category":..,"contentPath":..,"completedAt":..}`. Torrents older than `ArrHistoryWindow` are skipped (history window can't prove anything).

**`wrong_file_type`**: for every torrent, `Files(ctx, hash)`; for each file, `ext := strings.ToLower(filepath.Ext(name))`; bad if in `WrongFileExts`, or `ext == ".iso"` and `Category` in `TVCategories`. Any bad file → one **critical** finding per torrent, tier correct, key `qbit:<hash>`, summary `"<name> contains <n> disallowed file(s): <first bad name>"`, Data `{"files":[...bad names], "category":..}`. `Files` error → returned error. Metric `qbit_wrong_file_torrents`.

**`seeded_done`**: `prefs := Preferences()` (error returned). Torrent qualifies if `Progress >= 1` and ((`prefs.MaxRatio > 0 && Ratio >= prefs.MaxRatio`) or (`prefs.MaxSeedingTime > 0 && SeedingTime >= time.Duration(prefs.MaxSeedingTime)*time.Minute`)) and `d.Now().Sub(CompletionOn) >= SeededMinAge` and hash in `importedDownloadIDs(...)`. → **info**, tier nudge, key `qbit:<hash>`, summary `"<name> seeded to target (ratio <r>, <h>h) and imported; safe to remove"`. Metric `qbit_seeded_done`.

Tests per check per convention, using `qbittorrent.Fake{TorrentList, FilesByHash, Prefs}` and `sonarr.Fake{HistoryRecords}`. Include a fixture named `"fake .exe episode"` in `wrong_file_test.go`: torrent category `tv`, files `["Show.S01E01.mkv.exe", "readme.txt"]` → critical.

- [ ] Steps 1–5; commit `feat(checks): qbittorrent stalled, completed-not-imported, wrong-file-type and seeded-done checks`

---

### Task 8: `internal/checks/disk` — `disk_pressure_*`, `docker_image_bloat`, `log_size`, `recycle_bin_size`, `orphan_downloads`

**Files:**
- Create: `internal/checks/disk/{checks.go,pressure.go,image_bloat.go,dir_size.go,orphans.go}` + tests.

**Interfaces:**
- Consumes: `d.Host.{Usage,DirSize,ListDir}`, `d.Docker.DiskUsage`, `d.QBit.Torrents`, `cfg.Checks.{DiskPaths,DiskWarnPercent,DiskCritPercent,ImageBloatWarnGB,LogDirs,LogWarnGB,RecycleDirs,RecycleWarnGB,DownloadsDirs,OrphanAfter}`.
- Produces: `disk.Checks(cfg)` ids `disk_pressure_pi_sd` (pi, nudge, daily), `disk_pressure_nas_volume` (nas, correct, hourly), `docker_image_bloat` (pi, nudge, daily), `log_size` (pi, nudge, daily), `recycle_bin_size` (nas, correct, daily), `orphan_downloads` (nas, correct, daily).

`pressure.go`: one function `pressure(id string, nodes []config.Node, tier check.Tier, cadence time.Duration, cfg config.Config) check.Check` — both disk_pressure ids use it with `cfg.Checks.DiskPaths` (the per-node config file sets the right paths). For each path: `Usage` error → returned error; `UsedPercent >= DiskCritPercent` → **critical**; `>= DiskWarnPercent` → **warn**; key = path; summary `"<path> is <pct>% full (<free> GB free)"`; metric `"disk_used_percent:<path>"`.

`dir_size.go`: one function `dirSize(id string, nodes, tier, cadence, dirs func(config.Config) []string, warnGB func(config.Config) float64, label string) check.Check` used by `log_size` and `recycle_bin_size`. For each dir: `DirSize(ctx, dir, 0)`; error → returned error; `bytes >= warnGB*1e9` → **warn**, key = dir, summary `"<label> <dir> is <GB> GB"`; metric `"<id>_bytes:<dir>"`. Empty dir list → `ErrNotConfigured`.

`image_bloat.go`: `DiskUsage()`; `ImagesReclaimable >= ImageBloatWarnGB*1e9` → **warn**, key `docker:images`, summary `"<GB> GB of docker images reclaimable"`; metric `docker_images_reclaimable_bytes`.

`orphans.go` (`orphan_downloads`): requires `d.Host`, `d.QBit`, `len(DownloadsDirs)>0`. `torrents := Torrents(ctx,"","")` — error → returned error (**never** report orphans without the qBit view). Known set = every `ContentPath` and `SavePath+"/"+Name` (cleaned). For each dir: `ListDir` (error → returned error); each entry whose `filepath.Join(dir, Name)` is not in known set and whose parent-of-ContentPath doesn't match either, and `d.Now().Sub(ModTime) > OrphanAfter` → **warn**, tier correct, key = full path, summary `"<path> is not tracked by qBittorrent (modified <date>)"`, Data `{"size":Size,"isDir":IsDir}`. Metric `orphan_downloads_count`. Test: tracked entry ignored, untracked-old flagged, untracked-fresh ignored, qbit error → error, not configured.

- [ ] Steps 1–5; commit `feat(checks): disk pressure, image bloat, log/recycle size and orphan download checks`

---

### Task 9: `internal/checks/plex` + `internal/checks/all.go` catalogue aggregator

**Files:**
- Create: `internal/checks/plex/{checks.go,reachability.go,freshness.go}` + tests; `internal/checks/all.go`, `internal/checks/all_test.go`.

**`plex_reachability`** (nas, observe, 5m): requires `d.Plex`. `Identity()` error → **critical** key `plex` summary `"plex unreachable: <err>"` (nil error); else metric `plex_reachable = 1` and Data-less no finding. (Also record `Data{"version":Identity.Version}` on… no finding → skip; keep it simple.)

**`plex_scan_freshness`** (nas, nudge, daily): requires `d.Plex`, `d.Host`, `len(cfg.Checks.PlexLibraries)>0`. `libs := Libraries()` (error → returned). For each configured `PlexLibrary`: find lib by `Title` (case-insensitive) — missing → **warn** key `plex:<title>` summary `"plex library <title> not found"`. Else `entries := ListDir(ctx, path)` (error → returned); `newest := max(entry.ModTime)`; if `newest.Sub(lib.ScannedAt) > cfg.Checks.PlexScanStaleAfter` → **warn**, tier nudge, key `plex:<lib.Key>`, summary `"plex library <title> last scanned <RFC3339> but <path> changed <RFC3339>"`. Metric `"plex_scan_age_hours:<key>"`.

**`all.go`**:
```go
// Package checks aggregates every check family into one registry.
package checks

// Registry builds the full catalogue for cfg. It returns an error if two
// families register the same id.
func Registry(cfg config.Config) (*check.Registry, error) {
	r := check.NewRegistry()
	for _, family := range [][]check.Check{mounts.Checks(cfg), arr.Checks(cfg), indexers.Checks(cfg), requests.Checks(cfg), qbit.Checks(cfg), disk.Checks(cfg), plex.Checks(cfg)} {
		for _, c := range family { if err := r.Register(c); err != nil { return nil, err } }
	}
	return r, nil
}
```
`all_test.go`: `TestCatalogueMatchesSpec` — table of the 18 ids from spec C5 (all except `staleness_scan`) with expected nodes and cadence; assert each is registered with exactly those nodes and cadence, and that the registry has exactly 18 entries. `TestNoDuplicateMetricsAcrossChecks` is not feasible statically — skip.

- [ ] Steps 1–5; commit `feat(checks): plex reachability/scan freshness and the aggregated catalogue registry`

---

### Task 10: `internal/notify/digest.go` — templated plain-text digest

**Files:**
- Create: `internal/notify/digest.go`, `internal/notify/digest.tmpl` (embedded), `internal/notify/digest_test.go`

**Interfaces:**
- Produces:
```go
package notify

// DigestInput is everything the templated digest renders. Phase 3 fills
// Peer from the NAS report; Phase 5 prepends an LLM narrative.
type DigestInput struct {
	Node        config.Node
	GeneratedAt time.Time
	Findings    []check.Finding   // open findings, already sorted severity desc
	Errors      []check.CheckError
	Skipped     []string
	ChecksRun   int
	Metrics     map[string]float64
	BaseURL     string            // e.g. "http://192.0.2.20/healarr" — may be empty in Phase 2
}
// RenderDigest renders the plain-text digest. It never returns an empty
// string on success: with no findings it says so explicitly.
func RenderDigest(in DigestInput) (string, error)
```
Template layout (plain text, ≤78 cols): header `healarr digest — <node> — <YYYY-MM-DD HH:MM TZ>`; `Summary: N checks run, F failed, S skipped, X open findings (C critical, W warn, I info)`; sections `CRITICAL`, `WARN`, `INFO` each listing `- [<checkId>] <summary>` + indented detail when present; `Check errors:` list; `Skipped (not configured):` comma list; footer `Decisions: <BaseURL>/decisions` only when BaseURL non-empty. Use `text/template` with `embed`. Tests: empty findings → contains "no open findings"; mixed severities → section order and counts; errors section; BaseURL footer present/absent; template parse error impossible (embedded) — cover the `Execute` error path with a writer that fails? Not needed; keep ≥80 % via input variety.

- [ ] Steps 1–5; commit `feat(notify): templated plain-text digest renderer`

---

### Task 11: CLI — `healarr check list|run`, `healarr report generate`, Deps wiring

**Files:**
- Create: `internal/cli/cmd_check.go`, `internal/cli/cmd_report.go`, `internal/cli/checkdeps.go`, tests `cmd_check_test.go`, `cmd_report_test.go`
- Modify: `internal/cli/deps.go` (add fields), `internal/cli/root.go` (register), `internal/cli/cmd_test.go` (`newTestDeps` wires the new fields)

**Interfaces:**
- Consumes: `check.Run`, `checks.Registry`, `store.Open/SaveReport/LatestReport/OpenFindings`, `notify.RenderDigest`.
- Produces on `Deps`:
```go
Registry func(cfg config.Config) (*check.Registry, error)   // default: checks.Registry
OpenStore func(ctx context.Context, cfg config.Config) (StoreAPI, error) // default: store.Open(ctx, cfg.State.DBPath)
```
where `StoreAPI` is a small interface in `checkdeps.go` (`SaveReport`, `LatestReport`, `OpenFindings`, `Close`) satisfied by `*store.Store`, and a `FakeStore` in `cmd_check_test.go` records saved reports and returns canned open findings.

`checkdeps.go`: `buildCheckDeps(ctx, deps, flags, cfg, sec) (check.Deps, error)` — constructs each client via the existing `deps.<Svc>` constructor **only if** its URL is non-empty (Docker: always on pi, and on nas only if `cfg.Docker.Socket` exists as a file — `os.Stat`; Host: always). A constructor error is returned (not swallowed). `Now = time.Now`.

`cmd_check.go`:
- `healarr check list [--json]`: prints `ID, Nodes, Tier, Cadence` for the registry (all nodes) — no config needed beyond loading it for `Registry(cfg)`.
- `healarr check run [--all] [--id <id>] [--json]` (+ global `--dry-run`): exactly one of `--all`/`--id` required (error otherwise); `--id` must exist in the registry and apply to `cfg.Node` (error `check <id> does not run on node <node>`). Flow: load cfg → registry → select checks → if not dry-run: `st := deps.OpenStore(...)`, `prev, _ := st.LatestReport(cfg.Node)` and set `Deps.Previous`; run with `cfg.Checks.Timeout`; if not dry-run `SaveReport`. Output: JSON → `{"report":<report>,"persisted":bool,"upsert":{new,updated,resolved}}`; table → one row per finding `Severity | CheckID | EntityKey | Summary` then a line `checks: <run> run, <failed> failed, <skipped> skipped; findings: <n>` and, when errors exist, `errors:` lines. Exit code stays 0 even with findings (findings are data, not failure); a check error is also exit 0 but printed — only load/store failures return an error.
- `report generate [--json]` (+ `--dry-run`): if dry-run → run all node checks in memory (as above, Previous nil) and render the digest from that report; else open store, `OpenFindings(cfg.Node)` + `LatestReport` → render digest with stored findings and the latest report's counts. `--json` prints `DigestInput` as JSON instead of text.

Tests (through the real cobra tree with fakes, like `cmd_test.go`): `check list` shows 18 rows; `check run --all --dry-run` with a `sonarr.Fake{HealthItems: [{Type:"error",Source:"X",Message:"boom"}]}` prints an `arr_health` critical row and never calls `OpenStore` (assert FakeStore.Opened == false); `check run --all` (no dry-run) calls `SaveReport` once and prints `persisted` in JSON; `check run --id mount_race` on `nas` node config errors; `check run` with neither flag errors; `report generate --dry-run` output contains `healarr digest`; `report generate` (store path) renders canned findings from FakeStore.

- [ ] Steps 1–5; commit `feat(cli): check list/run and report generate verbs backed by the check engine and store`

---

### Task 12: Host deployment (operator task — run by the orchestrating session, not a subagent)

**Files:** none in-repo. Records go to `~/healarr-night-report.md`.

Preconditions: PR merged, local `main` fast-forwarded, `ssh pi ~/healthcheck.sh` prints `ALL OK`.

- [ ] `make build-pi build-nas` from `main`; note `dist/healarr-linux-arm64` / `-amd64` sizes.
- [ ] Pi: `scp dist/healarr-linux-arm64 pi:/tmp/healarr.new && ssh pi 'sudo install -m 0755 /tmp/healarr.new /usr/local/bin/healarr.next && ([ -f /usr/local/bin/healarr ] && sudo cp /usr/local/bin/healarr /usr/local/bin/healarr.prev || true) && sudo mv /usr/local/bin/healarr.next /usr/local/bin/healarr && sudo mkdir -p /etc/healarr /var/lib/healarr && sudo chown parso:parso /etc/healarr /var/lib/healarr'`.
- [ ] Pi config: write `/etc/healarr/config.toml` from `config.example.toml` with `node = "pi"`, service URLs `http://localhost:<port>`, `api_key_file` under the Pi's `$DOCKER_CONFIG` (from `~/simplarr/.env`), `[[mounts]]` for sonarr `/tv` `/downloads`, radarr `/movies` `/downloads` against `/mnt/nas/{tv,downloads,movies}`, `[checks] disk_paths=["/"]`, `log_dirs=["/var/log"]`. `secrets.toml` 0600: overseerr key from `$DOCKER_CONFIG/overseerr/settings.json` (`.main.apiKey`), tautulli from `$DOCKER_CONFIG/tautulli/config.ini` (`api_key`), `peer_token`/`web_token` = `openssl rand -hex 32`, qBit empty, plex token empty on the Pi.
- [ ] NAS: `scp dist/healarr-linux-amd64 nas:/volume1/docker/healarr/healarr.new && ssh nas 'cd /volume1/docker/healarr && ([ -f healarr ] && cp healarr healarr.prev || true) && mv healarr.new healarr && chmod 0755 healarr'`. Config `node = "nas"`, `services.qbittorrent.url = http://localhost:8080`, `services.plex.url` = the plex.direct URL from the Pi compose `extra_hosts`, sonarr/radarr URLs pointing at the Pi with their API keys copied into NAS `secrets.toml` (needed for history-based qBit checks), `[checks] disk_paths=["/volume1"]`, `downloads_dirs`, `recycle_dirs`, `plex_libraries`; `plex_token` from `/volume1/PlexMediaServer/AppData/Plex Media Server/Preferences.xml` `PlexOnlineToken` (find the actual path with `find /volume1 -maxdepth 4 -name Preferences.xml 2>/dev/null`); same `peer_token`.
- [ ] On each host: `healarr config validate --config <cfg>`; `healarr check run --all --dry-run --json --config <cfg>` → save output to `~/healarr-night-report.md` appendix; `healarr report generate --dry-run --config <cfg>`.
- [ ] `ssh pi ~/healthcheck.sh` → `ALL OK`. Nothing on the stack was touched (CLI only), but verify anyway.

---

### Task 13: Docs — README status, runbook, architecture, ADR-017, tools.md

**Files:**
- Modify: `README.md` (Phase 2 ✅, add `check run`/`report generate` examples), `docs/runbook.md` (new section "Running checks by hand": `check list`, `check run --all --dry-run`, `--id`, state DB location, `--dry-run` semantics, how findings dedupe/resolve), `docs/architecture.md` (Phase 2 ✅; State store section lists the real tables; Components mention `internal/check` runner + `internal/checks/*`), `docs/decisions.md` (ADR-017), `docs/tools.md` (top note: the check engine landed in Phase 2 as `internal/check` + `internal/checks/*`; the tool dispatcher for a future agentic loop is still unbuilt).

**ADR-017 — Stateless checks, stateful store** (write in the existing ADR voice: Context / Decision / Consequences):
- Context: checks must be table-testable against fakes; some rules need yesterday's number; the SD card must not see per-check writes.
- Decision: a check is a `check.Check` struct with a pure `Run(ctx, Deps) (Result, error)`; deltas come from `Deps.Previous` (the last persisted report's `Metrics`), so no check touches the store; the store dedups on `(check_id, entity_key)` while `status ∈ {open, snoozed}` and auto-resolves findings a *successful* check stopped emitting (a failed check never resolves anything); `--dry-run` means "no store"; every threshold is `[checks]` config; Phase 2 records tiers but executes nothing.
- Consequences: checks are trivially testable and deterministic; a stale `Previous` after a long outage produces one spurious spike finding (accepted); the store schema carries all C3 tables from migration 0001 so later phases add columns not tables.

- [ ] Write docs → `make lint` unaffected → commit `docs: phase 2 status, runbook for check/report verbs, ADR-017 stateless checks`

---

## Self-review notes

- **Spec coverage:** C3 store ✓ (Task 3, all tables, WAL/NORMAL, dedup rule); C5 rows: mount_race, host_mount_health (T4); arr_health, arr_queue_stuck, arr_wanted_missing_spike, service_update_available (T5); indexer_failures, tautulli_reachability, overseerr_stuck_processing (T6); qbit_stalled_errored, qbit_completed_not_imported, wrong_file_type, seeded_done (T7); disk_pressure_nas_volume, disk_pressure_pi_sd, docker_image_bloat, log_size, recycle_bin_size, orphan_downloads (T8); plex_reachability, plex_scan_freshness (T9). `staleness_scan` is Phase 4 by design. CLI verbs `check run --all|--id [--dry-run] --json`, `report generate` ✓ (T11). Mount-race incident fixture ✓ (T4). Table tests ✓ (every family). ≥80 % ✓ (gate in every task).
- **Deviations from spec, with reasons:** `service_update_available` is derived from the *arr `UpdateCheck` health item rather than a registry lookup (no registry client exists; adding one is Phase 3+ scope). `orphan_downloads` does not consult *arr history (qBit tracking is the reliable signal; a torrent removed from qBit but imported is by definition an orphan copy). `disk_pressure_*` share one implementation parametrised per node config. `Checks` config section is new (spec says "weights live in config, not code").
- **Type consistency:** `check.Result{Findings, Metrics}` is what every `Run` returns; `Report.Metrics` is what `Deps.PreviousMetric` reads; `store.SaveReport(ctx, check.Report)`; `notify.DigestInput.Findings []check.Finding`; `StoredFinding` embeds `check.Finding` so `report generate` can pass `[]check.Finding` by projecting.
- **Placeholder scan:** none; every threshold has a default value and a config key.
