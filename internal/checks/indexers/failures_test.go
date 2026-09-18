package indexers

import (
	"errors"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/prowlarr"
	"github.com/parsoFish/healarr/internal/config"
)

// checksCfg builds a config.Config carrying only the indexer_failures
// threshold.
func checksCfg(window time.Duration) config.Config {
	return config.Config{Checks: config.Checks{IndexerFailureWindow: window}}
}

func TestIndexerFailures(t *testing.T) {
	c := Checks(config.Config{})[0]
	cfg := checksCfg(24 * time.Hour)
	idxList := []prowlarr.Indexer{
		{ID: 1, Name: "NZBgeek", Enable: true},
		{ID: 2, Name: "IPT", Enable: false},
	}

	tests := []struct {
		name    string
		deps    func() check.Deps
		want    []wantFinding
		wantErr error
	}{
		{
			name: "disabled",
			deps: baseDeps(cfg, &prowlarr.Fake{
				IndexerList: idxList,
				Statuses:    []prowlarr.IndexerStatus{{IndexerID: 2, DisabledTill: fixedNow.Add(2 * time.Hour)}},
			}),
			want: []wantFinding{{key: "prowlarr:2", sev: check.SeverityWarn}},
		},
		{
			name: "recent failure",
			deps: baseDeps(cfg, &prowlarr.Fake{
				IndexerList: idxList,
				Statuses:    []prowlarr.IndexerStatus{{IndexerID: 1, MostRecentFailure: fixedNow.Add(-1 * time.Hour)}},
			}),
			want: []wantFinding{{key: "prowlarr:1", sev: check.SeverityInfo}},
		},
		{
			name: "old failure outside window",
			deps: baseDeps(cfg, &prowlarr.Fake{
				IndexerList: idxList,
				Statuses:    []prowlarr.IndexerStatus{{IndexerID: 1, MostRecentFailure: fixedNow.Add(-48 * time.Hour)}},
			}),
			want: nil,
		},
		{
			name: "unknown indexer id",
			deps: baseDeps(cfg, &prowlarr.Fake{
				IndexerList: idxList,
				Statuses:    []prowlarr.IndexerStatus{{IndexerID: 99, MostRecentFailure: fixedNow.Add(-1 * time.Hour)}},
			}),
			want: []wantFinding{{key: "prowlarr:99", sev: check.SeverityInfo}},
		},
		{
			name:    "indexers error",
			deps:    baseDeps(cfg, &prowlarr.Fake{Err: errors.New("prowlarr down")}),
			wantErr: errAny,
		},
		{
			name:    "not configured",
			deps:    baseDeps(cfg, nil),
			wantErr: check.ErrNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := run(t, c, tt.deps, tt.want, tt.wantErr)
			switch tt.name {
			case "disabled":
				assertSummary(t, res, "prowlarr:2", "indexer IPT disabled until "+fixedNow.Add(2*time.Hour).Format(time.RFC3339))
				if got := res.Metrics["prowlarr_indexers_enabled"]; got != 1 {
					t.Fatalf("prowlarr_indexers_enabled = %v, want 1", got)
				}
			case "recent failure":
				assertSummary(t, res, "prowlarr:1", "indexer NZBgeek failed at "+fixedNow.Add(-1*time.Hour).Format(time.RFC3339))
			case "unknown indexer id":
				assertSummary(t, res, "prowlarr:99", "indexer #99 failed at "+fixedNow.Add(-1*time.Hour).Format(time.RFC3339))
			}
		})
	}
}

// assertSummary asserts the finding with EntityKey key has exactly the
// given Summary text.
func assertSummary(t *testing.T, res check.Result, key, want string) {
	t.Helper()
	for _, f := range res.Findings {
		if f.EntityKey == key {
			if f.Summary != want {
				t.Fatalf("summary = %q, want %q", f.Summary, want)
			}
			return
		}
	}
	t.Fatalf("no finding for key %s in %+v", key, res.Findings)
}
