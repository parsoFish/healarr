package hostfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOSRemoveDeletesFileAndDir(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dir := filepath.Join(root, "sub")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}

	c := New("")
	if err := c.Remove(context.Background(), file); err != nil {
		t.Fatalf("Remove(file): %v", err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("file still exists after Remove: err=%v", err)
	}

	if err := c.Remove(context.Background(), dir); err != nil {
		t.Fatalf("Remove(dir): %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir still exists after Remove: err=%v", err)
	}
}

func TestOSRemoveRefusesEmptyAndRoot(t *testing.T) {
	c := New("")
	for _, path := range []string{"", "/"} {
		if err := c.Remove(context.Background(), path); err == nil {
			t.Errorf("Remove(%q) = nil error, want a refusal", path)
		}
	}
}

func TestOSRemoveRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New("")
	if err := c.Remove(ctx, "/tmp/whatever"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Remove with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

// TestOSRemoveNonexistentPathIsANoOp documents os.RemoveAll's own
// contract, which Remove inherits: removing something already gone
// succeeds rather than erroring, so a cleanup executor retrying after a
// partial failure doesn't trip over its own prior success.
func TestOSRemoveNonexistentPathIsANoOp(t *testing.T) {
	c := New("")
	if err := c.Remove(context.Background(), "/definitely/not/a/real/path"); err != nil {
		t.Fatalf("Remove(nonexistent) = %v, want nil (os.RemoveAll is idempotent)", err)
	}
}

func TestOSRemoveErrorsWhenParentUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	root := t.TempDir()
	parent := filepath.Join(root, "locked")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	target := filepath.Join(parent, "child")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir target: %v", err)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	c := New("")
	if err := c.Remove(context.Background(), target); err == nil {
		t.Fatal("expected an error removing a child of an unwritable directory")
	}
}

func TestFakeRemoveRecordsAndFailsOnErr(t *testing.T) {
	f := &Fake{}
	if err := f.Remove(context.Background(), "/downloads/orphan"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(f.Removed) != 1 || f.Removed[0] != "/downloads/orphan" {
		t.Errorf("Removed = %v, want [/downloads/orphan]", f.Removed)
	}

	boom := errors.New("boom")
	f.Err = boom
	if err := f.Remove(context.Background(), "/downloads/other"); !errors.Is(err, boom) {
		t.Fatalf("Remove with Err set: err = %v, want %v", err, boom)
	}
	if len(f.Removed) != 1 {
		t.Errorf("Removed should not grow on error: %v", f.Removed)
	}
}
