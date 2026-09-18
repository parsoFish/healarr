package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// ErrBadPeerMessage is returned when SavePeerMessage is called with a
// direction or kind outside the allowed set; the row is never stored.
var ErrBadPeerMessage = errors.New("store: bad peer message")

// PeerMessage mirrors one peer_messages row.
type PeerMessage struct {
	ID         int64
	Direction  string
	Kind       string
	Peer       config.Node
	Payload    string
	ReceivedAt time.Time
}

var validPeerDirections = map[string]struct{}{"in": {}, "out": {}}

var validPeerKinds = map[string]struct{}{"report": {}, "heartbeat": {}, "decision": {}}

// SavePeerMessage records one inbound ("in") or outbound ("out") message
// exchanged with peer, and returns the new row's id. kind must be one of
// report|heartbeat|decision; an unrecognised direction or kind is rejected
// with ErrBadPeerMessage and never stored.
func (s *Store) SavePeerMessage(ctx context.Context, direction, kind string, peer config.Node, payload []byte, at time.Time) (int64, error) {
	if _, ok := validPeerDirections[direction]; !ok {
		return 0, fmt.Errorf("store: peer message direction %q: %w", direction, ErrBadPeerMessage)
	}
	if _, ok := validPeerKinds[kind]; !ok {
		return 0, fmt.Errorf("store: peer message kind %q: %w", kind, ErrBadPeerMessage)
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO peer_messages (direction, kind, peer, payload, received_at)
		VALUES (?, ?, ?, ?, ?)`,
		direction, kind, string(peer), string(payload), formatTime(at),
	)
	if err != nil {
		return 0, fmt.Errorf("store: save peer message: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: peer message id: %w", err)
	}
	return id, nil
}

// LastPeerMessageAt returns the newest received_at among inbound ("in")
// messages from peer of the given kind; ok=false when none exist. Outbound
// ("out") messages never count towards this freshness check.
func (s *Store) LastPeerMessageAt(ctx context.Context, peer config.Node, kind string) (time.Time, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT received_at FROM peer_messages
		WHERE peer = ? AND kind = ? AND direction = 'in'
		ORDER BY received_at DESC, id DESC
		LIMIT 1`,
		string(peer), kind,
	)

	var receivedAtStr string
	if err := row.Scan(&receivedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("store: last peer message at: %w", err)
	}

	receivedAt, err := time.Parse(time.RFC3339, receivedAtStr)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: parse peer_messages.received_at: %w", err)
	}
	return receivedAt, true, nil
}

// PrunePeerMessages deletes every peer_messages row received strictly
// before olderThan and returns how many rows went. peer_messages is the
// fastest-growing table on either node (one row per heartbeat, per report
// and per decision, in both directions), and nothing else ever removes
// from it, so the daemon's nightly job calls this to hold it to
// Agent.PeerMessageRetention. received_at is stored as an RFC 3339 UTC
// string by formatTime, so the comparison is both parameterised and
// chronologically correct.
func (s *Store) PrunePeerMessages(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM peer_messages WHERE received_at < ?`, formatTime(olderThan),
	)
	if err != nil {
		return 0, fmt.Errorf("store: prune peer messages: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune peer messages: rows affected: %w", err)
	}
	return deleted, nil
}
