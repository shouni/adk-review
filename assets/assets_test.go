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

// 現在の全モードはエージェント実行です。単発へ戻すのは設計判断なので、
// 意図しない差し戻しに気付けるようテストで固定します。
func TestAllModesUseAgentEngine(t *testing.T) {
	modes, err := AvailableModes()
	if err != nil {
		t.Fatalf("AvailableModes failed: %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("モードが 1 つもありません")
	}

	for _, mode := range modes {
		if mode.EngineKind() != EngineAgent {
			t.Errorf("%s のエンジン = %q, want %q", mode.Key, mode.EngineKind(), EngineAgent)
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
	for _, key := range []string{"label:", "direction:", "use_when:", "engine:"} {
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

func TestLoadFindingsFormat(t *testing.T) {
	got, err := LoadFindingsFormat()
	if err != nil {
		t.Fatalf("LoadFindingsFormat failed: %v", err)
	}
	for _, want := range []string{"severity", "file", "excerpt", "message", "suggestion", "evidence"} {
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
	for _, want := range []string{"decision", "reason", "title", "summary"} {
		if !strings.Contains(got, want) {
			t.Errorf("LoadVerdictFormat() missing %q:\n%s", want, got)
		}
	}
}

func TestEngineFor(t *testing.T) {
	engine, err := EngineFor("novel")
	if err != nil {
		t.Fatalf("EngineFor failed: %v", err)
	}
	if engine != EngineAgent {
		t.Fatalf("novel は agent のはずです: got %q", engine)
	}

	if _, err := EngineFor("no-such-mode"); err == nil {
		t.Fatal("未知のモードがエラーになりません")
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

// EngineKind は front matter に engine が無いモードを単発として扱います。
func TestEngineKindDefaultsToSingle(t *testing.T) {
	if got := (Mode{Key: "x"}).EngineKind(); got != EngineSingle {
		t.Errorf("EngineKind() = %q, want %q", got, EngineSingle)
	}
}

// DisplayName は label が無ければキーで代替します（選択肢自体は消しません）。
func TestDisplayNameFallsBackToKey(t *testing.T) {
	if got := (Mode{Key: "x"}).DisplayName(); got != "x" {
		t.Errorf("DisplayName() = %q, want %q", got, "x")
	}
}

func TestResolveEngine(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		override string
		want     Engine
		wantErr  bool
	}{
		{name: "既定はモードの宣言に従う", mode: "code", want: EngineAgent},
		{name: "単発へ上書き", mode: "code", override: "single", want: EngineSingle},
		{name: "エージェントへ上書き", mode: "code", override: "agent", want: EngineAgent},
		{name: "未知のエンジンは拒否", mode: "code", override: "turbo", wantErr: true},
		{name: "未知のモードは拒否", mode: "no-such-mode", wantErr: true},
		// 上書きがあるときはモードを見ません。存在しないモードでも指定どおりに解決します。
		{name: "上書きはモードより優先", mode: "no-such-mode", override: "single", want: EngineSingle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveEngine(tt.mode, tt.override)
			if tt.wantErr {
				if err == nil {
					t.Fatal("エラーを期待しましたが nil でした")
				}
				return
			}
			if err != nil {
				t.Fatalf("予期しないエラー: %v", err)
			}
			if got != tt.want {
				t.Errorf("ResolveEngine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// 引用の見せ方は、プロンプトの front matter が宣言すること。
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
