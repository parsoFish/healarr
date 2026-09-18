package mounts

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/docker"
	"github.com/parsoFish/healarr/internal/clients/hostfs"
	"github.com/parsoFish/healarr/internal/config"
)

// fixedNow is the deterministic clock every test in this package uses.
var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

// wantFinding is what a table row asserts about one produced finding: key
// and severity only (Detail/Data are asserted separately where the brief
// pins exact values, e.g. the incident fixture).
type wantFinding struct {
	key string
	sev check.Severity
}

// errAny marks a table row that expects some error without pinning the
// exact sentinel — used for parse failures whose underlying error isn't a
// stable value to compare against.
var errAny = errors.New("mounts_test: any error")

// mountsCfg builds a config.Config carrying only the given mounts.
func mountsCfg(mounts ...config.Mount) config.Config {
	return config.Config{Mounts: mounts}
}

// baseDeps returns a check.Deps factory for table-driven tests: fixed node
// and clock, the given config and clients.
func baseDeps(cfg config.Config, dockerClient docker.Client, hostClient hostfs.Client) func() check.Deps {
	return func() check.Deps {
		return check.Deps{
			Node:   config.NodePi,
			Cfg:    cfg,
			Now:    func() time.Time { return fixedNow },
			Docker: dockerClient,
			Host:   hostClient,
		}
	}
}

// run executes c.Run against deps(), asserts the error against wantErr
// (errors.Is for a sentinel, non-nil for errAny, nil otherwise) and, when
// no error was expected, that the findings' keys+severities set-equal want.
func run(t *testing.T, c check.Check, deps func() check.Deps, want []wantFinding, wantErr error) check.Result {
	t.Helper()
	res, err := c.Run(context.Background(), deps())
	switch {
	case wantErr == errAny:
		if err == nil {
			t.Fatalf("expected an error, got nil")
		}
	case wantErr != nil:
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want errors.Is(%v)", err, wantErr)
		}
	default:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wantErr == nil {
		assertFindings(t, res.Findings, want)
	}
	return res
}

func assertFindings(t *testing.T, got []check.Finding, want []wantFinding) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("findings = %+v, want %+v", got, want)
	}
	gotSet := make(map[string]check.Severity, len(got))
	for _, f := range got {
		gotSet[f.EntityKey] = f.Severity
	}
	for _, w := range want {
		sev, ok := gotSet[w.key]
		if !ok {
			t.Fatalf("missing finding for key %s; got %+v", w.key, got)
		}
		if sev != w.sev {
			t.Fatalf("key %s severity = %s, want %s", w.key, sev, w.sev)
		}
	}
}

func TestMountRace(t *testing.T) {
	c := Checks(config.Config{})[0] // mount_race is the family's first check
	singleMount := mountsCfg(config.Mount{Host: "/mnt/nas/tv", Container: "sonarr", ContainerPath: "/tv"})
	execCmd := probeKey("sonarr", "/tv")
	healthyDevices := map[string]uint64{"/mnt/nas/tv": 41, "/mnt/nas": 2049}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "2026-09-12 mount race",
			deps: baseDeps(singleMount,
				&docker.Fake{ExecOutByCmd: map[string]string{execCmd: "2049\n2049\n0\n"}},
				&hostfs.Fake{Devices: healthyDevices},
			),
			want: []wantFinding{{key: "sonarr:/tv", sev: check.SeverityCritical}},
		},
		{
			name: "healthy",
			deps: baseDeps(singleMount,
				&docker.Fake{ExecOutByCmd: map[string]string{execCmd: "2049\n41\n120\n"}},
				&hostfs.Fake{Devices: healthyDevices},
			),
			want: nil,
		},
		{
			name: "empty but mounted",
			deps: baseDeps(singleMount,
				&docker.Fake{ExecOutByCmd: map[string]string{execCmd: "2049\n41\n0\n"}},
				&hostfs.Fake{Devices: healthyDevices},
			),
			want: []wantFinding{{key: "sonarr:/tv", sev: check.SeverityWarn}},
		},
		{
			name: "exec fails",
			deps: baseDeps(singleMount,
				&docker.Fake{Err: errors.New("no such container")},
				&hostfs.Fake{Devices: healthyDevices},
			),
			want: []wantFinding{{key: "sonarr:/tv", sev: check.SeverityCritical}},
		},
		{
			// An unparseable probe is a critical finding in its own right,
			// not a check error: the run must still report the other mounts.
			name: "garbage output",
			deps: baseDeps(singleMount,
				&docker.Fake{ExecOutByCmd: map[string]string{execCmd: "x\n"}},
				&hostfs.Fake{Devices: healthyDevices},
			),
			want: []wantFinding{{key: "sonarr:/tv", sev: check.SeverityCritical}},
		},
		{
			name:    "not configured",
			deps:    baseDeps(config.Config{}, &docker.Fake{}, &hostfs.Fake{}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "docker not configured",
			deps:    baseDeps(singleMount, nil, &hostfs.Fake{Devices: healthyDevices}),
			wantErr: check.ErrNotConfigured,
		},
		{
			name:    "host not configured",
			deps:    baseDeps(singleMount, &docker.Fake{ExecOutByCmd: map[string]string{execCmd: "2049\n41\n120\n"}}, nil),
			wantErr: check.ErrNotConfigured,
		},
		{
			name: "host mountpoint check fails",
			deps: baseDeps(singleMount,
				&docker.Fake{ExecOutByCmd: map[string]string{execCmd: "2049\n41\n120\n"}},
				&hostfs.Fake{Err: errors.New("nas offline")},
			),
			wantErr: errAny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			if tt.name == "2026-09-12 mount race" {
				assertIncidentFixture(t, res)
			}
		})
	}
}

