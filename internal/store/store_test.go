package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	s := openTemp(t)
	v, err := s.SchemaVersion(context.Background())
	if err != nil || v != 1 {
		t.Fatalf("version=%d err=%v", v, err)
	}
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	s1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s1.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	v2, err := s2.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if v1 != 1 || v2 != 1 {
		t.Fatalf("versions = %d, %d; want 1, 1", v1, v2)
	}
}

func TestOpenFailsWhenDirMissing(t *testing.T) {
	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "nope", "x.db"))
	if err == nil {
		t.Fatal("expected error when parent directory is missing")
	}
}

// closedStore opens then immediately closes a store, so every subsequent
// call against s.db surfaces a "database is closed" error. These tests
// exercise the wrap-and-return error paths that a healthy store never
// takes.
func closedStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSchemaVersionErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.SchemaVersion(context.Background()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestSaveReportErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	rep := check.Report{Node: config.NodePi, GeneratedAt: time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)}
	if _, _, err := s.SaveReport(context.Background(), rep); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestLatestReportErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, _, err := s.LatestReport(context.Background(), config.NodePi); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

func TestOpenFindingsErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if _, err := s.OpenFindings(context.Background(), config.NodePi); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestCheckpointSucceedsAndIsIdempotent is the happy path: with no other
// connection holding the WAL open, PRAGMA wal_checkpoint(TRUNCATE) reports
// busy=0 (see checkpointResult), so Checkpoint returns nil, and it stays
// nil across repeated calls.
func TestCheckpointSucceedsAndIsIdempotent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	rep := check.Report{Node: config.NodePi, GeneratedAt: time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)}
	if _, _, err := s.SaveReport(ctx, rep); err != nil {
		t.Fatal(err)
	}

	if err := s.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(ctx); err != nil {
		t.Fatalf("second checkpoint: %v", err)
	}
}

func TestCheckpointErrorsOnClosedStore(t *testing.T) {
	s := closedStore(t)
	if err := s.Checkpoint(context.Background()); err == nil {
		t.Fatal("expected error from a closed store")
	}
}

// TestCheckpointReturnsErrCheckpointBusyWhenReaderBlocksTruncate exercises
// the real busy path end to end: a second connection to the same file
// opens a read transaction and reads a page *before* the store writes a
// new one, pinning that connection's WAL snapshot to the older frame
// count. TRUNCATE then cannot reclaim the newer frames the pinned reader
// might still need, so PRAGMA wal_checkpoint(TRUNCATE) reports busy!=0
// and Checkpoint must surface ErrCheckpointBusy rather than nil.
func TestCheckpointReturnsErrCheckpointBusyWhenReaderBlocksTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	reader, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	readerTx, err := reader.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = readerTx.Rollback() })
	// A read establishes the transaction's WAL snapshot at the current
	// (pre-write) frame count.
	if _, err := readerTx.Exec(`SELECT count(*) FROM reports`); err != nil {
		t.Fatal(err)
	}

	rep := check.Report{Node: config.NodePi, GeneratedAt: time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC)}
	if _, _, err := s.SaveReport(context.Background(), rep); err != nil {
		t.Fatal(err)
	}

	err = s.Checkpoint(context.Background())
	if !errors.Is(err, ErrCheckpointBusy) {
		t.Fatalf("err = %v, want ErrCheckpointBusy", err)
	}
}

// TestCheckpointResultRejectsNonZeroBusy is a deterministic, driver- and
// timing-independent guard on the busy branch: checkpointResult must turn
// any non-zero busy count into ErrCheckpointBusy, and leave a zero count
// as success. This backs
// TestCheckpointReturnsErrCheckpointBusyWhenReaderBlocksTruncate, whose
// busy=1 outcome depends on real SQLite WAL locking behaviour.
func TestCheckpointResultRejectsNonZeroBusy(t *testing.T) {
	if err := checkpointResult(0); err != nil {
		t.Fatalf("checkpointResult(0) = %v, want nil", err)
	}
	for _, busy := range []int{1, 2, -1} {
		if err := checkpointResult(busy); !errors.Is(err, ErrCheckpointBusy) {
			t.Fatalf("checkpointResult(%d) = %v, want ErrCheckpointBusy", busy, err)
		}
	}
}

func TestMigrationVersionRejectsBadNames(t *testing.T) {
	cases := []string{"noUnderscore.sql", "abc_init.sql"}
	for _, name := range cases {
		if _, err := migrationVersion(name); err == nil {
			t.Fatalf("migrationVersion(%q): expected error", name)
		}
	}
}

func TestToJSONOrDefaultRejectsUnmarshalableValue(t *testing.T) {
	if _, err := toJSONOrDefault(make(chan int), "{}"); err == nil {
		t.Fatal("expected marshal error for an unmarshalable value")
	}
}
