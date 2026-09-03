package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/go-serve-kit/respond"
	"github.com/shouni/go-utils/jobid"
)

// maxSubmitBody は、JSON で受け取る投入内容の上限です。
// 入力は短い文字列 5 つなので、これを超えるものは誤りか攻撃です。
const maxSubmitBody = 16 << 10

// submitInput は、JSON で投入するときの入力です。
//
// domain.ReviewRequest を直接デコードしません。そうすると呼び出し元が JobID /
// StorageURI / PublicURL を指定できてしまい、成果物をバケット内の任意のパスへ
// 書かせられます。採番と配置は assignJob だけの責務です。
//
// 項目名はフォームの name と同じです（どちらも domain.ReviewRequest の json タグに
// 揃えてあるので、読み取りの分岐だけで済みます）。
type submitInput struct {
	RepoURL       string `json:"repo_url"`
	BaseBranch    string `json:"base_branch"`
	FeatureBranch string `json:"feature_branch"`
	Mode          string `json:"mode"`
	ModelName     string `json:"model_name"`
}

// submitResponse は、投入を受け付けたときの JSON 応答です。
//
// 進行状況は返しません。受付直後は必ず queued なので、載せても呼び出し元は
// 結局 GET /jobs/{jobID} を叩きます。
type submitResponse struct {
	JobID     string `json:"job_id"`
	DetailURL string `json:"detail_url"`
}

// HandleJobCreate は、レビューを投入します（POST /jobs）。
//
// フォームと JSON body の両方を受け付けます。処理の中身は同じで、入力の読み取りと
// 応答の形だけが分かれます。
func (h *Handler) HandleJobCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 1. 入力の取得（フォーム / JSON）
	req, err := readSubmitRequest(r)
	if err != nil {
		h.submitFailure(w, r, req, http.StatusBadRequest, err.Error())
		return
	}

	// 2. 入力バリデーション
	if err := h.validateReviewRequest(req); err != nil {
		h.submitFailure(w, r, req, http.StatusBadRequest, err.Error())
		return
	}

	// 3. ジョブ ID の採番と、成果物・閲覧先の決定
	//
	// 保存先も閲覧先もジョブ ID から決まります。ワーカー側で組み立て直さないのは、
	// 投入時に決めた場所と食い違わないようにするためです。
	if err := h.assignJob(&req); err != nil {
		slog.ErrorContext(ctx, "ジョブIDの採番に失敗", "error", err)
		h.submitFailure(w, r, req, http.StatusInternalServerError, "内部サーバーエラーが発生しました。")
		return
	}

	// 4. 受付を履歴へ残す
	//
	// ★ 投入より先に記録します。あとに回すと、Cloud Tasks の配送が数十ミリ秒で
	// 届くため「ワーカーが running を書く → web が queued で踏み潰す」順序が起こり、
	// 実行中のジョブが履歴で受付済みのまま止まって見えます。
	// 積めなかったジョブを履歴に残さない配慮は、下の投入失敗時の取り消しで担保します。
	h.recordQueued(ctx, req)

	// 5. Cloud Tasks へのタスク投入
	if err := h.taskEnqueuer.Enqueue(ctx, req); err != nil {
		slog.ErrorContext(ctx, "Cloud Tasksへの投入失敗", "error", err, "repo", req.RepoURL, "job_id", req.JobID)
		h.discardQueued(ctx, req.JobID)
		h.submitFailure(w, r, req, http.StatusServiceUnavailable,
			"現在レビューの受け付けができません。時間をおいて再度お試しください。")
		return
	}

	// 6. 成功応答を返します
	//
	// Location は進捗のポーリング先です。JSON の本文を読まなくても次に叩く URL が
	// 分かるよう、画面向けの応答にも同じヘッダを付けます。
	slog.InfoContext(ctx, "レビュータスク投入成功", "repo", req.RepoURL, "job_id", req.JobID)
	w.Header().Set("Location", req.PublicURL)
	if respond.WantsJSON(w, r) {
		respond.JSON(w, r, http.StatusAccepted, submitResponse{JobID: req.JobID, DetailURL: req.PublicURL})
		return
	}
	h.renderForm(w, r, http.StatusAccepted, reviewFormPageData(req, ReviewFormPageData{
		Message:   "✅ レビュータスクを受け付けました。完了後、以下のリンクから結果を確認できます。",
		ResultURL: req.PublicURL,
	}))
}

