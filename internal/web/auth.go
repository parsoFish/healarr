package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// sessionCookieName is the cookie the web UI sets on a successful login
// and reads on every subsequent request.
const sessionCookieName = "healarr_session"

// sessionTTL is how long a session stays valid after login, per the
// design ruling. It is a code constant (like peer's historyLookback),
// not a config field: it is an auth-mechanism detail, not something an
// operator needs to tune per deployment.
const sessionTTL = 30 * 24 * time.Hour

// sessionIDBytes is the size of the random session id, before hex
// encoding (32 bytes = 256 bits, generated with crypto/rand).
const sessionIDBytes = 32

// session is what requireSession attaches to a request's context once
// its cookie has been validated against the session table.
type session struct {
	ID string
}

type sessionCtxKeyType struct{}

var sessionCtxKey sessionCtxKeyType

// sessionFromContext returns the session requireSession attached to ctx.
// ok is false if called outside a requireSession-wrapped handler, which
// is always a programming error in this package, never a runtime one.
func sessionFromContext(ctx context.Context) (session, bool) {
	sess, ok := ctx.Value(sessionCtxKey).(session)
	return sess, ok
}

// sessionTable is the in-memory map of live sessions (design ruling:
// sessions survive only in memory — a process restart signs everyone
// out). It is safe for concurrent use by every request's goroutine.
type sessionTable struct {
	mu   sync.RWMutex
	byID map[string]time.Time // session id -> expiresAt
}

func newSessionTable() *sessionTable {
	return &sessionTable{byID: make(map[string]time.Time)}
}

// create mints a new random session id and stores it with an expiry
// sessionTTL from now.
func (t *sessionTable) create() (string, error) {
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("web: create session: %w", err)
	}
	id := hex.EncodeToString(raw)

	t.mu.Lock()
	t.byID[id] = time.Now().Add(sessionTTL)
	t.mu.Unlock()
	return id, nil
}

// valid reports whether id names a session that hasn't expired. An
// expired session is lazily evicted rather than left to leak forever.
func (t *sessionTable) valid(id string) bool {
	t.mu.RLock()
	expiresAt, ok := t.byID[id]
	t.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(expiresAt) {
		t.delete(id)
		return false
	}
	return true
}

func (t *sessionTable) delete(id string) {
	t.mu.Lock()
	delete(t.byID, id)
	t.mu.Unlock()
}

// csrfToken derives the per-session CSRF token from sessionID via
// HMAC-SHA256 with the server's per-process random key (design ruling).
// Deriving it this way means the session table never needs a second
// field to hold it: the token is always reproducible from the session
// id alone, for as long as this process (and its key) is alive.
func (s *server) csrfToken(sessionID string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

// validCSRF reports whether got matches sess's derived CSRF token,
// compared in constant time.
func (s *server) validCSRF(sess session, got string) bool {
	want := s.csrfToken(sess.ID)
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requireSession wraps next so it only runs once the request carries a
// valid session cookie. A GET without one is sent to the login page
// (303); any other method (a POST, per this package's routes) gets a
// bare 401, since there is no page to redirect a form submission to that
// wouldn't just resubmit it. The resolved session is attached to the
// request's context so handlers (and the templates they render) can read
// its id/CSRF token without re-deriving it.
func (s *server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" || !s.sessions.valid(cookie.Value) {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, s.basePath+"/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), sessionCtxKey, session{ID: cookie.Value})
		next(w, r.WithContext(ctx))
	}
}

// handleLoginGet either renders the login form (no token query param) or
// validates one against s.token. It never logs the token's value, on
// either the success or failure path.
func (s *server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		s.render(w, r, "login", loginView{PageData: PageData{BasePath: s.basePath, CSS: s.css}})
		return
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		s.logger.Warn("web: login rejected: token mismatch", "remote_addr", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := s.sessions.create()
	if err != nil {
		s.logger.Error("web: login: create session", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     s.rootPath(),
		Expires:  time.Now().Add(sessionTTL),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, s.basePath+"/", http.StatusSeeOther)
}

// handleLogout clears the caller's session (server-side and via cookie)
// and sends them back to the login page. Reaching this handler already
// proved a valid session (requireSession); it also requires the matching
// CSRF field below, same as every other POST route.
func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.validCSRF(sess, r.PostFormValue("csrf")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	s.sessions.delete(sess.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     s.rootPath(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, s.basePath+"/login", http.StatusSeeOther)
}

// rootPath is the cookie Path attribute: basePath itself, or "/" when
// the UI is mounted at the root (a cookie Path must never be empty).
func (s *server) rootPath() string {
	if s.basePath == "" {
		return "/"
	}
	return s.basePath
}
