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

// stateLabel は、進行状況と結末をあわせた表示名を返します。
//
// 「差分なしスキップ」はジョブとしては正常終了（succeeded）なので、State だけでは
// 完了と見分けがつきません。Outcome を併せて見ます。
func stateLabel(state jobstatus.State, outcome review.Status) string {
	switch state {
	case jobstatus.StateQueued:
		return "⏱ 受付済み"
	case jobstatus.StateRunning:
		return "⏳ 実行中"
	case jobstatus.StateFailed:
		return "❌ 失敗"
	case jobstatus.StateSucceeded:
		if outcome == review.StatusSkipped {
			return "⏭️ スキップ"
		}
		return "✅ 完了"
	default:
		return "— 不明"
	}
}

// stateClass は、状態バッジの Bootstrap クラスを返します。
func stateClass(state jobstatus.State, outcome review.Status) string {
	switch state {
	case jobstatus.StateQueued:
		return "text-bg-light"
	case jobstatus.StateRunning:
		return "text-bg-info"
	case jobstatus.StateFailed:
		return "text-bg-danger"
	case jobstatus.StateSucceeded:
		if outcome == review.StatusSkipped {
			return "text-bg-secondary"
		}
		return "text-bg-success"
	default:
		return "text-bg-light"
	}
}

// decisionLabel は、レビュー判定の表示名を返します。
func decisionLabel(decision review.Decision) string {
	switch decision {
	case review.DecisionNone:
		return "✅ 問題なし"
	case review.DecisionMinor:
		return "🟡 軽微"
	case review.DecisionMajor:
		return "🟠 要修正"
	case review.DecisionBlocker:
		return "🔴 ブロッカー"
	default:
		return "—"
	}
}

// severityClass は、指摘の重大度バッジの Bootstrap クラスを返します。
//
// Decision とは型が違うため別関数にしています。テンプレートの関数は型で解決されるので、
// 片方を使い回すと実行時に型不一致で落ちます。
func severityClass(severity review.Severity) string {
	return decisionClass(review.Decision(severity))
}

// decisionClass は、判定バッジの Bootstrap クラスを返します。
func decisionClass(decision review.Decision) string {
	switch decision {
	case review.DecisionNone:
		return "text-bg-success"
	case review.DecisionMinor:
		return "text-bg-warning"
	case review.DecisionMajor:
		return "text-bg-warning"
	case review.DecisionBlocker:
		return "text-bg-danger"
	default:
		return "text-bg-light"
	}
}
