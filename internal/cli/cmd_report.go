package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
)

// newReportCmd builds the "report" command tree.
func newReportCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "report", Short: "Render this node's health digest"}
	root.AddCommand(reportGenerateCmd(deps, flags))
	return root
}

func reportGenerateCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate the digest for this node (--dry-run: run checks in memory instead of reading the store)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReportGenerate(cmd, deps, flags)
		},
	}
}

// runReportGenerate builds the DigestInput (in memory on --dry-run,
// otherwise from the store) and prints either the rendered plain-text
// digest or, with --json, the DigestInput itself.
func runReportGenerate(cmd *cobra.Command, deps *Deps, flags *GlobalFlags) error {
	ctx := cmd.Context()
	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}

	var in notify.DigestInput
	if flags.DryRun {
		in, err = digestFromRun(ctx, deps, flags, cfg, sec)
	} else {
		in, err = digestFromStore(ctx, deps, cfg)
	}
	if err != nil {
		return err
	}

	if flags.JSON {
		return Print(cmd.OutOrStdout(), true, in)
	}
	text, err := notify.RenderDigest(in)
	if err != nil {
		return fmt.Errorf("report generate: render digest: %w", err)
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), text)
	return err
}

// digestFromRun runs every check that applies to cfg.Node in memory (no
// store, so Deps.Previous stays nil) and builds a DigestInput straight
// from that report. BaseURL is left empty: Phase 2's config carries no
// external URL yet.
func digestFromRun(ctx context.Context, deps *Deps, flags *GlobalFlags, cfg config.Config, sec config.Secrets) (notify.DigestInput, error) {
	reg, err := registryFor(deps, cfg)
	if err != nil {
		return notify.DigestInput{}, err
	}
	checkDeps, err := buildCheckDeps(ctx, deps, flags, cfg, sec)
	if err != nil {
		return notify.DigestInput{}, err
	}
	rep := check.Run(ctx, reg.ForNode(cfg.Node), checkDeps, cfg.Checks.Timeout)
	return notify.DigestInput{
		Node:        rep.Node,
		GeneratedAt: rep.GeneratedAt,
		Findings:    rep.Findings,
		Errors:      rep.Errors,
		Skipped:     rep.Skipped,
		ChecksRun:   rep.ChecksRun,
		Metrics:     rep.Metrics,
	}, nil
}

// digestFromStore reads cfg.Node's open findings and latest report from
// the store and builds a DigestInput from them.
func digestFromStore(ctx context.Context, deps *Deps, cfg config.Config) (in notify.DigestInput, err error) {
	st, err := deps.OpenStore(ctx, cfg)
	if err != nil {
		return notify.DigestInput{}, fmt.Errorf("report generate: open store: %w", err)
	}
	defer func() {
		if closeErr := st.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("report generate: close store: %w", closeErr))
		}
	}()

	stored, err := st.OpenFindings(ctx, cfg.Node)
	if err != nil {
		return notify.DigestInput{}, fmt.Errorf("report generate: open findings: %w", err)
	}
	rep, _, err := st.LatestReport(ctx, cfg.Node)
	if err != nil {
		return notify.DigestInput{}, fmt.Errorf("report generate: latest report: %w", err)
	}

	findings := make([]check.Finding, 0, len(stored))
	for _, f := range stored {
		findings = append(findings, f.Finding)
	}

	return notify.DigestInput{
		Node:        cfg.Node,
		GeneratedAt: rep.GeneratedAt,
		Findings:    findings,
		Errors:      rep.Errors,
		Skipped:     rep.Skipped,
		ChecksRun:   rep.ChecksRun,
		Metrics:     rep.Metrics,
	}, nil
}
