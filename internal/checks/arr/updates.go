package arr

import (
	"context"
	"fmt"

	"github.com/parsoFish/healarr/internal/check"
)

const serviceUpdateAvailableID = "service_update_available"

// serviceUpdateAvailableCheck surfaces the UpdateCheck health item each
// app reports when a newer version is available. It shares appHealths with
// arr_health but only looks at UpdateCheck items; reachability is
// arr_health's job, so a client error here is returned as a check error
// instead of a finding.
func serviceUpdateAvailableCheck() check.Check {
	return check.Check{
		ID:      serviceUpdateAvailableID,
		Nodes:   check.BothNodes,
		Tier:    check.TierObserve,
		Cadence: check.Daily,
		Run:     runServiceUpdateAvailable,
	}
}

func runServiceUpdateAvailable(ctx context.Context, d check.Deps) (check.Result, error) {
	apps, err := appHealths(ctx, d)
	if err != nil {
		return check.Result{}, err
	}
	var findings []check.Finding
	for _, a := range apps {
		if a.err != nil {
			return check.Result{}, fmt.Errorf("%s health: %w", a.name, a.err)
		}
		for _, item := range a.items {
			if item.Source != "UpdateCheck" {
				continue
			}
			findings = append(findings, d.NewFinding(serviceUpdateAvailableID, a.name+":update", check.SeverityInfo, check.TierObserve,
				fmt.Sprintf("%s: %s", a.name, item.Message)))
		}
	}
	return check.Result{Findings: findings}, nil
}
