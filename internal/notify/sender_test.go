package notify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureRunner returns a Runner that records the name/args it was called
// with and pipes the message it receives on stdin into capturedPath via a
// real `sh -c 'cat > ...'` subprocess — no network, no msmtp binary needed.
func captureRunner(capturedPath string, gotName *string, gotArgs *[]string) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		*gotName = name
		*gotArgs = args
		return exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("cat > %s", capturedPath))
	}
}

// failingRunner returns a Runner whose subprocess exits non-zero after
// writing stderrText to stderr, ignoring whatever was piped to stdin.
func failingRunner(exitCode int, stderrText string) func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		script := fmt.Sprintf("cat > /dev/null; printf '%%s' %s >&2; exit %d", shQuote(stderrText), exitCode)
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
}

// shQuote wraps s in single quotes for use inside a `sh -c` script,
// escaping any single quotes already present.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func headerLines(t *testing.T, msg string) []string {
	t.Helper()
	lines := strings.Split(msg, "\n")
	var headers []string
	for _, l := range lines {
		if l == "" {
			break
		}
		headers = append(headers, l)
	}
	return headers
}

// assertHeader fails the test unless headers[i] equals want.
func assertHeader(t *testing.T, headers []string, i int, name, want string) {
	t.Helper()
	if headers[i] != want {
		t.Fatalf("%s header: %q, want %q", name, headers[i], want)
	}
}

func TestMsmtpSenderSendBuildsExactMessageAndArgs(t *testing.T) {
	dir := t.TempDir()
	capturedPath := filepath.Join(dir, "stdin.txt")
	var gotName string
	var gotArgs []string

	s := &MsmtpSender{
		Path:   "/usr/bin/msmtp",
		From:   "healarr@192.0.2.10",
		Runner: captureRunner(capturedPath, &gotName, &gotArgs),
	}

	err := s.Send(context.Background(), "ops@192.0.2.20", "healarr digest — 2026-09-18", "line one\nline two\n")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotName != "/usr/bin/msmtp" {
		t.Fatalf("runner invoked with name %q, want /usr/bin/msmtp", gotName)
	}
	wantArgs := []string{"--read-envelope-from", "-t"}
	if len(gotArgs) != len(wantArgs) || gotArgs[0] != wantArgs[0] || gotArgs[1] != wantArgs[1] {
		t.Fatalf("runner invoked with args %v, want %v", gotArgs, wantArgs)
	}

	raw, err := os.ReadFile(capturedPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	msg := string(raw)

	headers := headerLines(t, msg)
	if len(headers) != 6 {
		t.Fatalf("expected 6 header lines, got %d: %v", len(headers), headers)
	}
	assertHeader(t, headers, 0, "From", "From: healarr@192.0.2.10")
	assertHeader(t, headers, 1, "To", "To: ops@192.0.2.20")
	assertHeader(t, headers, 2, "Subject", "Subject: healarr digest — 2026-09-18")
	if !strings.HasPrefix(headers[3], "Date: ") {
		t.Fatalf("Date header: %q", headers[3])
	}
	assertHeader(t, headers, 4, "MIME-Version", "MIME-Version: 1.0")
	assertHeader(t, headers, 5, "Content-Type", "Content-Type: text/plain; charset=utf-8")
	if !strings.HasSuffix(msg, "line one\nline two\n") {
		t.Fatalf("body not appended after blank line: %q", msg)
	}
	if !strings.Contains(msg, "\n\nline one") {
		t.Fatalf("expected exactly one blank line before body: %q", msg)
	}
}

func TestMsmtpSenderSendNonZeroExitWrapsStderr(t *testing.T) {
	s := &MsmtpSender{
		Path:   "/usr/bin/msmtp",
		From:   "healarr@192.0.2.10",
		Runner: failingRunner(3, "550 relay access denied"),
	}

	err := s.Send(context.Background(), "ops@192.0.2.20", "subject", "body")
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "550 relay access denied") {
		t.Fatalf("error does not include stderr text: %v", err)
	}
}

func TestMsmtpSenderSendStripsCRLFFromHeaders(t *testing.T) {
	dir := t.TempDir()
	capturedPath := filepath.Join(dir, "stdin.txt")
	var gotName string
	var gotArgs []string

	s := &MsmtpSender{
		Path:   "/usr/bin/msmtp",
		From:   "healarr@192.0.2.10\r\nBcc: attacker@192.0.2.99",
		Runner: captureRunner(capturedPath, &gotName, &gotArgs),
	}

	err := s.Send(context.Background(), "victim@192.0.2.20\r\nBcc: attacker@192.0.2.99",
		"Subject line\r\nBcc: attacker@192.0.2.99", "body")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	raw, err := os.ReadFile(capturedPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	msg := string(raw)

	headers := headerLines(t, msg)
	if len(headers) != 6 {
		t.Fatalf("CR/LF injection produced extra header lines: got %d: %v", len(headers), headers)
	}
	if headers[0] != "From: healarr@192.0.2.10Bcc: attacker@192.0.2.99" {
		t.Fatalf("From header not sanitised: %q", headers[0])
	}
	if headers[1] != "To: victim@192.0.2.20Bcc: attacker@192.0.2.99" {
		t.Fatalf("To header not sanitised: %q", headers[1])
	}
	if headers[2] != "Subject: Subject lineBcc: attacker@192.0.2.99" {
		t.Fatalf("Subject header not sanitised: %q", headers[2])
	}
	if strings.Contains(msg, "\r") {
		t.Fatalf("message still contains a bare CR: %q", msg)
	}
}

func TestFakeSenderRecordsSendsAndReturnsErr(t *testing.T) {
	fake := &FakeSender{}
	if err := fake.Send(context.Background(), "ops@192.0.2.20", "subj", "body"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(fake.Sends) != 1 {
		t.Fatalf("expected 1 recorded send, got %d", len(fake.Sends))
	}
	got := fake.Sends[0]
	if got.To != "ops@192.0.2.20" || got.Subject != "subj" || got.Body != "body" {
		t.Fatalf("recorded send mismatch: %+v", got)
	}

	fake.Err = fmt.Errorf("boom")
	if err := fake.Send(context.Background(), "ops@192.0.2.20", "subj2", "body2"); err == nil {
		t.Fatal("expected Err to be returned")
	}
	if len(fake.Sends) != 2 {
		t.Fatalf("expected send to still be recorded on error, got %d", len(fake.Sends))
	}
}

func TestNewMsmtpSenderDefaultsRunner(t *testing.T) {
	s := NewMsmtpSender("/usr/bin/msmtp", "healarr@192.0.2.10")
	if s.Path != "/usr/bin/msmtp" || s.From != "healarr@192.0.2.10" {
		t.Fatalf("fields not set: %+v", s)
	}
	if s.Runner == nil {
		t.Fatal("expected a default Runner")
	}
}
