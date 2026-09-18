package decision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/clients/overseerr"
	"github.com/parsoFish/healarr/internal/clients/radarr"
	"github.com/parsoFish/healarr/internal/clients/sonarr"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

// partialFailOverseerr is an overseerr.Client test double that can fail
// Requests and DeclineRequest independently — unlike overseerr.Fake,
// whose single Err field would fail both calls together, which can't
// exercise "the lookup succeeded but one decline failed" on its own.
type partialFailOverseerr struct {
	reqs          []overseerr.Request
	requestsErr   error
	declineErrFor map[int64]error
	declined      []int64
}

var _ overseerr.Client = (*partialFailOverseerr)(nil)

func (o *partialFailOverseerr) Status(context.Context) (overseerr.Status, error) {
	return overseerr.Status{}, nil
}

func (o *partialFailOverseerr) Requests(context.Context, string) ([]overseerr.Request, error) {
	return o.reqs, o.requestsErr
}

func (o *partialFailOverseerr) DeclineRequest(_ context.Context, id int64) error {
	o.declined = append(o.declined, id)
	return o.declineErrFor[id]
}

// newDeleteTestDeps builds a Deps for the delete branch: actions enabled,
// SnoozeDays irrelevant, fixed Now, given clients/peer.
func newDeleteTestDeps(fs *fakeDecisionStore, sonarrC sonarr.Client, radarrC radarr.Client, overseerrC overseerr.Client, peerC *fakePeerClient) Deps {
	d := Deps{
		Deps: check.Deps{
			Node: config.NodePi,
			Cfg:  config.Config{Node: config.NodePi, Actions: config.Actions{Enabled: true}},
			Now:  func() time.Time { return fixedNow },
		},
		Store: fs,
	}
	d.Sonarr = sonarrC
	d.Radarr = radarrC
	d.Overseerr = overseerrC
	if peerC != nil {
		d.Peer = peerC
	}
	return d
}

func TestExecuteDeleteBlockedWhenActionsDisabled(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	deps := newTestDeps(fs) // actions disabled by default; Sonarr stays nil
	_, err := Execute(context.Background(), deps, 1)

	if !errors.Is(err, ErrActionsDisabled) {
		t.Fatalf("Execute err = %v, want ErrActionsDisabled", err)
	}
	if len(fs.MarkCalls) != 1 || fs.MarkCalls[0].Status != "blocked" || !errors.Is(fs.MarkCalls[0].Cause, ErrActionsDisabled) {
		t.Fatalf("MarkCalls = %+v", fs.MarkCalls)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "blocked" {
		t.Fatalf("Remediations = %+v", fs.Remediations)
	}
}

func TestExecuteDeleteUnknownEntityKeyFails(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "plex:1", Kind: "delete"},
	}}
	deps := newDeleteTestDeps(fs, nil, nil, nil, nil)
	_, err := Execute(context.Background(), deps, 1)

	if !errors.Is(err, ErrUnknownEntityKey) {
		t.Fatalf("Execute err = %v, want ErrUnknownEntityKey", err)
	}
	if len(fs.MarkCalls) != 1 || fs.MarkCalls[0].Status != "failed" {
		t.Fatalf("MarkCalls = %+v", fs.MarkCalls)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" {
		t.Fatalf("Remediations = %+v", fs.Remediations)
	}
}

func TestExecuteDeleteSonarrClientNotConfiguredFails(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	deps := newDeleteTestDeps(fs, nil, nil, nil, nil)
	_, err := Execute(context.Background(), deps, 1)

	if !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("Execute err = %v, want check.ErrNotConfigured", err)
	}
}

func TestExecuteDeleteRadarrClientNotConfiguredFails(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "radarr:1", Kind: "delete"},
	}}
	deps := newDeleteTestDeps(fs, nil, nil, nil, nil)
	_, err := Execute(context.Background(), deps, 1)

	if !errors.Is(err, check.ErrNotConfigured) {
		t.Fatalf("Execute err = %v, want check.ErrNotConfigured", err)
	}
}

