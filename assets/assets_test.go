package assets

import (
	"strings"
	"testing"
)

func TestAvailableModesReadsPromptMetadata(t *testing.T) {
	modes, err := AvailableModes()
	if err != nil {
		t.Fatalf("AvailableModes failed: %v", err)
	}

	got := make(map[string]string, len(modes))
	for _, mode := range modes {
		got[mode.Name] = mode.Description
	}

	want := map[string]string{
		"article": "技術記事・ドキュメント品質レビュー",
		"code":    "詳細なコード品質レビュー",
		"novel":   "小説原稿の詳細レビュー",
	}
	for mode, description := range want {
		if got[mode] != description {
			t.Fatalf("unexpected description for %s: got %q want %q", mode, got[mode], description)
		}
	}
}

func TestLoadPromptsStripsModeDescriptionMetadata(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}

	code := prompts["code"]
	if strings.Contains(code, "mode-description:") {
		t.Fatalf("metadata should be stripped from prompt body: %q", code[:80])
	}
	if !strings.HasPrefix(code, "# ") {
		t.Fatalf("prompt body should start with markdown heading: %q", code[:80])
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

func TestLoadFindingsFormat(t *testing.T) {
	got, err := LoadFindingsFormat()
	if err != nil {
		t.Fatalf("LoadFindingsFormat failed: %v", err)
	}
	for _, want := range []string{"severity", "file", "excerpt", "message", "suggestion"} {
		if !strings.Contains(got, want) {
			t.Errorf("LoadFindingsFormat() missing %q:\n%s", want, got)
		}
	}
}

func TestLoadVerdictFormat(t *testing.T) {
	got, err := LoadVerdictFormat()
	if err != nil {
		t.Fatalf("LoadVerdictFormat failed: %v", err)
	}
	for _, want := range []string{"decision", "reason"} {
		if !strings.Contains(got, want) {
			t.Errorf("LoadVerdictFormat() missing %q:\n%s", want, got)
		}
	}
}

func TestParsePromptMetadataTrimsLeadingNoiseWithoutMetadata(t *testing.T) {
	description, engine, body, err := parsePromptMetadata("custom", "\ufeff\n\n# Custom Prompt")

	if err != nil {
		t.Fatalf("parsePromptMetadata failed: %v", err)
	}
	if description != "custom" {
		t.Fatalf("unexpected description: got %q want %q", description, "custom")
	}
	if engine != EngineSingle {
		t.Fatalf("unexpected engine: got %q want %q", engine, EngineSingle)
	}
	if body != "# Custom Prompt" {
		t.Fatalf("unexpected body: got %q", body)
	}
}

func TestParsePromptMetadataReadsEngine(t *testing.T) {
	input := "<!-- mode-description: \u539f\u7a3f\u30ec\u30d3\u30e5\u30fc -->\n<!-- engine: agent -->\n# Prompt"
	description, engine, body, err := parsePromptMetadata("novel", input)

	if err != nil {
		t.Fatalf("parsePromptMetadata failed: %v", err)
	}
	if description != "\u539f\u7a3f\u30ec\u30d3\u30e5\u30fc" {
		t.Fatalf("unexpected description: got %q", description)
	}
	if engine != EngineAgent {
		t.Fatalf("unexpected engine: got %q want %q", engine, EngineAgent)
	}
	if body != "# Prompt" {
		t.Fatalf("unexpected body: got %q", body)
	}
}

func TestParsePromptMetadataRejectsUnknownEngine(t *testing.T) {
	if _, _, _, err := parsePromptMetadata("novel", "<!-- engine: turbo -->\n# Prompt"); err == nil {
		t.Fatal("\u672a\u77e5\u306e engine \u304c\u30a8\u30e9\u30fc\u306b\u306a\u308a\u307e\u305b\u3093")
	}
}

func TestEngineFor(t *testing.T) {
	engine, err := EngineFor("novel")
	if err != nil {
		t.Fatalf("EngineFor failed: %v", err)
	}
	if engine != EngineAgent {
		t.Fatalf("novel \u306f agent \u306e\u306f\u305a\u3067\u3059: got %q", engine)
	}

	engine, err = EngineFor("code")
	if err != nil {
		t.Fatalf("EngineFor failed: %v", err)
	}
	if engine != EngineSingle {
		t.Fatalf("code \u306f single \u306e\u306f\u305a\u3067\u3059: got %q", engine)
	}

	if _, err := EngineFor("no-such-mode"); err == nil {
		t.Fatal("\u672a\u77e5\u306e\u30e2\u30fc\u30c9\u304c\u30a8\u30e9\u30fc\u306b\u306a\u308a\u307e\u305b\u3093")
	}
}
