// Package builder は、adk-review の全コンポーネントを組み立てます。
//
// 配線の要点は、モデル呼び出しの経路が 2 系統あることです。
//
//   - 単発レビュアー: go-gemini-client の gemini.Client を 1 個生成し、
//     go-review-kit の gemini.NewWithGenerator へ注入します。リトライ・認証の
//     ポリシーはこのクライアントに集約されます。
//   - エージェントレビュアー: ADK のモデル層（genai 直結）を adkagent が使います。
//     genai へ直接触れる例外はそちらのパッケージに閉じています。
//
// pipeline はレビュアーを 1 種類しか持てないため、アダプターを共有した Pipeline を
// 2 本組み、どちらで実行するかは呼び出し側（CLI のフラグ、将来はレビューモードの
// フロントマター）が選びます。
package builder

import (
	"context"
	"fmt"
	"log/slog"

	geminiclient "github.com/shouni/go-gemini-client/gemini"
	revgemini "github.com/shouni/go-review-kit/gemini"
	"github.com/shouni/go-review-kit/git"
	"github.com/shouni/go-review-kit/pipeline"
	"github.com/shouni/go-review-kit/review"
	"google.golang.org/genai"

	"github.com/shouni/adk-review/internal/adkagent"
	"github.com/shouni/adk-review/internal/console"
)

// Config は、App の組み立てに必要な設定です。
type Config struct {
	// APIKey は Gemini API キーです。ProjectID とはどちらか一方だけを設定します
	// （go-gemini-client が同時指定を拒否するため、優先順位で吸収せずそのまま契約にします）。
	APIKey string
	// ProjectID / LocationID は Vertex AI 経由で呼ぶ場合の GCP 設定です。
	ProjectID  string
	LocationID string

	// WorkRoot は、リポジトリをクローンする作業ディレクトリのルートです。
	WorkRoot string
	// SSHKeyPath は、SSH 形式のリポジトリの認証に使う秘密鍵のパスです（任意）。
	SSHKeyPath string

	// MaxToolCalls は、エージェントレビューのツール呼び出し回数上限です。
	// 0 の場合は adkagent.DefaultMaxToolCalls を使います。
	MaxToolCalls int

	// Logger は各コンポーネントが使うロガーです。nil なら slog.Default() を使います。
	Logger *slog.Logger
}

// App は、組み立て済みのパイプライン一式です。
type App struct {
	// Single は、単発レビュアー（go-review-kit の gemini）で実行するパイプラインです。
	Single *pipeline.Pipeline
	// Agent は、ADK エージェントレビュアーで実行するパイプラインです。
	Agent *pipeline.Pipeline
}

// New は Config から App を組み立てます。
func New(ctx context.Context, cfg Config) (*App, error) {
	if cfg.WorkRoot == "" {
		return nil, fmt.Errorf("builder: WorkRoot が未設定です")
	}
	if cfg.APIKey == "" && cfg.ProjectID == "" {
		return nil, fmt.Errorf("builder: APIKey または ProjectID の指定が必要です")
	}
	if cfg.APIKey != "" && cfg.ProjectID != "" {
		return nil, fmt.Errorf("builder: APIKey と ProjectID は同時に設定できません（どちらか一方にしてください）")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// モデル呼び出しの共有クライアント。単発レビュアーはこれ 1 個に相乗りします。
	client, err := geminiclient.NewClient(ctx, geminiclient.Config{
		APIKey:     cfg.APIKey,
		ProjectID:  cfg.ProjectID,
		LocationID: cfg.LocationID,
	})
	if err != nil {
		return nil, fmt.Errorf("builder: Gemini クライアントの初期化に失敗しました: %w", err)
	}

	single, err := revgemini.NewWithGenerator(client)
	if err != nil {
		return nil, fmt.Errorf("builder: 単発レビュアーの初期化に失敗しました: %w", err)
	}

	agentReviewer := adkagent.New(adkagent.Config{
		ClientConfig: genaiClientConfig(cfg),
		MaxToolCalls: cfg.MaxToolCalls,
	})

	// GoGit を選ぶのは、使い捨ての作業ディレクトリで完結させたいためです（Close で削除）。
	// 永続チェックアウトを再利用したくなったら CLI 実装へ差し替えます。
	gitOpts := []git.Option{git.WithLogger(logger)}
	if cfg.SSHKeyPath != "" {
		gitOpts = append(gitOpts, git.WithSSHKey(cfg.SSHKeyPath))
	}
	sources, err := git.NewGoGitFactory(cfg.WorkRoot, gitOpts...)
	if err != nil {
		return nil, fmt.Errorf("builder: Git ファクトリの初期化に失敗しました: %w", err)
	}

	prompts := &console.Prompts{}
	publisher := &console.Publisher{}
	notifier := &console.Notifier{Logger: logger}

	singlePipeline, err := newPipeline(pipeline.Deps{
		Sources:   sources,
		Prompts:   prompts,
		Reviewer:  single,
		Publisher: publisher,
		Notifier:  notifier,
	}, logger)
	if err != nil {
		return nil, err
	}

	agentPipeline, err := newPipeline(pipeline.Deps{
		Sources:           sources,
		Prompts:           prompts,
		WorkspaceReviewer: agentReviewer,
		Publisher:         publisher,
		Notifier:          notifier,
	}, logger)
	if err != nil {
		return nil, err
	}

	return &App{Single: singlePipeline, Agent: agentPipeline}, nil
}

func newPipeline(deps pipeline.Deps, logger *slog.Logger) (*pipeline.Pipeline, error) {
	p, err := pipeline.New(deps, pipeline.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("builder: パイプラインの初期化に失敗しました: %w", err)
	}
	return p, nil
}

// genaiClientConfig は、Config から ADK モデル層用の genai 設定を組み立てます。
// APIKey と ProjectID の排他は New の冒頭で検証済みなので、ここでは設定済みの方を使うだけです。
func genaiClientConfig(cfg Config) genai.ClientConfig {
	if cfg.APIKey != "" {
		return genai.ClientConfig{APIKey: cfg.APIKey}
	}
	location := cfg.LocationID
	if location == "" {
		location = "global"
	}
	return genai.ClientConfig{
		Backend:  genai.BackendVertexAI,
		Project:  cfg.ProjectID,
		Location: location,
	}
}

// Request は、CLI から渡された値で review.Request を組み立てる補助です。
func Request(repoURL, base, head, mode, model, outPath string) (review.Request, error) {
	return review.NewRequest(review.Request{
		RepoURL:    repoURL,
		Base:       base,
		Head:       head,
		Mode:       mode,
		Model:      model,
		StorageURI: outPath,
	})
}
