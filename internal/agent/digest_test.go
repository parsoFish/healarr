package agent

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
	"github.com/parsoFish/healarr/internal/store"
)

var digestT0 = time.Date(2026, 9, 19, 7, 0, 0, 0, time.UTC)

// newDigestTestAgent returns an Agent with sane digest-related defaults
// (a Pi node, a Sender, a 15m PeerStaleAfter). Callers that need to
// inspect the fake store/sender's calls must construct their own and wire
// them in via mutate — the defaults built here are only fallbacks for
// fields a given test doesn't care about.
func newDigestTestAgent(t *testing.T, mutate func(*Options)) *Agent {
	t.Helper()
	o := Options{
		Cfg: config.Config{
			Node:  config.NodePi,
			Agent: config.Agent{PeerStaleAfter: 15 * time.Minute},
			Email: config.Email{To: "ops@example.test"},
		},
		Registry: check.NewRegistry(),
		Deps:     fakeDeps(check.Deps{}, nil),
		Store:    &fakeStore{},
		Sender:   &notify.FakeSender{},
		Now:      fixedNow(digestT0),
		Version:  "v-test",
		Logger:   slog.New(slog.NewTextHandler(&strings.Builder{}, nil)),
	}
	if mutate != nil {
		mutate(&o)
	}
	a, err := New(o)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	return a
}

// TestSendDigestNoSenderErrors proves SendDigest refuses to run at all
// (never touching the store) when this agent has no Sender configured
// (the NAS, per constraints.md).
func TestSendDigestNoSenderErrors(t *testing.T) {
	fs := &fakeStore{}
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Sender = nil
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, ErrNoSender) {
		t.Fatalf("SendDigest() err = %v, want wrapping ErrNoSender", err)
	}
	if len(fs.Emails) != 0 {
		t.Fatalf("Emails = %d, want 0 (rejected before enqueuing)", len(fs.Emails))
	}
}

// TestSendDigestEnqueuesSendsAndMarksSent proves the happy path: an email
// is enqueued, sent through the Sender, and marked sent — with no peer
// section at all, since this node has never received a peer report.
func TestSendDigestEnqueuesSendsAndMarksSent(t *testing.T) {
	ownRep := check.Report{Node: config.NodePi, GeneratedAt: digestT0, ChecksRun: 3}
	fs := &fakeStore{LatestReportFound: true, LatestReportResult: ownRep}
	fsend := &notify.FakeSender{}
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Sender = fsend
	})

	id, err := a.SendDigest(context.Background())
	if err != nil {
		t.Fatalf("SendDigest() err = %v", err)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1", id)
	}
	if len(fs.Emails) != 1 {
		t.Fatalf("Emails = %d, want 1", len(fs.Emails))
	}
	if fs.Emails[0].To != "ops@example.test" {
		t.Fatalf("Emails[0].To = %q, want ops@example.test", fs.Emails[0].To)
	}
	if len(fsend.Sends) != 1 {
		t.Fatalf("Sends = %d, want 1", len(fsend.Sends))
	}
	if !reflect.DeepEqual(fs.SentEmailIDs, []int64{1}) {
		t.Fatalf("SentEmailIDs = %v, want [1]", fs.SentEmailIDs)
	}
	if len(fs.FailedEmails) != 0 {
		t.Fatalf("FailedEmails = %d, want 0", len(fs.FailedEmails))
	}
}

// TestSendDigestSendFailureMarksFailedAndReturnsError proves a Sender
// failure is recorded on the outbox row (MarkEmailFailed) and returned to
// the caller, unlike a peer push failure elsewhere in this package.
func TestSendDigestSendFailureMarksFailedAndReturnsError(t *testing.T) {
	sendErr := errors.New("smtp boom")
	fs := &fakeStore{}
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Sender = &notify.FakeSender{Err: sendErr}
	})

	id, err := a.SendDigest(context.Background())
	if !errors.Is(err, sendErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, sendErr)
	}
	if id != 1 {
		t.Fatalf("id = %d, want 1 (still the enqueued row)", id)
	}
	if len(fs.FailedEmails) != 1 {
		t.Fatalf("FailedEmails = %d, want 1", len(fs.FailedEmails))
	}
	failed := fs.FailedEmails[0]
	if failed.ID != 1 || !errors.Is(failed.Cause, sendErr) {
		t.Fatalf("FailedEmails[0] = %+v, want id=1 cause wrapping %v", failed, sendErr)
	}
	if len(fs.SentEmailIDs) != 0 {
		t.Fatalf("SentEmailIDs = %d, want 0", len(fs.SentEmailIDs))
	}
}

