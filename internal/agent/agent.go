// Package agent implements the healarr daemon's per-cycle logic: running
// this node's checks by cadence, persisting the resulting report, pushing
// it to the peer node over the peer channel, and answering the inbound
// peer.Handler calls the other node's daemon makes against this one.
//
// Scheduling (cron), heartbeats, the digest email and the top-level Run
// loop are Task 7 (schedule.go, digest.go, run.go); this package only
// implements one cycle (cycle.go) and the peer handler adapter
// (handler.go) for now.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/store"
)

// Store is the subset of *store.Store the agent uses. It is satisfied by
// *store.Store (see the compile-time assertion in agent_test.go); tests
// swap in a fakeStore instead.
type Store interface {
	SaveReport(ctx context.Context, rep check.Report) (int64, store.UpsertSummary, error)
	LatestReport(ctx context.Context, node config.Node) (check.Report, bool, error)
	OpenFindings(ctx context.Context, node config.Node) ([]store.StoredFinding, error)
	SavePeerMessage(ctx context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error)
	LastPeerMessageAt(ctx context.Context, peer config.Node, kind string) (time.Time, bool, error)
	EnqueueEmail(ctx context.Context, to, subject, body string, at time.Time) (int64, error)
	MarkEmailSent(ctx context.Context, id int64, at time.Time) error
	MarkEmailFailed(ctx context.Context, id int64, at time.Time, cause error) error
	Checkpoint(ctx context.Context) error
}

// ErrInvalidOptions wraps every field-validation error New returns.
var ErrInvalidOptions = errors.New("agent: invalid options")

// Options wires the daemon. Registry, Deps, Store, Cfg.Node, Now and
// Version are required; New rejects a zero value for any of them. Peer
// and Sender may be nil on the node that doesn't use them: a node with no
// peer configured never pushes or answers peer calls meaningfully, and
// only the Pi ever sends mail (constraints.md).
type Options struct {
	Cfg       config.Config
	Registry  *check.Registry
	Deps      func(ctx context.Context) (check.Deps, error) // builds clients per cycle (fresh Previous)
	Store     Store
	Peer      peer.Client   // nil => no push/heartbeat
	Sender    notify.Sender // nil => no email (NAS)
	Logger    *slog.Logger
	Now       func() time.Time
	Version   string
	PeerToken string // from secrets; Run requires this when cfg.Peer.ListenAddr is set
}

// Agent runs check cycles for one node, pushes reports to its peer, and
// answers the inbound calls the peer's daemon makes against this one.
type Agent struct {
	cfg       config.Config
	registry  *check.Registry
	deps      func(ctx context.Context) (check.Deps, error)
	store     Store
	peer      peer.Client
	sender    notify.Sender
	logger    *slog.Logger
	now       func() time.Time
	version   string
	peerToken string

	// startedAt is set once, by Run, to a.now() before the scheduler
	// starts dispatching jobs — never written concurrently with a read,
	// since every read happens inside a job goroutine cron spawns after
	// Run's call to sched.Start() (itself after this field is set), and
	// the Go memory model guarantees a goroutine's creation happens-after
	// everything before the "go" statement that started it. Zero
	// (unset) when SendHeartbeat is called directly without going
	// through Run, e.g. in tests; SendHeartbeat treats that as "unknown
	// uptime" rather than computing a bogus multi-century duration.
	startedAt time.Time
}

// New validates o and returns an Agent. Logger defaults to slog.Default()
// when nil (documented default; every other required field must be set).
func New(o Options) (*Agent, error) {
	if err := validateOptions(o); err != nil {
		return nil, err
	}

	logger := o.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Agent{
		cfg:       o.Cfg,
		registry:  o.Registry,
		deps:      o.Deps,
		store:     o.Store,
		peer:      o.Peer,
		sender:    o.Sender,
		logger:    logger,
		now:       o.Now,
		version:   o.Version,
		peerToken: o.PeerToken,
	}, nil
}

// validateOptions checks every required field (everything except Peer,
// Sender and Logger, which have their own defaulting/optionality rules).
func validateOptions(o Options) error {
	switch {
	case o.Cfg.Node == "":
		return fmt.Errorf("%w: cfg.node is required", ErrInvalidOptions)
	case o.Registry == nil:
		return fmt.Errorf("%w: registry is required", ErrInvalidOptions)
	case o.Deps == nil:
		return fmt.Errorf("%w: deps is required", ErrInvalidOptions)
	case o.Store == nil:
		return fmt.Errorf("%w: store is required", ErrInvalidOptions)
	case o.Now == nil:
		return fmt.Errorf("%w: now is required", ErrInvalidOptions)
	case o.Version == "":
		return fmt.Errorf("%w: version is required", ErrInvalidOptions)
	default:
		return nil
	}
}

// HasSender reports whether this agent can send email (Options.Sender !=
// nil). Task 7's SendDigest checks this before enqueuing.
func (a *Agent) HasSender() bool { return a.sender != nil }

// otherNode returns the two-node deployment's other node: pi's peer is
// nas and vice versa (mirrors internal/notify's private otherNode, which
// this package cannot import without exporting it).
func otherNode(n config.Node) config.Node {
	if n == config.NodePi {
		return config.NodeNAS
	}
	return config.NodePi
}

// fallbackPayload builds a best-effort JSON payload to record when
// marshalling the real payload failed, so the peer message is still
// recorded (never silently dropped) even though its body only carries the
// marshal error.
func fallbackPayload(err error) []byte {
	return []byte(fmt.Sprintf(`{"marshalError":%q}`, err.Error()))
}
