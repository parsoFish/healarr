package notify

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Sender delivers one plain-text email.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// MsmtpSender shells out to msmtp to deliver the digest. Runner defaults
// to exec.CommandContext (via NewMsmtpSender); tests inject a fake so
// Send can be exercised without a real MTA or network access. Only the
// Pi node ever constructs one (constraints.md: "the NAS never calls the
// sender").
type MsmtpSender struct {
	Path   string
	From   string
	Runner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewMsmtpSender returns an MsmtpSender wired to the real exec.CommandContext.
func NewMsmtpSender(path, from string) *MsmtpSender {
	return &MsmtpSender{Path: path, From: from, Runner: exec.CommandContext}
}

// Send builds the RFC-5322-style message and pipes it to
// `<path> --read-envelope-from -t` on stdin. A non-zero exit is wrapped
// into an error that includes msmtp's trimmed stderr, so the caller (and
// its logs) see the actual delivery failure.
func (m *MsmtpSender) Send(ctx context.Context, to, subject, body string) error {
	msg := buildMessage(m.From, to, subject, body, time.Now())

	cmd := m.Runner(ctx, m.Path, "--read-envelope-from", "-t")
	cmd.Stdin = strings.NewReader(msg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("notify: send mail: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// buildMessage renders the exact header block msmtp expects on stdin, in
// order: From, To, Subject, Date, MIME-Version, Content-Type, a blank
// line, then the body. Header values are sanitised against CR/LF
// injection before assembly.
func buildMessage(from, to, subject, body string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\n", headerSafe(from))
	fmt.Fprintf(&b, "To: %s\n", headerSafe(to))
	fmt.Fprintf(&b, "Subject: %s\n", headerSafe(subject))
	fmt.Fprintf(&b, "Date: %s\n", now.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\n")
	b.WriteString("\n")
	b.WriteString(body)
	return b.String()
}

// headerSafe strips CR and LF from a header value. Without this, a
// crafted subject/to/from containing "\r\nBcc: ..." could inject an
// arbitrary extra header into the message.
func headerSafe(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}

// Send is one call FakeSender recorded.
type Send struct {
	To, Subject, Body string
}

// FakeSender is an in-memory Sender: it records every Send call and
// returns Err (nil unless the test sets it). It lives in this file
// (not a _test.go one) so other packages — the daemon, its tests, later
// phases — can use it without duplicating it.
type FakeSender struct {
	Sends []Send
	Err   error
}

var _ Sender = (*FakeSender)(nil)

// Send records the call and returns Err.
func (f *FakeSender) Send(_ context.Context, to, subject, body string) error {
	f.Sends = append(f.Sends, Send{To: to, Subject: subject, Body: body})
	return f.Err
}