// TestSendDigestPeerNeverReportedOmitsPeerSection proves a node that has
// never received a peer report (LastPeerMessageAt not found) gets no peer
// section at all — the "Peer == nil" render case — rather than a
// fabricated stale one.
func TestSendDigestPeerNeverReportedOmitsPeerSection(t *testing.T) {
	fs := &fakeStore{LastPeerMessageAtFound: false}
	a := newDigestTestAgent(t, func(o *Options) { o.Store = fs })

	section, err := a.buildPeerSection(context.Background())
	if err != nil {
		t.Fatalf("buildPeerSection() err = %v", err)
	}
	if section != nil {
		t.Fatalf("section = %+v, want nil (peer never reported)", section)
	}

	if _, err := a.SendDigest(context.Background()); err != nil {
		t.Fatalf("SendDigest() err = %v", err)
	}
	if len(fs.Emails) != 1 {
		t.Fatalf("Emails = %d, want 1", len(fs.Emails))
	}
}

// TestSendDigestPeerFreshIncludesFindings proves a peer whose last report
// message is within PeerStaleAfter renders as fresh (StaleSince nil via
// buildPeerSection) and carries its findings through to the digest body.
func TestSendDigestPeerFreshIncludesFindings(t *testing.T) {
	peerRep := check.Report{Node: config.NodeNAS, GeneratedAt: digestT0.Add(-time.Minute), ChecksRun: 5, ChecksFailed: 1}
	peerFinding := store.StoredFinding{
		Finding: check.Finding{CheckID: "disk", Node: config.NodeNAS, EntityKey: "/", Severity: check.SeverityCritical, Summary: "disk full"},
		Status:  "open",
	}
	fs := &fakeStore{
		LastPeerMessageAtFound:  true,
		LastPeerMessageAtResult: digestT0.Add(-time.Minute), // fresh: 1m < 15m PeerStaleAfter
		LatestReportFound:       true,
		LatestReportResult:      peerRep,
		OpenFindingsResult:      []store.StoredFinding{peerFinding},
	}
	a := newDigestTestAgent(t, func(o *Options) { o.Store = fs })

	section, err := a.buildPeerSection(context.Background())
	if err != nil {
		t.Fatalf("buildPeerSection() err = %v", err)
	}
	if section == nil {
		t.Fatal("section = nil, want a fresh section")
	}
	if section.StaleSince != nil {
		t.Fatalf("section.StaleSince = %v, want nil (fresh)", section.StaleSince)
	}
	if section.ChecksRun != 5 || section.ChecksFailed != 1 {
		t.Fatalf("section = %+v, want ChecksRun=5 ChecksFailed=1", section)
	}
	if len(section.Findings) != 1 || section.Findings[0].Summary != "disk full" {
		t.Fatalf("section.Findings = %+v, want the peer's finding", section.Findings)
	}

	if _, err := a.SendDigest(context.Background()); err != nil {
		t.Fatalf("SendDigest() err = %v", err)
	}
	if !strings.Contains(fs.Emails[0].Body, "disk full") {
		t.Fatalf("digest body = %q, want it to include the peer's finding", fs.Emails[0].Body)
	}
}

