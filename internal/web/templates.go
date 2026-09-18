package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static.css
var cssBytes []byte

// maxCSSBytes is the constraint's "inline CSS ≤ 20 KB" ceiling, enforced
// by a test rather than at runtime: static.css is authored content, not
// external input, so there's nothing to fail fast against at request
// time — but a future edit that blows past the budget should fail a
// build, not silently ship a bigger page.
const maxCSSBytes = 20 * 1024

// PageData is embedded by every page's view struct. BasePath and CSS are
// always set; CSRFToken is empty on the (unauthenticated) login page,
// which the header partial uses to decide whether to render the logout
// form. Flash carries a one-line, already-worded status message decoded
// from the "flash" query parameter a POST redirects with.
type PageData struct {
	BasePath  string
	CSRFToken string
	CSS       template.CSS
	Flash     string
}

// pageData builds the common fields for an authenticated page, deriving
// CSRFToken from sess and Flash from the request's "flash" query param.
func (s *server) pageData(sess session, r *http.Request) PageData {
	return PageData{
		BasePath:  s.basePath,
		CSRFToken: s.csrfToken(sess.ID),
		CSS:       s.css,
		Flash:     flashMessage(r.URL.Query().Get("flash")),
	}
}

// flashMessage decodes a "flash" query value into the human-readable
// sentence the page displays; an unrecognised or empty value renders
// nothing rather than a raw code.
func flashMessage(code string) string {
	switch code {
	case "blocked":
		return "Recorded — actions are disabled in config, so it was not executed."
	case "ok":
		return "Decision recorded."
	default:
		return ""
	}
}

// funcMap is the small set of pure, side-effect-free helpers templates
// use for display formatting. None of them touch the store or config
// directly, so they stay trivially testable and reusable across pages.
// ago/humanizeBytes are hand-rolled rather than pulling in a formatting
// library: the display logic is a handful of lines, and every other
// dependency in this project earns its place against real client
// surfaces (ADR-013), not a couple of string helpers.
var funcMap = template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return ago(t)
	},
	"bytes": func(n int64) string {
		return humanizeBytes(n)
	},
	"pct": func(f float64) string {
		return fmt.Sprintf("%.1f%%", f)
	},
	"round": func(f float64) int {
		return int(math.Round(f))
	},
}

// ago renders t as a coarse "N unit(s) ago" string relative to now. A
// clock-skewed t slightly in the future (or an equal instant) folds into
// "just now" rather than printing a negative duration's nonsense
// ("−3 minutes ago").
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	case d < 365*24*time.Hour:
		return plural(int(d/(30*24*time.Hour)), "month") + " ago"
	default:
		return plural(int(d/(365*24*time.Hour)), "year") + " ago"
	}
}

// plural renders "1 thing" or "N things".
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// humanizeBytes renders n as a binary-prefixed size ("12.0 GiB"),
// clamping a negative value to 0 rather than printing a nonsense size.
func humanizeBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// parseTemplates parses every embedded template into one shared set.
// Each page defines its own top-level named template ("dashboard",
// "decisions", "history", "login") that includes the "header"/"footer"
// partials layout.html defines — see templates/layout.html for why that
// indirection replaces the more common (but not name-collision-safe
// across files) "content" block pattern.
func parseTemplates() (*template.Template, error) {
	tmpl, err := template.New("web").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return tmpl, nil
}

// render executes the named template into a buffer first, so a template
// execution error (a nil pointer in view data, a bad field reference)
// becomes a clean 500 with the detail logged, never a half-written page
// with a 200 already sent (constraints.md: template exec errors are
// never swallowed).
func (s *server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("web: render template", "template", name, "path", r.URL.Path, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		s.logger.Error("web: write response", "path", r.URL.Path, "error", err)
	}
}
