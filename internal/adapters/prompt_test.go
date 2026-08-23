package adapters

import (
	"strings"
	"testing"

	"github.com/shouni/adk-review/assets"
)

const sampleDiff = "--- diff ---\n+ example line\n"

func TestPromptAdapter_GenerateReview(t *testing.T) {
	pa, err := NewPromptAdapter()
	if err != nil {
		t.Fatalf("NewPromptAdapter() failed: %v", err)
	}

	modes, err := assets.AvailableModes()
	if err != nil {
		t.Fatalf("AvailableModes() failed: %v", err)
	}
	if len(modes) == 0 {
		t.Fatal("no review modes available")
	}

	gen := pa.For(assets.EngineAgent)
	for _, mode := range modes {
		t.Run(mode.Key, func(t *testing.T) {
			prompt, err := gen.Generate(mode.Key, sampleDiff)
			if err != nil {
				t.Fatalf("Generate(%q) failed: %v", mode.Key, err)
			}

			// 共有パーシャル(findings_format.md / verdict_format.md /
			// finding_policy.md)がテンプレート実行時に正しく展開されていること
			for _, want := range []string{"findings", "verdict", "decision", "reason", "evidence", "hunk", "日本語"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("Generate(%q) result missing %q\n--- prompt ---\n%s", mode.Key, want, prompt)
				}
			}

			if strings.Contains(prompt, "<no value>") {
				t.Errorf("Generate(%q) has unresolved template placeholder:\n%s", mode.Key, prompt)
			}
		})
	}
}

// 単発エンジンのプロンプトに、ツールと evidence の指示が残っていないことを見ます。
//
// **残すと、差分しか読めないレビュアーに「差分の外を確認した根拠」を書かせることに
// なります。** 単発の出力スキーマに evidence は無く、書かせても捨てられるだけでなく、
// 確認しようのない事柄について根拠の捏造を促します。
func TestPromptAdapter_SingleEngineOmitsToolInstructions(t *testing.T) {
	pa, err := NewPromptAdapter()
	if err != nil {
		t.Fatalf("NewPromptAdapter() failed: %v", err)
	}

	modes, err := assets.AvailableModes()
	if err != nil {
		t.Fatalf("AvailableModes() failed: %v", err)
	}

	gen := pa.For(assets.EngineSingle)
	for _, mode := range modes {
		t.Run(mode.Key, func(t *testing.T) {
			prompt, err := gen.Generate(mode.Key, sampleDiff)
			if err != nil {
				t.Fatalf("Generate(%q) failed: %v", mode.Key, err)
			}

			for _, unwanted := range []string{"evidence", "read_file", "search_text", "list_files"} {
				if strings.Contains(prompt, unwanted) {
					t.Errorf("単発のプロンプトに %q が残っています\n--- prompt ---\n%s", unwanted, prompt)
				}
			}
			if strings.Contains(prompt, "<no value>") {
				t.Errorf("Generate(%q) has unresolved template placeholder:\n%s", mode.Key, prompt)
			}
			// 差分と共通の方針は、エンジンによらず必要です。
			for _, want := range []string{"example line", "hunk", "日本語"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("単発のプロンプトに %q がありません\n--- prompt ---\n%s", want, prompt)
				}
			}
		})
	}
}
