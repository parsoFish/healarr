package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/peer"
	"github.com/parsoFish/healarr/internal/version"
)

// errPeerNotConfigured is returned by `peer ping` when cfg.Peer.PeerURL is
// empty: there is nowhere to ping.
var errPeerNotConfigured = errors.New("peer_url is not configured")

func newPeerCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "peer", Short: "Talk to the other node over the peer channel"}
	root.AddCommand(peerPingCmd(deps, flags))
	return root
}

func peerPingCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Heartbeat the peer and report its latest pushed report",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPeerPing(cmd, deps, flags)
		},
	}
}

// peerPingResult is the JSON/table shape for `peer ping`.
type peerPingResult struct {
	Node             string  `json:"node"`
	Reachable        bool    `json:"reachable"`
	Error            string  `json:"error,omitempty"`
	HasReport        bool    `json:"hasReport"`
	ReportAgeSeconds float64 `json:"reportAgeSeconds,omitempty"`
	ChecksRun        int     `json:"checksRun,omitempty"`
	ChecksFailed     int     `json:"checksFailed,omitempty"`
	Findings         int     `json:"findings,omitempty"`
}

// runPeerPing is a diagnostic: it always exits 0 (even when the peer is
// unreachable), printing whatever it learned — including the error text —
// rather than failing the command. Only a local problem (bad config, a
// broken client constructor, or a write failure) returns a non-nil error.
func runPeerPing(cmd *cobra.Command, deps *Deps, flags *GlobalFlags) error {
	ctx := cmd.Context()
	cfg, sec, err := deps.load(flags)
	if err != nil {
		return err
	}

	client, err := peerClientFor(deps, cfg, sec)
	if err != nil {
		return fmt.Errorf("peer ping: %w", err)
	}
	if client == nil {
		return errPeerNotConfigured
	}

	res := peerPingResult{Node: string(cfg.Node)}
	now := time.Now()

	_, hbErr := client.Heartbeat(ctx, peer.Heartbeat{Node: cfg.Node, At: now, Version: version.Version})
	env, hasLatest, fetchErr := client.FetchLatest(ctx)

	res.Reachable = hbErr == nil && fetchErr == nil
	res.Error = pingErrorText(hbErr, fetchErr)
	if fetchErr == nil {
		res.HasReport = hasLatest
		if hasLatest {
			res.ReportAgeSeconds = now.Sub(env.SentAt).Seconds()
			res.ChecksRun = env.Report.ChecksRun
			res.ChecksFailed = env.Report.ChecksFailed
			res.Findings = len(env.Report.Findings)
		}
	}

	return Print(cmd.OutOrStdout(), flags.JSON, res)
}

// pingErrorText combines the heartbeat and fetch-latest errors into one
// diagnostic string, naming which call(s) failed rather than surfacing
// only one and dropping the other.
func pingErrorText(hbErr, fetchErr error) string {
	switch {
	case hbErr != nil && fetchErr != nil:
		return fmt.Sprintf("heartbeat: %v; fetch latest: %v", hbErr, fetchErr)
	case hbErr != nil:
		return fmt.Sprintf("heartbeat: %v", hbErr)
	case fetchErr != nil:
		return fmt.Sprintf("fetch latest: %v", fetchErr)
	default:
		return ""
	}
}
