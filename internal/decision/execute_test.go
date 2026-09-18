package decision

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/store"
)

var fixedNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// newTestDeps builds a Deps whose Now is pinned to fixedNow and whose
// Store is fs, with every other check.Deps field at its zero value
// (nil clients, disabled actions) unless a test overrides it.
func newTestDeps(fs *fakeDecisionStore) Deps {
	return Deps{
		Deps: check.Deps{
			Node: config.NodePi,
			Cfg:  config.Config{Node: config.NodePi, Staleness: config.Staleness{SnoozeDays: 60}},
			Now:  func() time.Time { return fixedNow },
		},
		Store: fs,
	}
}

func TestExecuteReturnsErrorWhenDecisionLookupFails(t *testing.T) {
	fs := &fakeDecisionStore{ByIDErr: errors.New("db boom")}
	_, err := Execute(context.Background(), newTestDeps(fs), 1)
	if err == nil || !strings.Contains(err.Error(), "db boom") {
		t.Fatalf("Execute err = %v, want wrapping db boom", err)
	}
}

func TestExecuteReturnsNotFoundForUnknownID(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{}}
	_, err := Execute(context.Background(), newTestDeps(fs), 42)
	if !errors.Is(err, store.ErrDecisionNotFound) {
		t.Fatalf("Execute err = %v, want ErrDecisionNotFound", err)
	}
}

func TestExecuteReturnsErrUnknownKindForBadKind(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		1: {ID: 1, EntityKey: "sonarr:1", Kind: "frobnicate"},
	}}
	_, err := Execute(context.Background(), newTestDeps(fs), 1)
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Execute err = %v, want ErrUnknownKind", err)
	}
}

func TestExecuteKeepSnoozesAndMarksExecuted(t *testing.T) {
	fs := &fakeDecisionStore{Decisions: map[int64]store.Decision{
		7: {ID: 7, EntityKey: "sonarr:99", Kind: "keep"},
	}}
	deps := newTestDeps(fs)
	// Keep never checks the actions gate: prove it stays disabled and the
	// decision still executes.
	deps.Cfg.Actions.Enabled = false

	dec, err := Execute(context.Background(), deps, 7)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if dec.Status != "executed" {
		t.Errorf("dec.Status = %q, want executed", dec.Status)
	}
	if dec.SnoozeUntil == nil || !dec.SnoozeUntil.Equal(fixedNow.AddDate(0, 0, 60)) {
		t.Errorf("dec.SnoozeUntil = %v, want %v", dec.SnoozeUntil, fixedNow.AddDate(0, 0, 60))
	}

	if len(fs.SnoozeCalls) != 1 || fs.SnoozeCalls[0].EntityKey != "sonarr:99" {
		t.Fatalf("SnoozeCalls = %+v", fs.SnoozeCalls)
	}
	if !fs.SnoozeCalls[0].Until.Equal(fixedNow.AddDate(0, 0, 60)) {
		t.Errorf("snooze until = %v, want %v", fs.SnoozeCalls[0].Until, fixedNow.AddDate(0, 0, 60))
	}
	if len(fs.MarkCalls) != 1 || fs.MarkCalls[0].Status != "executed" || fs.MarkCalls[0].Cause != nil {
		t.Fatalf("MarkCalls = %+v", fs.MarkCalls)
	}
	// Keep never touches the remediations table (constraints.md: only
	// delete attempts are recorded there).
	if len(fs.Remediations) != 0 {
		t.Errorf("Remediations = %+v, want none for keep", fs.Remediations)
	}
}

func TestExecuteKeepSnoozeErrorMarksFailed(t *testing.T) {
	fs := &fakeDecisionStore{
		Decisions: map[int64]store.Decision{1: {ID: 1, EntityKey: "sonarr:1", Kind: "keep"}},
		SnoozeErr: errors.New("snooze boom"),
	}
	_, err := Execute(context.Background(), newTestDeps(fs), 1)
	if err == nil || !strings.Contains(err.Error(), "snooze boom") {
		t.Fatalf("Execute err = %v, want wrapping snooze boom", err)
	}
	if len(fs.MarkCalls) != 1 || fs.MarkCalls[0].Status != "failed" || fs.MarkCalls[0].Cause == nil {
		t.Fatalf("MarkCalls = %+v", fs.MarkCalls)
	}
}

func TestExecuteKeepSnoozeErrorAndMarkFailedErrorBothSurface(t *testing.T) {
	fs := &fakeDecisionStore{
		Decisions: map[int64]store.Decision{1: {ID: 1, EntityKey: "sonarr:1", Kind: "keep"}},
		SnoozeErr: errors.New("snooze boom"),
		MarkErr:   errors.New("mark boom"),
	}
	_, err := Execute(context.Background(), newTestDeps(fs), 1)
	if err == nil || !strings.Contains(err.Error(), "snooze boom") || !strings.Contains(err.Error(), "mark boom") {
		t.Fatalf("Execute err = %v, want both snooze boom and mark boom", err)
	}
}

func TestExecuteKeepMarkExecutedErrorPropagates(t *testing.T) {
	fs := &fakeDecisionStore{
		Decisions: map[int64]store.Decision{1: {ID: 1, EntityKey: "sonarr:1", Kind: "keep"}},
		MarkErr:   errors.New("mark boom"),
	}
	_, err := Execute(context.Background(), newTestDeps(fs), 1)
	if err == nil || !strings.Contains(err.Error(), "mark executed") || !strings.Contains(err.Error(), "mark boom") {
		t.Fatalf("Execute err = %v, want wrapping mark executed/mark boom", err)
	}
	// The snooze itself succeeded before the mark call failed.
	if len(fs.SnoozeCalls) != 1 {
		t.Fatalf("SnoozeCalls = %+v, want one successful snooze", fs.SnoozeCalls)
	}
}

func TestParseEntityKeyValid(t *testing.T) {
	cases := []struct {
		key      string
		provider string
		id       int64
	}{
		{"sonarr:12", "sonarr", 12},
		{"radarr:34", "radarr", 34},
	}
	for _, c := range cases {
		ref, err := parseEntityKey(c.key)
		if err != nil {
			t.Fatalf("parseEntityKey(%q): %v", c.key, err)
		}
		if ref.Provider != c.provider || ref.ID != c.id {
			t.Errorf("parseEntityKey(%q) = %+v, want {%s %d}", c.key, ref, c.provider, c.id)
		}
	}
}

func TestParseEntityKeyRejectsBadInput(t *testing.T) {
	for _, key := range []string{"sonarr", "plex:1", "sonarr:notanumber", ""} {
		if _, err := parseEntityKey(key); !errors.Is(err, ErrUnknownEntityKey) {
			t.Errorf("parseEntityKey(%q) err = %v, want ErrUnknownEntityKey", key, err)
		}
	}
}
