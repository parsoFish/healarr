package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/parsoFish/healarr/internal/config"
)

func newConfigCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Inspect the loaded configuration"}
	root.AddCommand(configValidateCmd(deps, flags))
	return root
}

func configValidateCmd(deps *Deps, flags *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Load config + secrets and report what was found (never key values)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, sec, err := deps.load(flags)
			if err != nil {
				return err
			}
			return printValidation(cmd, flags, cfg, sec)
		},
	}
}

// keyStatus reports "set" or "missing" for a secret, never its value.
func keyStatus(v string) string {
	if v == "" {
		return "missing"
	}
	return "set"
}

func printValidation(cmd *cobra.Command, flags *GlobalFlags, cfg config.Config, sec config.Secrets) error {
	if flags.JSON {
		return Print(cmd.OutOrStdout(), true, validationReport(cfg, sec))
	}
	w := cmd.OutOrStdout()
	lines := []string{
		fmt.Sprintf("node: %s", cfg.Node),
		fmt.Sprintf("actions_enabled: %v", cfg.Actions.Enabled),
		fmt.Sprintf("cleanup_dry_run: %v", cfg.Cleanup.DryRun),
		fmt.Sprintf("staleness_candidate_threshold: %v", cfg.Staleness.CandidateThreshold),
		fmt.Sprintf("staleness_watchlist_threshold: %v", cfg.Staleness.WatchlistThreshold),
		fmt.Sprintf("sonarr_url: %s", cfg.Services.Sonarr.URL),
		fmt.Sprintf("radarr_url: %s", cfg.Services.Radarr.URL),
		fmt.Sprintf("prowlarr_url: %s", cfg.Services.Prowlarr.URL),
		fmt.Sprintf("overseerr_url: %s", cfg.Services.Overseerr.URL),
		fmt.Sprintf("qbittorrent_url: %s", cfg.Services.QBittorrent.URL),
		fmt.Sprintf("plex_url: %s", cfg.Services.Plex.URL),
		fmt.Sprintf("tautulli_url: %s", cfg.Services.Tautulli.URL),
		fmt.Sprintf("sonarr_api_key: %s", keyStatus(sec.SonarrAPIKey)),
		fmt.Sprintf("radarr_api_key: %s", keyStatus(sec.RadarrAPIKey)),
		fmt.Sprintf("prowlarr_api_key: %s", keyStatus(sec.ProwlarrAPIKey)),
		fmt.Sprintf("overseerr_api_key: %s", keyStatus(sec.OverseerrAPIKey)),
		fmt.Sprintf("qbit_user: %s", keyStatus(sec.QBitUser)),
		fmt.Sprintf("qbit_pass: %s", keyStatus(sec.QBitPass)),
		fmt.Sprintf("plex_token: %s", keyStatus(sec.PlexToken)),
		fmt.Sprintf("tautulli_api_key: %s", keyStatus(sec.TautulliAPIKey)),
		fmt.Sprintf("peer_token: %s", keyStatus(sec.PeerToken)),
		fmt.Sprintf("web_token: %s", keyStatus(sec.WebToken)),
		fmt.Sprintf("anthropic_api_key: %s", keyStatus(sec.AnthropicAPIKey)),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func validationReport(cfg config.Config, sec config.Secrets) map[string]any {
	return map[string]any{
		"node":                          string(cfg.Node),
		"actions_enabled":               cfg.Actions.Enabled,
		"cleanup_dry_run":               cfg.Cleanup.DryRun,
		"staleness_candidate_threshold": cfg.Staleness.CandidateThreshold,
		"staleness_watchlist_threshold": cfg.Staleness.WatchlistThreshold,
		"service_urls": map[string]string{
			"sonarr":      cfg.Services.Sonarr.URL,
			"radarr":      cfg.Services.Radarr.URL,
			"prowlarr":    cfg.Services.Prowlarr.URL,
			"overseerr":   cfg.Services.Overseerr.URL,
			"qbittorrent": cfg.Services.QBittorrent.URL,
			"plex":        cfg.Services.Plex.URL,
			"tautulli":    cfg.Services.Tautulli.URL,
		},
		"api_keys": map[string]string{
			"sonarr_api_key":    keyStatus(sec.SonarrAPIKey),
			"radarr_api_key":    keyStatus(sec.RadarrAPIKey),
			"prowlarr_api_key":  keyStatus(sec.ProwlarrAPIKey),
			"overseerr_api_key": keyStatus(sec.OverseerrAPIKey),
			"qbit_user":         keyStatus(sec.QBitUser),
			"qbit_pass":         keyStatus(sec.QBitPass),
			"plex_token":        keyStatus(sec.PlexToken),
			"tautulli_api_key":  keyStatus(sec.TautulliAPIKey),
			"peer_token":        keyStatus(sec.PeerToken),
			"web_token":         keyStatus(sec.WebToken),
			"anthropic_api_key": keyStatus(sec.AnthropicAPIKey),
		},
	}
}