func TestExecuteDeleteSonarrSuccessDeclinesOverseerrAndHintsPeer(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{
		SeriesList: []sonarr.Series{{ID: 1, TVDBID: 555, Title: "Show"}},
		HistoryRecords: []sonarr.HistoryRecord{
			{SeriesID: 1, DownloadID: "abc123", Date: fixedNow},
			{SeriesID: 1, DownloadID: "abc123", Date: fixedNow}, // duplicate: hashes must dedup
			{SeriesID: 2, DownloadID: "other", Date: fixedNow},  // different series: must be excluded
		},
	}
	overseerrFake := &partialFailOverseerr{reqs: []overseerr.Request{
		{ID: 10, MediaType: "tv", TVDBID: 555},
		{ID: 11, MediaType: "movie", TMDBID: 1},
		{ID: 12, MediaType: "tv", TVDBID: 999},
	}}
	peer := &fakePeerClient{}

	deps := newDeleteTestDeps(fs, sonarrFake, nil, overseerrFake, peer)
	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if len(sonarrFake.Calls) == 0 || sonarrFake.Calls[len(sonarrFake.Calls)-2] != "DeleteSeries(1,true,true)" {
		t.Errorf("sonarr Calls = %v, want DeleteSeries(1,true,true)", sonarrFake.Calls)
	}
	if len(overseerrFake.declined) != 1 || overseerrFake.declined[0] != 10 {
		t.Errorf("declined = %v, want [10]", overseerrFake.declined)
	}

	sent := peer.Sent()
	if len(sent) != 1 {
		t.Fatalf("peer Sent = %+v, want one decision", sent)
	}
	if sent[0].Kind != "qbit_delete" || sent[0].EntityKey != "sonarr:1" {
		t.Errorf("sent decision = %+v", sent[0])
	}
	hashes, _ := sent[0].Payload["hashes"].([]string)
	if len(hashes) != 1 || hashes[0] != "ABC123" {
		t.Errorf("hashes = %v, want [ABC123]", hashes)
	}

	if len(fs.Remediations) != 1 {
		t.Fatalf("Remediations = %+v", fs.Remediations)
	}
	detail := fs.Remediations[0].Detail
	for _, want := range []string{"deleted sonarr series 1 (Show)", "declined 1 overseerr request(s)", "sent qbit_delete hint for 1 torrent(s)"} {
		if !strings.Contains(detail, want) {
			t.Errorf("Detail = %q, want containing %q", detail, want)
		}
	}
}

func TestExecuteDeleteRadarrSuccess(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "radarr:2", Kind: "delete"},
	}}
	radarrFake := &radarr.Fake{
		MovieList:      []radarr.Movie{{ID: 2, TMDBID: 777, Title: "Movie"}},
		HistoryRecords: []radarr.HistoryRecord{{MovieID: 2, DownloadID: "def456", Date: fixedNow}},
	}
	overseerrFake := &partialFailOverseerr{reqs: []overseerr.Request{{ID: 20, MediaType: "movie", TMDBID: 777}}}
	peer := &fakePeerClient{}

	deps := newDeleteTestDeps(fs, nil, radarrFake, overseerrFake, peer)
	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if len(overseerrFake.declined) != 1 || overseerrFake.declined[0] != 20 {
		t.Errorf("declined = %v, want [20]", overseerrFake.declined)
	}
	sent := peer.Sent()
	if len(sent) != 1 || sent[0].EntityKey != "radarr:2" {
		t.Fatalf("peer Sent = %+v", sent)
	}
}

func TestExecuteDeleteSonarrDeleteSeriesErrorFails(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{Err: errors.New("sonarr down")}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, nil, nil)

	_, err := Execute(context.Background(), deps, 1)
	if err == nil || !strings.Contains(err.Error(), "delete series") || !strings.Contains(err.Error(), "sonarr down") {
		t.Fatalf("Execute err = %v, want wrapping delete series/sonarr down", err)
	}
	if len(fs.MarkCalls) != 1 || fs.MarkCalls[0].Status != "failed" {
		t.Fatalf("MarkCalls = %+v", fs.MarkCalls)
	}
	if len(fs.Remediations) != 1 || fs.Remediations[0].Status != "failed" {
		t.Fatalf("Remediations = %+v", fs.Remediations)
	}
}

