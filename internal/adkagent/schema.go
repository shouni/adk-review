package adkagent

import (
	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// reportSchema は、review.Report に対応する構造化出力スキーマです。
//
// 列挙値は review パッケージの定義から組み立てます。**アプリ側で []string へ詰め替え
// 直さないでください。** 写しが増えると、値を足したときにスキーマと検証が食い違い、
// モデルはスキーマ上正当な値を返すのにデコードで弾かれます。
//
// findings に evidence を含めるのは、このレビュアーが作業ディレクトリを実際に調べるため、
// 「どこを見て判断したか」を自己申告させる意味があるからです。**差分しか読めない
// レビュアーへ同じ項目を持ち込まないでください。** 確認する手段が無いまま根拠を
// 求めると、モデルはそれを捏造して埋めます。
func reportSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title":   {Type: genai.TypeString},
			"summary": {Type: genai.TypeString},
			"verdict": {
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"decision": {Type: genai.TypeString, Enum: review.DecisionStrings()},
					"reason":   {Type: genai.TypeString},
				},
				Required: []string{"decision", "reason"},
			},
			"findings": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"severity":   {Type: genai.TypeString, Enum: review.SeverityStrings()},
						"file":       {Type: genai.TypeString},
						"line":       {Type: genai.TypeInteger},
						"excerpt":    {Type: genai.TypeString},
						"message":    {Type: genai.TypeString},
						"suggestion": {Type: genai.TypeString},
						"evidence": {
							Type:  genai.TypeArray,
							Items: &genai.Schema{Type: genai.TypeString},
						},
					},
					Required: []string{"severity", "file", "excerpt", "message"},
				},
			},
		},
		Required: []string{"title", "summary", "verdict", "findings"},
	}
}
