package assets

import (
	"strings"
	"testing"
)

func TestAvailableModesReadsFrontMatter(t *testing.T) {
	modes, err := AvailableModes()
	if err != nil {
		t.Fatalf("AvailableModes failed: %v", err)
	}

	got := make(map[string]Mode, len(modes))
	for _, mode := range modes {
		got[mode.Key] = mode
	}

	for _, key := range []string{"article", "code", "novel"} {
		mode, ok := got[key]
		if !ok {
			t.Fatalf("mode %s is missing", key)
		}
		// A forgotten description makes the choice meaningless, so every mode must have one.
		if mode.Label == "" {
			t.Errorf("%s: label is missing", key)
		}
		if mode.Direction == "" {
			t.Errorf("%s: direction is missing", key)
		}
		if mode.UseWhen == "" {
			t.Errorf("%s: use_when is missing", key)
		}
	}
}

func TestLoadPromptsStripsFrontMatter(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}

	code, ok := prompts["code"]
	if !ok {
		t.Fatal("the code prompt is missing")
	}
	for _, key := range []string{"label:", "direction:", "use_when:", "excerpt:"} {
		if strings.Contains(code, key) {
			t.Errorf("front matter is left in the body: %q", key)
		}
	}
	if !strings.HasPrefix(code, "# ") {
		t.Errorf("the body does not start with a heading: %q", code[:min(80, len(code))])
	}
}

func TestLoadPromptsReturnsCopy(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}
	prompts["code"] = "mutated"

	reloaded, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts reload failed: %v", err)
	}
	if reloaded["code"] == "mutated" {
		t.Fatal("LoadPrompts should not expose the cached map to callers")
	}
}

// Shared fragments join the same set as the bodies under "_"-prefixed template names.
// If the keys drift, the bodies' {{template "_..."}} cannot resolve and every mode fails at startup.
func TestPromptTemplatesIncludePartials(t *testing.T) {
	templates, err := PromptTemplates()
	if err != nil {
		t.Fatalf("PromptTemplates failed: %v", err)
	}

	want := map[string][]string{
		"_findings_format": {"severity", "file", "excerpt", "message", "suggestion", "evidence"},
		"_verdict_format":  {"decision", "reason", "title", "summary"},
		// The line-number rule and the output language live here only, with no copies.
		"_finding_policy": {"hunk", "findings", "日本語"},
	}
	for name, keywords := range want {
		body, ok := templates[name]
		if !ok {
			t.Errorf("shared fragment %q is not in the template set", name)
			continue
		}
		for _, kw := range keywords {
			if !strings.Contains(body, kw) {
				t.Errorf("%s does not contain %q:\n%s", name, kw, body)
			}
		}
	}

	// The mode bodies must be in the same set too.
	if _, ok := templates["code"]; !ok {
		t.Error("the mode body is not in the template set")
	}
}

// The common policy lives once, in partials. If a copy returns to a prompt, a fix
// meant for all leaves one mode on the old text.
func TestPromptsDoNotDuplicateSharedPolicy(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}

	for key, body := range prompts {
		if !strings.Contains(body, `{{template "_finding_policy" .}}`) {
			t.Errorf("%s: the common finding policy is not expanded", key)
		}
		if strings.Contains(body, "hunk") {
			t.Errorf("%s: the line-number rule is copied into the prompt", key)
		}
		// How to use evidence belongs to findings_format and the instruction. If a copy
		// returns, the same instruction appears 3 times per review and goes stale per mode.
		if strings.Contains(body, "`evidence` に挙げ") {
			t.Errorf("%s: the evidence instruction is copied into the prompt", key)
		}
		// "Looking beyond the diff is the value" is said by the instruction. A prompt
		// should only say what to open in its mode.
		if strings.Contains(body, "こそがこのレビューの価値です") {
			t.Errorf("%s: the point of looking beyond the diff duplicates the instruction", key)
		}
	}
}

// Do not ask for checks the tools cannot verify.
//
// This reviewer's tools read only the working directory (read_file / list_files /
// search_text). Asking for a broken-link check collides with the finding policy's "do not
// report what you cannot verify", and the model either stays silent or makes things up.
func TestPromptsDoNotAskForUnverifiableChecks(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}

	for key, body := range prompts {
		for _, ng := range []string{"公式ドキュメントへのリンク", "一次情報の明示"} {
			if strings.Contains(body, ng) {
				t.Errorf("%s: asks for %q, which the tools cannot verify", key, ng)
			}
		}
		// "リンク切れ" (broken link) may appear only where it says not to report one. Whether it
		// is listed as a check or a severity example is judged by a disclaimer on the same line.
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, "リンク切れ") && !strings.Contains(line, "推測で書かず") {
				t.Errorf("%s: asks for a broken-link check: %s", key, line)
			}
		}
	}
}

func TestIsValidMode(t *testing.T) {
	if !IsValidMode("code") {
		t.Error("code is not judged valid")
	}
	if IsValidMode("no-such-mode") {
		t.Error("an unknown mode was judged valid")
	}
}

// DisplayName falls back to the key without a label (the choice itself is never removed).
func TestDisplayNameFallsBackToKey(t *testing.T) {
	if got := (Mode{Key: "x"}).DisplayName(); got != "x" {
		t.Errorf("DisplayName() = %q, want %q", got, "x")
	}
}

func TestExcerptStyleFor(t *testing.T) {
	t.Parallel()

	tests := map[string]ExcerptStyle{
		"code":  ExcerptCode,
		"novel": ExcerptProse,
		// An unknown mode is not an error and is treated as prose. The history keeps the
		// names of deleted modes, so failing here would make past reviews unopenable.
		"すでに消したモード": ExcerptProse,
		"":          ExcerptProse,
	}

	for mode, want := range tests {
		if got := ExcerptStyleFor(mode); got != want {
			t.Errorf("ExcerptStyleFor(%q) = %q, want %q", mode, got, want)
		}
	}
}
