package handlers

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/internal/domain"
)

const (
	// historyBasePath は履歴ページのパスです。詳細URLの組み立てにも使います。
	historyBasePath = "/history"
	// defaultPerPage は履歴一覧の 1 ページあたりの件数です。
	defaultPerPage = 20
	// maxPerPage は per_page に許容する上限です。1 ページの読み取り件数がそのまま
	// ストレージへの往復数になるため、際限なく増やせないようにします。
	maxPerPage = 100
)

// HistoryPageData は履歴一覧テンプレートに渡すデータです。
type HistoryPageData struct {
	Items []domain.JobStatus
	Meta  domain.PageMeta
	Error string
}

// ReviewDetailPageData は履歴詳細テンプレートに渡すデータです。
type ReviewDetailPageData struct {
	Detail domain.ReviewDetail
	Error  string
	// CSRFToken は削除リクエストのヘッダへ載せる値です。フォーム送信ではないため、
	// 隠しフィールドから JavaScript が読み出します。
	CSRFToken string
}

// HandleHistory は履歴一覧を表示します。
func (h *Handler) HandleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page := positiveIntParam(r, "page", 1)
	perPage := clamp(positiveIntParam(r, "per_page", defaultPerPage), 1, maxPerPage)

	result, err := h.history.List(ctx, page, perPage)
	if err != nil {
		// 「読めなかった」を 500 に潰さず 502 として返します。潰すと、権限剥奪や
		// ストレージ障害がアプリ側の不具合として報告され、呼び出し元は再試行すべき
		// 場面で諦めます。
		code := recordErrorStatus(err)
		slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "error", err, "status", code)
		const message = "履歴を取得できませんでした。時間をおいて再度お試しください。"
		if wantsJSON(r) {
			writeJSON(w, r, code, errorResponse{Error: message})
			return
		}
		h.render(w, r, code, historyTemplate, HistoryPageData{Error: message})
		return
	}

	if wantsJSON(r) {
		writeJSON(w, r, http.StatusOK, result)
		return
	}
	h.render(w, r, http.StatusOK, historyTemplate, HistoryPageData{
		Items: result.Items,
		Meta:  result.Meta,
	})
}

// HandleReviewDetail は履歴 1 件の詳細を表示します。
func (h *Handler) HandleReviewDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ジョブ ID はストレージのパス要素になるため、URL から受け取った時点で検証します。
	rawJobID := chi.URLParam(r, "jobID")
	safeJobID, err := jobid.Sanitize(rawJobID)
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "job_id", rawJobID, "error", err)
		const message = "ジョブIDの形式が不正です。"
		if wantsJSON(r) {
			writeJSON(w, r, http.StatusBadRequest, errorResponse{Error: message})
			return
		}
		h.render(w, r, http.StatusBadRequest, reviewDetailTemplate, ReviewDetailPageData{Error: message})
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		// 「まだ記録が無い」だけを 404 にします。読み取り自体が失敗した場合は 502 です。
		// 両者を同一視すると、権限剥奪やストレージ障害が「そんなレビューはありません」
		// として表示されます。
		code := recordErrorStatus(err)
		message := "レビューを取得できませんでした。"
		if code == http.StatusNotFound {
			message = "指定されたレビューは見つかりませんでした。"
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
		} else {
			slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "job_id", safeJobID, "error", err, "status", code)
		}
		if wantsJSON(r) {
			writeJSON(w, r, code, errorResponse{Error: message})
			return
		}
		h.render(w, r, code, reviewDetailTemplate, ReviewDetailPageData{Error: message})
		return
	}

	if wantsJSON(r) {
		writeJSON(w, r, http.StatusOK, reviewDetailResponse{Status: detail.Status, Report: detail.Report})
		return
	}
	h.render(w, r, http.StatusOK, reviewDetailTemplate, ReviewDetailPageData{
		Detail:    detail,
		CSRFToken: CSRFTokenFromContext(ctx),
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

// HandleReviewDelete は、レビュー履歴を削除します。
//
// メソッドを DELETE にし、応答を本文なしで返すのは兄弟アプリと揃えるためです
// （ブラウザ側は assets/static/js/app.js の App.deleteResource が呼びます）。
// 失敗時の本文はそのまま画面のアラートに出るため、内部の詳細は載せません。
func (h *Handler) HandleReviewDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// ジョブ ID はストレージのパス要素になるため、受け取った時点で正規化します。
	safeJobID, err := jobid.Sanitize(chi.URLParam(r, "jobID"))
	if err != nil {
		slog.WarnContext(ctx, "不正なジョブIDを受け取りました", "error", err)
		writeError(w, r, http.StatusBadRequest, "ジョブIDの形式が不正です。")
		return
	}

	detail, err := h.history.Get(ctx, safeJobID)
	if err != nil {
		code := recordErrorStatus(err)
		if code == http.StatusNotFound {
			slog.WarnContext(ctx, "レビュー履歴が見つかりません", "job_id", safeJobID, "error", err)
			writeError(w, r, code, "指定されたレビューは見つかりませんでした。")
			return
		}
		slog.ErrorContext(ctx, "レビュー履歴の取得に失敗しました", "job_id", safeJobID, "error", err, "status", code)
		writeError(w, r, code, "レビューを取得できませんでした。")
		return
	}

	// 実行中のものを消すと、ワーカーがあとから status.json を書き戻して復活します。
	// 画面ではボタンを出していませんが、直接呼ばれても弾けるようここでも判定します。
	if !detail.Status.Deletable() {
		slog.WarnContext(ctx, "実行中のレビューに削除要求がありました", "job_id", safeJobID, "state", detail.Status.State)
		writeError(w, r, http.StatusConflict, "実行中のレビューは削除できません。")
		return
	}

	if err := h.history.Delete(ctx, safeJobID); err != nil {
		slog.ErrorContext(ctx, "レビュー履歴の削除に失敗しました", "job_id", safeJobID, "error", err)
		writeError(w, r, http.StatusInternalServerError, "削除に失敗しました。")
		return
	}

	slog.InfoContext(ctx, "レビュー履歴を削除しました", "job_id", safeJobID)
	w.WriteHeader(http.StatusNoContent)
}
