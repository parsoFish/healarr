// Package notify renders healarr's plain-text digest from a check run.
// Phase 3 adds the msmtp sender that mails the rendered text; Phase 5
// prepends an LLM narrative ahead of it.
package notify

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/parsoFish/healarr/internal/check"
	"github.com/parsoFish/healarr/internal/config"
)

//go:embed digest.tmpl
var digestTmplSrc string

// digestTmpl is parsed once at init from the embedded, version-controlled
// template, so a malformed template is a build-time-detectable panic, not
// a runtime error path callers need to handle.
var digestTmpl = template.Must(template.New("digest").Parse(digestTmplSrc))

// timestampLayout matches the digest brief: "2006-01-02 15:04 MST",
// rendered in the report's own time.Location rather than normalised to UTC.
const timestampLayout = "2006-01-02 15:04 MST"

// DigestInput is everything the templated digest renders. Phase 3 fills
// BaseURL from the NAS report; Phase 5 prepends an LLM narrative.
type DigestInput struct {
	Node        config.Node
	GeneratedAt time.Time
	Findings    []check.Finding // open findings, already sorted severity desc
	Errors      []check.CheckError
	Skipped     []string
	ChecksRun   int
	Metrics     map[string]float64
	BaseURL     string // e.g. "http://192.0.2.20/healarr" — may be empty in Phase 2
}

// RenderDigest renders the plain-text digest. It never returns an empty
// string on success: with no findings it says so explicitly.
func RenderDigest(in DigestInput) (string, error) {
	var buf strings.Builder
	if err := digestTmpl.Execute(&buf, buildDigestView(in)); err != nil {
		return "", fmt.Errorf("notify: render digest: %w", err)
	}
	return buf.String(), nil
}

// digestView is the template's render model. It is derived from
// DigestInput without mutating it: findings are bucketed by severity in
// their existing order, never re-sorted.
type digestView struct {
	Node          config.Node
	Timestamp     string
	ChecksRun     int
	ChecksFailed  int
	SkippedCount  int
	FindingsCount int
	CriticalCount int
	WarnCount     int
	InfoCount     int
	HasFindings   bool
	Critical      []findingView
	Warn          []findingView
	Info          []findingView
	Errors        []check.CheckError
	SkippedList   string
	BaseURL       string
}

// findingView is one `- [checkId] summary` line item under a severity
// section, with its optional indented detail.
type findingView struct {
	CheckID string
	Summary string
	Detail  string
}

// buildDigestView projects a DigestInput into the template's render model.
func buildDigestView(in DigestInput) digestView {
	view := digestView{
		Node:          in.Node,
		Timestamp:     in.GeneratedAt.Format(timestampLayout),
		ChecksRun:     in.ChecksRun,
		ChecksFailed:  len(in.Errors),
		SkippedCount:  len(in.Skipped),
		FindingsCount: len(in.Findings),
		Errors:        in.Errors,
		SkippedList:   strings.Join(in.Skipped, ", "),
		BaseURL:       in.BaseURL,
	}
	for _, f := range in.Findings {
		fv := findingView{CheckID: f.CheckID, Summary: f.Summary, Detail: f.Detail}
		switch f.Severity {
		case check.SeverityCritical:
			view.Critical = append(view.Critical, fv)
		case check.SeverityWarn:
			view.Warn = append(view.Warn, fv)
		default:
			// check.SeverityInfo and any unrecognised severity render
			// under INFO rather than being silently dropped.
			view.Info = append(view.Info, fv)
		}
	}
	view.CriticalCount = len(view.Critical)
	view.WarnCount = len(view.Warn)
	view.InfoCount = len(view.Info)
	view.HasFindings = view.FindingsCount > 0
	return view
}
