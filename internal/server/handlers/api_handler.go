package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/adk-review/internal/domain"

	"github.com/shouni/gcp-kit/negotiate"
)

// ModeInfo は、選べるレビューモード 1 件です。
//
// 出どころは assets/prompts/*.md の front matter です。呼び出し元にモード名を
// 直書きさせないための一覧で、モードを足しても呼び出し元を変えずに済みます。
type ModeInfo struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	UseWhen   string `json:"use_when"`
	Excerpt   string `json:"excerpt"`
}

// modesResponse は GET /modes の応答です。
type modesResponse struct {
	Modes []ModeInfo `json:"modes"`
}

// HandleModes は、選べるレビューモードの一覧を返します。
//
// このルートだけは HTML を持ちません。画面はフォームの <select> がモード一覧を
// 兼ねているため、人間向けの表示先が既にあります。
func (h *Handler) HandleModes(w http.ResponseWriter, r *http.Request) {
	modes, err := assets.AvailableModes()
	if err != nil {
		// プロンプト資産の破損なので、起動していれば通常は起きません。
		slog.ErrorContext(r.Context(), "レビューモード一覧の読み込みに失敗しました", "error", err)
		negotiate.JSON(w, r, http.StatusInternalServerError,
			errorResponse{Error: "レビューモードを読み込めませんでした。"})
		return
	}

	items := make([]ModeInfo, 0, len(modes))
	for _, mode := range modes {
		items = append(items, ModeInfo{
			Key:       mode.Key,
			Label:     mode.DisplayName(),
			Direction: mode.Direction,
			UseWhen:   mode.UseWhen,
			Excerpt:   string(mode.ExcerptKind()),
		})
	}
	negotiate.JSON(w, r, http.StatusOK, modesResponse{Modes: items})
}

// HandleJobStatus は、ジョブ 1 件の進行状況だけを返します。
//
// 詳細（HandleReviewDetail）と分けているのは、**完了検知のポーリング先だから**です。
// 詳細は指摘の全文を含むので、状態を確かめるたびに返すには重すぎます。
//
// このルートも HTML を持ちません。人間向けには履歴の詳細画面があります。
func (h *Handler) HandleJobStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ジョブ ID はストレージのパス要素になるため、受け取った時点で正規化します。
	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "error", err)
		negotiate.JSON(w, r, http.StatusBadRequest, errorResponse{Error: "ジョブIDの形式が不正です。"})
		return
	}

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
			negotiate.JSON(w, r, code, errorResponse{Error: "指定されたレビューは見つかりませんでした。"})
			return
		}
		slog.ErrorContext(ctx, "進行状況の取得に失敗しました", "job_id", safeJobID, "error", err)
		negotiate.JSON(w, r, code, errorResponse{Error: "進行状況を取得できませんでした。"})
		return
	}

	negotiate.JSON(w, r, http.StatusOK, status)
}

// reviewDetailResponse は GET /history/{jobID} の JSON 応答です。
//
// domain.ReviewDetail をそのまま出さずに包むのは、Report が nil のときに
// "report": null と明示したいためです（実行中・失敗・スキップでは成果物がありません）。
type reviewDetailResponse struct {
	Status domain.JobStatus `json:"status"`
	Report any              `json:"report"`
}
