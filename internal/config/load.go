package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
)

const secretsEnv = "HEALARR_SECRETS"

// Load reads config + secrets, applies env overrides, resolves *arr keys.
func Load(configPath string) (Config, Secrets, error) {
	cfg := Defaults()
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return Config{}, Secrets{}, fmt.Errorf("read config %s: %w", configPath, err)
	}
	secretsPath := os.Getenv(secretsEnv)
	if secretsPath == "" {
		secretsPath = filepath.Join(filepath.Dir(configPath), "secrets.toml")
	}
	sec, err := loadSecrets(secretsPath)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	cfg = applyConfigEnv(cfg)
	sec = applySecretsEnv(sec)
	sec, err = resolveArrKeys(cfg, sec)
	if err != nil {
		return Config{}, Secrets{}, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, Secrets{}, err
	}
	return cfg, sec, nil
}

func loadSecrets(path string) (Secrets, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Secrets{}, fmt.Errorf("secrets file: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Secrets{}, fmt.Errorf("secrets file %s has mode %o; must be 0600", path, perm)
	}
	var sec Secrets
	if _, err := toml.DecodeFile(path, &sec); err != nil {
		return Secrets{}, fmt.Errorf("read secrets %s: %w", path, err)
	}
	return sec, nil
}

func envOr(key, current string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return current
}

func applyConfigEnv(in Config) Config {
	out := in
	out.Services.Sonarr.URL = envOr("HEALARR_SONARR_URL", in.Services.Sonarr.URL)
	out.Services.Radarr.URL = envOr("HEALARR_RADARR_URL", in.Services.Radarr.URL)
	out.Services.Prowlarr.URL = envOr("HEALARR_PROWLARR_URL", in.Services.Prowlarr.URL)
	out.Services.Overseerr.URL = envOr("HEALARR_OVERSEERR_URL", in.Services.Overseerr.URL)
	out.Services.QBittorrent.URL = envOr("HEALARR_QBITTORRENT_URL", in.Services.QBittorrent.URL)
	out.Services.Plex.URL = envOr("HEALARR_PLEX_URL", in.Services.Plex.URL)
	out.Services.Tautulli.URL = envOr("HEALARR_TAUTULLI_URL", in.Services.Tautulli.URL)
	out.State.DBPath = envOr("HEALARR_STATE_DB", in.State.DBPath)
	out.Peer.PeerURL = envOr("HEALARR_PEER_URL", in.Peer.PeerURL)
	return out
}

func applySecretsEnv(in Secrets) Secrets {
	out := in
	out.SonarrAPIKey = envOr("HEALARR_SONARR_API_KEY", in.SonarrAPIKey)
	out.RadarrAPIKey = envOr("HEALARR_RADARR_API_KEY", in.RadarrAPIKey)
	out.ProwlarrAPIKey = envOr("HEALARR_PROWLARR_API_KEY", in.ProwlarrAPIKey)
	out.OverseerrAPIKey = envOr("HEALARR_OVERSEERR_API_KEY", in.OverseerrAPIKey)
	out.QBitUser = envOr("HEALARR_QBIT_USER", in.QBitUser)
	out.QBitPass = envOr("HEALARR_QBIT_PASS", in.QBitPass)
	out.PlexToken = envOr("HEALARR_PLEX_TOKEN", in.PlexToken)
	out.TautulliAPIKey = envOr("HEALARR_TAUTULLI_API_KEY", in.TautulliAPIKey)
	out.PeerToken = envOr("HEALARR_PEER_TOKEN", in.PeerToken)
	out.WebToken = envOr("HEALARR_WEB_TOKEN", in.WebToken)
	out.AnthropicAPIKey = envOr("ANTHROPIC_API_KEY", in.AnthropicAPIKey)
	return out
}

// resolveArrKeys fills empty *arr keys from their config.xml when a path is configured.
func resolveArrKeys(cfg Config, in Secrets) (Secrets, error) {
	out := in
	type slot struct {
		file string
		key  *string
		name string
	}
	slots := []slot{
		{cfg.Services.Sonarr.APIKeyFile, &out.SonarrAPIKey, "sonarr"},
		{cfg.Services.Radarr.APIKeyFile, &out.RadarrAPIKey, "radarr"},
		{cfg.Services.Prowlarr.APIKeyFile, &out.ProwlarrAPIKey, "prowlarr"},
	}
	for _, s := range slots {
		if *s.key != "" || s.file == "" {
			continue
		}
		k, err := ReadArrAPIKey(s.file)
		if err != nil {
			return Secrets{}, fmt.Errorf("%s api key: %w", s.name, err)
		}
		*s.key = k
	}
	return out, nil
}

// maxPercent is the upper bound of a disk-usage threshold: the percentages
// in [checks] describe a share of one filesystem, so nothing above this is
// reachable.
const maxPercent = 100

