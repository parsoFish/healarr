package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
)

func TestDefaultSender(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.Config
		wantNil bool
	}{
		{"nas node never sends, even with a recipient", config.Config{Node: config.NodeNAS, Email: config.Email{To: "ops@example.test"}}, true},
		{"pi node with no recipient has nothing to send to", config.Config{Node: config.NodePi, Email: config.Email{To: ""}}, true},
		{"pi node with a recipient sends", config.Config{Node: config.NodePi, Email: config.Email{To: "ops@example.test", MsmtpPath: "/usr/bin/msmtp", From: "healarr@example.test"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultSender(tc.cfg)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("defaultSender(%+v) = %v, want nil", tc.cfg, got)
				}
				return
			}
			sender, ok := got.(*notify.MsmtpSender)
			if !ok {
				t.Fatalf("defaultSender(%+v) = %T, want *notify.MsmtpSender", tc.cfg, got)
			}
			if sender.Path != tc.cfg.Email.MsmtpPath || sender.From != tc.cfg.Email.From {
				t.Errorf("sender = %+v, want Path=%q From=%q", sender, tc.cfg.Email.MsmtpPath, tc.cfg.Email.From)
			}
		})
	}
}

func TestDefaultPeerClient(t *testing.T) {
	t.Run("empty peer_url returns nil, nil", func(t *testing.T) {
		c, err := defaultPeerClient(config.Config{}, config.Secrets{})
		if c != nil || err != nil {
			t.Fatalf("defaultPeerClient = %v, %v, want nil, nil", c, err)
		}
	})
	t.Run("configured peer_url builds a client", func(t *testing.T) {
		cfg := config.Config{Peer: config.Peer{PeerURL: "http://192.0.2.10:8090"}}
		c, err := defaultPeerClient(cfg, config.Secrets{PeerToken: "tok"})
		if err != nil {
			t.Fatalf("defaultPeerClient err = %v", err)
		}
		if c == nil {
			t.Fatal("defaultPeerClient client = nil, want a real client")
		}
	})
	t.Run("invalid peer_url errors", func(t *testing.T) {
		cfg := config.Config{Peer: config.Peer{PeerURL: "not-a-valid-url"}}
		if _, err := defaultPeerClient(cfg, config.Secrets{}); err == nil {
			t.Fatal("expected an error for an invalid peer_url")
		}
	})
}

func TestDefaultOpenAgentStoreOpensAndCloses(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	st, err := defaultOpenAgentStore(context.Background(), config.Config{State: config.State{DBPath: dbPath}})
	if err != nil {
		t.Fatalf("defaultOpenAgentStore err = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close err = %v", err)
	}
}

func TestDefaultOpenAgentStoreMissingDirErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "no-such-dir", "state.db")
	if _, err := defaultOpenAgentStore(context.Background(), config.Config{State: config.State{DBPath: dbPath}}); err == nil {
		t.Fatal("expected an error opening a store whose parent directory doesn't exist")
	}
}
