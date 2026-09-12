package hostfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUsageRoot(t *testing.T) {
	c := New("")
	u, err := c.Usage(context.Background(), "/")
	if err != nil {
		t.Fatalf("Usage(/): %v", err)
	}
	if u.Total == 0 {
		t.Error("Total = 0, want > 0")
	}
	if u.UsedPercent < 0 || u.UsedPercent > 100 {
		t.Errorf("UsedPercent = %v, want within [0, 100]", u.UsedPercent)
	}
	if u.Path != "/" {
		t.Errorf("Path = %q, want /", u.Path)
	}
}

func TestUsageNonexistentPath(t *testing.T) {
	c := New("")
	if _, err := c.Usage(context.Background(), "/definitely/not/a/real/path"); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestUsageRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New("")
	if _, err := c.Usage(ctx, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Usage with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

// buildTree lays out:
//
//	root/file1.txt      (10 bytes, depth 1)
//	root/sub/file2.txt  (20 bytes, depth 2)
//
// so a maxDepth of 1 sees file1.txt but not the nested file2.txt.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file1.txt"), make([]byte, 10), 0o644); err != nil {
		t.Fatalf("WriteFile file1: %v", err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file2.txt"), make([]byte, 20), 0o644); err != nil {
		t.Fatalf("WriteFile file2: %v", err)
	}
	return root
}

func TestDirSizeUnlimitedDepth(t *testing.T) {
	root := buildTree(t)
	c := New("")
	size, err := c.DirSize(context.Background(), root, 0)
	if err != nil {
		t.Fatalf("DirSize: %v", err)
	}
	if size != 30 {
		t.Errorf("DirSize(maxDepth=0) = %d, want 30", size)
	}
}

func TestDirSizeMaxDepthExcludesNestedFile(t *testing.T) {
	root := buildTree(t)
	c := New("")
	size, err := c.DirSize(context.Background(), root, 1)
	if err != nil {
		t.Fatalf("DirSize: %v", err)
	}
	if size != 10 {
		t.Errorf("DirSize(maxDepth=1) = %d, want 10 (nested file2.txt excluded)", size)
	}
}

func TestDirSizeDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "outside.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatalf("WriteFile outside.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "in.txt"), make([]byte, 5), 0o644); err != nil {
		t.Fatalf("WriteFile in.txt: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	c := New("")
	size, err := c.DirSize(context.Background(), root, 0)
	if err != nil {
		t.Fatalf("DirSize: %v", err)
	}
	if size != 5 {
		t.Errorf("DirSize = %d, want 5 (symlinked dir's contents must not be counted)", size)
	}
}

func TestDirSizeRespectsCancelledContext(t *testing.T) {
	root := buildTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New("")
	if _, err := c.DirSize(ctx, root, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("DirSize with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestDirSizeNonexistentPath(t *testing.T) {
	c := New("")
	if _, err := c.DirSize(context.Background(), "/definitely/not/a/real/path", 0); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestListDirSortedByName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("Mkdir sub: %v", err)
	}

	c := New("")
	entries, err := c.ListDir(context.Background(), root)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	want := []string{"a.txt", "b.txt", "c.txt", "sub"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}

	for _, e := range entries {
		if e.Name == "sub" && !e.IsDir {
			t.Error("sub: IsDir = false, want true")
		}
		if e.Name == "a.txt" {
			if e.IsDir {
				t.Error("a.txt: IsDir = true, want false")
			}
			if e.Size != 1 {
				t.Errorf("a.txt: Size = %d, want 1", e.Size)
			}
			if e.ModTime.After(time.Now()) {
				t.Errorf("a.txt: ModTime %v is in the future", e.ModTime)
			}
		}
	}
}

func TestListDirNonexistentPath(t *testing.T) {
	c := New("")
	if _, err := c.ListDir(context.Background(), "/definitely/not/a/real/path"); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{
		Usages:  map[string]Usage{"/": {Total: 100}},
		Sizes:   map[string]int64{"/data": 42},
		Entries: map[string][]Entry{"/data": {{Name: "a"}}},
	}
	ctx := context.Background()

	if u, err := f.Usage(ctx, "/"); err != nil || u.Total != 100 {
		t.Fatalf("Usage: %+v, %v", u, err)
	}
	if size, err := f.DirSize(ctx, "/data", 2); err != nil || size != 42 {
		t.Fatalf("DirSize: %d, %v", size, err)
	}
	if entries, err := f.ListDir(ctx, "/data"); err != nil || len(entries) != 1 {
		t.Fatalf("ListDir: %+v, %v", entries, err)
	}

	want := []string{"Usage(/)", "DirSize(/data,2)", "ListDir(/data)"}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Errorf("Calls = %v, want %v", f.Calls, want)
	}
}

func TestFakeUsageDirSizeListDirPropagateErr(t *testing.T) {
	wantErr := errors.New("boom")
	f := &Fake{Err: wantErr}
	ctx := context.Background()

	if _, err := f.Usage(ctx, "/"); !errors.Is(err, wantErr) {
		t.Errorf("Usage err: %v", err)
	}
	if _, err := f.DirSize(ctx, "/", 0); !errors.Is(err, wantErr) {
		t.Errorf("DirSize err: %v", err)
	}
	if _, err := f.ListDir(ctx, "/"); !errors.Is(err, wantErr) {
		t.Errorf("ListDir err: %v", err)
	}
}