func validate(cfg Config) error {
	if err := validateNode(cfg.Node); err != nil {
		return err
	}
	if err := validateChecks(cfg.Checks); err != nil {
		return err
	}
	if err := validateAgent(cfg.Agent); err != nil {
		return err
	}
	if err := validateStaleness(cfg.Staleness); err != nil {
		return err
	}
	if err := validateCleanup(cfg.Cleanup); err != nil {
		return err
	}
	return validateEmailDigestAt(cfg.Email.DigestAt)
}

// hhmmPattern matches a local time-of-day in 24h "HH:MM" form, shared by
// agent.checkpoint_at and email.digest_at.
var hhmmPattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// validateAgent rejects [agent] values the daemon's scheduler could not
// act on: an unloadable timezone or malformed checkpoint_at would only
// surface at the first missed run, a non-positive heartbeat_interval
// would mean the ticker never fires, a non-positive peer_stale_after
// would make every peer heartbeat look stale immediately (zero) or never
// stale at all (negative), and a non-positive peer_message_retention would
// have the nightly prune delete the whole peer_messages log.
func validateAgent(a Agent) error {
	if a.Timezone != "" {
		if _, err := time.LoadLocation(a.Timezone); err != nil {
			return fmt.Errorf("config: agent.timezone %q is not a valid IANA timezone: %w", a.Timezone, err)
		}
	}
	if a.HeartbeatInterval <= 0 {
		return fmt.Errorf("config: agent.heartbeat_interval must be > 0 (got %s)", a.HeartbeatInterval)
	}
	if !hhmmPattern.MatchString(a.CheckpointAt) {
		return fmt.Errorf("config: agent.checkpoint_at must be \"HH:MM\" (got %q)", a.CheckpointAt)
	}
	if a.PeerStaleAfter <= 0 {
		return fmt.Errorf("config: agent.peer_stale_after must be > 0 (got %s)", a.PeerStaleAfter)
	}
	if a.PeerMessageRetention <= 0 {
		return fmt.Errorf("config: agent.peer_message_retention must be > 0 (got %s)", a.PeerMessageRetention)
	}
	return nil
}

// validateEmailDigestAt rejects an email.digest_at that isn't a "HH:MM"
// local time, so a typo is caught at load time rather than at 07:00 when
// the digest silently never fires.
func validateEmailDigestAt(digestAt string) error {
	if !hhmmPattern.MatchString(digestAt) {
		return fmt.Errorf("config: email.digest_at must be \"HH:MM\" (got %q)", digestAt)
	}
	return nil
}

func validateNode(node Node) error {
	switch node {
	case NodePi, NodeNAS:
		return nil
	case "":
		return errors.New("config: node is required (\"pi\" or \"nas\")")
	default:
		return fmt.Errorf("config: unknown node %q (want \"pi\" or \"nas\")", node)
	}
}

// durationField pairs one [checks] duration with the TOML key it was read
// from, so a rejection can name the line the operator has to fix.
type durationField struct {
	key string
	d   time.Duration
}

// checkDurations lists every duration in Checks against its TOML key.
// Adding a duration to Checks means adding it here;
// TestValidateRejectsEveryNegativeChecksDuration walks the struct by
// reflection and fails if one is missed.
func checkDurations(c Checks) []durationField {
	return []durationField{
		{"timeout", c.Timeout},
		{"queue_stuck_after", c.QueueStuckAfter},
		{"qbit_stalled_after", c.QBitStalledAfter},
		{"completed_not_imported_after", c.CompletedNotImportedAfter},
		{"arr_history_window", c.ArrHistoryWindow},
		{"orphan_after", c.OrphanAfter},
		{"indexer_failure_window", c.IndexerFailureWindow},
		{"overseerr_stuck_after", c.OverseerrStuckAfter},
		{"plex_scan_stale_after", c.PlexScanStaleAfter},
		{"seeded_min_age", c.SeededMinAge},
	}
}

// validateChecks rejects thresholds the check engine could not act on, at
// load time rather than at 07:00 when the digest comes out wrong: a
// non-positive per-check timeout would fail every check instantly, a
// negative duration is always a typo (every one of them is an age or a
// window measured forward from an event), and disk thresholds only mean
// anything as an ordered pair inside 0–100.
func validateChecks(c Checks) error {
	if c.Timeout <= 0 {
		return fmt.Errorf("config: checks.timeout must be > 0 (got %s)", c.Timeout)
	}
	for _, f := range checkDurations(c) {
		if f.d < 0 {
			return fmt.Errorf("config: checks.%s must be >= 0 (got %s)", f.key, f.d)
		}
	}
	switch {
	case c.DiskWarnPercent <= 0:
		return fmt.Errorf("config: checks.disk_warn_percent must be > 0 (got %v)", c.DiskWarnPercent)
	case c.DiskCritPercent < c.DiskWarnPercent:
		return fmt.Errorf("config: checks.disk_crit_percent must be >= checks.disk_warn_percent (got %v < %v)",
			c.DiskCritPercent, c.DiskWarnPercent)
	case c.DiskCritPercent > maxPercent:
		return fmt.Errorf("config: checks.disk_crit_percent must be <= %d (got %v)", maxPercent, c.DiskCritPercent)
	}
	return nil
}

