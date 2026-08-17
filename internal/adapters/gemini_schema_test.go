package adapters

import (
	"slices"
	"testing"

	geminiclient "github.com/shouni/go-gemini-client/gemini"

	"github.com/shouni/go-review-kit/review"
)

// スキーマの列挙値は review パッケージの定義から組み立てます。両者が食い違うと、
// モデルがスキーマ上は正当な値を返したのにデコードで弾かれる、という状態になります。
func TestSchemaEnumsMatchDomain(t *testing.T) {
	schema := reportSchema()

	verdict, ok := schema.Properties["verdict"]
	if !ok {
		t.Fatal("スキーマに verdict がありません")
	}
	gotDecisions := verdict.Properties["decision"].Enum

	wantDecisions := make([]string, 0, len(review.Decisions()))
	for _, d := range review.Decisions() {
		wantDecisions = append(wantDecisions, string(d))
	}
	if !slices.Equal(gotDecisions, wantDecisions) {
		t.Errorf("decision の列挙 = %v, want %v", gotDecisions, wantDecisions)
	}

	findings, ok := schema.Properties["findings"]
	if !ok {
		t.Fatal("スキーマに findings がありません")
	}
	gotSeverities := findings.Items.Properties["severity"].Enum

	wantSeverities := make([]string, 0, len(review.Severities()))
	for _, s := range review.Severities() {
		wantSeverities = append(wantSeverities, string(s))
	}
	if !slices.Equal(gotSeverities, wantSeverities) {
		t.Errorf("severity の列挙 = %v, want %v", gotSeverities, wantSeverities)
	}
}

// スキーマの必須項目は review.Report のデコードが前提とする形と一致している必要があります。
func TestSchemaShape(t *testing.T) {
	schema := reportSchema()

	if schema.Type != geminiclient.TypeObject {
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
	if findings.Type != geminiclient.TypeArray || findings.Items == nil {
		t.Fatal("findings が配列として定義されていません")
	}

	for _, key := range []string{"severity", "file", "excerpt", "message"} {
		if !slices.Contains(findings.Items.Required, key) {
			t.Errorf("findings[].%s が必須になっていません", key)
		}
	}

	// line と suggestion は任意です。指摘の位置が特定できない場合や、修正案を
	// 出せない場合にモデルが行き詰まらないようにするためです。
	for _, key := range []string{"line", "suggestion"} {
		if _, ok := findings.Items.Properties[key]; !ok {
			t.Errorf("findings[].%s がスキーマにありません", key)
		}
		if slices.Contains(findings.Items.Required, key) {
			t.Errorf("findings[].%s は任意であるべきです", key)
		}
	}

	// evidence は意図的にスキーマへ含めません（gemini_schema.go のコメント参照）。
	if _, ok := findings.Items.Properties["evidence"]; ok {
		t.Error("単発レビュアーのスキーマに evidence を含めてはいけません")
	}
}
