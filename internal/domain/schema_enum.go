package domain

import "github.com/shouni/go-review-kit/review"

// SeverityEnum は、findings[].severity が取りうる値を文字列で返します。
// DecisionEnum は、verdict.decision が取りうる値を文字列で返します。
//
// 単発レビュアー（internal/adapters）とエージェント（internal/adkagent）は、
// SDK の型が違うため出力スキーマを別々に組み立てますが、列挙値まで別々に持つと
// review パッケージに値が増えたとき片方だけ古いままになりえます。
// **食い違うとモデルがスキーマ上は正当な値を返してデコードで弾かれる**ので、
// 値の出どころはここ 1 箇所に集約します。
func SeverityEnum() []string {
	values := review.Severities()
	enum := make([]string, 0, len(values))
	for _, v := range values {
		enum = append(enum, string(v))
	}
	return enum
}

// DecisionEnum は、verdict.decision が取りうる値を文字列で返します。
// 集約する理由は SeverityEnum のコメントを参照してください。
func DecisionEnum() []string {
	values := review.Decisions()
	enum := make([]string, 0, len(values))
	for _, v := range values {
		enum = append(enum, string(v))
	}
	return enum
}
