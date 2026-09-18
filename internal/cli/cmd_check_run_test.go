package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

func TestSelectChecksAllReturnsForNode(t *testing.T) {
	deps, _ := newTestDeps()
	reg, err := registryFor(deps, config.Config{})
	if err != nil {
		t.Fatalf("registryFor: %v", err)
	}
	got, err := selectChecks(reg, config.NodeNAS, true, "")
	if err != nil {
		t.Fatalf("selectChecks: %v", err)
	}
	for _, c := range got {
		if !c.AppliesTo(config.NodeNAS) {
			t.Errorf("selectChecks(--all) returned %s which doesn't apply to nas", c.ID)
		}
	}
}

func TestPrintCheckRunTableWithErrors(t *testing.T) {
	var buf bytes.Buffer
	rep := check.Report{
		Ran:     []string{"a"},
		Errors:  []check.CheckError{{CheckID: "b", Error: "boom"}},
		Skipped: []string{"c"},
	}
	if err := printCheckRunTable(&buf, rep); err != nil {
		t.Fatalf("printCheckRunTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "checks: 1 run, 1 failed, 1 skipped; findings: 0") {
		t.Errorf("missing summary line: %q", out)
	}
	if !strings.Contains(out, "errors:") || !strings.Contains(out, "b: boom") {
		t.Errorf("missing errors section: %q", out)
	}
}

func TestPrintCheckRunJSONIncludesUpsertCounts(t *testing.T) {
	var buf bytes.Buffer
	err := printCheckRun(&buf, true, check.Report{}, true, store.UpsertSummary{New: 1, Updated: 2, Resolved: 3})
	if err != nil {
		t.Fatalf("printCheckRun: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"new": 1`, `"updated": 2`, `"resolved": 3`, `"persisted": true`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in JSON output: %s", want, out)
		}
	}
}
