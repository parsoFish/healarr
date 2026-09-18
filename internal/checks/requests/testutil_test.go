package requests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/tautulli"
	"github.com/parsoFish/healarr/internal/config"
)

// fixedNow is the deterministic clock every test in this package uses.
var fixedNow = time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)

// wantFinding is what a table row asserts about one produced finding: key
// and severity only (Detail/Data are asserted separately where a test pins
// exact values).
type wantFinding struct {
	key string
	sev check.Severity
}

// errAny marks a table row that expects some error without pinning the
// exact sentinel.
var errAny = errors.New("requests_test: any error")

// baseDeps returns a check.Deps factory for table-driven tests: fixed node
// and clock, the given config and clients. Either client may be nil for a
// test that doesn't exercise it.
func baseDeps(cfg config.Config, tc tautulli.Client, oc overseerr.Client) func() check.Deps {
	return func() check.Deps {
		return check.Deps{
			Node:      config.NodePi,
			Cfg:       cfg,
			Now:       func() time.Time { return fixedNow },
			Tautulli:  tc,
			Overseerr: oc,
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