// assertIncidentFixture pins the 2026-09-12 incident's exact finding shape:
// the mount race that took down the sonarr:/tv library.
func assertIncidentFixture(t *testing.T, res check.Result) {
	t.Helper()
	if len(res.Findings) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %+v", len(res.Findings), res.Findings)
	}
	f := res.Findings[0]
	if f.CheckID != mountRaceID || f.EntityKey != "sonarr:/tv" || f.Severity != check.SeverityCritical || f.Tier != check.TierCorrect {
		t.Fatalf("incident finding mismatch: %+v", f)
	}
	wantData := map[string]any{"rootDevice": uint64(2049), "pathDevice": uint64(2049), "entries": int64(0), "hostMounted": true}
	if !reflect.DeepEqual(f.Data, wantData) {
		t.Fatalf("incident Data = %+v, want %+v", f.Data, wantData)
	}
}

// probeKey builds the docker.Fake ExecOutByCmd key for one container's
// mount probe, so the tests track the probe command's exact form.
func probeKey(container, path string) string {
	return container + " sh -c " + probeCmd(path)
}

// TestMountRaceParseFailureDoesNotAbortRemainingMounts proves an
// unparseable probe on one mount becomes that mount's own critical finding
// and the loop keeps going: the second mount's genuine mount race is still
// reported.
func TestMountRaceParseFailureDoesNotAbortRemainingMounts(t *testing.T) {
	c := Checks(config.Config{})[0]
	cfg := mountsCfg(
		config.Mount{Host: "/mnt/nas/tv", Container: "sonarr", ContainerPath: "/tv"},
		config.Mount{Host: "/mnt/nas/movies", Container: "radarr", ContainerPath: "/movies"},
	)
	devices := map[string]uint64{"/mnt/nas/tv": 41, "/mnt/nas/movies": 42, "/mnt/nas": 2049}
	dockerFake := &docker.Fake{ExecOutByCmd: map[string]string{
		probeKey("sonarr", "/tv"):     "stat: cannot statx '/tv'\n",
		probeKey("radarr", "/movies"): "2049\n2049\n0\n",
	}}

	res := run(t, c, baseDeps(cfg, dockerFake, &hostfs.Fake{Devices: devices}), []wantFinding{
		{key: "sonarr:/tv", sev: check.SeverityCritical},
		{key: "radarr:/movies", sev: check.SeverityCritical},
	}, nil)

	var parseFinding check.Finding
	for _, f := range res.Findings {
		if f.EntityKey == "sonarr:/tv" {
			parseFinding = f
		}
	}
	wantSummary := "cannot parse mount probe for sonarr:/tv"
	if parseFinding.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", parseFinding.Summary, wantSummary)
	}
	if got := parseFinding.Data["raw"]; got != "stat: cannot statx '/tv'\n" {
		t.Errorf("Data[raw] = %v, want the raw probe output", got)
	}
	if parseFinding.Detail == "" {
		t.Error("Detail should carry the parse error rather than swallow it")
	}
}

// TestProbeCmdSuppressesStderr pins the probe's shell form: stderr is
// discarded for the whole command so a stat or ls diagnostic cannot be
// interleaved into the three lines the parser reads.
func TestProbeCmdSuppressesStderr(t *testing.T) {
	want := "( stat -c %d / '/tv'; ls -A '/tv' | wc -l ) 2>/dev/null"
	if got := probeCmd("/tv"); got != want {
		t.Fatalf("probeCmd(/tv) = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/tv", "'/tv'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, tt := range tests {
		if got := shellQuote(tt.in); got != tt.want {
			t.Fatalf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