// TestSendDigestPeerStaleMarksStaleSince proves a peer whose last report
// message is older than PeerStaleAfter still gets a (non-nil) section, but
// with StaleSince set rather than nil.
func TestSendDigestPeerStaleMarksStaleSince(t *testing.T) {
	staleAt := digestT0.Add(-time.Hour) // 1h > 15m PeerStaleAfter
	fs := &fakeStore{
		LastPeerMessageAtFound:  true,
		LastPeerMessageAtResult: staleAt,
		LatestReportFound:       true,
		LatestReportResult:      check.Report{Node: config.NodeNAS, GeneratedAt: staleAt},
	}
	a := newDigestTestAgent(t, func(o *Options) { o.Store = fs })

	section, err := a.buildPeerSection(context.Background())
	if err != nil {
		t.Fatalf("buildPeerSection() err = %v", err)
	}
	if section == nil {
		t.Fatal("section = nil, want a stale section, not none at all")
	}
	if section.StaleSince == nil || !section.StaleSince.Equal(staleAt) {
		t.Fatalf("section.StaleSince = %v, want %v", section.StaleSince, staleAt)
	}
}

func TestSendDigestOwnLatestReportErrorPropagates(t *testing.T) {
	wantErr := errors.New("own report boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{LatestReportErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestSendDigestOwnOpenFindingsErrorPropagates(t *testing.T) {
	wantErr := errors.New("own findings boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{OpenFindingsErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestSendDigestPeerLastMessageErrorPropagates(t *testing.T) {
	wantErr := errors.New("last message boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{LastPeerMessageAtErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestSendDigestEnqueueErrorPropagates(t *testing.T) {
	wantErr := errors.New("enqueue boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{EnqueueEmailErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestSendDigestMarkFailedErrorIsLoggedNotFatal proves a MarkEmailFailed
// failure (on top of the original send failure) is logged, not returned —
// the caller already gets the original send error back.
func TestSendDigestMarkFailedErrorIsLoggedNotFatal(t *testing.T) {
	sendErr := errors.New("smtp boom")
	markErr := errors.New("mark failed boom")
	var logBuf strings.Builder
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{MarkEmailFailedErr: markErr}
		o.Sender = &notify.FakeSender{Err: sendErr}
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, sendErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, sendErr)
	}
	if !strings.Contains(logBuf.String(), "mark failed boom") {
		t.Fatalf("log = %q, want it to mention the mark-failed error", logBuf.String())
	}
}

// TestSendDigestMarkSentErrorIsLoggedNotFatal proves a MarkEmailSent
// failure on the happy path is logged, not returned — the digest still
// sent successfully.
func TestSendDigestMarkSentErrorIsLoggedNotFatal(t *testing.T) {
	markErr := errors.New("mark sent boom")
	var logBuf strings.Builder
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{MarkEmailSentErr: markErr}
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	if _, err := a.SendDigest(context.Background()); err != nil {
		t.Fatalf("SendDigest() err = %v, want nil (send itself succeeded)", err)
	}
	if !strings.Contains(logBuf.String(), "mark sent boom") {
		t.Fatalf("log = %q, want it to mention the mark-sent error", logBuf.String())
	}
}

func TestBuildPeerSectionPeerLatestReportErrorPropagates(t *testing.T) {
	wantErr := errors.New("peer latest report boom")
	fs := &fakeStore{LastPeerMessageAtFound: true, LatestReportErr: wantErr}
	a := newDigestTestAgent(t, func(o *Options) { o.Store = fs })

	if _, err := a.buildPeerSection(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("buildPeerSection() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestBuildPeerSectionPeerOpenFindingsErrorPropagates(t *testing.T) {
	wantErr := errors.New("peer findings boom")
	fs := &fakeStore{LastPeerMessageAtFound: true, OpenFindingsErr: wantErr}
	a := newDigestTestAgent(t, func(o *Options) { o.Store = fs })

	if _, err := a.buildPeerSection(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("buildPeerSection() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestToFindingsExtractsEmbeddedFinding(t *testing.T) {
	stored := []store.StoredFinding{
		{Finding: check.Finding{CheckID: "a", EntityKey: "1"}, Status: "open"},
		{Finding: check.Finding{CheckID: "b", EntityKey: "2"}, Status: "open"},
	}
	got := toFindings(stored)
	want := []check.Finding{stored[0].Finding, stored[1].Finding}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("toFindings() = %+v, want %+v", got, want)
	}
}

func TestToFindingsEmptyInputReturnsEmptyNotNil(t *testing.T) {
	got := toFindings(nil)
	if got == nil {
		t.Fatal("toFindings(nil) = nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("toFindings(nil) = %+v, want empty", got)
	}
}

// digestStalenessFinding builds a check.Finding shaped like
// internal/staleness/check.go's stalenessFinding, for tests exercising
// collectStalenessFindings/SendDigest's STALENESS section wiring.
func digestStalenessFinding(node config.Node, entity, title string, score float64) check.Finding {
	return check.Finding{
		CheckID:   stalenessScanCheckID,
		Node:      node,
		EntityKey: entity,
		Severity:  check.SeverityWarn,
		Tier:      check.TierEscalate,
		Data:      map[string]any{"score": score, "title": title},
	}
}

// TestCollectStalenessFindingsSortsByScoreDescAcrossBothNodes proves the
// digest's STALENESS input combines own and peer staleness_scan findings
// (ignoring any other check id) and sorts the result by score descending.
func TestCollectStalenessFindingsSortsByScoreDescAcrossBothNodes(t *testing.T) {
	own := []check.Finding{
		digestStalenessFinding(config.NodePi, "sonarr:1", "Low", 40),
		{CheckID: "disk_pressure", EntityKey: "/"}, // not staleness_scan: excluded
		digestStalenessFinding(config.NodePi, "sonarr:2", "High", 90),
	}
	peer := &notify.PeerSection{
		Findings: []check.Finding{digestStalenessFinding(config.NodeNAS, "radarr:1", "Mid", 65)},
	}

	got := collectStalenessFindings(own, peer)

	want := []string{"High", "Mid", "Low"}
	if len(got) != len(want) {
		t.Fatalf("collectStalenessFindings() = %d findings, want %d: %+v", len(got), len(want), got)
	}
	for i, title := range want {
		if got[i].Data["title"] != title {
			t.Fatalf("collectStalenessFindings()[%d].Data[title] = %v, want %q", i, got[i].Data["title"], title)
		}
	}
}

// TestCollectStalenessFindingsNilPeerOmitsPeerFindings proves a nil
// PeerSection (no peer report ever received) still returns the own-node
// findings, rather than erroring or panicking on a nil dereference.
func TestCollectStalenessFindingsNilPeerOmitsPeerFindings(t *testing.T) {
	own := []check.Finding{digestStalenessFinding(config.NodePi, "sonarr:1", "Only", 50)}
	got := collectStalenessFindings(own, nil)
	if len(got) != 1 || got[0].Data["title"] != "Only" {
		t.Fatalf("collectStalenessFindings() = %+v, want the one own finding", got)
	}
}

// remediationAt builds one "cleanup:<kind>" remediations row at "planned"
// status with a cleanupjob.go-shaped Detail, for cleanup-plan digest
// tests. Rows must be passed to cleanupSummariesFromRemediations
// newest-first, matching store.RecentRemediations' own ordering.
func remediationAt(kind, detail, status string, at time.Time) store.Remediation {
	return store.Remediation{Action: "cleanup:" + kind, Status: status, Detail: detail, DryRun: true, CreatedAt: at}
}

func TestCleanupSummariesFromRemediationsKeepsLatestPerKind(t *testing.T) {
	newest := remediationAt("recycle", "3 items, 100 bytes", "planned", digestT0)
	older := remediationAt("recycle", "9 items, 999 bytes", "planned", digestT0.Add(-time.Hour))
	other := remediationAt("orphans", "1 items, 10 bytes", "planned", digestT0)

	got := cleanupSummariesFromRemediations([]store.Remediation{newest, other, older})

	want := []notify.CleanupSummary{
		{Kind: "recycle", Items: 3, Bytes: 100},
		{Kind: "orphans", Items: 1, Bytes: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanupSummariesFromRemediations() = %+v, want %+v", got, want)
	}
}

// TestCleanupSummariesFromRemediationsIgnoresNonPlannedAndNonCleanup
// proves a "blocked"/"failed"/"executed" row and a non-cleanup Action
// (e.g. "decision_delete") are both excluded.
func TestCleanupSummariesFromRemediationsIgnoresNonPlannedAndNonCleanup(t *testing.T) {
	rows := []store.Remediation{
		remediationAt("recycle", "1 items, 1 bytes", "failed", digestT0),
		remediationAt("recycle", "1 items, 1 bytes", "blocked", digestT0),
		{Action: "decision_delete", Status: "planned", Detail: "1 items, 1 bytes", CreatedAt: digestT0},
	}
	if got := cleanupSummariesFromRemediations(rows); len(got) != 0 {
		t.Fatalf("cleanupSummariesFromRemediations() = %+v, want none", got)
	}
}

func TestCleanupSummariesFromRemediationsUnparsableDetailYieldsZeroCounts(t *testing.T) {
	rows := []store.Remediation{remediationAt("docker", "not a number", "planned", digestT0)}
	got := cleanupSummariesFromRemediations(rows)
	want := []notify.CleanupSummary{{Kind: "docker", Items: 0, Bytes: 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanupSummariesFromRemediations() = %+v, want %+v", got, want)
	}
}

// TestSendDigestIncludesStalenessCleanupPlansAndPendingDecisions is the
// end-to-end happy path for Task 7's digest enrichment: a staleness
// finding, a recent cleanup plan and two pending decisions all reach the
// rendered body, and BaseURL comes from cfg.Web.PublicURL.
func TestSendDigestIncludesStalenessCleanupPlansAndPendingDecisions(t *testing.T) {
	fs := &fakeStore{
		OpenFindingsResult:       []store.StoredFinding{{Finding: digestStalenessFinding(config.NodePi, "sonarr:1", "Stale Show", 77)}},
		RecentRemediationsResult: []store.Remediation{remediationAt("recycle", "4 items, 2000 bytes", "planned", digestT0)},
		PendingDecisionsResult:   []store.Decision{{ID: 1}, {ID: 2}},
	}
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = fs
		o.Cfg.Web.PublicURL = "http://192.0.2.10/healarr"
	})

	if _, err := a.SendDigest(context.Background()); err != nil {
		t.Fatalf("SendDigest() err = %v", err)
	}

	body := fs.Emails[0].Body
	for _, want := range []string{
		"STALENESS", "Stale Show (score 77, candidate,",
		"CLEANUP (dry-run plans)", "- recycle: 4 item(s),",
		"Decisions pending: 2",
		"Decisions: http://192.0.2.10/healarr/decisions",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("digest body = %q, want it to contain %q", body, want)
		}
	}
}

func TestSendDigestRecentRemediationsErrorPropagates(t *testing.T) {
	wantErr := errors.New("recent remediations boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{RecentRemediationsErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

func TestSendDigestPendingDecisionsErrorPropagates(t *testing.T) {
	wantErr := errors.New("pending decisions boom")
	a := newDigestTestAgent(t, func(o *Options) {
		o.Store = &fakeStore{PendingDecisionsErr: wantErr}
	})

	if _, err := a.SendDigest(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("SendDigest() err = %v, want wrapping %v", err, wantErr)
	}
}

// TestSendDigestLogsSentDigest proves a delivered digest is recorded at
// Info with the outbox row it came from, so "did this morning's digest go
// out?" is answerable from the log alone, without opening the database.
func TestSendDigestLogsSentDigest(t *testing.T) {
	var logBuf strings.Builder
	a := newDigestTestAgent(t, func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	})

	id, err := a.SendDigest(context.Background())
	if err != nil {
		t.Fatalf("SendDigest() err = %v", err)
	}

	out := logBuf.String()
	if !strings.Contains(out, "outbox_id=1") || id != 1 {
		t.Fatalf("digest log = %q (id=%d), want it to name outbox id 1", out, id)
	}
	if !strings.Contains(out, "subject=") {
		t.Fatalf("digest log = %q, want it to name the subject", out)
	}
}
