package handlers

import (
	"context"
	"html/template"
	"time"

	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
)

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
