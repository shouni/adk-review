package builder

import (
	"fmt"
	"net/url"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
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
	Auth   *session.Handler
	Web    *handlers.Handler
	Worker *worker.Handler[domain.ReviewRequest]
	// TaskAuth は Cloud Tasks からの OIDC を検証します。Auth と違い OAuth 設定を
	// 必要としないため、検証だけを担う独立した部品として持ちます。
	TaskAuth *oidc.Verifier
	// M2M は、他サービス（ap-mcp）からの OIDC Bearer を検証します。web 面を担うなら
	// 必ず構成されます（未設定は起動時に落とします。newM2MVerifier を参照）。
	M2M *oidc.Verifier
}

// Validate は、面ごとのハンドラーが揃っているかを確かめます。
//
// 揃っていないまま起動すると、ルーターは該当ルートの登録を飛ばすだけなので
// **デプロイは成功し、/health も通り、壊れているのは投入経路だけ**になります。
// Cloud Tasks は 404 を受けて max_attempts = 1 でタスクを捨てるため、レビュー依頼は
// 静かに失われます。ルーターが 404 を返す前に、起動を失敗させます。
func (h *AppHandlers) Validate() error {
	if (h.Auth == nil) != (h.Web == nil) {
		return fmt.Errorf("web 面のハンドラーが揃っていません (auth: %t, web: %t)", h.Auth != nil, h.Web != nil)
	}
	if (h.TaskAuth == nil) != (h.Worker == nil) {
		return fmt.Errorf("worker 面のハンドラーが揃っていません (task_auth: %t, worker: %t)", h.TaskAuth != nil, h.Worker != nil)
	}
	if h.Auth == nil && h.Worker == nil {
		return fmt.Errorf("担当する面のハンドラーが 1 つも構築されていません")
	}
	return nil
}

// BuildHandlers は依存関係を注入し、役割が担う面のハンドラーだけを生成します。
func BuildHandlers(appCtx *app.Container) (*AppHandlers, error) {
	appHandlers := &AppHandlers{}
	role := appCtx.Config.Server.Role

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

		m2m, err := newM2MVerifier(appCtx.Config.Server.ServiceURL, appCtx.Config.Auth.AllowedM2MServiceAccounts)
		if err != nil {
			return nil, err
		}
		appHandlers.M2M = m2m
	}

	if role.ServesWorker() {
		// Worker ハンドラーと、その入口を守る Cloud Tasks OIDC 検証器の生成。
		// audience と発行元サービスアカウントの両方が揃わないと検証は常に失敗する
		// （fail-closed）ため、起動時に構成を確かめておきます。
		appHandlers.Worker = worker.NewHandler[domain.ReviewRequest](appCtx.Pipeline)
		taskAuth := oidc.New(appCtx.Config.Tasks.TaskAudienceURL, appCtx.Config.Tasks.AllowedServiceAccounts)
		if !taskAuth.Configured() {
			return nil, fmt.Errorf("cloud Tasks の OIDC 検証を構成できません: TASK_AUDIENCE_URL と ALLOWED_TASK_SERVICE_ACCOUNTS が必要です")
		}
		appHandlers.TaskAuth = taskAuth
	}

	if err := appHandlers.Validate(); err != nil {
		return nil, err
	}

	return appHandlers, nil
}

// createAuthHandler は、提供された設定(Config)に基づいて認証ハンドラーを初期化して返します。
func createAuthHandler(cfg *config.Config) (*session.Handler, error) {
	redirectURL, err := url.JoinPath(cfg.Server.ServiceURL, "/auth/callback")
	if err != nil {
		return nil, fmt.Errorf("リダイレクトURLの構築失敗: %w", err)
	}

	return session.New(session.Config{
		ClientID:          cfg.Auth.GoogleClientID,
		ClientSecret:      cfg.Auth.GoogleClientSecret,
		RedirectURL:       redirectURL,
		SessionAuthKey:    cfg.Auth.SessionSecret,
		SessionEncryptKey: cfg.Auth.SessionEncryptKey,
		SessionName:       defaultSessionName,
		IsSecureCookie:    cfg.IsSecureServiceURL(),
		AllowedEmails:     cfg.Auth.AllowedEmails,
		AllowedDomains:    cfg.Auth.AllowedDomains,
	})
}

// newM2MVerifier は M2M（サーバー間通信）用の OIDC 検証器を構成します。
//
// 未設定を「意図的に無効化した」とは解釈せず、起動時に落とします。auth.Protected は
// M2M を無効化できないためです。許可リストか audience が欠けていても経路は生き続け、
// 検証が必ず失敗してセッション認証へフォールバックします。つまり設定漏れは
// 「ブラウザは正常に動くが ap-mcp だけログイン画面の HTML を受け取る」という形でしか
// 現れません。意図的な無効化と設定漏れを区別する手段が無い以上、空は後者としか
// 解釈できないので、TaskVerifier と同じく起動時に弾きます。
//
// 以前はここだけ「未設定なら nil のまま（＝素通り）」にしていましたが、
// 兄弟アプリ 4 本はいずれも起動時に落とす形で、adk-review だけが違っていました。
//
// 構成の可否を config ではなく検証器自身に尋ねるのは、必要な設定が何かを知っているのが
// gcp-kit 側だからです。許可リストの空だけを config で見ると audience（SERVICE_URL）の
// 欠落を拾えず、キットが要件を増やしても追随しません。
func newM2MVerifier(serviceURL string, allowedServiceAccounts []string) (*oidc.Verifier, error) {
	m2m := oidc.New(serviceURL, allowedServiceAccounts)
	if !m2m.Configured() {
		return nil, fmt.Errorf("m2m の OIDC 検証を構成できません: SERVICE_URL と ALLOWED_M2M_SERVICE_ACCOUNTS が必要です")
	}
	return m2m, nil
}
