package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

func fixedFinding(id, entity string, sev check.Severity, tier check.Tier, summary, detail string, at time.Time) check.Finding {
	return check.Finding{
		CheckID:   id,
		Node:      config.NodeNAS,
		EntityKey: entity,
		Severity:  sev,
		Tier:      tier,
		Summary:   summary,
		Detail:    detail,
		FirstSeen: at,
		LastSeen:  at,
	}
}

func TestRenderDigestNoOpenFindings(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 30, 0, 0, time.UTC)
	in := DigestInput{
		Node:        config.NodePi,
		GeneratedAt: at,
		ChecksRun:   4,
	}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if out == "" {
		t.Fatal("RenderDigest must not return an empty string")
	}
	if !strings.Contains(out, "healarr digest — pi — 2026-09-18 07:30 UTC") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "Summary: 4 checks run, 0 failed, 0 skipped, 0 open findings (0 critical, 0 warn, 0 info)") {
		t.Fatalf("summary line: %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "no open findings") {
		t.Fatalf("expected 'no open findings' text, got: %q", out)
	}
	for _, section := range []string{"CRITICAL", "WARN", "INFO", "Check errors:", "Skipped", "Decisions:"} {
		if strings.Contains(out, section) {
			t.Fatalf("unexpected section %q in output: %q", section, out)
		}
	}
}

func TestRenderDigestMixedSeveritiesOrderAndCounts(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	in := DigestInput{
		Node:        config.NodeNAS,
		GeneratedAt: at,
		ChecksRun:   6,
		Findings: []check.Finding{
			fixedFinding("mount_race", "smb1", check.SeverityCritical, check.TierCorrect, "SMB mount flapping", "3 remounts in 5m", at),
			fixedFinding("arr_health", "sonarr", check.SeverityCritical, check.TierEscalate, "sonarr unhealthy", "", at),
			fixedFinding("wanted_missing", "sonarr", check.SeverityWarn, check.TierNudge, "wanted spike", "", at),
			fixedFinding("update_available", "radarr", check.SeverityInfo, check.TierObserve, "update available", "v5.2.1", at),
		},
	}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}

	iCrit := strings.Index(out, "CRITICAL")
	iWarn := strings.Index(out, "WARN")
	iInfo := strings.Index(out, "INFO")
	if iCrit < 0 || iWarn < 0 || iInfo < 0 {
		t.Fatalf("expected all three severity sections, got: %q", out)
	}
	if iCrit >= iWarn || iWarn >= iInfo {
		t.Fatalf("severity sections out of order: crit=%d warn=%d info=%d", iCrit, iWarn, iInfo)
	}
	if !strings.Contains(out, "Summary: 6 checks run, 0 failed, 0 skipped, 4 open findings (2 critical, 1 warn, 1 info)") {
		t.Fatalf("summary line: %q", out)
	}
	if !strings.Contains(out, "- [mount_race] SMB mount flapping") || !strings.Contains(out, "3 remounts in 5m") {
		t.Fatalf("missing critical finding with detail: %q", out)
	}
	if !strings.Contains(out, "- [arr_health] sonarr unhealthy") {
		t.Fatalf("missing second critical finding: %q", out)
	}
	if !strings.Contains(out, "- [wanted_missing] wanted spike") {
		t.Fatalf("missing warn finding: %q", out)
	}
	if !strings.Contains(out, "- [update_available] update available") || !strings.Contains(out, "v5.2.1") {
		t.Fatalf("missing info finding with detail: %q", out)
	}
	if strings.Contains(out, "no open findings") {
		t.Fatalf("should not claim no open findings: %q", out)
	}
}

func TestRenderDigestCheckErrorsSection(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	in := DigestInput{
		Node:        config.NodePi,
		GeneratedAt: at,
		ChecksRun:   3,
		Errors: []check.CheckError{
			{CheckID: "prowlarr_indexers", Error: "check prowlarr_indexers: dial tcp: connection refused"},
		},
	}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if !strings.Contains(out, "Check errors:") {
		t.Fatalf("missing check errors header: %q", out)
	}
	if !strings.Contains(out, "- [prowlarr_indexers] check prowlarr_indexers: dial tcp: connection refused") {
		t.Fatalf("missing check error line: %q", out)
	}
	if !strings.Contains(out, "Summary: 3 checks run, 1 failed, 0 skipped, 0 open findings (0 critical, 0 warn, 0 info)") {
		t.Fatalf("summary line: %q", out)
	}
}

func TestRenderDigestSkippedList(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	in := DigestInput{
		Node:        config.NodeNAS,
		GeneratedAt: at,
		ChecksRun:   2,
		Skipped:     []string{"tautulli_reachability", "overseerr_stuck_requests"},
	}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if !strings.Contains(out, "Skipped (not configured): tautulli_reachability, overseerr_stuck_requests") {
		t.Fatalf("missing skipped list: %q", out)
	}
	if !strings.Contains(out, "Summary: 2 checks run, 0 failed, 2 skipped, 0 open findings (0 critical, 0 warn, 0 info)") {
		t.Fatalf("summary line: %q", out)
	}
}

func TestRenderDigestBaseURLFooter(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	withURL := DigestInput{Node: config.NodePi, GeneratedAt: at, ChecksRun: 1, BaseURL: "http://192.0.2.20/healarr"}
	out, err := RenderDigest(withURL)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if !strings.Contains(out, "Decisions: http://192.0.2.20/healarr/decisions") {
		t.Fatalf("missing decisions footer: %q", out)
	}

	noURL := DigestInput{Node: config.NodePi, GeneratedAt: at, ChecksRun: 1}
	out2, err := RenderDigest(noURL)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if strings.Contains(out2, "Decisions:") {
		t.Fatalf("unexpected decisions footer: %q", out2)
	}
}

func TestRenderDigestUsesInputLocation(t *testing.T) {
	loc := time.FixedZone("AEST", 10*60*60)
	at := time.Date(2026, 9, 18, 17, 15, 0, 0, loc)
	in := DigestInput{Node: config.NodeNAS, GeneratedAt: at, ChecksRun: 1}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if !strings.Contains(out, "2026-09-18 17:15 AEST") {
		t.Fatalf("timestamp not rendered in input location: %q", out)
	}
}

func TestRenderDigestHeaderLineWidth(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	in := DigestInput{Node: config.NodeNAS, GeneratedAt: at, ChecksRun: 1}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	header := strings.SplitN(out, "\n", 2)[0]
	if n := len([]rune(header)); n > 78 {
		t.Fatalf("header line exceeds 78 columns (%d): %q", n, header)
	}
}

func TestRenderDigestUnknownSeverityRendersAsInfo(t *testing.T) {
	at := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)
	in := DigestInput{
		Node:        config.NodePi,
		GeneratedAt: at,
		ChecksRun:   1,
		Findings: []check.Finding{
			fixedFinding("mystery", "e1", check.Severity("weird"), check.TierObserve, "mystery finding", "", at),
		},
	}
	out, err := RenderDigest(in)
	if err != nil {
		t.Fatalf("RenderDigest: %v", err)
	}
	if !strings.Contains(out, "INFO") || !strings.Contains(out, "- [mystery] mystery finding") {
		t.Fatalf("unknown severity not rendered under INFO: %q", out)
	}
}
