package web

// Unit tests in this file exercise package-internal behaviour directly
// (this file is part of package web, not web_test) where doing so is
// clearer or more direct than driving it through the full HTTP stack —
// session expiry, template render failures, and the CSRF derivation
// itself.

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionTableRejectsExpiredSession(t *testing.T) {
	st := newSessionTable()
	id, err := st.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !st.valid(id) {
		t.Fatal("freshly created session should be valid")
	}

	// Force it into the past, as if sessionTTL had elapsed.
	st.mu.Lock()
	st.byID[id] = time.Now().Add(-time.Second)
	st.mu.Unlock()

	if st.valid(id) {
		t.Fatal("expired session should not be valid")
	}
	// valid() lazily evicts; confirm it's actually gone.
	st.mu.RLock()
	_, stillThere := st.byID[id]
	st.mu.RUnlock()
	if stillThere {
		t.Fatal("expired session should have been evicted from the table")
	}
}

func TestSessionTableRejectsUnknownID(t *testing.T) {
	st := newSessionTable()
	if st.valid("does-not-exist") {
		t.Fatal("unknown session id should not be valid")
	}
}

func TestCSRFTokenIsStableForSameSessionAndDiffersAcrossSessions(t *testing.T) {
	s := &server{csrfKey: []byte("test-key-not-random-but-fine-here")}
	t1 := s.csrfToken("session-a")
	t2 := s.csrfToken("session-a")
	t3 := s.csrfToken("session-b")

	if t1 != t2 {
		t.Errorf("csrfToken should be deterministic for the same session id: %q != %q", t1, t2)
	}
	if t1 == t3 {
		t.Errorf("csrfToken should differ across session ids")
	}
	if !s.validCSRF(session{ID: "session-a"}, t1) {
		t.Error("validCSRF should accept the token derived from the same session")
	}
	if s.validCSRF(session{ID: "session-a"}, "wrong") {
		t.Error("validCSRF should reject a mismatched token")
	}
}

func TestRootPathFallsBackToSlashWhenBasePathIsRoot(t *testing.T) {
	s := &server{basePath: ""}
	if got := s.rootPath(); got != "/" {
		t.Errorf("rootPath() = %q, want \"/\"", got)
	}
}

func TestRenderTemplateExecutionErrorReturns500AndLogs(t *testing.T) {
	var logBuf bytes.Buffer
	tmpl, err := parseTemplates()
	if err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	s := &server{
		tmpl:   tmpl,
		logger: slog.New(slog.NewTextHandler(&logBuf, nil)),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healarr/", nil)
	// "does-not-exist" was never defined by any template file, so
	// ExecuteTemplate fails, exercising render's error path.
	s.render(w, r, "does-not-exist", nil)

	if w.Code != 500 {
		t.Errorf("render with a missing template = %d, want 500", w.Code)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("render template")) {
		t.Errorf("expected render error to be logged, got: %s", logBuf.String())
	}
}

func TestAgoRendersCoarseRelativeTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero value handled by caller, not ago itself", now, "just now"},
		{"30 seconds", now.Add(-30 * time.Second), "just now"},
		{"future (clock skew)", now.Add(30 * time.Second), "just now"},
		{"1 minute", now.Add(-1 * time.Minute), "1 minute ago"},
		{"5 minutes", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour", now.Add(-1 * time.Hour), "1 hour ago"},
		{"3 hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day", now.Add(-24 * time.Hour), "1 day ago"},
		{"5 days", now.Add(-5 * 24 * time.Hour), "5 days ago"},
		{"1 month", now.Add(-30 * 24 * time.Hour), "1 month ago"},
		{"2 months", now.Add(-61 * 24 * time.Hour), "2 months ago"},
		{"1 year", now.Add(-366 * 24 * time.Hour), "1 year ago"},
		{"2 years", now.Add(-731 * 24 * time.Hour), "2 years ago"},
	}
	for _, c := range cases {
		if got := ago(c.t); got != c.want {
			t.Errorf("%s: ago(t) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestHumanizeBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{-5, "0 B"},
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{12_000_000_000, "11.2 GiB"},
	}
	for _, c := range cases {
		if got := humanizeBytes(c.n); got != c.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "day"); got != "1 day" {
		t.Errorf("plural(1, day) = %q, want %q", got, "1 day")
	}
	if got := plural(2, "day"); got != "2 days" {
		t.Errorf("plural(2, day) = %q, want %q", got, "2 days")
	}
	if got := plural(0, "day"); got != "0 days" {
		t.Errorf("plural(0, day) = %q, want %q", got, "0 days")
	}
}

func TestFlashMessageUnknownCodeIsEmpty(t *testing.T) {
	if got := flashMessage("some-unrecognised-code"); got != "" {
		t.Errorf("flashMessage(unknown) = %q, want empty", got)
	}
	if got := flashMessage(""); got != "" {
		t.Errorf("flashMessage(\"\") = %q, want empty", got)
	}
}
