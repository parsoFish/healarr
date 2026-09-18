package web

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/parsoFish/healarr/internal/config"
)

// newTestServer builds a full New()-constructed handler (using
// testConfig()) and wraps it in an httptest.Server. Its per-process CSRF
// key is only reachable from inside the package, so tests that need a
// valid CSRF token scrape one off a rendered page instead (see
// loginAndCSRF).
func newTestServer(t *testing.T, fs *fakeStore, fr *fakeRunner, token string) *httptest.Server {
	t.Helper()
	return newTestServerWithConfig(t, testConfig(), fs, fr, token)
}

// newTestServerWithConfig is newTestServer with a caller-supplied
// config.Config, for tests that need to vary e.g. Actions.Enabled.
func newTestServerWithConfig(t *testing.T, cfg config.Config, fs *fakeStore, fr *fakeRunner, token string) *httptest.Server {
	t.Helper()
	if fs == nil {
		fs = &fakeStore{}
	}
	if fr == nil {
		fr = &fakeRunner{}
	}
	h, err := New(cfg, token, fs, fr, testLogger())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func doGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func noRedirectClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestUnauthenticatedGetRedirectsToLogin(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	resp := doGet(t, noRedirectClient(), srv.URL+"/healarr/")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /healarr/ = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/healarr/login" {
		t.Fatalf("Location = %q, want /healarr/login", loc)
	}
}

func TestUnauthenticatedPostReturns401(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	resp, err := http.PostForm(srv.URL+"/healarr/decisions", url.Values{})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /healarr/decisions = %d, want 401", resp.StatusCode)
	}
}

func TestLoginWithBadTokenReturns401(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	resp := doGet(t, noRedirectClient(), srv.URL+"/healarr/login?token=wrong")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token login = %d, want 401", resp.StatusCode)
	}
}

func TestLoginWithGoodTokenSetsCookieAndRedirects(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	client := noRedirectClient()
	resp := doGet(t, client, srv.URL+"/healarr/login?token=tok")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("good token login = %d, want 303", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "/healarr/" {
		t.Fatalf("Location = %q, want /healarr/", resp.Header.Get("Location"))
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no healarr_session cookie set")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Error("session cookie must be SameSite=Strict")
	}
	if cookie.Value == "" {
		t.Error("session cookie value must not be empty")
	}
}

func TestLoginRendersFormWithoutToken(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	resp := doGet(t, noRedirectClient(), srv.URL+"/healarr/login")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /login (no token) = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "healarr") {
		t.Fatalf("login page body missing expected content: %s", body)
	}
}

// justLogin logs in against srv and returns a client whose jar now holds
// a valid session cookie, without depending on any page rendering
// successfully afterwards.
func justLogin(t *testing.T, srv *httptest.Server, token string) *http.Client {
	t.Helper()
	client := noRedirectClient()
	resp := doGet(t, client, srv.URL+"/healarr/login?token="+token)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303", resp.StatusCode)
	}

	// The session cookie's Path is the base path ("/healarr"), so it is
	// only returned by the jar for a lookup URL under that path — not
	// for srv.URL's bare root.
	u, err := url.Parse(srv.URL + "/healarr/")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	var sessionID string
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == sessionCookieName {
			sessionID = c.Value
		}
	}
	if sessionID == "" {
		t.Fatal("no session cookie after login")
	}
	return client
}

// loginAndCSRF is justLogin plus scraping a rendered page's CSRF token,
// rather than recomputing HMAC(sessionID) with a key this test doesn't
// have access to (each New() call mints its own random csrfKey).
func loginAndCSRF(t *testing.T, srv *httptest.Server, token string) (*http.Client, string) {
	t.Helper()
	client := justLogin(t, srv, token)

	dash := doGet(t, client, srv.URL+"/healarr/")
	body, _ := io.ReadAll(dash.Body)
	csrf := extractCSRF(string(body))
	if csrf == "" {
		t.Fatalf("could not find a csrf token on the dashboard page: %s", body)
	}
	return client, csrf
}

// extractCSRF pulls the first name="csrf" value="..." hidden field out
// of rendered HTML, good enough for a test fixture without a full parser.
func extractCSRF(body string) string {
	const marker = `name="csrf" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestLogoutClearsSessionAndRedirects(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	client, csrf := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/logout", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("POST /logout: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", resp.StatusCode)
	}
	if resp.Header.Get("Location") != "/healarr/login" {
		t.Fatalf("Location = %q, want /healarr/login", resp.Header.Get("Location"))
	}

	// The session must now be dead: fetching the dashboard again bounces
	// back to login.
	dash := doGet(t, client, srv.URL+"/healarr/")
	if dash.StatusCode != http.StatusSeeOther {
		t.Fatalf("dashboard after logout = %d, want 303 (session should be dead)", dash.StatusCode)
	}
}

func TestPostWithoutCSRFReturns403(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	client, _ := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:1"},
		"kind":       {"keep"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without csrf = %d, want 403", resp.StatusCode)
	}
}

func TestPostWithWrongCSRFReturns403(t *testing.T) {
	srv := newTestServer(t, nil, nil, "tok")
	client, _ := loginAndCSRF(t, srv, "tok")

	resp, err := client.PostForm(srv.URL+"/healarr/decisions", url.Values{
		"entity_key": {"sonarr:1"},
		"kind":       {"keep"},
		"csrf":       {"not-the-right-token"},
	})
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST with wrong csrf = %d, want 403", resp.StatusCode)
	}
}