// readSubmitRequest は、フォームか JSON body から投入内容を読み取ります。
func readSubmitRequest(r *http.Request) (domain.ReviewRequest, error) {
	if isJSONBody(r) {
		var in submitInput
		dec := json.NewDecoder(io.LimitReader(r.Body, maxSubmitBody))
		// 知らない項目はエラーにします。黙って捨てると、storage_uri のように
		// 受け付けない項目を送った呼び出し元が、効いたと思い込みます。
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			return domain.ReviewRequest{}, fmt.Errorf("リクエスト本文を解釈できません: %w", err)
		}
		return domain.ReviewRequest{
			RepoURL:       strings.TrimSpace(in.RepoURL),
			BaseBranch:    strings.TrimSpace(in.BaseBranch),
			FeatureBranch: strings.TrimSpace(in.FeatureBranch),
			Mode:          in.Mode,
			ModelName:     in.ModelName,
		}, nil
	}

	if err := r.ParseForm(); err != nil {
		return domain.ReviewRequest{}, errors.New("リクエストのパースに失敗しました。")
	}
	return domain.ReviewRequest{
		RepoURL:       strings.TrimSpace(r.PostFormValue("repo_url")),
		BaseBranch:    strings.TrimSpace(r.PostFormValue("base_branch")),
		FeatureBranch: strings.TrimSpace(r.PostFormValue("feature_branch")),
		Mode:          r.PostFormValue("mode"),
		ModelName:     r.PostFormValue("model_name"),
	}, nil
}

// isJSONBody は、本文が JSON かどうかを Content-Type で判定します。
func isJSONBody(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json")
}

// submitFailure は、投入に失敗したことを呼び出し元へ返します。
//
// 画面にはフォームを入力内容ごと描き直します（打ち直させないため）。JSON には
// 理由だけを返します。
func (h *Handler) submitFailure(
	w http.ResponseWriter, r *http.Request, req domain.ReviewRequest, status int, message string,
) {
	if respond.WantsJSON(w, r) {
		respond.ErrorJSON(w, r, status, message)
		return
	}
	h.renderForm(w, r, status, reviewFormPageData(req, ReviewFormPageData{Error: message}))
}

// assignJob は、ジョブ ID を採番して保存先と閲覧先を決めます。
func (h *Handler) assignJob(req *domain.ReviewRequest) error {
	jobID, err := h.newJobID()
	if err != nil {
		return err
	}

	detailURL := h.jobURL(jobID)
	if detailURL == "" {
		return errors.New("詳細URLの構築に失敗しました")
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

	// 投入直後に一覧へ現れるよう、ジョブ ID 一覧のキャッシュを捨てます。
	h.history.Invalidate()
}

// discardQueued は、投入に失敗した受付記録を取り消します。
//
// 取り消せなくても投入の失敗自体は利用者へ伝わっているので、ログだけ残して続けます
// （残った場合も queued のまま古びるだけで、実行はされません）。
func (h *Handler) discardQueued(ctx context.Context, jobID string) {
	if err := h.history.Delete(ctx, jobID); err != nil {
		slog.WarnContext(ctx, "投入に失敗した受付記録を取り消せませんでした", "job_id", jobID, "error", err)
		return
	}
	h.history.Invalidate()
}

// newJobID はジョブ ID を採番します。
func newJobID() (string, error) {
	return jobid.New("")
}
