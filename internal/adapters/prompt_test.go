package adapters

import (
	"strings"
	"testing"

	"github.com/shouni/adk-review/assets"
)

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

	for _, mode := range modes {
		t.Run(mode.Name, func(t *testing.T) {
			prompt, err := pa.Generate(mode.Name, "--- diff ---\n+ example line\n")
			if err != nil {
				t.Fatalf("GenerateReview(%q) failed: %v", mode.Name, err)
			}

			// 共有パーシャル(findings_format.md / verdict_format.md)が
			// テンプレート実行時に正しく展開されていること
			for _, want := range []string{"findings配列", "verdict", "decision", "reason"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("GenerateReview(%q) result missing %q\n--- prompt ---\n%s", mode.Name, want, prompt)
				}
			}

			if strings.Contains(prompt, "<no value>") {
				t.Errorf("GenerateReview(%q) has unresolved template placeholder:\n%s", mode.Name, prompt)
			}
		})
	}
}
