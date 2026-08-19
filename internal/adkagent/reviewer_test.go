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
