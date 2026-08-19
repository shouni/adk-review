package handlers

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

// ReviewFormPageData はフォームテンプレートに渡すデータ構造です。
type ReviewFormPageData struct {
	Message        string
	Error          string
	ResultURL      string
	RepoURL        string
	BaseBranch     string
	FeatureBranch  string
	ReviewMode     string
	ModelName      string
	Engine         string
	ReviewModes    []ReviewModeOption
	Models         []ModelOption
	Engines        []EngineOption
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

// EngineOption はフォームに表示するレビューエンジンの選択肢です。
type EngineOption struct {
	Value       string
	Label       string
	Description string
	Selected    bool
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
func (h *Handler) HandleReviewForm(w http.ResponseWriter, r *http.Request) {
	h.renderForm(w, r, http.StatusOK, defaultReviewFormPageData())
}
