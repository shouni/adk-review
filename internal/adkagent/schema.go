package adkagent

import (
	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// reportSchema は、review.Report に対応する構造化出力スキーマです。
//
// go-review-kit の gemini アダプターが持つスキーマと同じく、列挙値は review パッケージの
// 定義から組み立てます。あちらと違い findings に evidence を含めるのは、このレビュアーは
// 作業ディレクトリを実際に調べるため、「どこを見て判断したか」を自己申告させる意味が
// あるからです（単発レビュアーに出させると根拠の捏造を促すため、あちらには含めません）。
func reportSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title":   {Type: genai.TypeString},
			"summary": {Type: genai.TypeString},
			"verdict": {
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"decision": {Type: genai.TypeString, Enum: decisionEnum()},
					"reason":   {Type: genai.TypeString},
				},
				Required: []string{"decision", "reason"},
			},
			"findings": {
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"severity":   {Type: genai.TypeString, Enum: severityEnum()},
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

// severityEnum は、findings[].severity が取りうる値を返します。
func severityEnum() []string {
	values := review.Severities()
	enum := make([]string, 0, len(values))
	for _, v := range values {
		enum = append(enum, string(v))
	}
	return enum
}

// decisionEnum は、verdict.decision が取りうる値を返します。
func decisionEnum() []string {
	values := review.Decisions()
	enum := make([]string, 0, len(values))
	for _, v := range values {
		enum = append(enum, string(v))
	}
	return enum
}
