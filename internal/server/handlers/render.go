package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/jst"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/giturl"
)

const (
	layoutTemplate       = "templates/layout.html"
	reviewFormTemplate   = "review_form.html"
	historyTemplate      = "history.html"
	reviewDetailTemplate = "review_detail.html"
)

// pageTemplates are the page templates rendered together with the layout.
var pageTemplates = []string{reviewFormTemplate, historyTemplate, reviewDetailTemplate}

// parsePageTemplates builds an independent template set per page.
//
// They cannot be parsed into one set because every page defines its body with
// {{define "content"}}. A later definition of the same name overwrites an earlier one, so
// together only one page's body would remain and every other page would render it.
func parsePageTemplates() (map[string]*template.Template, error) {
	set := make(map[string]*template.Template, len(pageTemplates))

	for _, page := range pageTemplates {
		tmpl, err := template.New(page).
			Funcs(templateFuncs()).
			ParseFS(assets.Templates, layoutTemplate, "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		set[page] = tmpl
	}
	return set, nil
}

// render renders the given page within the layout.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := h.templates[page]
	if !ok {
		slog.ErrorContext(r.Context(), "unregistered template", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Write to a buffer first. If executing the template fails midway, it can turn into an error
	// instead of returning broken HTML with a 200.
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.ErrorContext(r.Context(), "template execution error", "error", err, "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		slog.ErrorContext(r.Context(), "response write error", "error", err)
	}
}

// templateFuncs are the formatting functions used to display the history.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"repoPath":      giturl.GetRepositoryPath,
		"formatTime":    formatTime,
		"stateLabel":    stateLabel,
		"stateClass":    stateClass,
		"decisionLabel": decisionLabel,
		"decisionClass": decisionClass,
		"severityClass": severityClass,
		"codeText":      codeText,
		"excerptClass":  excerptClass,
		"byteSize":      byteSize,
		"durationText":  durationText,
	}
}

// byteSize formats a byte count in readable units.
//
// The diff size is compared against MAX_DIFF_BYTES (default 320 KiB); as raw bytes one would
// have to count digits.
func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// durationText formats milliseconds as a readable duration (the text is user-facing Japanese).
func durationText(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Minute {
		return fmt.Sprintf("%.1f 秒", d.Seconds())
	}
	return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
}

// CSS classes of the quote blocks. The look itself lives in app.css.
const (
	codeBlockClass  = "code-block"
	quoteBlockClass = "quote-block"
)

// excerptClass returns the quote block class for a mode.
//
// What to show and how is declared on the prompt side (excerpt in the front matter).
// Branching on mode names here would mean fixing the screen for every new mode, breaking
// "placing prompts/<mode>.md is all it takes".
func excerptClass(mode string) string {
	if assets.ExcerptStyleFor(mode) == assets.ExcerptCode {
		return codeBlockClass
	}
	return quoteBlockClass
}

// codeText tidies a finding's quote (excerpt) and suggested fix for display.
//
// ★ The model brings Markdown habits into JSON string values too, and sometimes puts a whole
// code fence into excerpt. Fed into <pre> as is, the ``` shows up as text and the rawness of
// the model's output reaches the screen. Only a fence wrapping the whole value is stripped;
// fences inside it stay as content.
//
// Leading and trailing blank lines are dropped too. <pre> turns blank lines into height, leaving
// empty bands above and below the quote (the model tends to add newlines around quotes).
func codeText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	lines = trimBlankLines(lines)

	if len(lines) >= 2 &&
		strings.HasPrefix(strings.TrimSpace(lines[0]), "```") &&
		strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = trimBlankLines(lines[1 : len(lines)-1])
	}
	return strings.Join(lines, "\n")
}

// trimBlankLines drops leading and trailing whitespace-only lines.
func trimBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// formatTime displays Japan time. The zero value is left blank
// (so an unrecorded time does not appear as 0001/01/01).
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return jst.FormatTimestamp(t)
}

// badge pairs a display name with a badge class.
//
// With the name and the class in separate switches, adding a state invites fixing only one
// (both are the same "state -> look" table). They are kept together in one place.
type badge struct {
	label string
	class string
}

// stateBadges defines how progress is displayed.
// A "skipped, no diff" is a normal end for the job (succeeded), so State alone cannot tell it
// from completion. stateBadge fills in by also looking at Outcome.
var stateBadges = map[jobstatus.State]badge{
	jobstatus.StateQueued:    {"⏱ 受付済み", "text-bg-light"},
	jobstatus.StateRunning:   {"⏳ 実行中", "text-bg-info"},
	jobstatus.StateFailed:    {"❌ 失敗", "text-bg-danger"},
	jobstatus.StateSucceeded: {"✅ 完了", "text-bg-success"},
}

// skippedBadge is shown when the review was skipped for lack of a diff.
var skippedBadge = badge{"⏭️ スキップ", "text-bg-secondary"}

// unknownStateBadge is shown for an unknown state.
var unknownStateBadge = badge{"— 不明", "text-bg-light"}

// decisionBadges defines how review decisions are displayed.
var decisionBadges = map[review.Decision]badge{
	review.DecisionNone:    {"✅ 問題なし", "text-bg-success"},
	review.DecisionMinor:   {"🟡 軽微", "text-bg-warning"},
	review.DecisionMajor:   {"🟠 要修正", "text-bg-warning"},
	review.DecisionBlocker: {"🔴 ブロッカー", "text-bg-danger"},
}

// unknownDecisionBadge is shown for an unknown decision.
var unknownDecisionBadge = badge{"—", "text-bg-light"}

// stateBadge returns the display for the progress and outcome.
func stateBadge(state jobstatus.State, outcome review.Status) badge {
	if state == jobstatus.StateSucceeded && outcome == review.StatusSkipped {
		return skippedBadge
	}
	if b, ok := stateBadges[state]; ok {
		return b
	}
	return unknownStateBadge
}

// decisionBadge returns the display for a decision.
func decisionBadge(decision review.Decision) badge {
	if b, ok := decisionBadges[decision]; ok {
		return b
	}
	return unknownDecisionBadge
}

// stateLabel returns the display name combining the progress and outcome.
func stateLabel(state jobstatus.State, outcome review.Status) string {
	return stateBadge(state, outcome).label
}

// stateClass returns the Bootstrap class of the state badge.
func stateClass(state jobstatus.State, outcome review.Status) string {
	return stateBadge(state, outcome).class
}

// decisionLabel returns the display name of a review decision.
func decisionLabel(decision review.Decision) string {
	return decisionBadge(decision).label
}

// decisionClass returns the Bootstrap class of the decision badge.
func decisionClass(decision review.Decision) string {
	return decisionBadge(decision).class
}

// severityClass returns the Bootstrap class of a finding's severity badge.
//
// It is a separate function because its type differs from Decision. Template functions resolve
// by type, so reusing one would fail at run time with a type mismatch.
func severityClass(severity review.Severity) string {
	return decisionClass(review.Decision(severity))
}
