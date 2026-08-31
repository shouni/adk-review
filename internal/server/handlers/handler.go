package handlers

import (
	"cmp"
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/shouni/go-utils/jobid"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// ReviewFormPageData はフォームテンプレートに渡すデータ構造です。
type ReviewFormPageData struct {
	Message string
	// Notice は、成功でも失敗でもない補足です（再実行でフォームを埋めたこと等）。
	// Message と分けているのは、そちらが投入完了の緑色の枠に固定されているためです。
	Notice         string
	Error          string
	ResultURL      string
	RepoURL        string
	BaseBranch     string
	FeatureBranch  string
	ReviewMode     string
	ModelName      string
	ReviewModes    []ReviewModeOption
	Models         []ModelOption
	CSRFToken      string
	CSRFTokenField string
	RepoURLPattern string
	BranchPattern  string
}

// ReviewModeOption はフォームに表示するレビューモードの選択肢です。
type ReviewModeOption struct {
	Value       string
	Description string
	Selected    bool
}

// ModelOption はフォームに表示するGeminiモデルの選択肢です。
type ModelOption struct {
	Value    string
	Selected bool
}

type reviewTaskEnqueuer interface {
	Enqueue(ctx context.Context, payload domain.ReviewRequest) error
}

// Deps は Handler が必要とする依存です。
type Deps struct {
	Config       *config.Config
	TaskEnqueuer reviewTaskEnqueuer
	Layout       domain.StorageLayout
	StatusStore  domain.StatusStore
	History      domain.HistoryRepository
}

// Handler は HTTPリクエストを処理する構造体です。
type Handler struct {
	cfg          *config.Config
	taskEnqueuer reviewTaskEnqueuer
	layout       domain.StorageLayout
	statusStore  domain.StatusStore
	history      domain.HistoryRepository
	templates    map[string]*template.Template
	now          func() time.Time
	newJobID     func() (string, error)
}

// NewHandler は新しい Handler インスタンスを作成します。
func NewHandler(deps Deps) (*Handler, error) {
	templates, err := parsePageTemplates()
	if err != nil {
		return nil, err
	}

	return &Handler{
		cfg:          deps.Config,
		taskEnqueuer: deps.TaskEnqueuer,
		layout:       deps.Layout,
		statusStore:  deps.StatusStore,
		history:      deps.History,
		templates:    templates,
		now:          time.Now,
		newJobID:     newJobID,
	}, nil
}

// HandleReviewForm は GET リクエストに対してフォームを表示します。
//
// ?from={jobID} を付けると、そのレビューの依頼内容でフォームを埋めます。失敗した
// レビューは review-queue が max_attempts = 1 なので再試行されず、やり直すには
// 依頼内容を打ち直すしかありませんでした。
func (h *Handler) HandleReviewForm(w http.ResponseWriter, r *http.Request) {
	data := defaultReviewFormPageData()
	if from := r.URL.Query().Get("from"); from != "" {
		data = h.rerunFormData(r.Context(), from, data)
	}
	h.renderForm(w, r, http.StatusOK, data)
}

// rerunFormData は、過去のレビューの依頼内容でフォームの初期値を埋めます。
//
// 読めなかった場合もエラーにはせず、既定値のフォームを断り書き付きで出します。
// 再実行は入力を省くための便宜で、それが効かないことは新しい依頼を妨げません。
//
// 記録が空の項目には既定値を残します。依頼内容を記録する前の形式で保存された
// ジョブを開いたときに、既定値まで空へ倒さないためです。
func (h *Handler) rerunFormData(ctx context.Context, jobID string, data ReviewFormPageData) ReviewFormPageData {
	const unavailable = "元のレビューを読み込めなかったため、既定値で表示しています。"

	safeJobID, err := jobid.Sanitize(jobID)
	if err != nil {
		slog.WarnContext(ctx, "再実行の元ジョブIDが不正です", "job_id", jobID, "error", err)
		data.Notice = unavailable
		return data
	}

	status, err := h.statusStore.Get(ctx, safeJobID)
	if err != nil {
		slog.WarnContext(ctx, "再実行の元になるレビューを読み込めませんでした", "job_id", safeJobID, "error", err)
		data.Notice = unavailable
		return data
	}

	data.RepoURL = cmp.Or(status.RepoURL, data.RepoURL)
	data.BaseBranch = cmp.Or(status.BaseBranch, data.BaseBranch)
	data.FeatureBranch = cmp.Or(status.FeatureBranch, data.FeatureBranch)
	data.ReviewMode = cmp.Or(status.Mode, data.ReviewMode)
	data.ModelName = cmp.Or(status.ModelName, data.ModelName)
	data.Notice = "過去の依頼内容を引き継いでいます。必要な箇所を直して依頼してください。"
	return data
}
