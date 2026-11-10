package adkagent

import (
	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// maxFindings is the limit on findings returned in one review.
//
// It keeps the output within 64Ki tokens. Hitting that limit loses everything, including the
// JSON that was correct up to that point (in practice a 10.7 KiB diff got 212 KiB written and
// was cut, losing a completed Blocker finding).
//
// It is 20 because the most findings measured is 2, so normal runs never reach 20.
// It is not a target of "up to 20 findings is fine" but a net that stops a runaway.
// The number the instruction quotes is filled from here too (written in by hand, if only
// one moved the model would write against a limit other than the real one).
const maxFindings = 20

// maxTitleLength is the limit on the title's length.
//
// The history list puts the title into one column as is, without truncating it
// (assets/templates/history.html). A long title breaks the table, so the length is requested
// here rather than cut on screen. The target length (about 30 characters) is stated by
// verdict_format.md; this number, twice that, is the last safeguard. Like excerpt, it is not
// strictly enforced.
const maxTitleLength = 60

// maxExcerptLength is the limit on the length of one excerpt.
//
// The tail of a cut run was filled with the same code fragment (`Server(t *testing.T) {`)
// repeated. What balloons is usually the quote. The finding text itself is not limited.
const maxExcerptLength = 500

// reportSchema is the structured output schema matching review.Report.
//
// The enumerated values are built from the review package's definitions. Do not repack them
// into a []string in the app. With more copies, adding a value makes the schema and validation
// disagree, and the model returns a value valid by the schema only to be rejected on decode.
//
// findings carry evidence because this reviewer actually investigates the working directory,
// so having it declare "where it looked to decide" means something. Do not carry the same field
// over to a reviewer that reads only the diff. Asking for grounds with no means to check them
// makes the model fill them in with fabrications.
func reportSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"title": {
				Type:        genai.TypeString,
				MaxLength:   new(int64(maxTitleLength)),
				Description: "何の変更かが分かる 1 行。一覧表の 1 列に収まる長さにする",
			},
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
				// ★ A length constraint is not enforced as strictly as Enum. It is set anyway
				// because until now nothing told the model how much it may write.
				// Check in the logs' response_bytes whether it works.
				MaxItems:    new(int64(maxFindings)),
				Description: "重大な順に並べ、重要でないものは落とす",
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"severity": {Type: genai.TypeString, Enum: review.SeverityStrings()},
						"file":     {Type: genai.TypeString},
						"line":     {Type: genai.TypeInteger},
						"excerpt": {
							Type:        genai.TypeString,
							MaxLength:   new(int64(maxExcerptLength)),
							Description: "指摘した箇所だけを引用する。前後の文脈やファイル全体は含めない",
						},
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
