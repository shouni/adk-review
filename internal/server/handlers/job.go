package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

const (
	// jobsBasePath はジョブのパスです。投入から削除まで 1 件をこの配下で指し、
	// 詳細 URL と成果物 URL の組み立てにも使います。
	jobsBasePath = "/jobs"
	// reportSegment は、ジョブ配下で指摘の全文を指す末尾です。
	reportSegment = "report"
)

// JobDetailPageData はジョブ詳細テンプレートに渡すデータです。
type JobDetailPageData struct {
	Detail domain.ReviewDetail
	Error  string
	// CSRFToken は削除リクエストのヘッダへ載せる値です。フォーム送信ではないため、
	// 隠しフィールドから JavaScript が読み出します。
	CSRFToken string
}

// jobURL は、ジョブ 1 件（と、その配下の成果物）の絶対 URL を組み立てます。
func (h *Handler) jobURL(jobID string, segments ...string) string {
	u, err := url.JoinPath(h.cfg.Server.ServiceURL, append([]string{jobsBasePath, jobID}, segments...)...)
	if err != nil {
		// ServiceURL は起動時に検査済みで、jobID は Sanitize 済みなので通常は起きません。
		return ""
	}
	return u
}

// jobResponse は GET /jobs/{jobID} の JSON 応答です。
//
// domain.JobStatus を埋め込んでフラットに出すのは、投入直後からのポーリング先が
// この 1 本だからです。指摘の全文は載せず、ReportURL で GET /jobs/{jobID}/report を
// 指します。完了を確かめるたびに全文を返すには重すぎます。
type jobResponse struct {
	domain.JobStatus
	// ReportURL は指摘の全文の取得先です。成果物が無い間は省きます。
	ReportURL string `json:"report_url,omitempty"`
}

// HandleJob は、ジョブ 1 件を返します。
//
// JSON は進行状況と成果物へのリンク、HTML は詳細画面です。投入した瞬間から削除する
// までを同じ URL で指すので、呼び出し元は「今どちらを叩くべきか」を状態で切り替えずに
// 済みます。HTML が全文を含むのは、画面には別の取得先を用意する意味が無いためです。
func (h *Handler) HandleJob(w http.ResponseWriter, r *http.Request) {
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

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
			respond.ErrorJSON(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "進行状況の取得に失敗しました", "job_id", safeJobID, "error", err)
		respond.ErrorJSON(w, r, code, "進行状況を取得できませんでした。")
		return
	}

	out := jobResponse{JobStatus: status}
	if status.HasReport() {
		out.ReportURL = h.jobURL(safeJobID, reportSegment)
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// HandleJobReport は、レビュー結果の全文を返します（JSON のみ）。
//
// 実行中は 409、終わったが成果物が無い（失敗・スキップ）は 404 です。同じ「無い」でも
// 前者は待てば出るので、呼び出し元が再試行してよいかを状態コードで分けます。
func (h *Handler) HandleJobReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "error", err)
		respond.ErrorJSON(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
			respond.ErrorJSON(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "job_id", safeJobID, "error", err, "status", code)
		respond.ErrorJSON(w, r, code, "レビューを取得できませんでした。")
		return
	}

	if detail.Report == nil {
		if !detail.Status.Finished() {
			respond.ErrorJSON(w, r, http.StatusConflict, "レビューはまだ実行中です。")
			return
		}
		respond.ErrorJSON(w, r, http.StatusNotFound, "このレビューには成果物がありません。")
		return
	}
	respond.JSON(w, r, http.StatusOK, detail.Report)
}

// renderJobDetail は、ジョブ 1 件の詳細画面を描きます。
func (h *Handler) renderJobDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	rawJobID := chi.URLParam(r, "jobID")
	safeJobID, err := jobid.Sanitize(rawJobID)
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "job_id", rawJobID, "error", err)
		h.render(w, r, http.StatusBadRequest, reviewDetailTemplate, JobDetailPageData{Error: "ジョブIDの形式が不正です。"})
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code, message := detailFailure(ctx, safeJobID, err)
		h.render(w, r, code, reviewDetailTemplate, JobDetailPageData{Error: message})
		return
	}

	h.render(w, r, http.StatusOK, reviewDetailTemplate, JobDetailPageData{
		Detail:    detail,
		CSRFToken: CSRFTokenFromContext(ctx),
	})
}

// detailFailure は、詳細の読み出し失敗をログに残し、状態コードと利用者向けの文言を返します。
// 「まだ無い」だけが 404 です（recordErrorStatus の項）。
func detailFailure(ctx context.Context, jobID string, err error) (int, string) {
	code := recordErrorStatus(err)
	if code == http.StatusNotFound {
		slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", jobID, "error", err)
		return code, "指定されたレビューは見つかりませんでした。"
	}
	slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "job_id", jobID, "error", err, "status", code)
	return code, "レビューを取得できませんでした。"
}

// HandleJobDelete は、レビュー履歴を削除します（DELETE /jobs/{jobID}）。
//
// メソッドを DELETE にし、応答を本文なしで返すのは兄弟アプリと揃えるためです
// （ブラウザ側は assets/static/js/app.js の App.deleteResource が呼びます）。
// 失敗時の本文はそのまま画面のアラートに出るため、内部の詳細は載せません。
func (h *Handler) HandleJobDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "error", err)
		respond.Error(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
			respond.Error(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "job_id", safeJobID, "error", err, "status", code)
		respond.Error(w, r, code, "レビューを取得できませんでした。")
		return
	}

	// 判定の理由は domain.JobStatus.Deletable が持ちます。画面ではボタンを出して
	// いませんが、直接呼ばれても弾けるようここでも通します。
	if !detail.Status.Deletable() {
		slog.WarnContext(ctx, "実行中のレビューに削除要求がありました", "job_id", safeJobID, "state", detail.Status.State)
		respond.Error(w, r, http.StatusConflict, "実行中のレビューは削除できません。")
		return
	}

	if err := h.history.Delete(ctx, safeJobID); err != nil {
		slog.ErrorContext(ctx, "レビュー履歴の削除に失敗しました", "job_id", safeJobID, "error", err)
		respond.Error(w, r, http.StatusInternalServerError, "削除に失敗しました。")
		return
	}

	slog.InfoContext(ctx, "レビュー履歴を削除しました", "job_id", safeJobID)
	w.WriteHeader(http.StatusNoContent)
}
