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
