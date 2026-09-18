package staleness

import (
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// defaultWeights returns the C6 default weights (config.Defaults), so
// every isolated-row test below tracks the numbers in the spec table and
// the task brief (e.g. 200GB capped at 15, 365 days of age capped at 10)
// rather than a bespoke test fixture.
func defaultWeights() config.Staleness { return config.Defaults().Staleness }

func TestScoreItemDaysComponent(t *testing.T) {
	w := defaultWeights()
	tests := []struct {
		name string
		it   Item
		want float64
	}{
		{"never watched, no added date", Item{}, 0},
		{"never watched, added recently", Item{Added: fixedNow.Add(-5 * 24 * time.Hour)}, 0},
		{"never watched, added just past threshold", Item{Added: fixedNow.Add(-31 * 24 * time.Hour)}, w.DaysMaxPoints},
		{"never watched, added long ago", Item{Added: fixedNow.Add(-200 * 24 * time.Hour)}, w.DaysMaxPoints},
		{"watched just now", Item{LastWatched: fixedNow}, 0},
		{"watched half horizon ago", Item{LastWatched: fixedNow.Add(-time.Duration(w.DaysHorizon/2) * 24 * time.Hour)}, w.DaysMaxPoints / 2},
		{"watched exactly at horizon", Item{LastWatched: fixedNow.Add(-time.Duration(w.DaysHorizon) * 24 * time.Hour)}, w.DaysMaxPoints},
		{"watched beyond horizon", Item{LastWatched: fixedNow.Add(-time.Duration(w.DaysHorizon*2) * 24 * time.Hour)}, w.DaysMaxPoints},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(tt.it, w, fixedNow).Components["days"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("days = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemDaysHorizonZeroGuardsDivideByZero(t *testing.T) {
	w := defaultWeights()
	w.DaysHorizon = 0 // must be treated as 1, not divide by zero / never clamp
	it := Item{LastWatched: fixedNow.Add(-2 * 24 * time.Hour)}
	got := ScoreItem(it, w, fixedNow).Components["days"]
	if !almostEqual(got, w.DaysMaxPoints) {
		t.Fatalf("days = %v, want %v (DaysHorizon<=0 treated as 1)", got, w.DaysMaxPoints)
	}
}

func TestScoreItemNeverWatchedAfterDaysZeroGuardsToZero(t *testing.T) {
	w := defaultWeights()
	w.NeverWatchedAfterDays = 0
	it := Item{Added: fixedNow.Add(-1000 * 24 * time.Hour)}
	got := ScoreItem(it, w, fixedNow).Components["days"]
	if got != 0 {
		t.Fatalf("days = %v, want 0 (NeverWatchedAfterDays<=0 guarded)", got)
	}
}

func TestScoreItemCompletionComponent(t *testing.T) {
	w := defaultWeights()
	tests := []struct {
		name string
		it   Item
		want float64
	}{
		{"fully watched", Item{WatchedFully: true}, w.WatchedFullPoints},
		{"partially watched", Item{WatchedPartially: true}, w.WatchedPartialPoints},
		{"neither", Item{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(tt.it, w, fixedNow).Components["completion"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("completion = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemArrComponent(t *testing.T) {
	w := defaultWeights()
	tests := []struct {
		name string
		it   Item
		want float64
	}{
		{"series ended", Item{Kind: "series", Ended: true, Monitored: false}, w.EndedPoints},
		{"movie nothing monitored", Item{Kind: "movie", Ended: true, Monitored: false}, w.EndedPoints},
		{"continuing and monitored", Item{Ended: false, Monitored: true}, w.ContinuingPoints},
		{"neither ended nor monitored", Item{Ended: false, Monitored: false}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(tt.it, w, fixedNow).Components["arr"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("arr = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemSizeComponent(t *testing.T) {
	w := defaultWeights()
	tests := []struct {
		name      string
		sizeBytes int64
		want      float64
	}{
		{"zero", 0, 0},
		{"50GB under cap", 50_000_000_000, 50 * w.SizePointsPerGB},
		{"200GB capped", 200_000_000_000, w.SizeMaxPoints},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(Item{SizeBytes: tt.sizeBytes}, w, fixedNow).Components["size"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("size = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemRequesterComponent(t *testing.T) {
	w := defaultWeights()
	w.Owner = "owner@example.com"
	tests := []struct {
		name string
		it   Item
		want float64
	}{
		{"unknown requester", Item{}, 0},
		{"other requester, unwatched", Item{RequestedBy: "guest@example.com"}, w.OtherRequesterPoints},
		{"other requester, watched", Item{RequestedBy: "guest@example.com", WatchedFully: true}, 0},
		{"owner, unwatched", Item{RequestedBy: "owner@example.com"}, w.OwnerRequesterPoints},
		{"owner, watched", Item{RequestedBy: "owner@example.com", WatchedFully: true}, w.OwnerRequesterPoints},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(tt.it, w, fixedNow).Components["requester"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("requester = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemAgeComponent(t *testing.T) {
	w := defaultWeights()
	tests := []struct {
		name  string
		added time.Time
		want  float64
	}{
		{"no added date", time.Time{}, 0},
		{"added 100 days ago", fixedNow.Add(-100 * 24 * time.Hour), 100 * w.AgePointsPerDay},
		{"added 365 days ago capped", fixedNow.Add(-365 * 24 * time.Hour), w.AgeMaxPoints},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreItem(Item{Added: tt.added}, w, fixedNow).Components["age"]
			if !almostEqual(got, tt.want) {
				t.Fatalf("age = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreItemClampsTotalAt100(t *testing.T) {
	w := defaultWeights()
	// Crank two weights past their normal range so the unclamped sum
	// exceeds 100, proving Total itself is clamped rather than just
	// happening to land in range with default weights.
	w.DaysMaxPoints = 100
	w.EndedPoints = 100
	it := Item{
		Added:     fixedNow.Add(-1000 * 24 * time.Hour), // never watched, long past NeverWatchedAfterDays
		Ended:     true,
		SizeBytes: 500_000_000_000,
	}
	got := ScoreItem(it, w, fixedNow).Total
	if got != 100 {
		t.Fatalf("Total = %v, want 100 (clamped)", got)
	}
}

func TestScoreItemClampsTotalAt0(t *testing.T) {
	w := defaultWeights()
	w.ContinuingPoints = -500 // far more negative than any positive component can offset
	it := Item{Monitored: true}
	got := ScoreItem(it, w, fixedNow).Total
	if got != 0 {
		t.Fatalf("Total = %v, want 0 (clamped)", got)
	}
}

func TestScoreItemNeverMutatesInput(t *testing.T) {
	w := defaultWeights()
	it := Item{Title: "Untouched", Added: fixedNow.Add(-10 * 24 * time.Hour)}
	before := it
	_ = ScoreItem(it, w, fixedNow)
	if it != before {
		t.Fatalf("ScoreItem mutated its input: got %+v, want %+v", it, before)
	}
}

func TestBand(t *testing.T) {
	w := defaultWeights() // CandidateThreshold=70, WatchlistThreshold=50
	tests := []struct {
		total float64
		want  string
	}{
		{100, BandCandidate},
		{70, BandCandidate},
		{69.9, BandWatchlist},
		{50, BandWatchlist},
		{49.9, BandSuppressed},
		{0, BandSuppressed},
	}
	for _, tt := range tests {
		if got := Band(tt.total, w); got != tt.want {
			t.Errorf("Band(%v) = %s, want %s", tt.total, got, tt.want)
		}
	}
}
