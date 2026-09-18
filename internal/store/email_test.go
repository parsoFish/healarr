package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnqueueEmailAndPendingEmailsRoundTrip(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id, err := s.EnqueueEmail(ctx, "ops@example.invalid", "subject", "body", t0)
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("id = %d, want positive", id)
	}

	pending, err := s.PendingEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1: %+v", len(pending), pending)
	}
	got := pending[0]
	if got.ID != id || got.To != "ops@example.invalid" || got.Subject != "subject" || got.Body != "body" {
		t.Fatalf("pending[0] = %+v", got)
	}
	if !got.CreatedAt.Equal(t0) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, t0)
	}
}

func TestPendingEmailsOrdersOldestFirst(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	idNew, err := s.EnqueueEmail(ctx, "b@example.invalid", "s2", "b2", t1)
	if err != nil {
		t.Fatal(err)
	}
	idOld, err := s.EnqueueEmail(ctx, "a@example.invalid", "s1", "b1", t0)
	if err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].ID != idOld || pending[1].ID != idNew {
		t.Fatalf("pending = %+v, want oldest (%d) first then %d", pending, idOld, idNew)
	}
}

func TestMarkEmailSentRemovesFromPending(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id, err := s.EnqueueEmail(ctx, "a@example.invalid", "s", "b", t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkEmailSent(ctx, id, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after sent = %d, want 0: %+v", len(pending), pending)
	}

	var status string
	var sentAt string
	if err := s.db.QueryRow(`SELECT status, sent_at FROM email_outbox WHERE id = ?`, id).Scan(&status, &sentAt); err != nil {
		t.Fatal(err)
	}
	if status != "sent" || sentAt == "" {
		t.Fatalf("status=%q sent_at=%q, want sent / non-empty", status, sentAt)
	}
}

func TestMarkEmailFailedRecordsCauseAndLeavesOutOfPending(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)

	id, err := s.EnqueueEmail(ctx, "a@example.invalid", "s", "b", t0)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("msmtp: connection refused")
	if err := s.MarkEmailFailed(ctx, id, t0.Add(time.Minute), cause); err != nil {
		t.Fatal(err)
	}

	pending, err := s.PendingEmails(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after failed = %d, want 0: %+v", len(pending), pending)
	}

	var status, storedErr string
	if err := s.db.QueryRow(`SELECT status, error FROM email_outbox WHERE id = ?`, id).Scan(&status, &storedErr); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || storedErr != cause.Error() {
		t.Fatalf("status=%q error=%q, want failed / %q", status, storedErr, cause.Error())
	}
}

func TestMarkEmailSentErrorsWhenIDMissing(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if err := s.MarkEmailSent(context.Background(), 999, t0); err == nil {
		t.Fatal("expected error marking a missing email sent")
	}
}

func TestMarkEmailFailedErrorsWhenIDMissing(t *testing.T) {
	s := openTemp(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if err := s.MarkEmailFailed(context.Background(), 999, t0, errors.New("x")); err == nil {
		t.Fatal("expected error marking a missing email failed")
	}
}

func TestEnqueueEmailErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if _, err := s.EnqueueEmail(context.Background(), "a@example.invalid", "s", "b", t0); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestPendingEmailsErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.PendingEmails(context.Background()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestMarkEmailSentErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if err := s.MarkEmailSent(context.Background(), 1, t0); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestMarkEmailFailedErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	t0 := time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)
	if err := s.MarkEmailFailed(context.Background(), 1, t0, errors.New("x")); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestPendingEmailsRejectsCorruptRow guards the created_at parse branch,
// which a row written through EnqueueEmail never reaches.
func TestPendingEmailsRejectsCorruptRow(t *testing.T) {
	s := openTemp(t)
	if _, err := s.db.Exec(`
		INSERT INTO email_outbox (to_addr, subject, body, status, created_at)
		VALUES ('a@example.invalid', 's', 'b', 'pending', 'not-a-time')`,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PendingEmails(context.Background()); err == nil {
		t.Fatal("expected error parsing a corrupt created_at")
	}
}
