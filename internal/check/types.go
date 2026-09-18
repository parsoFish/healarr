// Package check defines the check engine: the Check descriptor, the
// Finding and Report value types every check family produces, and the
// registry/runner that execute checks against a Deps bundle.
package check

import (
	"context"
	"errors"
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
	for _, n := range c.Nodes {
		if n == node {
			return true
		}
	}
	return false
}
