package hostfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const testdataMountinfo = "testdata/mountinfo.txt"

func TestMountsParsesFixture(t *testing.T) {
	c := New(testdataMountinfo)
	mounts, err := c.Mounts(context.Background())
	if err != nil {
		t.Fatalf("Mounts: %v", err)
	}

	byTarget := make(map[string]Mount, len(mounts))
	for _, m := range mounts {
		byTarget[m.Target] = m
	}

	t.Run("root ext4", func(t *testing.T) {
		want := Mount{Target: "/", Source: "/dev/mmcblk0p2", FSType: "ext4", Options: []string{"rw", "noatime"}}
		if got, ok := byTarget["/"]; !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("byTarget[/] = %+v (ok=%v), want %+v", got, ok, want)
		}
	})

	t.Run("nfs mount", func(t *testing.T) {
		want := Mount{FSType: "nfs", Source: "192.0.2.10:/volume1/tv"}
		got, ok := byTarget["/mnt/nas/tv"]
		if !ok || got.FSType != want.FSType || got.Source != want.Source {
			t.Errorf("byTarget[/mnt/nas/tv] = %+v (ok=%v), want fstype/source %+v", got, ok, want)
		}
	})

	t.Run("autofs present", func(t *testing.T) {
		if !hasFSType(mounts, "autofs") {
			t.Errorf("expected at least one autofs mount, got fstypes %v", fstypes(mounts))
		}
	})

	t.Run("escaped space unescaped", func(t *testing.T) {
		want := Mount{Target: "/mnt/my share", Source: "/dev/sdz1", FSType: "ext4"}
		got, ok := byTarget["/mnt/my share"]
		if !ok || got.FSType != want.FSType || got.Source != want.Source {
			t.Errorf("byTarget[/mnt/my share] = %+v (ok=%v), want fstype/source %+v; targets: %v", got, ok, want, targets(mounts))
		}
	})
}

func hasFSType(mounts []Mount, fsType string) bool {
	for _, m := range mounts {
		if m.FSType == fsType {
			return true
		}
	}
	return false
}

func targets(mounts []Mount) []string {
	out := make([]string, len(mounts))
	for i, m := range mounts {
		out[i] = m.Target
	}
	return out
}

func fstypes(mounts []Mount) []string {
	out := make([]string, len(mounts))
	for i, m := range mounts {
		out[i] = m.FSType
	}
	return out
}

func TestMountsMissingFile(t *testing.T) {
	c := New(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if _, err := c.Mounts(context.Background()); err == nil {
		t.Fatal("expected an error for a missing mountinfo file")
	}
}

func TestMountsMalformedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mountinfo.txt")
	if err := os.WriteFile(path, []byte("36 35 98:0 / /mnt1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c := New(path)
	if _, err := c.Mounts(context.Background()); err == nil {
		t.Fatal("expected an error for a malformed mountinfo line")
	}
}

func TestMountsMissingSeparator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mountinfo.txt")
	line := "36 35 98:0 / /mnt1 rw,noatime shared:1 ext3 /dev/root rw\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c := New(path)
	if _, err := c.Mounts(context.Background()); err == nil {
		t.Fatal("expected an error for a mountinfo line missing the \" - \" separator")
	}
}

func TestMountsRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(testdataMountinfo)
	if _, err := c.Mounts(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Mounts with cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestIsMountpointRootIsAlwaysTrue(t *testing.T) {
	c := New("")
	is, err := c.IsMountpoint(context.Background(), "/")
	if err != nil {
		t.Fatalf("IsMountpoint(/): %v", err)
	}
	if !is {
		t.Error("IsMountpoint(/) = false, want true")
	}
}

func TestIsMountpointFreshTempDirIsFalse(t *testing.T) {
	c := New("")
	dir := t.TempDir()
	is, err := c.IsMountpoint(context.Background(), dir)
	if err != nil {
		t.Fatalf("IsMountpoint(%s): %v", dir, err)
	}
	if is {
		t.Errorf("IsMountpoint(%s) = true, want false (shares its parent's device)", dir)
	}
}

func TestIsMountpointNonexistentPath(t *testing.T) {
	c := New("")
	if _, err := c.IsMountpoint(context.Background(), "/definitely/not/a/real/path"); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestDeviceIDNonexistentPath(t *testing.T) {
	c := New("")
	if _, err := c.DeviceID(context.Background(), "/definitely/not/a/real/path"); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestUnescapeOctal(t *testing.T) {
	cases := map[string]string{
		"/mnt/my\\040share": "/mnt/my share",
		"/plain/path":       "/plain/path",
		"trailing\\":        "trailing\\",
		"bad\\zzz escape":   "bad\\zzz escape",
	}
	for in, want := range cases {
		if got := unescapeOctal(in); got != want {
			t.Errorf("unescapeOctal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFakeMountsAndDeviceCalls(t *testing.T) {
	f := &Fake{
		MountList: []Mount{{Target: "/", FSType: "ext4"}},
		Devices:   map[string]uint64{"/mnt/data": 5, "/mnt": 1},
	}
	ctx := context.Background()

	mounts, err := f.Mounts(ctx)
	if err != nil || len(mounts) != 1 {
		t.Fatalf("Mounts: %+v, %v", mounts, err)
	}

	dev, err := f.DeviceID(ctx, "/mnt/data")
	if err != nil || dev != 5 {
		t.Fatalf("DeviceID: %d, %v", dev, err)
	}

	is, err := f.IsMountpoint(ctx, "/mnt/data")
	if err != nil || !is {
		t.Fatalf("IsMountpoint(/mnt/data): %v, %v", is, err)
	}

	isRoot, err := f.IsMountpoint(ctx, "/")
	if err != nil || !isRoot {
		t.Fatalf("IsMountpoint(/): %v, %v", isRoot, err)
	}

	want := []string{"Mounts()", "DeviceID(/mnt/data)", "IsMountpoint(/mnt/data)", "IsMountpoint(/)"}
	if !reflect.DeepEqual(f.Calls, want) {
		t.Errorf("Calls = %v, want %v", f.Calls, want)
	}
}

func TestFakePropagatesErr(t *testing.T) {
	wantErr := errors.New("boom")
	f := &Fake{Err: wantErr}
	ctx := context.Background()

	if _, err := f.Mounts(ctx); !errors.Is(err, wantErr) {
		t.Errorf("Mounts err: %v", err)
	}
	if _, err := f.DeviceID(ctx, "/x"); !errors.Is(err, wantErr) {
		t.Errorf("DeviceID err: %v", err)
	}
	if _, err := f.IsMountpoint(ctx, "/x"); !errors.Is(err, wantErr) {
		t.Errorf("IsMountpoint err: %v", err)
	}
}
