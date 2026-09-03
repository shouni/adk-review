package handlers

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// reviewDetailResponse は GET /history/{jobID}（旧パス）の JSON 応答です。
//
// 進行状況と全文を 1 度に返す形で、MCP サーバーがこの形で読んでいる間だけ残します。
// 新しい形は jobResponse と GET /jobs/{jobID}/report です。
//
// domain.ReviewDetail をそのまま出さずに包むのは、Report が nil のときに
// "report": null と明示したいためです（実行中・失敗・スキップでは成果物がありません）。
type reviewDetailResponse struct {
	Status domain.JobStatus `json:"status"`
	Report any              `json:"report"`
}

// HandleLegacyReviewDetail は、旧パス GET /history/{jobID} です。
//
// HTML は HandleJob と同じ詳細画面です。JSON だけ旧い形（status と report を 1 度に
// 返す）で、MCP サーバーが GET /jobs/{jobID} と /report に切り替わったら
// ルートごと消します。
func (h *Handler) HandleLegacyReviewDetail(w http.ResponseWriter, r *http.Request) {
	if !respond.WantsJSON(w, r) {
		h.renderJobDetail(w, r)
		return
	}

	ctx := r.Context()
	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "error", err)
		respond.ErrorJSON(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code, message := detailFailure(ctx, safeJobID, err)
		respond.ErrorJSON(w, r, code, message)
		return
	}
	respond.JSON(w, r, http.StatusOK, reviewDetailResponse{Status: detail.Status, Report: detail.Report})
}
