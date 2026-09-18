package config

import "time"

// Defaults returns the baseline configuration; Load overlays the file on it.
func Defaults() Config {
	return Config{
		Peer:   Peer{ListenAddr: "0.0.0.0:8090"},
		Web:    Web{ListenAddr: "0.0.0.0:8091", BasePath: "/healarr"},
		Email:  Email{MsmtpPath: "/usr/bin/msmtp"},
		LLM:    LLM{Enabled: true, Model: "claude-haiku-4-5-20251001", DailyBudgetUSD: 1.0},
		State:  State{DBPath: "/var/lib/healarr/state.db"},
		Docker: Docker{Socket: "/var/run/docker.sock", Binary: "docker"},
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
	}
}
