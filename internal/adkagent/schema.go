package adkagent

import (
	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// maxFindings は、1 レビューで返させる指摘の上限です。
//
// 出力の 64Ki トークンに収めるためのものです。上限に当たると、途中まで正しく
// 書けていた JSON ごと全損になります（実測で 10.7 KiB の差分に 212 KiB を書いて
// 切れた例があります。Blocker の指摘が完成した状態で失われました）。
//
// 20 なのは、実測の指摘数が最大 2 件で、20 でも正常系には当たらないからです。
// これは「20 件まで指摘してよい」という目標ではなく、暴走を止める網です。
// instruction が語る数字もここから埋めます（直書きすると片方だけ動いたときに、
// モデルは実際と違う上限のつもりで書きます）。
const maxFindings = 20

// maxTitleLength は、title の長さの上限です。
//
// 履歴一覧は title を 1 列へそのまま流し込み、切り詰めません
// （assets/templates/history.html）。長い題が来ると表が崩れるので、画面側で切るのでは
// なくここで頼みます。狙いの長さ（30 文字程度）は verdict_format.md が伝えていて、
// この数字はそれを 2 倍に取った最後の歯止めです。excerpt と同じく硬い強制ではありません。
const maxTitleLength = 60

// maxExcerptLength は、excerpt 1 件の長さの上限です。
//
// 切れた実行の末尾は、同じコード片（`Server(t *testing.T) {`）の反復で埋まって
// いました。膨らむのはたいてい引用です。指摘そのものの長さは縛りません。
const maxExcerptLength = 500

// reportSchema は、review.Report に対応する構造化出力スキーマです。
//
// 列挙値は review パッケージの定義から組み立てます。アプリ側で []string へ詰め替え
// 直さないでください。写しが増えると、値を足したときにスキーマと検証が食い違い、
// モデルはスキーマ上正当な値を返すのにデコードで弾かれます。
//
// findings に evidence を含めるのは、このレビュアーが作業ディレクトリを実際に調べるため、
// 「どこを見て判断したか」を自己申告させる意味があるからです。差分しか読めない
// レビュアーへ同じ項目を持ち込まないでください。確認する手段が無いまま根拠を
// 求めると、モデルはそれを捏造して埋めます。
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
				// ★ 長さの制約は Enum ほど硬く強制されません。それでも置くのは、
				// 今まで「どれだけ書いてよいか」を一言も伝えていなかったためです。
				// 効いているかはログの response_bytes で見てください。
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
