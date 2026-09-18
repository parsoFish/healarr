// Package store persists healarr's reports and findings in a local SQLite
// database. It is the single writer for a node's state file: one open
// connection (see Open), embedded migrations, and dedup/resolve logic for
// findings across successive check runs.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, cgo-free)
)

// ErrCheckpointBusy is returned by Checkpoint when PRAGMA
// wal_checkpoint(TRUNCATE) could not checkpoint every WAL frame (another
// connection's read transaction is pinning an older snapshot). It is not
// fatal: the nightly checkpoint job logs it and simply retries the next
// night, once that reader has gone away.
var ErrCheckpointBusy = errors.New("store: wal checkpoint busy: WAL not fully truncated")

// Store wraps a single SQLite connection. It is safe for concurrent use by
// multiple goroutines (database/sql serialises access), but SetMaxOpenConns
// is pinned to 1 so only one writer ever touches the file at a time.
type Store struct {
	db *sql.DB
}

// dsn builds the SQLite connection string: WAL journalling, a five second
// busy timeout (so concurrent readers/writers on the Pi's SD card back off
// instead of failing immediately), and NORMAL synchronous durability.
func dsn(path string) string {
	return fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		path,
	)
}

// Open opens (creating if needed) the SQLite file at path, applies the
// WAL/synchronous/busy_timeout pragmas, and runs any pending migrations.
// The parent directory must already exist; Open does not create it.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("store: ping %s: %w (close: %v)", path, err, closeErr)
		}
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	if err := migrate(ctx, db); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("store: migrate %s: %w (close: %v)", path, err, closeErr)
		}
		return nil, fmt.Errorf("store: migrate %s: %w", path, err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}

// Checkpoint runs a WAL checkpoint that truncates the WAL file back to
// empty once every frame has been copied into the main database file
// (spec C3's nightly checkpoint job, which keeps the WAL from growing
// unbounded on the Pi's SD card). It is safe to call repeatedly, including
// on a store with no pending WAL frames. When another connection's read
// transaction pins an older WAL snapshot, the checkpoint cannot complete
// and Checkpoint returns ErrCheckpointBusy instead of silently reporting
// success on a WAL that wasn't actually truncated.
func (s *Store) Checkpoint(ctx context.Context) error {
	var busy, log, checkpointed int
	row := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err := row.Scan(&busy, &log, &checkpointed); err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	if err := checkpointResult(busy); err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	return nil
}

// checkpointResult turns the "busy" column of PRAGMA
// wal_checkpoint(TRUNCATE)'s result row into an error: any non-zero value
// means the checkpoint could not process every WAL frame, so the WAL was
// not (fully) truncated.
func checkpointResult(busy int) error {
	if busy != 0 {
		return ErrCheckpointBusy
	}
	return nil
}

// SchemaVersion returns the applied migration number.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_meta`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}
