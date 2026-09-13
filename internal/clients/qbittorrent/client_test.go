package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mustWrite writes data to w, failing the test on error, so the handler
// switch below stays a single check per case instead of a repeated
// if-err-then-t.Error block (which pushed newQB's cyclomatic complexity over
// the project's gocyclo limit).
func mustWrite(t *testing.T, w http.ResponseWriter, data []byte) {
	t.Helper()
	if _, err := w.Write(data); err != nil {
		t.Error(err)
	}
}

// newQB serves the qBittorrent v2 API shape used by these tests: a
// stateful login (cookie required afterwards for /app/version), fixtures for
// the read endpoints, and a generic 200-OK-after-ParseForm for the
// write endpoints (delete/reannounce/resume/setPreferences).
func newQB(t *testing.T, loginOK bool, seen *[]*http.Request) *httptest.Server {
	t.Helper()
	loggedIn := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r)
		switch r.URL.Path {
		case "/api/v2/auth/login":
			if !loginOK {
				mustWrite(t, w, []byte("Fails."))
				return
			}
			loggedIn = true
			// Real qBittorrent sets Path=/ on its SID cookie; without it,
			// net/http/cookiejar defaults the path to the *directory* of
			// this login request ("/api/v2/auth"), which would then never
			// match the sibling "/api/v2/app/..." endpoints below.
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "abc", Path: "/"})
			mustWrite(t, w, []byte("Ok."))
		case "/api/v2/app/version":
			if c, _ := r.Cookie("SID"); c == nil || !loggedIn {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			mustWrite(t, w, []byte("v5.1.4"))
		case "/api/v2/torrents/info":
			mustWrite(t, w, fixture(t, "torrents_info.json"))
		case "/api/v2/torrents/files":
			mustWrite(t, w, fixture(t, "torrent_files.json"))
		case "/api/v2/app/preferences":
			mustWrite(t, w, fixture(t, "preferences.json"))
		default: // delete/reannounce/resume/setPreferences
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func TestLoginThenVersion(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if v, err := c.Version(ctx); err != nil || v != "v5.1.4" {
		t.Fatalf("Version (1st): %q %v", v, err)
	}
	if v, err := c.Version(ctx); err != nil || v != "v5.1.4" {
		t.Fatalf("Version (2nd): %q %v", v, err)
	}

	logins := 0
	for _, r := range seen {
		if r.URL.Path == "/api/v2/auth/login" {
			logins++
		}
	}
	if logins != 1 {
		t.Errorf("login requests = %d, want exactly 1 across two Version calls", logins)
	}
}

func TestLoginFailureIsError(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, false, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "admin", "wrong")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("expected an error mentioning credentials, got %v", err)
	}
}

func TestTorrentsDecodeEpochAndDuration(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	torrents, err := c.Torrents(context.Background(), "", "")
	if err != nil || len(torrents) != 2 {
		t.Fatalf("Torrents: %+v %v", torrents, err)
	}
	if torrents[0].SeedingTime != 48*time.Hour {
		t.Errorf("SeedingTime = %v, want 48h", torrents[0].SeedingTime)
	}
	if !torrents[1].CompletionOn.IsZero() {
		t.Errorf("CompletionOn = %v, want zero (completion_on -1)", torrents[1].CompletionOn)
	}
	if torrents[1].State != "stalledDL" {
		t.Errorf("State = %q, want stalledDL", torrents[1].State)
	}
}

func TestDeleteJoinsHashesAndFlags(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "admin", "adminadmin")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), []string{"a", "b"}, true); err != nil {
		t.Fatal(err)
	}

	var del *http.Request
	for _, r := range seen {
		if r.URL.Path == "/api/v2/torrents/delete" {
			del = r
		}
	}
	if del == nil {
		t.Fatal("no delete request seen")
	}
	if got := del.FormValue("hashes"); got != "a|b" {
		t.Errorf("hashes = %q, want a|b", got)
	}
	if got := del.FormValue("deleteFiles"); got != "true" {
		t.Errorf("deleteFiles = %q, want true", got)
	}
}

func TestNoCredsSkipsLogin(t *testing.T) {
	var seen []*http.Request
	srv := newQB(t, true, &seen)
	defer srv.Close()

	c, err := New(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// No creds means no login is ever attempted; exercise a call that the
	// fixture server answers without requiring the SID cookie.
	if _, err := c.Torrents(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	for _, r := range seen {
		if r.URL.Path == "/api/v2/auth/login" {
			t.Fatalf("unexpected login request: %v", r)
		}
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &Fake{TorrentList: []Torrent{{Hash: "x"}}}
	torrents, err := f.Torrents(context.Background(), "stalled", "tv-sonarr")
	if err != nil || len(torrents) != 1 {
		t.Fatalf("Torrents: %+v %v", torrents, err)
	}
	if len(f.Calls) != 1 || f.Calls[0] != `Torrents("stalled", "tv-sonarr")` {
		t.Fatalf("Calls = %v", f.Calls)
	}
}
