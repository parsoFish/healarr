package docker

import (
	"context"
	"errors"
	"testing"
)

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{List: []Container{{Name: "sonarr"}}}
	list, err := f.Containers(context.Background())
	if err != nil || len(list) != 1 || len(f.Calls) != 1 || f.Calls[0] != "Containers()" {
		t.Fatalf("fake: %+v %v %v", list, err, f.Calls)
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := errors.New("boom")
	f := &Fake{
		List:       []Container{{Name: "sonarr"}},
		LogsByName: map[string]string{"sonarr": "log lines"},
		Usage:      DiskUsage{ImagesTotal: 2, ImagesReclaimable: 42},
		ExecOut:    map[string]string{"sonarr": "pong"},
		Err:        wantErr,
	}
	ctx := context.Background()

	if _, err := f.Containers(ctx); !errors.Is(err, wantErr) {
		t.Errorf("Containers err: %v", err)
	}
	if _, err := f.Logs(ctx, "sonarr", 50); !errors.Is(err, wantErr) {
		t.Errorf("Logs err: %v", err)
	}
	if _, err := f.DiskUsage(ctx); !errors.Is(err, wantErr) {
		t.Errorf("DiskUsage err: %v", err)
	}
	if _, err := f.PruneImages(ctx, true); !errors.Is(err, wantErr) {
		t.Errorf("PruneImages err: %v", err)
	}
	if _, err := f.Exec(ctx, "sonarr", "ping"); !errors.Is(err, wantErr) {
		t.Errorf("Exec err: %v", err)
	}

	want := []string{"Containers()", "Logs(sonarr,50)", "DiskUsage()", "PruneImages(true)", "Exec(sonarr, ping)"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}

func TestFakeExecOutByCmd(t *testing.T) {
	f := &Fake{
		ExecOut:      map[string]string{"sonarr": "default-out"},
		ExecOutByCmd: map[string]string{"sonarr ping -c 1": "pong-specific"},
	}
	ctx := context.Background()

	if out, err := f.Exec(ctx, "sonarr", "ping", "-c", "1"); err != nil || out != "pong-specific" {
		t.Errorf("Exec with matching per-command key = %q, %v, want %q, nil", out, err, "pong-specific")
	}
	if out, err := f.Exec(ctx, "sonarr", "other", "args"); err != nil || out != "default-out" {
		t.Errorf("Exec falling back to ExecOut = %q, %v, want %q, nil", out, err, "default-out")
	}
}

func TestFakeReturnsConfiguredData(t *testing.T) {
	f := &Fake{
		List:       []Container{{Name: "sonarr"}},
		LogsByName: map[string]string{"sonarr": "log lines"},
		Usage:      DiskUsage{ImagesTotal: 2, ImagesReclaimable: 42},
		ExecOut:    map[string]string{"sonarr": "pong"},
	}
	ctx := context.Background()

	if logs, _ := f.Logs(ctx, "sonarr", 50); logs != "log lines" {
		t.Errorf("expected configured logs, got %q", logs)
	}
	if du, _ := f.DiskUsage(ctx); du.ImagesTotal != 2 {
		t.Errorf("expected configured usage, got %+v", du)
	}
	if reclaimed, _ := f.PruneImages(ctx, true); reclaimed != 42 {
		t.Errorf("expected PruneImages to return configured reclaimable, got %d", reclaimed)
	}
	if out, _ := f.Exec(ctx, "sonarr", "ping"); out != "pong" {
		t.Errorf("expected configured exec output, got %q", out)
	}
}
