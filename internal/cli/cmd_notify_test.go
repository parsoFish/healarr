package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
	"github.com/parsoFish/healarr/internal/notify"
)

func TestNotifyTestSendsEnqueuesAndMarksSent(t *testing.T) {
	deps, fk, p3 := newPhase3Deps(t)

	out := runCLI(t, deps, "notify", "test")

	if len(p3.Sender.Sends) != 1 {
		t.Fatalf("Sender.Sends = %d, want 1", len(p3.Sender.Sends))
	}
	if want := "healarr test email from pi"; p3.Sender.Sends[0].Subject != want {
		t.Errorf("Subject = %q, want %q", p3.Sender.Sends[0].Subject, want)
	}
	if !fk.Store.Opened {
		t.Error("expected notify test to open the store")
	}
	if len(fk.Store.Emails) != 1 {
		t.Fatalf("Emails = %d, want 1 (enqueued)", len(fk.Store.Emails))
	}
	if len(fk.Store.SentEmailIDs) != 1 || fk.Store.SentEmailIDs[0] != 1 {
		t.Errorf("SentEmailIDs = %v, want [1]", fk.Store.SentEmailIDs)
	}
	if !strings.Contains(out, "outbox id: 1") {
		t.Errorf("output = %q, want it to name the outbox id", out)
	}
}

func TestNotifyTestDryRunSendsWithoutRecording(t *testing.T) {
	deps, fk, p3 := newPhase3Deps(t)

	out := runCLI(t, deps, "--dry-run", "notify", "test")

	if len(p3.Sender.Sends) != 1 {
		t.Fatalf("Sender.Sends = %d, want 1", len(p3.Sender.Sends))
	}
	if fk.Store.Opened {
		t.Error("expected --dry-run to never open the store")
	}
	if len(fk.Store.Emails) != 0 {
		t.Errorf("Emails = %d, want 0 (dry-run never enqueues)", len(fk.Store.Emails))
	}
	if !strings.Contains(out, "sent (not recorded)") {
		t.Errorf("output = %q, want %q", out, "sent (not recorded)")
	}
}

func TestNotifyTestSendFailureMarksFailedAndErrors(t *testing.T) {
	deps, fk, p3 := newPhase3Deps(t)
	p3.Sender.Err = errors.New("boom: send failed")

	_, err := runCLIErr(t, deps, "notify", "test")
	if err == nil || !strings.Contains(err.Error(), "send") {
		t.Fatalf("err = %v, want a send error", err)
	}
	if len(fk.Store.FailedEmails) != 1 {
		t.Fatalf("FailedEmails = %d, want 1", len(fk.Store.FailedEmails))
	}
}

func TestNotifyTestNoSenderErrors(t *testing.T) {
	deps, _, _ := newPhase3Deps(t)
	deps.Sender = func(config.Config) notify.Sender { return nil }

	_, err := runCLIErr(t, deps, "notify", "test")
	if err == nil || err.Error() != "email is only configured on the pi node" {
		t.Fatalf("err = %v, want the exact no-sender message", err)
	}
}

// TestNotifyTestNASNodeUsesRealDefaultSenderErrors exercises the actual
// defaultSender (not the phase3 fake override): a NAS node has no sender
// even with a recipient configured, per constraints.md ("the NAS never
// calls the sender").
func TestNotifyTestNASNodeUsesRealDefaultSenderErrors(t *testing.T) {
	deps, _ := newTestDeps() // deps.Sender left nil: falls back to defaultSender
	deps.Load = func(string) (config.Config, config.Secrets, error) {
		return config.Config{Node: config.NodeNAS, Email: config.Email{To: "ops@example.test"}}, config.Secrets{}, nil
	}

	_, err := runCLIErr(t, deps, "notify", "test")
	if err == nil || err.Error() != "email is only configured on the pi node" {
		t.Fatalf("err = %v, want the exact no-sender message", err)
	}
}
