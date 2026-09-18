// Package decision executes a human's keep/delete choice about a
// staleness candidate (ADR-016): "keep" snoozes the entity for
// cfg.Staleness.SnoozeDays; "delete" removes it from Sonarr/Radarr (with
// files and an import-list exclusion), best-effort declines any matching
// Overseerr request, and best-effort tells the peer node which torrents
// to remove too. Per ADR-006/constraints.md, every mutating path checks
// cfg.Actions.Enabled before any client write; "keep" never mutates the
// media stack, so it is always allowed.
package decision

import (
	"context"
	"errors"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// Kind is a decision's kind, mirroring the "kind" column CreateDecision
// writes: "keep" (snooze) or "delete" (remove from the *arr apps, decline
// matching requests, best-effort tell the peer).
type Kind string

const (
	KindKeep   Kind = "keep"
	KindDelete Kind = "delete"
)

// ErrActionsDisabled is returned by Execute's delete branch when
// cfg.Actions.Enabled is false, before any client is touched. It is
// never returned for a "keep" decision: snoozing findings isn't a stack
// mutation (constraints.md).
var ErrActionsDisabled = errors.New("decision: actions are disabled in config")

// ErrUnknownKind is returned by Execute when a decision's stored kind
// isn't "keep" or "delete".
var ErrUnknownKind = errors.New("decision: unknown kind")

// ErrUnknownEntityKey is returned by Execute's delete branch when a
// decision's entity key doesn't parse as "sonarr:<id>" or "radarr:<id>".
var ErrUnknownEntityKey = errors.New("decision: unknown entity key")

// historyLookback bounds how far back Execute searches Sonarr/Radarr
// history for the download ids behind a deleted series/movie, when
// telling the peer which torrents to remove too. It is a code constant
// (like peer.DefaultTimeout/DefaultRetries) rather than a config field:
// it is a best-effort hint's search window, not a safety rail an operator
// needs to tune.
const historyLookback = 90 * 24 * time.Hour

// DecisionStore is the subset of *store.Store Execute needs: read back a
// pending decision, resolve it, apply a "keep"'s snooze, and record every
// delete attempt. It is satisfied by *store.Store (see the compile-time
// assertion in decision_test.go); CreateDecision/PendingDecisions stay on
// the concrete store type since only the CLI (which already holds one)
// creates decisions, not Execute.
type DecisionStore interface {
	DecisionByID(ctx context.Context, id int64) (store.Decision, bool, error)
	MarkDecision(ctx context.Context, id int64, status string, at time.Time, cause error) error
	SnoozeEntity(ctx context.Context, entityKey string, until time.Time) error
	RecordRemediation(ctx context.Context, r store.Remediation) (int64, error)
}

// Deps carries everything Execute needs: the same clients/config checks
// run against (check.Deps — Now, Cfg, Sonarr, Radarr, Overseerr), the
// decision-scoped store surface, and the peer client that carries a
// best-effort qbit_delete hint to the NAS. Peer is nil on a node with no
// peer configured, in which case the hint is simply never sent.
type Deps struct {
	check.Deps
	Store DecisionStore
	Peer  peer.Client
}
