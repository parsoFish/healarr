package web

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testConfig returns a fixture config.Config with real staleness
// thresholds (config.Defaults()'s, not a bare zero-value Config) so
// tests that render candidate/watchlist bands actually exercise
// staleness.Band's threshold logic instead of both bands collapsing to
// "candidate" at the zero-value (0 >= 0).
func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Web = config.Web{ListenAddr: "127.0.0.1:0", BasePath: "/healarr"}
	cfg.Actions = config.Actions{Enabled: false}
	return cfg
}

func TestNewRejectsEmptyToken(t *testing.T) {
	_, err := New(testConfig(), "", &fakeStore{}, &fakeRunner{}, testLogger())
	if err == nil {
		t.Fatal("New: want error for empty token")
	}
}

func TestNewRejectsNilStore(t *testing.T) {
	_, err := New(testConfig(), "tok", nil, &fakeRunner{}, testLogger())
	if err == nil {
		t.Fatal("New: want error for nil store")
	}
}

func TestNewRejectsNilRunner(t *testing.T) {
	_, err := New(testConfig(), "tok", &fakeStore{}, nil, testLogger())
	if err == nil {
		t.Fatal("New: want error for nil runner")
	}
}

func TestNewAcceptsNilLoggerAndUsesDefault(t *testing.T) {
	h, err := New(testConfig(), "tok", &fakeStore{}, &fakeRunner{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if h == nil {
		t.Fatal("New: want non-nil handler")
	}
}

func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{
		"":              "/healarr",
		"/":             "",
		"/healarr":      "/healarr",
		"/healarr/":     "/healarr",
		"healarr":       "/healarr",
		"/sub/healarr/": "/sub/healarr",
	}
	for in, want := range cases {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewHTTPServerSetsNonZeroTimeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	for name, got := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if got <= 0 {
			t.Errorf("%s = %v, want a positive timeout (never the zero-value default)", name, got)
		}
	}
}

func TestEmbeddedCSSUnderBudget(t *testing.T) {
	if len(cssBytes) > maxCSSBytes {
		t.Fatalf("static.css is %d bytes, want <= %d (constraints.md: inline CSS <= 20 KB)", len(cssBytes), maxCSSBytes)
	}
}

func TestListenAndServeStopsOnContextCancel(t *testing.T) {
	h, err := New(testConfig(), "tok", &fakeStore{}, &fakeRunner{}, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ListenAndServe(ctx, "127.0.0.1:0", h) }()

	// Give the listener a moment to bind before cancelling, so this
	// exercises a real running server rather than an already-cancelled
	// context racing the bind.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not stop within timeout")
	}
}

func TestListenAndServeReturnsErrorForInvalidAddr(t *testing.T) {
	h, err := New(testConfig(), "tok", &fakeStore{}, &fakeRunner{}, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ListenAndServe(context.Background(), "not-a-valid-address", h); err == nil {
		t.Fatal("ListenAndServe: want error for invalid address")
	}
}

func TestServeSurfacesUnexpectedListenerError(t *testing.T) {
	h, err := New(testConfig(), "tok", &fakeStore{}, &fakeRunner{}, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), ln, h) }()

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from the closed listener")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after its listener was closed")
	}
}
