// Package peer implements the Phase 3 node-to-node HTTP channel: the wire
// types the Pi and NAS exchange, and a retrying bearer-token client for the
// caller side. The server side (Task 4) accepts the same types.
package peer

import (
	"context"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

// ReportEnvelope is the body of POST /v1/report and GET /v1/report/latest.
type ReportEnvelope struct {
	Node    config.Node  `json:"node"`
	SentAt  time.Time    `json:"sentAt"`
	Version string       `json:"version"`
	Report  check.Report `json:"report"`
}

// Heartbeat is the body of POST /v1/heartbeat.
type Heartbeat struct {
	Node          config.Node `json:"node"`
	At            time.Time   `json:"at"`
	Version       string      `json:"version"`
	UptimeSeconds int64       `json:"uptimeSeconds"`
}

// Decision is the body of POST /v1/decision. Phase 3 only records it;
// nothing acts on it until Phase 4 (ADR-006, observe-only).
type Decision struct {
	ID          int64          `json:"id"`
	Kind        string         `json:"kind"`
	EntityKey   string         `json:"entityKey"`
	Payload     map[string]any `json:"payload,omitempty"`
	RequestedAt time.Time      `json:"requestedAt"`
}

// Ack is the body of every POST's 2xx response.
type Ack struct {
	Status string `json:"status"`
	ID     int64  `json:"id,omitempty"`
}

// Client talks to one peer over the node-to-node HTTP channel.
type Client interface {
	PushReport(ctx context.Context, env ReportEnvelope) (Ack, error)
	// FetchLatest reports ok=false (with a nil error) when the peer has no
	// report yet (404), rather than treating that as a failure.
	FetchLatest(ctx context.Context) (ReportEnvelope, bool, error)
	SendDecision(ctx context.Context, d Decision) (Ack, error)
	Heartbeat(ctx context.Context, hb Heartbeat) (Ack, error)
}
