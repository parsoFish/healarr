package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/clients/httpx"
)

// These tests supplement the brief's verbatim client_test.go to reach the
// project's 80% coverage gate: the 403-then-relogin retry path (both the
// success and the persistent-failure cases), Files/Preferences/
// SetPreferences/Reannounce/Resume, query building, decode-error surfacing,
// New's invalid-URL path, and the Fake's remaining methods.

func TestNewInvalidBaseURL(t *testing.T) {
	if _, err := New("://bad-url", "admin", "adminadmin"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

// TestVersionRetriesAfterSessionExpiry exercises withAuth's retry-once path:
// the session looks valid to the client (already logged in) but the server
// answers 403 as if the SID had expired; the client must re-login and retry
// exactly once, succeeding on the retry.
func TestVersionRetriesAfterSessionExpiry(t *testing.T) {
	loginCount, versionCount := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginCount++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
			if _, err := w.Write([]byte("Ok.")); err != nil {
				t.Error(err)
			}
		case "/api/v2/app/version":
			versionCount++
			if versionCount == 2 {
				w.WriteHeader(http.StatusForbidden) // simulate expired session
				return
			}
			if _, err := w.Write([]byte("v5.1.4")); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("first Version: %v", err)
	}
	if v, err := c.Version(ctx); err != nil || v != "v5.1.4" {
		t.Fatalf("second Version (after relogin): %q %v", v, err)
	}
	if loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (initial login + retry-after-403)", loginCount)
	}
}

// TestVersionFailsAfterSingleRetry ensures a persistent 403 is returned
// as-is after the one allowed retry, rather than looping forever.
func TestVersionFailsAfterSingleRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
			if _, err := w.Write([]byte("Ok.")); err != nil {
				t.Error(err)
			}
		case "/api/v2/app/version":
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Version(context.Background()); err == nil || !httpx.IsStatus(err, http.StatusForbidden) {
		t.Fatalf("expected persistent 403 error, got %v", err)
	}
}

// TestReloginFailureAfter403IsReturned covers withAuth's retry-login-failure
// branch: the first login succeeds, a later call gets a 403, but the retry
// login itself fails ("Fails.") — that error must propagate, not a generic
// wrapped 403.
func TestReloginFailureAfter403IsReturned(t *testing.T) {
	loginN := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			loginN++
			if loginN == 1 {
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
				if _, err := w.Write([]byte("Ok.")); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := w.Write([]byte("Fails.")); err != nil {
				t.Error(err)
			}
		case "/api/v2/app/version":
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected credentials error from failed relogin, got %v", err)
	}
}

func TestTorrentsSendsFilterAndCategoryQuery(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Torrents(context.Background(), "stalled", "tv-sonarr"); err != nil {
		t.Fatal(err)
	}
	info := findRequest(seen, "/api/v2/torrents/info")
	if info == nil {
		t.Fatal("no torrents/info request seen")
	}
	if got := info.URL.Query().Get("filter"); got != "stalled" {
		t.Errorf("filter query = %q, want stalled", got)
	}
	if got := info.URL.Query().Get("category"); got != "tv-sonarr" {
		t.Errorf("category query = %q, want tv-sonarr", got)
	}
}

