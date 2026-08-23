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
			t.Fatalf("モード %s がありません", key)
		}
		// 説明の書き忘れは選択肢の意味を失わせるため、全モードに要求します。
		if mode.Label == "" {
			t.Errorf("%s: label がありません", key)
		}
		if mode.Direction == "" {
			t.Errorf("%s: direction がありません", key)
		}
		if mode.UseWhen == "" {
			t.Errorf("%s: use_when がありません", key)
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
		t.Fatal("code のプロンプトがありません")
	}
	for _, key := range []string{"label:", "direction:", "use_when:", "excerpt:"} {
		if strings.Contains(code, key) {
			t.Errorf("front matter が本文に残っています: %q", key)
		}
	}
	if !strings.HasPrefix(code, "# ") {
		t.Errorf("本文が見出しから始まっていません: %q", code[:min(80, len(code))])
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

// 共有断片は "_" 付きのテンプレート名で本文と同じ集合に入ります。
// キーがずれると本文の {{template "_..."}} が解決できず、全モードが起動時に落ちます。
func TestPromptTemplatesIncludePartials(t *testing.T) {
	templates, err := PromptTemplates()
	if err != nil {
		t.Fatalf("PromptTemplates failed: %v", err)
	}

	want := map[string][]string{
		"_findings_format": {"severity", "file", "excerpt", "message", "suggestion", "evidence"},
		"_verdict_format":  {"decision", "reason", "title", "summary"},
		// 行番号の算出と出力言語は、写しを作らずここ 1 箇所に置く決まりです。
		"_finding_policy": {"hunk", "findings", "日本語"},
	}
	for name, keywords := range want {
		body, ok := templates[name]
		if !ok {
			t.Errorf("共有断片 %q がテンプレート集にありません", name)
			continue
		}
		for _, kw := range keywords {
			if !strings.Contains(body, kw) {
				t.Errorf("%s に %q がありません:\n%s", name, kw, body)
			}
		}
	}

	// モード本文も同じ集合に入っている必要があります。
	if _, ok := templates["code"]; !ok {
		t.Error("モード本文がテンプレート集にありません")
	}
}

// 共通の方針は partials に 1 つだけ置きます。プロンプト側へ写しが戻ると、
// 直したつもりが 1 モードだけ古いまま残ります。
func TestPromptsDoNotDuplicateSharedPolicy(t *testing.T) {
	prompts, err := LoadPrompts()
	if err != nil {
		t.Fatalf("LoadPrompts failed: %v", err)
	}

	for key, body := range prompts {
		if !strings.Contains(body, `{{template "_finding_policy" .}}`) {
			t.Errorf("%s: 共通の指摘方針が展開されていません", key)
		}
		if strings.Contains(body, "hunk") {
			t.Errorf("%s: 行番号の算出規則がプロンプトに写されています", key)
		}
	}
}

func TestIsValidMode(t *testing.T) {
	if !IsValidMode("code") {
		t.Error("code が有効と判定されません")
	}
	if IsValidMode("no-such-mode") {
		t.Error("未知のモードが有効と判定されました")
	}
}

// DisplayName は label が無ければキーで代替します（選択肢自体は消しません）。
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
		// 未知のモードでもエラーにせず、地の文として扱います。履歴には消したモードの
		// 名前も残るため、ここで落とすと過去のレビューが開けなくなります。
		"すでに消したモード": ExcerptProse,
		"":          ExcerptProse,
	}

	for mode, want := range tests {
		if got := ExcerptStyleFor(mode); got != want {
			t.Errorf("ExcerptStyleFor(%q) = %q, want %q", mode, got, want)
		}
	}
}
