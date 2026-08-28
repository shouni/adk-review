package builder

import (
	"strings"
	"testing"

	"github.com/shouni/gcp-kit/auth/oidc"
	"github.com/shouni/gcp-kit/auth/session"
	"github.com/shouni/gcp-kit/worker"

	"github.com/shouni/adk-review/internal/domain"
	"github.com/shouni/adk-review/internal/server/handlers"
)

// Validate は、面ごとのハンドラーが揃っていないまま起動するのを止めます。
//
// 揃っていないとルーターは該当ルートの登録を飛ばすだけなので、デプロイは成功し
// /health も通り、壊れているのは投入経路だけになります。Cloud Tasks は 404 を受けて
// max_attempts = 1 でタスクを捨てるため、レビュー依頼が静かに失われます。
func TestAppHandlersValidate(t *testing.T) {
	t.Parallel()

	// 中身は見ないので、非 nil であることだけが意味を持ちます。
	authHandler := &session.Handler{}
	webHandler := &handlers.Handler{}
	workerHandler := &worker.Handler[domain.ReviewRequest]{}
	taskAuth := &oidc.Verifier{}

	tests := []struct {
		name    string
		h       AppHandlers
		wantErr string
	}{
		{
			name: "web だけの構成は通る",
			h:    AppHandlers{Auth: authHandler, Web: webHandler},
		},
		{
			name: "worker だけの構成は通る",
			h:    AppHandlers{Worker: workerHandler, TaskAuth: taskAuth},
		},
		{
			name: "both の構成は通る",
			h: AppHandlers{
				Auth: authHandler, Web: webHandler,
				Worker: workerHandler, TaskAuth: taskAuth,
			},
		},
		{
			// これを通すと、画面が全部 302 になるのに起動は成功します。
			name:    "web ハンドラーだけで認証が無い",
			h:       AppHandlers{Web: webHandler},
			wantErr: "web 面",
		},
		{
			name:    "認証だけで web ハンドラーが無い",
			h:       AppHandlers{Auth: authHandler},
			wantErr: "web 面",
		},
		{
			// ★ これを通すと /tasks/execute_review が無認証で開くか、
			// あるいは登録されずタスクが 404 で捨てられます。
			name:    "worker ハンドラーだけで OIDC 検証が無い",
			h:       AppHandlers{Worker: workerHandler},
			wantErr: "worker 面",
		},
		{
			name:    "OIDC 検証だけで worker ハンドラーが無い",
			h:       AppHandlers{TaskAuth: taskAuth},
			wantErr: "worker 面",
		},
		{
			name:    "どの面も担当していない",
			h:       AppHandlers{},
			wantErr: "1 つも構築されていません",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.h.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("通るはずの構成で失敗した: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("不整合が素通りした（%q を含むエラーを期待）", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("エラー %q に %q が含まれていない", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestNewM2MVerifier は、M2M の設定漏れを起動時に落とすことを固定します。
//
// auth.Protected は M2M を無効化できません。未設定でも経路は生き続けて検証が必ず失敗し、
// セッション認証へフォールバックするため、設定漏れは「ブラウザは動くが ap-mcp だけ
// ログイン画面の HTML を受け取る」という形でしか現れません。素通りさせると気付けません。
func TestNewM2MVerifier(t *testing.T) {
	t.Parallel()

	const serviceURL = "https://adk-review.example.com"

	tests := []struct {
		name       string
		serviceURL string
		allowed    []string
		wantErr    bool
	}{
		{
			name:       "両方揃っていれば構成できる",
			serviceURL: serviceURL,
			allowed:    []string{"ap-mcp@example.iam.gserviceaccount.com"},
		},
		{
			name:       "許可リストが空なら起動を止める",
			serviceURL: serviceURL,
			wantErr:    true,
		},
		{
			name:    "audience（SERVICE_URL）が空なら起動を止める",
			allowed: []string{"ap-mcp@example.iam.gserviceaccount.com"},
			wantErr: true,
		},
		{
			name:       "空白だけの要素は許可リストとして数えない",
			serviceURL: serviceURL,
			allowed:    []string{"", "   "},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			verifier, err := newM2MVerifier(tt.serviceURL, tt.allowed)
			if tt.wantErr {
				if err == nil {
					t.Fatal("newM2MVerifier = nil, want エラー（設定漏れは起動時に落とす）")
				}
				return
			}
			if err != nil {
				t.Fatalf("newM2MVerifier = %v", err)
			}
			if !verifier.Configured() {
				t.Error("構成できたはずの検証器が Configured() = false です")
			}
		})
	}
}
