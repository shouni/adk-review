package adkagent

import (
	"slices"
	"testing"

	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// スキーマの列挙値は review パッケージの定義から組み立てます。両者が食い違うと、
// モデルがスキーマ上は正当な値を返したのにデコードで弾かれる、という状態になります。
func TestSchemaEnumsMatchDomain(t *testing.T) {
	t.Parallel()

	schema := reportSchema()

	verdict, ok := schema.Properties["verdict"]
	if !ok {
		t.Fatal("スキーマに verdict がありません")
	}
	if got, want := verdict.Properties["decision"].Enum, review.DecisionStrings(); !slices.Equal(got, want) {
		t.Errorf("decision の列挙 = %v, want %v", got, want)
	}

	findings, ok := schema.Properties["findings"]
	if !ok {
		t.Fatal("スキーマに findings がありません")
	}
	if got, want := findings.Items.Properties["severity"].Enum, review.SeverityStrings(); !slices.Equal(got, want) {
		t.Errorf("severity の列挙 = %v, want %v", got, want)
	}
}

// スキーマの必須項目は review.Report のデコードが前提とする形と一致している必要があります。
func TestSchemaShape(t *testing.T) {
	t.Parallel()

	schema := reportSchema()

	if schema.Type != genai.TypeObject {
		t.Fatalf("トップレベルの型 = %v", schema.Type)
	}

	for _, key := range []string{"title", "summary", "verdict", "findings"} {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("スキーマに %s がありません", key)
		}
		if !slices.Contains(schema.Required, key) {
			t.Errorf("%s が必須になっていません", key)
		}
	}

	findings := schema.Properties["findings"]
	if findings.Type != genai.TypeArray || findings.Items == nil {
		t.Fatal("findings が配列として定義されていません")
	}

	for _, key := range []string{"severity", "file", "excerpt", "message"} {
		if !slices.Contains(findings.Items.Required, key) {
			t.Errorf("findings[].%s が必須になっていません", key)
		}
	}

	// evidence はこちらにだけ含めます。作業ディレクトリを実際に調べるレビュアーなので、
	// 「どこを見て判断したか」を自己申告させる意味があります（schema.go のコメント参照）。
	if _, ok := findings.Items.Properties["evidence"]; !ok {
		t.Error("エージェントのスキーマには evidence が必要です")
	}
}
