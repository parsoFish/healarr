package config

import "time"

// defaultPeerMessageRetention is how long peer_messages rows are kept
// before the nightly prune removes them: 30 days, long enough to explain
// any digest the operator is still looking at and short enough that the
// table (one row per heartbeat in each direction) stays small on the Pi's
// SD card.
const defaultPeerMessageRetention = 30 * 24 * time.Hour

// Defaults returns the baseline configuration; Load overlays the file on it.
func Defaults() Config {
	return Config{
		Peer:   Peer{ListenAddr: "0.0.0.0:8090"},
		Web:    Web{ListenAddr: "0.0.0.0:8091", BasePath: "/healarr"},
		Email:  Email{MsmtpPath: "/usr/bin/msmtp", DigestAt: "07:00"},
		LLM:    LLM{Enabled: true, Model: "claude-haiku-4-5-20251001", DailyBudgetUSD: 1.0},
		State:  State{DBPath: "/var/lib/healarr/state.db"},
		Docker: Docker{Socket: "/var/run/docker.sock", Binary: "docker"},
		Agent: Agent{
			HeartbeatInterval:    5 * time.Minute,
			CheckpointAt:         "03:00",
			PeerStaleAfter:       15 * time.Minute,
			PeerMessageRetention: defaultPeerMessageRetention,
		},
		Checks: Checks{
			Timeout:                   60 * time.Second,
			DiskPaths:                 []string{"/"},
			DiskWarnPercent:           85,
			DiskCritPercent:           92,
			QueueStuckAfter:           24 * time.Hour,
			QBitStalledAfter:          time.Hour,
			CompletedNotImportedAfter: 30 * time.Minute,
			ArrHistoryWindow:          7 * 24 * time.Hour,
			WrongFileExts:             []string{".exe", ".scr", ".bat", ".lnk", ".msi"},
			TVCategories:              []string{"tv"},
			OrphanAfter:               7 * 24 * time.Hour,
			RecycleWarnGB:             20,
			ImageBloatWarnGB:          2,
			LogDirs:                   []string{"/var/log"},
			LogWarnGB:                 1,
			WantedSpikePercent:        20,
			WantedSpikeMin:            10,
			IndexerFailureWindow:      24 * time.Hour,
			OverseerrStuckAfter:       7 * 24 * time.Hour,
			PlexScanStaleAfter:        2 * time.Hour,
			SeededMinAge:              24 * time.Hour,
		},
		Staleness: Staleness{
			DaysMaxPoints:         40,
			DaysHorizon:           180,
			NeverWatchedAfterDays: 30,
			WatchedFullPoints:     15,
			WatchedPartialPoints:  -10,
			EndedPoints:           10,
			ContinuingPoints:      -15,
			SizePointsPerGB:       0.1,
			SizeMaxPoints:         15,
			OtherRequesterPoints:  10,
			OwnerRequesterPoints:  -5,
			AgePointsPerDay:       0.05,
			AgeMaxPoints:          10,
			CandidateThreshold:    70,
			WatchlistThreshold:    50,
			SnoozeDays:            60,
		},
		Cleanup: Cleanup{
			DryRun:         true,
			OrphanMinAge:   168 * time.Hour,
			RecycleMinAge:  24 * time.Hour,
			DockerDangling: true,
		},
		Actions: Actions{Enabled: false},
	}
}
