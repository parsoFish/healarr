package config

// Defaults returns the baseline configuration; Load overlays the file on it.
func Defaults() Config {
	return Config{
		Peer:   Peer{ListenAddr: "0.0.0.0:8090"},
		Web:    Web{ListenAddr: "0.0.0.0:8091", BasePath: "/healarr"},
		Email:  Email{MsmtpPath: "/usr/bin/msmtp"},
		LLM:    LLM{Enabled: true, Model: "claude-haiku-4-5-20251001", DailyBudgetUSD: 1.0},
		State:  State{DBPath: "/var/lib/healarr/state.db"},
		Docker: Docker{Socket: "/var/run/docker.sock", Binary: "docker"},
	}
}
