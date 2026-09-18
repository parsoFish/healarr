package store

import (
	"context"
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
