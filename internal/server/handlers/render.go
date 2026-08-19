package handlers

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
	"github.com/shouni/go-utils/jst"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/giturl"
)

const (
	layoutTemplate       = "templates/layout.html"
	reviewFormTemplate   = "review_form.html"
	historyTemplate      = "history.html"
	reviewDetailTemplate = "review_detail.html"
)

// pageTemplates は、レイアウトと組み合わせて描画するページテンプレートです。
var pageTemplates = []string{reviewFormTemplate, historyTemplate, reviewDetailTemplate}

// parsePageTemplates は、ページごとに独立したテンプレートセットを構築します。
//
// 1 セットへまとめてパースできないのは、どのページも本文を {{define "content"}} で
// 定義しているためです。同じ名前の定義は後からパースしたものが前を上書きするので、
// まとめると 1 ページ分の本文しか残らず、他のページはそれを描いてしまいます。
func parsePageTemplates() (map[string]*template.Template, error) {
	set := make(map[string]*template.Template, len(pageTemplates))

	for _, page := range pageTemplates {
		tmpl, err := template.New(page).
			Funcs(templateFuncs()).
			ParseFS(assets.Templates, layoutTemplate, "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("テンプレート %s のパースに失敗しました: %w", page, err)
		}
		set[page] = tmpl
	}
	return set, nil
}

// render は、指定ページをレイアウトに載せて描画します。
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := h.templates[page]
	if !ok {
		slog.ErrorContext(r.Context(), "未登録のテンプレートです", "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 先にバッファへ書き出します。テンプレートの実行が途中で失敗しても、
	// 壊れた HTML を 200 で返さずにエラーへ倒せます。
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.ErrorContext(r.Context(), "テンプレート実行エラー", "error", err, "page", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		slog.ErrorContext(r.Context(), "レスポンス書き込みエラー", "error", err)
	}
}

// templateFuncs は、履歴の表示に使う整形関数です。
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"repoPath":      giturl.GetRepositoryPath,
		"formatTime":    formatTime,
		"stateLabel":    stateLabel,
		"stateClass":    stateClass,
		"decisionLabel": decisionLabel,
		"decisionClass": decisionClass,
		"severityClass": severityClass,
	}
}

// formatTime は日本時間で表示します。ゼロ値は空欄にします
// （未記録の時刻が 0001/01/01 として並ぶのを避けるためです）。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(jst.Location()).Format(jst.LayoutTimestamp)
}

// badge は、表示名とバッジのクラスの組です。
//
// 表示名とクラスを別々の switch で持つと、状態を増やしたときに片方だけ直す事故が
// 起きます（どちらも「状態 → 見た目」の同じ対応表です）。1 箇所に束ねます。
type badge struct {
	label string
	class string
}

// stateBadges は、進行状況の表示定義です。
// 「差分なしスキップ」はジョブとしては正常終了（succeeded）なので、State だけでは
// 完了と見分けがつきません。Outcome を併せて見る分だけ stateBadge が補います。
var stateBadges = map[jobstatus.State]badge{
	jobstatus.StateQueued:    {"⏱ 受付済み", "text-bg-light"},
	jobstatus.StateRunning:   {"⏳ 実行中", "text-bg-info"},
	jobstatus.StateFailed:    {"❌ 失敗", "text-bg-danger"},
	jobstatus.StateSucceeded: {"✅ 完了", "text-bg-success"},
}

// skippedBadge は、差分が無くスキップされた場合の表示です。
var skippedBadge = badge{"⏭️ スキップ", "text-bg-secondary"}

// unknownStateBadge は、未知の状態の表示です。
var unknownStateBadge = badge{"— 不明", "text-bg-light"}

// decisionBadges は、レビュー判定の表示定義です。
var decisionBadges = map[review.Decision]badge{
	review.DecisionNone:    {"✅ 問題なし", "text-bg-success"},
	review.DecisionMinor:   {"🟡 軽微", "text-bg-warning"},
	review.DecisionMajor:   {"🟠 要修正", "text-bg-warning"},
	review.DecisionBlocker: {"🔴 ブロッカー", "text-bg-danger"},
}

// unknownDecisionBadge は、未知の判定の表示です。
var unknownDecisionBadge = badge{"—", "text-bg-light"}

// stateBadge は、進行状況と結末に対応する表示を返します。
func stateBadge(state jobstatus.State, outcome review.Status) badge {
	if state == jobstatus.StateSucceeded && outcome == review.StatusSkipped {
		return skippedBadge
	}
	if b, ok := stateBadges[state]; ok {
		return b
	}
	return unknownStateBadge
}

// decisionBadge は、判定に対応する表示を返します。
func decisionBadge(decision review.Decision) badge {
	if b, ok := decisionBadges[decision]; ok {
		return b
	}
	return unknownDecisionBadge
}

// stateLabel は、進行状況と結末をあわせた表示名を返します。
func stateLabel(state jobstatus.State, outcome review.Status) string {
	return stateBadge(state, outcome).label
}

// stateClass は、状態バッジの Bootstrap クラスを返します。
func stateClass(state jobstatus.State, outcome review.Status) string {
	return stateBadge(state, outcome).class
}

// decisionLabel は、レビュー判定の表示名を返します。
func decisionLabel(decision review.Decision) string {
	return decisionBadge(decision).label
}

// decisionClass は、判定バッジの Bootstrap クラスを返します。
func decisionClass(decision review.Decision) string {
	return decisionBadge(decision).class
}

// severityClass は、指摘の重大度バッジの Bootstrap クラスを返します。
//
// Decision とは型が違うため別関数にしています。テンプレートの関数は型で解決されるので、
// 片方を使い回すと実行時に型不一致で落ちます。
func severityClass(severity review.Severity) string {
	return decisionClass(review.Decision(severity))
}
