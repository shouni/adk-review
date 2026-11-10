package adkagent

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// When logging an unparsable response, cut at a character boundary.
// Cutting inside a multi-byte character shows up as a broken character in the logs.
func TestHeadForLogKeepsRuneBoundary(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("あ", 500) // 3-byte character
	for _, limit := range []int{0, 1, 2, 3, 100, 101, 102} {
		got := headForLog(body, limit)
		trimmed := strings.TrimSuffix(got, "…(truncated)")
		if !utf8.ValidString(trimmed) {
			t.Errorf("limit=%d produced broken UTF-8", limit)
		}
		if len(trimmed) > limit {
			t.Errorf("exceeds limit=%d: %d bytes", limit, len(trimmed))
		}
	}
}

// At or under the limit, return it unchanged.
func TestHeadForLogKeepsShortInput(t *testing.T) {
	t.Parallel()

	const body = `{"title":"レビュー"}`
	if got := headForLog(body, maxLoggedResponse); got != body {
		t.Errorf("headForLog() = %q, want %q", got, body)
	}
}

// Cut the tail at a character boundary too. As with the head, cutting midway breaks a character.
func TestTailForLogKeepsRuneBoundary(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("あ", 500)
	for _, limit := range []int{1, 2, 3, 100, 101, 102} {
		got := tailForLog(body, limit)
		trimmed := strings.TrimPrefix(got, "(truncated)…")
		if !utf8.ValidString(trimmed) {
			t.Errorf("limit=%d produced broken UTF-8", limit)
		}
		if len(trimmed) > limit {
			t.Errorf("exceeds limit=%d: %d bytes", limit, len(trimmed))
		}
		if !strings.HasSuffix(body, trimmed) {
			t.Errorf("limit=%d: not the tail", limit)
		}
	}
}

// Add no tail to a response that fits in the head.
// With one, the same content sits under two keys and the "a tail is present = cut" cue is lost.
func TestTailForLogSkipsShortInput(t *testing.T) {
	t.Parallel()

	const body = `{"title":"レビュー"}`
	if got := tailForLog(body, maxLoggedResponse); got != "" {
		t.Errorf("tailForLog() = %q, want \"\"", got)
	}
}

// Fail only on the finish reasons that leave the review unable to continue.
// Why MAX_TOKENS does not fail is in finishError's doc.
func TestFinalResponseFinishError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reason  genai.FinishReason
		wantErr bool
		want    string
	}{
		"STOP succeeds":         {reason: genai.FinishReasonStop},
		"empty succeeds":        {reason: ""},
		"MAX_TOKENS passes too": {reason: genai.FinishReasonMaxTokens},
		"SAFETY fails":          {reason: genai.FinishReasonSafety, wantErr: true, want: "did not finish its output"},
		"RECITATION fails too":  {reason: genai.FinishReasonRecitation, wantErr: true, want: "did not finish its output"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := finalResponse{text: "{", finishReason: tt.reason}.finishError()
			if (err != nil) != tt.wantErr {
				t.Fatalf("finishError() = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("finishError() = %v, want contains %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), string(tt.reason)) {
				t.Errorf("finishError() does not include the finish reason: %v", err)
			}
		})
	}
}

// The failure log carries both the head and the tail.
//
// With the head alone there is no telling whether the model wrote to the end and was cut, or
// ballooned by repeating itself midway (a 212 KB output once could not be diagnosed from its first 2 KB).
func TestFinalResponseLogAttrs(t *testing.T) {
	t.Parallel()

	body := `{"title":"` + strings.Repeat("あ", 5000) + `末尾の目印`
	final := finalResponse{
		text:         body,
		finishReason: genai.FinishReasonMaxTokens,
		usage:        &genai.GenerateContentResponseUsageMetadata{CandidatesTokenCount: 65535},
	}

	attrs := map[string]any{}
	pairs := final.logAttrs()
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("key is not a string: %T", pairs[i])
		}
		attrs[key] = pairs[i+1]
	}

	for _, key := range []string{"finish_reason", "response_bytes", "response_head", "response_tail", "output_tokens"} {
		if _, ok := attrs[key]; !ok {
			t.Errorf("log is missing %q: %v", key, attrs)
		}
	}
	tail, _ := attrs["response_tail"].(string)
	if !strings.HasSuffix(tail, "末尾の目印") {
		t.Errorf("the tail is missing: %q", tail)
	}
	if head, _ := attrs["response_head"].(string); !strings.HasPrefix(head, `{"title":"`) {
		t.Errorf("the head is missing: %q", head)
	}
}