// stalenessFloatField pairs one [staleness] float64 value (a point weight
// or a threshold) with the TOML key it was read from, so a rejection can
// name the line to fix.
type stalenessFloatField struct {
	key string
	v   float64
}

// stalenessFloats lists every float64 field in Staleness against its TOML
// key. Adding a field to Staleness means adding it here;
// TestValidateRejectsEveryNonFiniteStalenessFloat walks the struct by
// reflection and fails if one is missed.
func stalenessFloats(s Staleness) []stalenessFloatField {
	return []stalenessFloatField{
		{"days_max_points", s.DaysMaxPoints},
		{"watched_full_points", s.WatchedFullPoints},
		{"watched_partial_points", s.WatchedPartialPoints},
		{"ended_points", s.EndedPoints},
		{"continuing_points", s.ContinuingPoints},
		{"size_points_per_gb", s.SizePointsPerGB},
		{"size_max_points", s.SizeMaxPoints},
		{"other_requester_points", s.OtherRequesterPoints},
		{"owner_requester_points", s.OwnerRequesterPoints},
		{"age_points_per_day", s.AgePointsPerDay},
		{"age_max_points", s.AgeMaxPoints},
		{"candidate_threshold", s.CandidateThreshold},
		{"watchlist_threshold", s.WatchlistThreshold},
	}
}

// validateStaleness rejects [staleness] values the C6 scorer could not act
// on: TOML's nan/inf float literals decode cleanly, so a non-finite point
// weight or threshold is checked explicitly rather than trusted; a
// non-positive days_horizon would make the days-since-added component
// divide by zero (or flip sign on a negative horizon); a negative
// never_watched_after_days is always a typo (it's compared against a
// count of days since a title was added); the two thresholds only mean
// anything as an ordered pair inside (0, 100] (a candidate must first
// clear the watchlist bar); and a non-positive snooze_days would mean a
// snoozed title never resurfaces (<= 0 leaves it permanently — or,
// negative, immediately — eligible again).
func validateStaleness(s Staleness) error {
	for _, f := range stalenessFloats(s) {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) {
			return fmt.Errorf("config: staleness.%s must be finite (got %v)", f.key, f.v)
		}
	}
	if s.DaysHorizon <= 0 {
		return fmt.Errorf("config: staleness.days_horizon must be > 0 (got %d)", s.DaysHorizon)
	}
	if s.NeverWatchedAfterDays < 0 {
		return fmt.Errorf("config: staleness.never_watched_after_days must be >= 0 (got %d)", s.NeverWatchedAfterDays)
	}
	switch {
	case s.WatchlistThreshold <= 0:
		return fmt.Errorf("config: staleness.watchlist_threshold must be > 0 (got %v)", s.WatchlistThreshold)
	case s.CandidateThreshold <= s.WatchlistThreshold:
		return fmt.Errorf("config: staleness.candidate_threshold must be > staleness.watchlist_threshold (got %v <= %v)",
			s.CandidateThreshold, s.WatchlistThreshold)
	case s.CandidateThreshold > maxPercent:
		return fmt.Errorf("config: staleness.candidate_threshold must be <= %d (got %v)", maxPercent, s.CandidateThreshold)
	}
	if s.SnoozeDays <= 0 {
		return fmt.Errorf("config: staleness.snooze_days must be > 0 (got %d)", s.SnoozeDays)
	}
	return nil
}

// cleanupDurations lists every duration in Cleanup against its TOML key,
// mirroring checkDurations for Checks.
func cleanupDurations(c Cleanup) []durationField {
	return []durationField{
		{"orphan_min_age", c.OrphanMinAge},
		{"recycle_min_age", c.RecycleMinAge},
	}
}

// validateCleanup rejects [cleanup] durations the executor could not act
// on: every one is a minimum age measured forward from an event, so
// negative is always a typo. dry_run and docker_dangling are plain bools —
// every value is valid, and dry_run's safety comes from its default
// (true), not from validation here.
func validateCleanup(c Cleanup) error {
	for _, f := range cleanupDurations(c) {
		if f.d < 0 {
			return fmt.Errorf("config: cleanup.%s must be >= 0 (got %s)", f.key, f.d)
		}
	}
	return nil
}
