package adkagent

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// 解析に失敗した応答をログへ載せるとき、文字境界で切ること。
// マルチバイトの途中で切ると、ログ側で壊れた文字として出ます。
func TestHeadForLogKeepsRuneBoundary(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("あ", 500) // 3 バイト文字
	for _, limit := range []int{0, 1, 2, 3, 100, 101, 102} {
		got := headForLog(body, limit)
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
func TestHeadForLogKeepsShortInput(t *testing.T) {
	t.Parallel()

	const body = `{"title":"レビュー"}`
	if got := headForLog(body, maxLoggedResponse); got != body {
		t.Errorf("headForLog() = %q, want %q", got, body)
	}
}

// 末尾も文字境界で切ること。頭と同じく、途中で切ると壊れた文字になります。
func TestTailForLogKeepsRuneBoundary(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("あ", 500)
	for _, limit := range []int{1, 2, 3, 100, 101, 102} {
		got := tailForLog(body, limit)
		trimmed := strings.TrimPrefix(got, "(前略)…")
		if !utf8.ValidString(trimmed) {
			t.Errorf("limit=%d で壊れた UTF-8 になりました", limit)
		}
		if len(trimmed) > limit {
			t.Errorf("limit=%d を超えています: %d バイト", limit, len(trimmed))
		}
		if !strings.HasSuffix(body, trimmed) {
			t.Errorf("limit=%d で末尾になっていません", limit)
		}
	}
}

// 頭だけで全文が載る応答には末尾を付けないこと。
// 付けると同じ内容が 2 つのキーに並び、「末尾がある＝切れている」手掛かりが消えます。
func TestTailForLogSkipsShortInput(t *testing.T) {
	t.Parallel()

	const body = `{"title":"レビュー"}`
	if got := tailForLog(body, maxLoggedResponse); got != "" {
		t.Errorf("tailForLog() = %q, want \"\"", got)
	}
}

// 出力上限で切られた応答は、JSON の解析へ進む前にその理由で落とすこと。
//
// **ADK は切り詰められた出力も正常な最終応答として通します。** ここで見ないと、
// 上限超過が「JSONとして解釈できません」として報告され、症状しか残りません。
func TestFinalResponseFinishError(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		reason  genai.FinishReason
		wantErr bool
		want    string
	}{
		"STOP は成功":       {reason: genai.FinishReasonStop},
		"空は成功":           {reason: ""},
		"MAX_TOKENS は失敗": {reason: genai.FinishReasonMaxTokens, wantErr: true, want: "途中で切れました"},
		"SAFETY も失敗":     {reason: genai.FinishReasonSafety, wantErr: true, want: "最後まで出力しませんでした"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := finalResponse{text: "{", finishReason: tt.reason}.finishError()
			if (err != nil) != tt.wantErr {
				t.Fatalf("finishError() = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("finishError() = %v, want contains %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), string(tt.reason)) {
				t.Errorf("finishError() が終了理由を含みません: %v", err)
			}
		})
	}
}

// 失敗ログには頭と末尾の両方を載せること。
//
// 頭だけだと、最後まで書いた末に切れたのか、途中から繰り返して膨らんだのかが
// 区別できません（212 KB の出力を頭 2 KB だけ見て判断できなかった例があります）。
func TestFinalResponseLogAttrs(t *testing.T) {
	t.Parallel()

	body := `{"title":"` + strings.Repeat("あ", 5000) + `末尾の目印`
	final := finalResponse{
		text:         body,
		finishReason: genai.FinishReasonMaxTokens,
		usage:        &genai.GenerateContentResponseUsageMetadata{CandidatesTokenCount: 65535},
	}

	attrs := map[string]any{}
	pairs := final.logAttrs()
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			t.Fatalf("キーが文字列ではありません: %T", pairs[i])
		}
		attrs[key] = pairs[i+1]
	}

	for _, key := range []string{"finish_reason", "response_bytes", "response_head", "response_tail", "output_tokens"} {
		if _, ok := attrs[key]; !ok {
			t.Errorf("ログに %q がありません: %v", key, attrs)
		}
	}
	tail, _ := attrs["response_tail"].(string)
	if !strings.HasSuffix(tail, "末尾の目印") {
		t.Errorf("末尾が載っていません: %q", tail)
	}
	if head, _ := attrs["response_head"].(string); !strings.HasPrefix(head, `{"title":"`) {
		t.Errorf("冒頭が載っていません: %q", head)
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
