package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/staleness"
)

// newStalenessCmd builds the "staleness" command tree: score runs the
// same Collect+ScoreItem pipeline as the staleness_scan check (ADR-016)
// on demand, read-only — it never touches the store — so an operator can
// preview what the next scheduled run would surface.
func newStalenessCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "staleness", Short: "Preview the ADR-016 staleness score"}
	root.AddCommand(stalenessScoreCmd(deps, flags))
	return root
}

func stalenessScoreCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	var min float64
	cmd := &cobra.Command{
		Use:   "score",
		Short: "Score every series/movie and list candidates sorted by score, highest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStalenessScore(cmd, deps, flags, min, cmd.Flags().Changed("min"))
		},
	}
	cmd.Flags().Float64Var(&min, "min", 0, "only list items scoring at least this (default: config's watchlist threshold)")
	return cmd
}

// runStalenessScore builds check.Deps via buildCheckDeps (the same path
// `check run` uses), runs staleness.Collect + staleness.ScoreItem over
// every item, and prints the result honouring --json. minSet distinguishes
// an explicit `--min 0` from the flag never being passed, since the
// latter's default (config's WatchlistThreshold) isn't known until config
// loads.
func runStalenessScore(cmd *cobra.Command, deps *Deps, flags *GlobalFlags, min float64, minSet bool) error {
	ctx := cmd.Context()
	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}
	checkDeps, err := buildCheckDeps(ctx, deps, flags, cfg, sec)
	if err != nil {
		return err
	}
	if !minSet {
		min = cfg.Staleness.WatchlistThreshold
	}

	items, err := staleness.Collect(ctx, checkDeps)
	if err != nil {
		return fmt.Errorf("staleness score: %w", err)
	}

	rows := stalenessScoreRows(items, cfg.Staleness, checkDeps.Now(), min)
	return Print(cmd.OutOrStdout(), flags.JSON, rows)
}

// stalenessScoreRow is one row of `staleness score`'s table: EntityKey,
// Title, Score, Band, and the top two components explaining the score.
type stalenessScoreRow struct {
	EntityKey string
	Title     string
	Score     float64
	Band      string
	Top       string
}

// stalenessScoreRows scores every item, drops anything below min, and
// sorts the rest by score descending (ties broken by EntityKey, for
// deterministic output).
func stalenessScoreRows(items []staleness.Item, w config.Staleness, now time.Time, min float64) []stalenessScoreRow {
	rows := make([]stalenessScoreRow, 0, len(items))
	for _, it := range items {
		score := staleness.ScoreItem(it, w, now)
		if score.Total < min {
			continue
		}
		rows = append(rows, stalenessScoreRow{
			EntityKey: it.EntityKey,
			Title:     it.Title,
			Score:     score.Total,
			Band:      staleness.Band(score.Total, w),
			Top:       strings.Join(staleness.TopComponents(score.Components), ", "),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].EntityKey < rows[j].EntityKey
	})
	return rows
}
