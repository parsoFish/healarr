package staleness

import (
	"math"
	"time"
)

// fixedNow is the deterministic clock every test in this package uses.
var fixedNow = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// almostEqual compares two float64 scores allowing for floating-point
// rounding noise, since ScoreItem's components are sums and products of
// config-supplied weights rather than exact integers.
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
