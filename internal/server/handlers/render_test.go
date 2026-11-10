package handlers

import (
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// Every decision has a display.
//
// For a review tool, a Blocker shown as "—" or in pale grey is the most painful way to break.
// A test that touches only one decision cannot notice the other branches breaking.
func TestDecisionBadgeCoversAllDecisions(t *testing.T) {
	t.Parallel()

	for _, decision := range review.Decisions() {
		t.Run(string(decision), func(t *testing.T) {
			t.Parallel()

			b := decisionBadge(decision)
			if b == unknownDecisionBadge {
				t.Fatalf("%q has no display defined", decision)
			}
			if b.label == "" || b.class == "" {
				t.Fatalf("%q has an empty display: %+v", decision, b)
			}
		})
	}

	if got := decisionBadge("なにかの新しい判定"); got != unknownDecisionBadge {
		t.Errorf("unknown decision = %+v, want %+v", got, unknownDecisionBadge)
	}
}

// A Blocker shows in the danger colour. If the colour does not carry the weight, it is missed in the list.
func TestDecisionBlockerIsDanger(t *testing.T) {
	t.Parallel()

	if got := decisionClass(review.DecisionBlocker); got != "text-bg-danger" {
		t.Errorf("Blocker class = %q, want text-bg-danger", got)
	}
	if got := decisionLabel(review.DecisionBlocker); got == "—" {
		t.Error("the Blocker display name is treated as undefined")
	}
}

// severityClass delegates to decisionClass but is a separate function because the type differs.
// This checks that the delegation is still in place.
func TestSeverityClassMatchesDecisionClass(t *testing.T) {
	t.Parallel()

	for _, severity := range review.Severities() {
		if got, want := severityClass(severity), decisionClass(review.Decision(severity)); got != want {
			t.Errorf("severityClass(%q) = %q, want %q", severity, got, want)
		}
	}
}

// Every state has a progress display.
func TestStateBadgeCoversAllStates(t *testing.T) {
	t.Parallel()

	states := []jobstatus.State{
		jobstatus.StateQueued,
		jobstatus.StateRunning,
		jobstatus.StateFailed,
		jobstatus.StateSucceeded,
	}
	for _, state := range states {
		b := stateBadge(state, review.StatusSucceeded)
		if b == unknownStateBadge {
			t.Errorf("%q has no display defined", state)
		}
	}

	// A skip is succeeded but displayed separately from completion.
	skipped := stateBadge(jobstatus.StateSucceeded, review.StatusSkipped)
	done := stateBadge(jobstatus.StateSucceeded, review.StatusSucceeded)
	if skipped == done {
		t.Error("skipped and succeeded look the same")
	}

	if got := stateBadge("なにかの新しい状態", review.StatusSucceeded); got != unknownStateBadge {
		t.Errorf("unknown state = %+v, want %+v", got, unknownStateBadge)
	}
}

// Formatting quotes: drop the code fences the model adds and the surrounding blank lines.
func TestCodeText(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want string
	}{
		"strips a fence with a language": {
			in:   "```go\nfunc main() {}\n```",
			want: "func main() {}",
		},
		"strips a fence without a language": {
			in:   "```\nhello\n```",
			want: "hello",
		},
		"drops surrounding blank lines": {
			in:   "\n\n    アキラは剣を抜いた。\n\n",
			want: "    アキラは剣を抜いた。",
		},
		"keeps indentation": {
			in:   "if x {\n\treturn nil\n}",
			want: "if x {\n\treturn nil\n}",
		},
		"keeps an unclosed fence as content": {
			in:   "```go\nfunc main() {}",
			want: "```go\nfunc main() {}",
		},
		"keeps a fence in the middle": {
			in:   "見出し\n```go\ncode\n```\n続き",
			want: "見出し\n```go\ncode\n```\n続き",
		},
		"normalises CRLF to LF": {
			in:   "a\r\nb",
			want: "a\nb",
		},
		"empty string": {in: "", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := codeText(tt.in); got != tt.want {
				t.Errorf("codeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// The quote class depends on the mode. The decision comes from the prompt's front matter.
func TestExcerptClass(t *testing.T) {
	t.Parallel()

	if got := excerptClass("code"); got != codeBlockClass {
		t.Errorf("excerptClass(code) = %q, want %q", got, codeBlockClass)
	}
	for _, mode := range []string{"novel", "article", "消えたモード", ""} {
		if got := excerptClass(mode); got != quoteBlockClass {
			t.Errorf("excerptClass(%q) = %q, want %q", mode, got, quoteBlockClass)
		}
	}
}

// The diff size is compared against MAX_DIFF_BYTES (default 320 KiB), so it must be readable
// without counting digits.
func TestByteSize(t *testing.T) {
	t.Parallel()

	tests := map[int]string{
		0:       "0 B",
		512:     "512 B",
		1024:    "1 KiB",
		327680:  "320 KiB",
		491186:  "480 KiB",
		1 << 20: "1.0 MiB",
	}
	for in, want := range tests {
		if got := byteSize(in); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDurationText(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		0:       "0.0 秒",
		39_000:  "39.0 秒",
		152_000: "2 分 32 秒",
	}
	for in, want := range tests {
		if got := durationText(in); got != want {
			t.Errorf("durationText(%d) = %q, want %q", in, got, want)
		}
	}
}