func TestExecuteDeleteOverseerrRequestsLookupErrorIsBestEffort(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{SeriesList: []sonarr.Series{{ID: 1, TVDBID: 1}}}
	overseerrFake := &partialFailOverseerr{requestsErr: errors.New("overseerr down")}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, overseerrFake, nil)

	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v, want success (overseerr failure is best effort)", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if !strings.Contains(fs.Remediations[0].Detail, "overseerr down") {
		t.Errorf("Detail = %q, want it to mention the overseerr failure", fs.Remediations[0].Detail)
	}
}

func TestExecuteDeleteOverseerrDeclineErrorIsBestEffort(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{SeriesList: []sonarr.Series{{ID: 1, TVDBID: 1}}}
	overseerrFake := &partialFailOverseerr{
		reqs:          []overseerr.Request{{ID: 30, MediaType: "tv", TVDBID: 1}},
		declineErrFor: map[int64]error{30: errors.New("decline boom")},
	}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, overseerrFake, nil)

	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v, want success (decline failure is best effort)", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if !strings.Contains(fs.Remediations[0].Detail, "decline overseerr request 30") {
		t.Errorf("Detail = %q, want it to mention the decline failure", fs.Remediations[0].Detail)
	}
}

func TestExecuteDeletePeerSendErrorIsBestEffort(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{{SeriesID: 1, DownloadID: "xyz", Date: fixedNow}}}
	peer := &fakePeerClient{SendErr: errors.New("peer unreachable")}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, nil, peer)

	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v, want success (peer send failure is best effort)", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if !strings.Contains(fs.Remediations[0].Detail, "qbit_delete peer send") {
		t.Errorf("Detail = %q, want it to mention the peer send failure", fs.Remediations[0].Detail)
	}
}

func TestExecuteDeleteNoPeerConfiguredSkipsHint(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{HistoryRecords: []sonarr.HistoryRecord{{SeriesID: 1, DownloadID: "xyz", Date: fixedNow}}}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, nil, nil)

	dec, err := Execute(context.Background(), deps, 1)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if dec.Status != "executed" {
		t.Fatalf("dec.Status = %q, want executed", dec.Status)
	}
	if strings.Contains(fs.Remediations[0].Detail, "qbit_delete") {
		t.Errorf("Detail = %q, want no qbit_delete mention with no peer configured", fs.Remediations[0].Detail)
	}
}

func TestExecuteDeleteNoHashesSkipsPeerSend(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"},
	}}
	sonarrFake := &sonarr.Fake{} // no history records at all
	peer := &fakePeerClient{}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, nil, peer)

	if _, err := Execute(context.Background(), deps, 1); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(peer.Sent()) != 0 {
		t.Errorf("peer Sent = %+v, want none with no known hashes", peer.Sent())
	}
}

func TestExecuteDeleteMarkExecutedErrorPropagates(t *testing.T) {
	fs := &fakeDecisionStore{
		Decisions: map[int64]store.Decision{1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"}},
		MarkErr:   errors.New("mark boom"),
	}
	sonarrFake := &sonarr.Fake{}
	deps := newDeleteTestDeps(fs, sonarrFake, nil, nil, nil)

	_, err := Execute(context.Background(), deps, 1)
	if err == nil || !strings.Contains(err.Error(), "mark executed") || !strings.Contains(err.Error(), "mark boom") {
		t.Fatalf("Execute err = %v, want wrapping mark executed/mark boom", err)
	}
}

func TestExecuteDeleteRecordRemediationFailureIsNotFatal(t *testing.T) {
	fs := &fakeDecisionStore{
		Decisions: map[int64]store.Decision{1: {ID: 1, EntityKey: "sonarr:1", Kind: "delete"}},
		RecordErr: errors.New("remediation write boom"),
	}
	deps := newTestDeps(fs) // actions disabled: exercises the blocked path's recordDeleteRemediation

	_, err := Execute(context.Background(), deps, 1)
	if !errors.Is(err, ErrActionsDisabled) {
		t.Fatalf("Execute err = %v, want ErrActionsDisabled despite the remediation write failing", err)
	}
}
