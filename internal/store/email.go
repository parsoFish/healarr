package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrEmailNotFound is returned by MarkEmailSent/MarkEmailFailed when id
// doesn't match any email_outbox row.
var ErrEmailNotFound = errors.New("store: email not found")

// ErrBadEmailFailure is returned by MarkEmailFailed when cause is nil. A
// nil cause is a caller bug (fail fast per project convention), never a
// value to paper over with placeholder text: the whole point of the
// outbox's error column is to record what actually went wrong.
var ErrBadEmailFailure = errors.New("store: mark email failed requires a non-nil cause")

// OutboxEmail mirrors one email_outbox row returned by PendingEmails.
type OutboxEmail struct {
	ID        int64
	To        string
	Subject   string
	Body      string
	CreatedAt time.Time
}

// EnqueueEmail inserts a pending outbox row and returns its id.
func (s *Store) EnqueueEmail(ctx context.Context, to, subject, body string, at time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO email_outbox (to_addr, subject, body, status, created_at)
		VALUES (?, ?, ?, 'pending', ?)`,
		to, subject, body, formatTime(at),
	)
	if err != nil {
		return 0, fmt.Errorf("store: enqueue email: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: enqueued email id: %w", err)
	}
	return id, nil
}

// MarkEmailSent marks id "sent" at the given time and clears any previous
// error. It returns ErrEmailNotFound when id doesn't exist.
func (s *Store) MarkEmailSent(ctx context.Context, id int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE email_outbox SET status = 'sent', sent_at = ?, error = '' WHERE id = ?`,
		formatTime(at), id,
	)
	if err != nil {
		return fmt.Errorf("store: mark email %d sent: %w", id, err)
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return fmt.Errorf("store: mark email %d sent: %w", id, err)
	}
	return nil
}

// MarkEmailFailed marks id "failed" at the given time, recording cause's
// message. It returns ErrEmailNotFound when id doesn't exist, and
// ErrBadEmailFailure when cause is nil.
func (s *Store) MarkEmailFailed(ctx context.Context, id int64, at time.Time, cause error) error {
	if cause == nil {
		return fmt.Errorf("store: mark email %d failed: %w", id, ErrBadEmailFailure)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE email_outbox SET status = 'failed', sent_at = ?, error = ? WHERE id = ?`,
		formatTime(at), cause.Error(), id,
	)
	if err != nil {
		return fmt.Errorf("store: mark email %d failed: %w", id, err)
	}
	if err := rowsAffectedOrNotFound(res); err != nil {
		return fmt.Errorf("store: mark email %d failed: %w", id, err)
	}
	return nil
}

// rowsAffectedOrNotFound returns ErrEmailNotFound when res reports zero
// rows affected (the id didn't match any email_outbox row).
func rowsAffectedOrNotFound(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrEmailNotFound
	}
	return nil
}

// PendingEmails lists status="pending" rows oldest first, for the retry
// job and `notify test` to drain.
func (s *Store) PendingEmails(ctx context.Context) (out []OutboxEmail, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, to_addr, subject, body, created_at
		FROM email_outbox
		WHERE status = 'pending'
		ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: pending emails: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("store: close pending emails rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		email, scanErr := scanOutboxEmail(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, email)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("store: iterate pending emails: %w", rowsErr)
	}
	return out, nil
}

func scanOutboxEmail(rows *sql.Rows) (OutboxEmail, error) {
	var (
		id                             int64
		to, subject, body, createdStr string
	)
	if err := rows.Scan(&id, &to, &subject, &body, &createdStr); err != nil {
		return OutboxEmail{}, fmt.Errorf("store: scan pending email: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return OutboxEmail{}, fmt.Errorf("store: parse email_outbox.created_at: %w", err)
	}

	return OutboxEmail{ID: id, To: to, Subject: subject, Body: body, CreatedAt: createdAt}, nil
}