func TestTorrentsOmitsEmptyFilterAndCategory(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Torrents(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	info := findRequest(seen, "/api/v2/torrents/info")
	if info == nil {
		t.Fatal("no torrents/info request seen")
	}
	if info.URL.RawQuery != "" {
		t.Errorf("query = %q, want empty", info.URL.RawQuery)
	}
}

func TestTorrentsSurfacesDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc"})
			if _, err := w.Write([]byte("Ok.")); err != nil {
				t.Error(err)
			}
		case "/api/v2/torrents/info":
			if _, err := w.Write([]byte("not-json")); err != nil {
				t.Error(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Torrents(context.Background(), "", ""); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestFilesDecodesFixture(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	files, err := c.Files(context.Background(), "853ed40dabcdef")
	if err != nil || len(files) != 1 {
		t.Fatalf("Files: %+v %v", files, err)
	}
	if files[0].Name != "A.Knight.S01E06.720p.exe" || files[0].Priority != 1 {
		t.Errorf("unexpected file: %+v", files[0])
	}
	req := findRequest(seen, "/api/v2/torrents/files")
	if req == nil || req.URL.Query().Get("hash") != "853ed40dabcdef" {
		t.Errorf("expected hash query param, got %+v", req)
	}
}

func TestPreferencesDecodesFixture(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := c.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if prefs.SavePath != "/downloads" || !prefs.TempPathEnabled || prefs.MaxSeedingTime != 2880 {
		t.Errorf("unexpected preferences: %+v", prefs)
	}
}

func TestSetPreferencesSendsJSONForm(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetPreferences(context.Background(), map[string]any{"max_ratio": 2}); err != nil {
		t.Fatal(err)
	}
	req := findRequest(seen, "/api/v2/app/setPreferences")
	if req == nil {
		t.Fatal("no setPreferences request seen")
	}
	if got := req.FormValue("json"); !strings.Contains(got, `"max_ratio":2`) {
		t.Errorf("json form value = %q, want to contain max_ratio", got)
	}
}

func TestReannounceAndResumeJoinHashes(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()
	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reannounce(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Resume(context.Background(), []string{"c"}); err != nil {
		t.Fatal(err)
	}

	reannounce := findRequest(seen, "/api/v2/torrents/reannounce")
	if reannounce == nil || reannounce.FormValue("hashes") != "a|b" {
		t.Fatalf("reannounce request: %+v", reannounce)
	}
	resume := findRequest(seen, "/api/v2/torrents/resume")
	if resume == nil || resume.FormValue("hashes") != "c" {
		t.Fatalf("resume request: %+v", resume)
	}
}

func TestEpochTimeUnmarshalError(t *testing.T) {
	var e epochTime
	if err := e.UnmarshalJSON([]byte("not-a-number")); err == nil {
		t.Fatal("expected error for invalid epoch value")
	}
}

func TestFakeRecordsAllCallsAndPropagatesErr(t *testing.T) {
	wantErr := context.DeadlineExceeded
	f := &Fake{
		TorrentList: []Torrent{{Hash: "a"}},
		FilesByHash: map[string][]File{"a": {{Index: 0}}},
		Prefs:       Preferences{SavePath: "/x"},
		Err:         wantErr,
	}
	ctx := context.Background()

	if _, err := f.Version(ctx); err != wantErr {
		t.Errorf("Version err: %v", err)
	}
	if _, err := f.Torrents(ctx, "", ""); err != wantErr {
		t.Errorf("Torrents err: %v", err)
	}
	if _, err := f.Files(ctx, "a"); err != wantErr {
		t.Errorf("Files err: %v", err)
	}
	if err := f.Delete(ctx, []string{"a"}, true); err != wantErr {
		t.Errorf("Delete err: %v", err)
	}
	if err := f.Reannounce(ctx, []string{"a"}); err != wantErr {
		t.Errorf("Reannounce err: %v", err)
	}
	if err := f.Resume(ctx, []string{"a"}); err != wantErr {
		t.Errorf("Resume err: %v", err)
	}
	if _, err := f.Preferences(ctx); err != wantErr {
		t.Errorf("Preferences err: %v", err)
	}
	if err := f.SetPreferences(ctx, map[string]any{"x": 1}); err != wantErr {
		t.Errorf("SetPreferences err: %v", err)
	}

	want := []string{
		"Version()", `Torrents("", "")`, "Files(a)", "Delete([a], true)",
		"Reannounce([a])", "Resume([a])", "Preferences()", "SetPreferences(map[x:1])",
	}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i, w := range want {
		if f.Calls[i] != w {
			t.Errorf("Calls[%d] = %q, want %q", i, f.Calls[i], w)
		}
	}
}

func findRequest(seen []*http.Request, path string) *http.Request {
	for _, r := range seen {
		if r.URL.Path == path {
			return r
		}
	}
	return nil
}
