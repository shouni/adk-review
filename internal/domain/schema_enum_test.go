package domain

import (
	"slices"
	"testing"

	"github.com/shouni/go-review-kit/review"
)

// 列挙値が review パッケージの定義をそのまま写していること。
//
// ここが空になったり並びが変わったりすると、2 つのレビュアーの出力スキーマが同時に
// 壊れます。モデルはスキーマ上正当な値を返すのにデコードで弾かれるため、
// 症状は「全レビューが失敗する」になります。
func TestEnumsMirrorReviewPackage(t *testing.T) {
	t.Parallel()

	wantSeverities := make([]string, 0, len(review.Severities()))
	for _, s := range review.Severities() {
		wantSeverities = append(wantSeverities, string(s))
	}
	if got := SeverityEnum(); !slices.Equal(got, wantSeverities) {
		t.Errorf("SeverityEnum() = %v, want %v", got, wantSeverities)
	}

	wantDecisions := make([]string, 0, len(review.Decisions()))
	for _, d := range review.Decisions() {
		wantDecisions = append(wantDecisions, string(d))
	}
	if got := DecisionEnum(); !slices.Equal(got, wantDecisions) {
		t.Errorf("DecisionEnum() = %v, want %v", got, wantDecisions)
	}

	// 空だとスキーマの enum が消え、モデルが任意の文字列を返せるようになります。
	if len(SeverityEnum()) == 0 || len(DecisionEnum()) == 0 {
		t.Fatal("列挙が空です")
	}
}
