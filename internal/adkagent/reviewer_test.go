package adkagent

import (
	"strings"
	"testing"
	"unicode/utf8"
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
