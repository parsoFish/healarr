package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGetJSONSendsHeadersAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" || r.URL.Path != "/api/v3/health" || r.URL.Query().Get("a") != "1" {
			t.Errorf("bad request: %s %s %v", r.Method, r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[{"type":"warning"}]`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, WithHeader("X-Api-Key", "k"))
	if err != nil {
		t.Fatal(err)
	}
	var out []struct{ Type string }
	if err := c.GetJSON(context.Background(), "/api/v3/health", url.Values{"a": {"1"}}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Type != "warning" {
		t.Fatalf("decoded %+v", out)
	}
}

func TestNon2xxBecomesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"nope"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	err := c.GetJSON(context.Background(), "/x", nil, nil)
	if !IsStatus(err, 401) {
		t.Fatalf("want APIError 401, got %v", err)
	}
}

func TestPostFormReturnsBodyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.FormValue("username") != "u" {
			t.Errorf("form not sent")
		}
		if _, err := w.Write([]byte("Ok.")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	var body string
	if err := c.PostForm(context.Background(), "/login", url.Values{"username": {"u"}}, &body); err != nil || body != "Ok." {
		t.Fatalf("got %q %v", body, err)
	}
}

func TestGetTextReturnsBodyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/app/version" {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		if _, err := w.Write([]byte("v5.1.4")); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	var body string
	if err := c.GetText(context.Background(), "/api/v2/app/version", nil, &body); err != nil || body != "v5.1.4" {
		t.Fatalf("got %q %v", body, err)
	}
}

func TestGetTextSurfacesNon2xxAndDiscardsNilOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if err := c.GetText(context.Background(), "/x", nil, nil); !IsStatus(err, http.StatusForbidden) {
		t.Fatalf("want APIError 403, got %v", err)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	if _, err := New("://bad"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWithTLSServerNameClonesDefaultTransportWhenNil(t *testing.T) {
	c, err := New("https://example.com", WithTLSServerName("x"))
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.HTTP().Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected *http.Transport, got %T", c.HTTP().Transport)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.ServerName != "x" {
		t.Fatalf("expected TLSClientConfig.ServerName %q, got %+v", "x", tr.TLSClientConfig)
	}
}

func TestWithTLSServerNamePreservesExistingTransport(t *testing.T) {
	existing := &http.Transport{MaxIdleConns: 7}
	c, err := New("https://example.com", WithHTTPClient(&http.Client{Transport: existing}), WithTLSServerName("y"))
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.HTTP().Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected *http.Transport, got %T", c.HTTP().Transport)
	}
	if tr.MaxIdleConns != 7 {
		t.Fatalf("expected existing transport to be reused, MaxIdleConns=%d", tr.MaxIdleConns)
	}
	if tr.TLSClientConfig == nil || tr.TLSClientConfig.ServerName != "y" {
		t.Fatalf("expected TLSClientConfig.ServerName %q, got %+v", "y", tr.TLSClientConfig)
	}
}

func TestPostJSONSendsBodyAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":1}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	var out struct{ ID int }
	if err := c.PostJSON(context.Background(), "/things", map[string]string{"name": "x"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != 1 {
		t.Fatalf("decoded %+v", out)
	}
}

func TestPutJSONSendsBodyAndDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":2}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	var out struct{ ID int }
	if err := c.PutJSON(context.Background(), "/things/2", map[string]string{"name": "y"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != 2 {
		t.Fatalf("decoded %+v", out)
	}
}

func TestDeleteDiscardsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Query().Get("force") != "1" {
			t.Errorf("bad request: %s %v", r.Method, r.URL.Query())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if err := c.Delete(context.Background(), "/things/1", url.Values{"force": {"1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIErrorMessageTruncatesLongBody(t *testing.T) {
	err := &APIError{Status: 500, Method: http.MethodGet, URL: "http://x/y", Body: strings.Repeat("a", 300)}
	msg := err.Error()
	if !strings.Contains(msg, "HTTP 500") || !strings.Contains(msg, "…") {
		t.Fatalf("expected truncated 500 message, got %q", msg)
	}
	if len(msg) > 260 {
		t.Fatalf("expected message to be truncated, got %d chars", len(msg))
	}
}

func TestGetJSONDecodeErrorWraps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`not-json`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	var out struct{ ID int }
	err := c.GetJSON(context.Background(), "/bad", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestSendJSONEncodeErrorWraps(t *testing.T) {
	c, _ := New("http://example.com")
	err := c.PostJSON(context.Background(), "/x", make(chan int), nil)
	if err == nil || !strings.Contains(err.Error(), "encode body") {
		t.Fatalf("expected encode error, got %v", err)
	}
}

func TestDoNetworkErrorWraps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	c, _ := New(srv.URL)
	srv.Close() // connection now refused
	err := c.GetJSON(context.Background(), "/x", nil, nil)
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestWithTimeoutSetsHTTPClientTimeout(t *testing.T) {
	c, err := New("http://example.com", WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP().Timeout != 5*time.Second {
		t.Fatalf("expected 5s timeout, got %v", c.HTTP().Timeout)
	}
}

func TestBaseURLReturnsConfiguredURL(t *testing.T) {
	c, err := New("http://example.com/base")
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL() != "http://example.com/base" {
		t.Fatalf("got %q", c.BaseURL())
	}
}

func TestWithHeaderAcceptOverridesDefault(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, WithHeader("Accept", "text/xml"))
	if err := c.GetJSON(context.Background(), "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotAccept != "text/xml" {
		t.Fatalf("expected caller's Accept header to win, got %q", gotAccept)
	}
}

func TestDefaultAcceptHeaderSentWhenNotOverridden(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
	}))
	defer srv.Close()
	c, _ := New(srv.URL)
	if err := c.GetJSON(context.Background(), "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotAccept != "application/json, text/plain;q=0.9, */*;q=0.5" {
		t.Fatalf("expected default Accept header, got %q", gotAccept)
	}
}

func TestWithTimeoutThenWithHTTPClientStillAppliesTimeout(t *testing.T) {
	custom := &http.Client{}
	c, err := New("http://example.com", WithTimeout(3*time.Second), WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP().Timeout != 3*time.Second {
		t.Fatalf("expected 3s timeout applied after WithHTTPClient, got %v", c.HTTP().Timeout)
	}
	if custom.Timeout != 0 {
		t.Fatalf("caller's original http.Client must not be mutated, got Timeout=%v", custom.Timeout)
	}
}

func TestWithHTTPClientThenWithTimeoutAppliesTimeoutAndCopiesClient(t *testing.T) {
	custom := &http.Client{Timeout: 99 * time.Second}
	c, err := New("http://example.com", WithHTTPClient(custom), WithTimeout(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP().Timeout != 3*time.Second {
		t.Fatalf("expected 3s timeout, got %v", c.HTTP().Timeout)
	}
	if custom.Timeout != 99*time.Second {
		t.Fatalf("caller's original http.Client must not be mutated, got Timeout=%v", custom.Timeout)
	}
	if c.HTTP() == custom {
		t.Fatal("expected WithHTTPClient to store a copy, not the caller's pointer")
	}
}

func TestWithHTTPClientAloneCopiesCallerClientWithoutMutating(t *testing.T) {
	custom := &http.Client{Timeout: 42 * time.Second}
	c, err := New("http://example.com", WithHTTPClient(custom))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP() == custom {
		t.Fatal("expected a copy, got the caller's own pointer")
	}
	if c.HTTP().Timeout != 42*time.Second {
		t.Fatalf("expected copied Timeout 42s, got %v", c.HTTP().Timeout)
	}
	c.HTTP().Timeout = 1 * time.Second
	if custom.Timeout != 42*time.Second {
		t.Fatalf("mutating the client's copy must not affect the caller's original, got %v", custom.Timeout)
	}
}

func TestNewWrapsURLParseError(t *testing.T) {
	_, err := New("://bad")
	if err == nil || !strings.Contains(err.Error(), "invalid base URL") {
		t.Fatalf("expected wrapped parse error, got %v", err)
	}
}
