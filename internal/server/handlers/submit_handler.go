package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/internal/domain"
)

// HandleReviewSubmit は POST リクエストを処理します。
func (h *Handler) HandleReviewSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		data := defaultReviewFormPageData()
		data.Error = "リクエストのパースに失敗しました。"
		h.renderForm(w, r, http.StatusBadRequest, data)
		return
	}

	// 1. フォーム値の取得
	req := domain.ReviewRequest{
		RepoURL:       strings.TrimSpace(r.PostFormValue("repo_url")),
		BaseBranch:    strings.TrimSpace(r.PostFormValue("base_branch")),
		FeatureBranch: strings.TrimSpace(r.PostFormValue("feature_branch")),
		Mode:          r.PostFormValue("review_mode"),
		ModelName:     r.PostFormValue("gemini_model"),
		Engine:        r.PostFormValue("engine"),
	}

	// 2. 入力バリデーション
	if err := h.validateReviewRequest(req); err != nil {
		h.renderForm(w, r, http.StatusBadRequest, reviewFormPageData(req, ReviewFormPageData{
			Error: err.Error(),
		}))
		return
	}

	// 3. ジョブIDの採番と、成果物・閲覧先の決定
	//
	// 保存先も閲覧先もジョブIDから決まります。ワーカー側で組み立て直さないのは、
	// 投入時に決めた場所と食い違わないようにするためです。
	if err := h.assignJob(&req); err != nil {
		slog.ErrorContext(ctx, "ジョブIDの採番に失敗", "error", err)
		h.renderForm(w, r, http.StatusInternalServerError, reviewFormPageData(req, ReviewFormPageData{
			Error: "内部サーバーエラーが発生しました。",
		}))
		return
	}

	// 4. Cloud Tasks へのタスク投入
	if err := h.taskEnqueuer.Enqueue(ctx, req); err != nil {
		slog.ErrorContext(ctx, "Cloud Tasksへの投入失敗", "error", err, "repo", req.RepoURL, "job_id", req.JobID)
		h.renderForm(w, r, http.StatusServiceUnavailable, reviewFormPageData(req, ReviewFormPageData{
			Error: "現在レビューの受け付けができません。時間をおいて再度お試しください。",
		}))
		return
	}

	// 5. 受付を履歴へ残す
	//
	// 投入したあとに記録するのは、積めていないジョブを履歴に出さないためです。
	// 記録に失敗しても投入自体は成立しているので、受付は成功として返します
	// （ワーカーが running を記録した時点で履歴には現れます）。
	h.recordQueued(ctx, req)

	// 6. 成功応答を返します
	slog.InfoContext(ctx, "レビュータスク投入成功", "repo", req.RepoURL, "job_id", req.JobID)
	h.renderForm(w, r, http.StatusAccepted, reviewFormPageData(req, ReviewFormPageData{
		Message:   "✅ レビュータスクを受け付けました。完了後、以下のリンクから結果を確認できます。",
		ResultURL: req.PublicURL,
	}))
}

// assignJob は、ジョブIDを採番して保存先と閲覧先を決めます。
func (h *Handler) assignJob(req *domain.ReviewRequest) error {
	jobID, err := h.newJobID()
	if err != nil {
		return err
	}

	detailURL, err := url.JoinPath(h.cfg.Server.ServiceURL, historyBasePath, jobID)
	if err != nil {
		return fmt.Errorf("詳細URLの構築に失敗しました: %w", err)
	}

	req.JobID = jobID
	req.StorageURI = h.layout.ReportURI(jobID)
	req.PublicURL = detailURL
	return nil
}

// recordQueued は、受付済みであることを進行状況へ記録します。
func (h *Handler) recordQueued(ctx context.Context, req domain.ReviewRequest) {
	status := domain.NewQueuedStatus(req)
	status.QueuedAt = h.now()

	if err := h.statusStore.Save(ctx, req.JobID, status); err != nil {
		slog.WarnContext(ctx, "受付の記録に失敗しました", "job_id", req.JobID, "error", err)
		return
	}

	// 投入直後に一覧へ現れるよう、ジョブID一覧のキャッシュを捨てます。
	h.history.Invalidate()
}

// newJobID はジョブIDを採番します。
func newJobID() (string, error) {
	return jobid.New("")
}

func defaultReviewFormPageData() ReviewFormPageData {
	return ReviewFormPageData{
		BaseBranch:    defaultBaseBranch,
		FeatureBranch: defaultFeatureBranch,
		ReviewMode:    defaultReviewMode,
	}
}

func reviewFormPageData(req domain.ReviewRequest, data ReviewFormPageData) ReviewFormPageData {
	data.RepoURL = req.RepoURL
	data.BaseBranch = req.BaseBranch
	data.FeatureBranch = req.FeatureBranch
	data.ReviewMode = req.Mode
	data.GeminiModel = req.ModelName
	data.Engine = req.Engine
	return data
}