// Output that needed repair is logged as a warning.
//
// ParseReport fixes broken output silently and succeeds, so without recording it here operations
// cannot tell "the repair worked" from "it was never broken".
func TestSanitizeDetectsRepairedOutput(t *testing.T) {
	t.Parallel()

	// The shape that actually failed: a backslash in an excerpt's regexp left unescaped.
	broken := `{"title":"a","summary":"s","verdict":{"decision":"Minor","reason":"r"},` +
		`"findings":[{"severity":"Minor","file":"x.go","excerpt":"regexp.MustCompile(\d+)","message":"m"}]}`

	_, info, err := review.ParseReport([]byte(broken))
	if err != nil {
		t.Fatalf("cannot parse after repair: %v", err)
	}
	if !info.Repaired {
		t.Error("not repaired; the warning condition does not hold")
	}

	// Sound output is not repaired (= no warning).
	sound := `{"title":"a","summary":"s","verdict":{"decision":"None","reason":"r"},"findings":[]}`
	if _, info, err := review.ParseReport([]byte(sound)); err != nil || info.Repaired {
		t.Errorf("sound output: info = %+v, err = %v", info, err)
	}
}

// The code of conduct states the tool budget in numbers. If the instruction's numbers drift from
// the actual budget, the model keeps investigating as if calls remained and is cut off by surprise.
func TestInstructionForCarriesToolBudget(t *testing.T) {
	t.Parallel()

	got := instructionFor(32)
	for _, want := range []string{"32 回", "25 回"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructionFor(32) missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "%!") {
		t.Errorf("format verbs not filled:\n%s", got)
	}
}

// ★ The limit on findings must quote the same number as the schema too
// (what happens when they drift is in the doc above instructionTemplate).
func TestInstructionForCarriesFindingsCap(t *testing.T) {
	t.Parallel()

	got := instructionFor(32)
	if want := fmt.Sprintf("%d 件", maxFindings); !strings.Contains(got, want) {
		t.Errorf("instructionFor(32) missing %q:\n%s", want, got)
	}
}

// Wrapping up starts at 80% of the limit. Even a tiny budget never gives 0
// (0 would mean "wrap up before the first call").
func TestWrapUpAfter(t *testing.T) {
	t.Parallel()

	tests := map[int64]int64{32: 25, 10: 8, 5: 4, 1: 1, 0: 1}
	for in, want := range tests {
		if got := wrapUpAfter(in); got != want {
			t.Errorf("wrapUpAfter(%d) = %d, want %d", in, got, want)
		}
	}
}

// truncated reports that the output was cut. Now that finishError lets it through, this is the
// only path that carries the fact.
func TestFinalResponseTruncated(t *testing.T) {
	t.Parallel()

	if !(finalResponse{finishReason: genai.FinishReasonMaxTokens}).truncated() {
		t.Error("MAX_TOKENS does not set truncated")
	}
	for _, reason := range []genai.FinishReason{"", genai.FinishReasonStop} {
		if (finalResponse{finishReason: reason}).truncated() {
			t.Errorf("finish_reason %q sets truncated", reason)
		}
	}
}

// The measurements reach RunInfo.
//
// ★ Usage can arrive missing (some backends and paths do not carry it). Even then the tool
// count and whether the output was cut must be kept. Whether the material for recalibrating the
// limit is "all missing" or "partly missing" changes the effort of the next investigation.
func TestFinalResponseRunInfo(t *testing.T) {
	t.Parallel()

	final := finalResponse{
		finishReason: genai.FinishReasonMaxTokens,
		usage: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     219062,
			CandidatesTokenCount: 63950,
			ThoughtsTokenCount:   1579,
		},
	}

	got := final.runInfo(5)
	want := review.RunInfo{
		Truncated:     true,
		PromptTokens:  219062,
		OutputTokens:  63950,
		ThoughtTokens: 1579,
		ToolCalls:     5,
	}
	if got != want {
		t.Errorf("runInfo() = %+v, want %+v", got, want)
	}

	// Even without usage, keep what is known.
	bare := finalResponse{}.runInfo(3)
	if bare != (review.RunInfo{ToolCalls: 3}) {
		t.Errorf("runInfo() without usage = %+v", bare)
	}
}

// The remaining count keeps decreasing after the budget is spent, so used must not exceed the limit.
// Otherwise the record would say "35 calls with a limit of 32".
func TestToolboxUsedCapsAtBudget(t *testing.T) {
	t.Parallel()

	tb, err := newToolbox(t.TempDir(), 2)
	if err != nil {
		t.Fatalf("newToolbox: %v", err)
	}
	for range 5 {
		tb.spend()
	}
	if got := tb.used(); got != 2 {
		t.Errorf("used() = %d, want 2", got)
	}
}
