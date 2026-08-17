package builder

import (
	"fmt"
	"net/url"

	"github.com/shouni/gcp-kit/auth"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/config"
	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
)

const defaultSessionName = "adk-review-session"

// AppHandlers は生成されたすべての HTTP ハンドラーを保持する構造体です。
//
// 担当しない面のハンドラーは nil のままにします。ルーターは nil を見て登録を省くため、
// 役割が増えてもルーティング側を触らずに済みます（兄弟アプリと同じ形です）。
type AppHandlers struct {
	Auth   *auth.Handler
	Web    *handlers.Handler
	Worker *worker.Handler[domain.ReviewRequest]
	// TaskAuth は Cloud Tasks からの OIDC を検証します。Auth と違い OAuth 設定を
	// 必要としないため、検証だけを担う独立した部品として持ちます。
	TaskAuth *auth.TaskVerifier
}

// BuildHandlers は依存関係を注入し、役割が担う面のハンドラーだけを生成します。
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	appHandlers := &AppHandlers{}
	role := appCtx.Config.Role

	if role.ServesWeb() {
		authHandler, err := createAuthHandler(appCtx.Config)
		if err != nil {
			return nil, err
		}
		appHandlers.Auth = authHandler

		webHandler, err := handlers.NewHandler(handlers.Deps{
			Config:       appCtx.Config,
			TaskEnqueuer: appCtx.TaskEnqueuer,
			Layout:       appCtx.Layout,
			StatusStore:  appCtx.StatusStore,
			History:      appCtx.History,
		})
		if err != nil {
			return nil, fmt.Errorf("WebHandlerの初期化失敗: %w", err)
		}
		appHandlers.Web = webHandler
	}

	if role.ServesWorker() {
		// Worker ハンドラーと、その入口を守る Cloud Tasks OIDC 検証器の生成。
		// audience と発行元サービスアカウントの両方が揃わないと検証は常に失敗する
		// （fail-closed）ため、起動時に構成を確かめておきます。
		appHandlers.Worker = worker.NewHandler[domain.ReviewRequest](appCtx.Pipeline)
		taskAuth := auth.NewTaskVerifier(appCtx.Config.TaskAudienceURL, appCtx.Config.AllowedTaskServiceAccounts)
		if !taskAuth.Configured() {
			return nil, fmt.Errorf("cloud Tasks の OIDC 検証を構成できません: TASK_AUDIENCE_URL と ALLOWED_TASK_SERVICE_ACCOUNTS が必要です")
		}
		appHandlers.TaskAuth = taskAuth
	}

	return appHandlers, nil
}

// createAuthHandler は、提供された設定(Config)に基づいて認証ハンドラーを初期化して返します。
func createAuthHandler(cfg *config.Config) (*auth.Handler, error) {
	redirectURL, err := url.JoinPath(cfg.ServiceURL, "/auth/callback")
	if err != nil {
		return nil, fmt.Errorf("リダイレクトURLの構築失敗: %w", err)
	}

	return auth.NewHandler(auth.Config{
		ClientID:          cfg.GoogleClientID,
		ClientSecret:      cfg.GoogleClientSecret,
		RedirectURL:       redirectURL,
		SessionAuthKey:    cfg.SessionSecret,
		SessionEncryptKey: cfg.SessionEncryptKey,
		SessionName:       defaultSessionName,
		IsSecureCookie:    cfg.IsSecureServiceURL(),
		AllowedEmails:     cfg.AllowedEmails,
		AllowedDomains:    cfg.AllowedDomains,
	})
}
