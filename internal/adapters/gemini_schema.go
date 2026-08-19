package adapters

import (
	geminiclient "github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-review-kit/review"
)

// reportSchema は、review.Report に対応する構造化出力スキーマです。
//
// 列挙値は review パッケージの定義から組み立てます。スキーマ側に文字列を二重に持つと、
// 値を足したときに検証側と食い違う余地が生まれるためです。
//
// findings に evidence が無いのは同期漏れではありません。Evidence は作業ディレクトリを
// 調べる WorkspaceReviewer（internal/adkagent）が「どこを見て判断したか」を残す
// フィールドで、差分しか見ていない単発レビュアーに出力させると根拠の捏造を促します。
func reportSchema() *geminiclient.Schema {
	return &geminiclient.Schema{
		Type: geminiclient.TypeObject,
		Properties: map[string]*geminiclient.Schema{
			"title":   {Type: geminiclient.TypeString},
			"summary": {Type: geminiclient.TypeString},
			"verdict": {
				Type: geminiclient.TypeObject,
				Properties: map[string]*geminiclient.Schema{
					"decision": {Type: geminiclient.TypeString, Enum: review.DecisionStrings()},
					"reason":   {Type: geminiclient.TypeString},
				},
				Required: []string{"decision", "reason"},
			},
			"findings": {
				Type: geminiclient.TypeArray,
				Items: &geminiclient.Schema{
					Type: geminiclient.TypeObject,
					Properties: map[string]*geminiclient.Schema{
						"severity":   {Type: geminiclient.TypeString, Enum: review.SeverityStrings()},
						"file":       {Type: geminiclient.TypeString},
						"line":       {Type: geminiclient.TypeInteger},
						"excerpt":    {Type: geminiclient.TypeString},
						"message":    {Type: geminiclient.TypeString},
						"suggestion": {Type: geminiclient.TypeString},
					},
					Required: []string{"severity", "file", "excerpt", "message"},
				},
			},
		},
		Required: []string{"title", "summary", "verdict", "findings"},
	}
}
