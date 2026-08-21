package adkagent

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/shouni/go-review-kit/review"
)

// 解析に失敗した応答をログへ載せるとき、文字境界で切ること。
// マルチバイトの途中で切ると、ログ側で壊れた文字として出ます。
func TestTruncateForLogKeepsRuneBoundary(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("あ", 500) // 3 バイト文字
	for _, limit := range []int{0, 1, 2, 3, 100, 101, 102} {
		got := truncateForLog(body, limit)
		trimmed := strings.TrimSuffix(got, "…(以下略)")
		if !utf8.ValidString(trimmed) {
			t.Errorf("limit=%d で壊れた UTF-8 になりました", limit)
		}
		if len(trimmed) > limit {
			t.Errorf("limit=%d を超えています: %d バイト", limit, len(trimmed))
		}
	}
}

// 上限以下ならそのまま返すこと。
func TestTruncateForLogKeepsShortInput(t *testing.T) {
	t.Parallel()

	const body = `{"title":"レビュー"}`
	if got := truncateForLog(body, maxLoggedResponse); got != body {
		t.Errorf("truncateForLog() = %q, want %q", got, body)
	}
}

// 補修が要った出力は warn として残ること。
//
// ParseReport は壊れた出力を黙って直して成功するので、ここで記録しないと
// 「補修が効いた」のか「そもそも壊れていない」のかが運用から区別できません。
func TestSanitizeDetectsRepairedOutput(t *testing.T) {
	t.Parallel()

	// 実際に落ちた形。excerpt の正規表現でバックスラッシュがエスケープされていない。
	broken := `{"title":"a","summary":"s","verdict":{"decision":"Minor","reason":"r"},` +
		`"findings":[{"severity":"Minor","file":"x.go","excerpt":"regexp.MustCompile(\d+)","message":"m"}]}`

	raw := []byte(broken)
	cleaned := review.SanitizeJSON(raw)
	if bytes.Equal(cleaned, raw) {
		t.Fatal("補修されていません。warn の判定条件が成立しません")
	}
	if _, err := review.ParseReport(cleaned); err != nil {
		t.Fatalf("補修後に解釈できません: %v", err)
	}

	// 壊れていない出力は補修されない（＝ warn が出ない）こと。
	sound := []byte(`{"title":"a","summary":"s","verdict":{"decision":"None","reason":"r"},"findings":[]}`)
	if !bytes.Equal(review.SanitizeJSON(sound), sound) {
		t.Error("壊れていない出力が補修されています")
	}
}

// 行動指針はツール予算を数字で伝えること。**指示の数字が実際の予算とずれると、
// モデルは残りがあるつもりで調査を続け、打ち切りに不意打ちされます。**
func TestInstructionForCarriesToolBudget(t *testing.T) {
	t.Parallel()

	got := instructionFor(32)
	for _, want := range []string{"32 回", "25 回"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructionFor(32) missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "%!") {
		t.Errorf("書式が埋まっていません:\n%s", got)
	}
}

// まとめに入る目安は上限の 8 割。予算が極端に小さくても 0 回にはしません
// （0 だと「最初の 1 回を呼ぶ前にまとめろ」という指示になります）。
func TestWrapUpAfter(t *testing.T) {
	t.Parallel()

	tests := map[int64]int64{32: 25, 10: 8, 5: 4, 1: 1, 0: 1}
	for in, want := range tests {
		if got := wrapUpAfter(in); got != want {
			t.Errorf("wrapUpAfter(%d) = %d, want %d", in, got, want)
		}
	}
}
