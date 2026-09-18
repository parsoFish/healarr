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
