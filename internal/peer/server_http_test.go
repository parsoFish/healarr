package peer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/parsoFish/healarr/internal/config"
)

// rawRequest sends a raw HTTP request directly (bypassing the peer.Client),
// so tests can exercise wire-level behaviour a well-formed client never
// triggers: missing/wrong auth, wrong method, malformed JSON, oversized
// bodies.
func rawRequest(t *testing.T, srv *httptest.Server, method, path, token string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func newRawServer(t *testing.T, fh *FakeHandler, token string) *httptest.Server {
	t.Helper()
	h, err := NewServer(token, fh, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func allRoutes() []struct{ method, path string } {
	return []struct{ method, path string }{
		{http.MethodPost, reportPath},
		{http.MethodGet, latestReportPath},
		{http.MethodPost, decisionPath},
		{http.MethodPost, heartbeatPath},
	}
}

func TestMissingTokenReturns401ForEveryRoute(t *testing.T) {
	srv := newRawServer(t, &FakeHandler{}, "tok")
	for _, rt := range allRoutes() {
		resp := rawRequest(t, srv, rt.method, rt.path, "", strings.NewReader(`{}`))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestWrongTokenReturns401ForEveryRoute(t *testing.T) {
	srv := newRawServer(t, &FakeHandler{}, "tok")
	for _, rt := range allRoutes() {
		resp := rawRequest(t, srv, rt.method, rt.path, "wrong", strings.NewReader(`{}`))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestWrongMethodReturns405(t *testing.T) {
	srv := newRawServer(t, &FakeHandler{}, "tok")
	routes := []struct{ method, path string }{
		{http.MethodGet, reportPath},
		{http.MethodPost, latestReportPath},
		{http.MethodGet, decisionPath},
		{http.MethodGet, heartbeatPath},
	}
	for _, rt := range routes {
		resp := rawRequest(t, srv, rt.method, rt.path, "tok", strings.NewReader(`{}`))
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want 405", rt.method, rt.path, resp.StatusCode)
		}
	}
}

func TestBadJSONReturns400(t *testing.T) {
	srv := newRawServer(t, &FakeHandler{}, "tok")
	for _, path := range []string{reportPath, decisionPath, heartbeatPath} {
		resp := rawRequest(t, srv, http.MethodPost, path, "tok", strings.NewReader(`not-json`))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", path, resp.StatusCode)
		}
	}
}

func TestOversizedBodyReturns413(t *testing.T) {
	srv := newRawServer(t, &FakeHandler{}, "tok")
	huge := bytes.Repeat([]byte("a"), (4<<20)+1)
	body := fmt.Sprintf(`{"node":"pi","padding":"%s"}`, huge)
	resp := rawRequest(t, srv, http.MethodPost, reportPath, "tok", strings.NewReader(body))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", resp.StatusCode)
	}
}

func TestServeStopsGracefullyOnContextCancelWithNoInFlightRequests(t *testing.T) {
	h, err := NewServer("tok", &FakeHandler{}, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, h) }()

	c, err := New("http://"+ln.Addr().String(), "tok", WithBackoff(noBackoff))
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	if _, err := c.Heartbeat(context.Background(), Heartbeat{Node: config.NodePi}); err != nil {
		t.Fatalf("Heartbeat before shutdown: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop within timeout")
	}
}

func TestServeWaitsForInFlightRequestBeforeShutdown(t *testing.T) {
	block := make(chan struct{})
	fh := &FakeHandler{ReportBlock: block}
	h, err := NewServer("tok", fh, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- Serve(ctx, ln, h) }()

	c, err := New("http://"+ln.Addr().String(), "tok", WithBackoff(noBackoff))
	if err != nil {
		t.Fatalf("New client: %v", err)
	}

	reqDone := make(chan error, 1)
	go func() {
		_, err := c.PushReport(context.Background(), ReportEnvelope{Node: config.NodePi})
		reqDone <- err
	}()

	// Give the request time to reach the (blocked) handler before starting
	// shutdown, so the test actually exercises the "wait for in-flight
	// request" behaviour rather than an already-idle shutdown.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-serveDone:
		t.Fatal("Serve returned before the in-flight request finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(block)
	if err := <-reqDone; err != nil {
		t.Fatalf("PushReport: %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after in-flight request finished")
	}
}

func TestListenAndServeStopsOnAlreadyCancelledContext(t *testing.T) {
	h, err := NewServer("tok", &FakeHandler{}, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- ListenAndServe(ctx, "127.0.0.1:0", h) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListenAndServe did not stop within timeout")
	}
}

func TestServeSurfacesUnexpectedListenerErrorWithoutCtxCancel(t *testing.T) {
	h, err := NewServer("tok", &FakeHandler{}, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), ln, h) }()

	// Closing the listener out from under Serve (without ever cancelling
	// its ctx) forces srv.Serve to fail with something other than
	// http.ErrServerClosed, exercising the non-graceful error path.
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

func TestListenAndServeReturnsErrorForInvalidAddr(t *testing.T) {
	h, err := NewServer("tok", &FakeHandler{}, testLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := ListenAndServe(context.Background(), "not-a-valid-address", h); err == nil {
		t.Fatal("expected error for invalid address")
	}
}
