package arr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/parsoFish/healarr/internal/check"
)

const arrHealthID = "arr_health"

// healthItem is the shape common to sonarr.HealthItem, radarr.HealthItem,
// and prowlarr.HealthItem: three separate types with identical fields
// declared in their own client packages. Adapting each to this one local
// type lets arr_health and service_update_available iterate a single slice
// type instead of duplicating logic per app.
type healthItem struct {
	Type    string
	Source  string
	Message string
}

// appHealth is one app's health fetch outcome. The raw error is preserved
// rather than converted here because arr_health and
// service_update_available react to a client error differently:
// arr_health turns it into a critical "unreachable" finding (that is its
// purpose), service_update_available returns it as a check error (health
// already reported reachability).
type appHealth struct {
	name  string
	items []healthItem
	err   error
}

// appHealths fetches health from every configured *arr/Prowlarr client. It
// returns check.ErrNotConfigured only when none of the three clients are
// set; a client that is configured but errors surfaces via appHealth.err
// for the caller to interpret.
func appHealths(ctx context.Context, d check.Deps) ([]appHealth, error) {
	if d.Sonarr == nil && d.Radarr == nil && d.Prowlarr == nil {
		return nil, check.ErrNotConfigured
	}
	fetchers := []struct {
		name string
		fn   func(context.Context, check.Deps) ([]healthItem, error)
	}{
		{"sonarr", sonarrHealth},
		{"radarr", radarrHealth},
		{"prowlarr", prowlarrHealth},
	}
	apps := make([]appHealth, 0, len(fetchers))
	for _, f := range fetchers {
		items, err := f.fn(ctx, d)
		apps = append(apps, appHealth{name: f.name, items: items, err: err})
	}
	return apps, nil
}

func sonarrHealth(ctx context.Context, d check.Deps) ([]healthItem, error) {
	if d.Sonarr == nil {
		return nil, nil
	}
	raw, err := d.Sonarr.Health(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]healthItem, len(raw))
	for i, r := range raw {
		out[i] = healthItem{Type: r.Type, Source: r.Source, Message: r.Message}
	}
	return out, nil
}

func radarrHealth(ctx context.Context, d check.Deps) ([]healthItem, error) {
	if d.Radarr == nil {
		return nil, nil
	}
	raw, err := d.Radarr.Health(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]healthItem, len(raw))
	for i, r := range raw {
		out[i] = healthItem{Type: r.Type, Source: r.Source, Message: r.Message}
	}
	return out, nil
}

func prowlarrHealth(ctx context.Context, d check.Deps) ([]healthItem, error) {
	if d.Prowlarr == nil {
		return nil, nil
	}
	raw, err := d.Prowlarr.Health(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]healthItem, len(raw))
	for i, r := range raw {
		out[i] = healthItem{Type: r.Type, Source: r.Source, Message: r.Message}
	}
	return out, nil
}

// shortHash returns the first 8 hex characters of s's sha256 sum, used to
// keep a health item's entity key stable and short even when its message
// is long or contains punctuation.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// arrHealthCheck watches each configured app's /health items (excluding
// UpdateCheck, which service_update_available owns) and a client that
// can't be reached at all.
func arrHealthCheck() check.Check {
	return check.Check{
		ID:      arrHealthID,
		Nodes:   check.PiOnly,
		Tier:    check.TierObserve,
		Cadence: check.Every5m,
		Run:     runArrHealth,
	}
}

func runArrHealth(ctx context.Context, d check.Deps) (check.Result, error) {
	apps, err := appHealths(ctx, d)
	if err != nil {
		return check.Result{}, err
	}
	var findings []check.Finding
	for _, a := range apps {
		findings = append(findings, arrHealthAppFindings(d, a)...)
	}
	return check.Result{Findings: findings}, nil
}

// arrHealthAppFindings turns one app's health fetch outcome into findings:
// an unreachable client becomes a single critical finding (reachability is
// this check's purpose, so the error never propagates as a check error),
// otherwise each non-UpdateCheck health item becomes its own finding.
func arrHealthAppFindings(d check.Deps, a appHealth) []check.Finding {
	if a.err != nil {
		f := d.NewFinding(arrHealthID, a.name, check.SeverityCritical, check.TierObserve,
			fmt.Sprintf("%s unreachable: %s", a.name, a.err.Error()))
		return []check.Finding{f}
	}
	var findings []check.Finding
	for _, item := range a.items {
		if item.Source == "UpdateCheck" {
			continue
		}
		sev := check.SeverityWarn
		if strings.EqualFold(item.Type, "error") {
			sev = check.SeverityCritical
		}
		key := a.name + ":" + item.Source + ":" + shortHash(item.Message)
		findings = append(findings, d.NewFinding(arrHealthID, key, sev, check.TierObserve,
			fmt.Sprintf("%s: %s", a.name, item.Message)))
	}
	return findings
}
