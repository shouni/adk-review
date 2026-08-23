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

	for _, mode := range modes {
		t.Run(mode.Key, func(t *testing.T) {
			prompt, err := pa.Generate(mode.Key, sampleDiff)
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
