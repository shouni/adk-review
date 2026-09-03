package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
)

const (
	// defaultPerPage は履歴一覧の 1 ページあたりの件数です。
	defaultPerPage = 20
	// maxPerPage は per_page に許容する上限です。1 ページの読み取り件数がそのまま
	// ストレージへの往復数になるため、際限なく増やせないようにします。
	maxPerPage = 100
)

// JobListPageData は履歴一覧テンプレートに渡すデータです。
type JobListPageData struct {
	Items []domain.JobStatus
	Meta  domain.PageMeta
	Error string
}

// HandleJobList はジョブ一覧を表示します（GET /jobs）。
func (h *Handler) HandleJobList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := positiveIntParam(r, "page", 1)
	perPage := clamp(positiveIntParam(r, "per_page", defaultPerPage), 1, maxPerPage)

	result, err := h.history.List(ctx, page, perPage)
	if err != nil {
		// 「まだ無い」と「読めなかった」は別物として返します（recordErrorStatus の項）。
		code := recordErrorStatus(err)
		slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "error", err, "status", code)
		const message = "履歴を取得できませんでした。時間をおいて再度お試しください。"
		if respond.WantsJSON(w, r) {
			respond.ErrorJSON(w, r, code, message)
			return
		}
		h.render(w, r, code, historyTemplate, JobListPageData{Error: message})
		return
	}

	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusOK, result)
		return
	}
	h.render(w, r, http.StatusOK, historyTemplate, JobListPageData{
		Items: result.Items,
		Meta:  result.Meta,
	})
}

// positiveIntParam はクエリから正の整数を読み取ります。
// 未指定・不正値・0 以下はすべて既定値へ落とします。ページ番号の指定ミスで
// 画面がエラーになるより、1 ページ目を出すほうが扱いやすいためです。
func positiveIntParam(r *http.Request, name string, fallback int) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
