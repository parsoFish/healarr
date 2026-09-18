package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

func TestSavePeerMessageAndLastPeerMessageAtRoundTrip(t *testing.T) {
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	kinds := []string{"report", "heartbeat", "decision"}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			s := openTemp(t)
			ctx := context.Background()

			id, err := s.SavePeerMessage(ctx, "in", kind, config.NodeNAS, []byte(`{"x":1}`), t0)
			if err != nil {
				t.Fatal(err)
			}
			if id <= 0 {
				t.Fatalf("id = %d, want positive", id)
			}

			got, ok, err := s.LastPeerMessageAt(ctx, config.NodeNAS, kind)
			if err != nil || !ok {
				t.Fatalf("LastPeerMessageAt: ok=%v err=%v", ok, err)
			}
			if !got.Equal(t0) {
				t.Fatalf("LastPeerMessageAt = %v, want %v", got, t0)
			}
		})
	}
}

// TestLastPeerMessageAtIgnoresOutboundMessages guards the direction filter:
// an "out" row (this node's own send) must never satisfy LastPeerMessageAt,
// which reports only inbound freshness.
func TestLastPeerMessageAtIgnoresOutboundMessages(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	if _, err := s.SavePeerMessage(ctx, "out", "heartbeat", config.NodeNAS, []byte("{}"), t0); err != nil {
		t.Fatal(err)
	}

	_, ok, err := s.LastPeerMessageAt(ctx, config.NodeNAS, "heartbeat")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false: only an outbound message was saved")
	}
}

func TestLastPeerMessageAtReturnsNewest(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	if _, err := s.SavePeerMessage(ctx, "in", "report", config.NodeNAS, []byte("{}"), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePeerMessage(ctx, "in", "report", config.NodeNAS, []byte("{}"), t1); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.LastPeerMessageAt(ctx, config.NodeNAS, "report")
	if err != nil || !ok {
		t.Fatalf("LastPeerMessageAt: ok=%v err=%v", ok, err)
	}
	if !got.Equal(t1) {
		t.Fatalf("LastPeerMessageAt = %v, want %v", got, t1)
	}
}

// TestLastPeerMessageAtIsScopedByPeerAndKind guards against a message for
// one peer or kind bleeding into another's freshness check.
func TestLastPeerMessageAtIsScopedByPeerAndKind(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	if _, err := s.SavePeerMessage(ctx, "in", "report", config.NodeNAS, []byte("{}"), t0); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.LastPeerMessageAt(ctx, config.NodePi, "report"); err != nil || ok {
		t.Fatalf("other peer: ok=%v err=%v, want ok=false", ok, err)
	}
	if _, ok, err := s.LastPeerMessageAt(ctx, config.NodeNAS, "heartbeat"); err != nil || ok {
		t.Fatalf("other kind: ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestLastPeerMessageAtNoneForPeer(t *testing.T) {
	s := openTemp(t)
	_, ok, err := s.LastPeerMessageAt(context.Background(), config.NodePi, "report")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected ok=false for empty store")
	}
}

func TestSavePeerMessageRejectsBadDirection(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	_, err := s.SavePeerMessage(context.Background(), "sideways", "report", config.NodeNAS, []byte("{}"), t0)
	if !errors.Is(err, ErrBadPeerMessage) {
		t.Fatalf("err = %v, want ErrBadPeerMessage", err)
	}
}

func TestSavePeerMessageRejectsBadKind(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	_, err := s.SavePeerMessage(context.Background(), "in", "gossip", config.NodeNAS, []byte("{}"), t0)
	if !errors.Is(err, ErrBadPeerMessage) {
		t.Fatalf("err = %v, want ErrBadPeerMessage", err)
	}
}

// TestSavePeerMessageRejectsBadKindWithoutStoring checks the reject is a
// pure validation failure: the row must never land in peer_messages.
func TestSavePeerMessageRejectsBadKindWithoutStoring(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	if _, err := s.SavePeerMessage(ctx, "in", "gossip", config.NodeNAS, []byte("{}"), t0); err == nil {
		t.Fatal("expected error for bad kind")
	}

	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM peer_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("peer_messages rows = %d, want 0", count)
	}
}

func TestSavePeerMessageErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	_, err := s.SavePeerMessage(context.Background(), "in", "report", config.NodeNAS, []byte("{}"), t0)
	if err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestLastPeerMessageAtErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	_, _, err := s.LastPeerMessageAt(context.Background(), config.NodeNAS, "report")
	if err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestLastPeerMessageAtRejectsCorruptRow guards the received_at parse
// branch, which a row written through SavePeerMessage never reaches.
func TestLastPeerMessageAtRejectsCorruptRow(t *testing.T) {
	s := openTemp(t)
	if _, err := s.db.Exec(`
		INSERT INTO peer_messages (direction, kind, peer, payload, received_at)
		VALUES ('in', 'report', 'nas', '{}', 'not-a-time')`,
	); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.LastPeerMessageAt(context.Background(), config.NodeNAS, "report"); err == nil {
		t.Fatal("expected error parsing a corrupt received_at")
	}
}

// TestPrunePeerMessagesKeepsNewerRows proves retention pruning is bounded
// by the cutoff: only rows received strictly before it go, the count
// returned is what was actually deleted, and everything at or after the
// cutoff survives.
func TestPrunePeerMessagesKeepsNewerRows(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	old := time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	recent := cutoff.Add(time.Hour)

	for _, at := range []time.Time{old, old.Add(time.Minute), recent} {
		if _, err := s.SavePeerMessage(ctx, "in", "report", config.NodeNAS, []byte("{}"), at); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.PrunePeerMessages(ctx, cutoff)
	if err != nil {
		t.Fatalf("PrunePeerMessages: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}

	got, ok, err := s.LastPeerMessageAt(ctx, config.NodeNAS, "report")
	if err != nil || !ok {
		t.Fatalf("LastPeerMessageAt: ok=%v err=%v", ok, err)
	}
	if !got.Equal(recent) {
		t.Fatalf("surviving row = %v, want the one newer than the cutoff (%v)", got, recent)
	}

	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM peer_messages`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("peer_messages rows = %d, want 1", count)
	}
}

// TestPrunePeerMessagesOnEmptyStoreDeletesNothing proves a prune with
// nothing to remove is a no-op reporting zero, not an error.
func TestPrunePeerMessagesOnEmptyStoreDeletesNothing(t *testing.T) {
	s := openTemp(t)
	deleted, err := s.PrunePeerMessages(context.Background(), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PrunePeerMessages: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0", deleted)
	}
}

func TestPrunePeerMessagesErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.PrunePeerMessages(context.Background(), time.Now()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestPeerMessagesLookupIndexExists guards the index the digest's
// freshness query (LastPeerMessageAt) and the retention prune both rely
// on: without it every lookup is a full scan of the table that grows
// fastest.
func TestPeerMessagesLookupIndexExists(t *testing.T) {
	s := openTemp(t)
	var name string
	err := s.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'peer_messages_lookup'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("peer_messages_lookup index missing: %v", err)
	}
}
