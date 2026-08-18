package config

import (
	"fmt"

	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL は、設定されたServiceURLが安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.Server.ServiceURL)
}

// ValidateEssentialConfig は設定バリデーションを行います。
//
// 書式の検証（Duration や SERVER_ROLE の解析など）は LoadConfig が済ませているため、
// ここでは値の組み合わせと不足だけを確かめます。役割に関係ない共通項目を先に確かめ、
// Web 面の設定は担当するプロセスでだけ要求します。worker に OAuth の設定を要求すると、
// 面ごとにサービスを分けた意味（worker の env を最小に保つ）が失われるためです。
func (c *Config) ValidateEssentialConfig() error {
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.Server.ServiceURL)
	}

	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS が設定されていません（カンマ区切りで複数指定すると、先頭が既定でフォームの選択肢になります）")
	}

	if err := c.validatePipelineTimeout(); err != nil {
		return err
	}

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	return nil
}

// validateWebConfig は、Web 面（OAuth・セッション）の設定を検証します。
func (c *Config) validateWebConfig() error {
	if c.Auth.GoogleClientID == "" || c.Auth.GoogleClientSecret == "" || c.Auth.SessionSecret == "" {
		return fmt.Errorf("google OAuth 関連の設定（ClientID, ClientSecret, SessionSecret）が不足しています")
	}

	if len(c.Auth.AllowedEmails) == 0 && len(c.Auth.AllowedDomains) == 0 {
		return fmt.Errorf("許可されたメールアドレスまたはドメインが一つも設定されていません（認可リストが空です）")
	}

	if c.Auth.SessionEncryptKey == "" {
		return fmt.Errorf("SESSION_ENCRYPT_KEY が設定されていません。セキュアな運用のために必須です")
	}

	// SessionEncryptKey の長さチェック (AES要件: 16, 24, 32 bytes)
	keyLen := len(c.Auth.SessionEncryptKey)
	if keyLen != 16 && keyLen != 24 && keyLen != 32 {
		return fmt.Errorf("SESSION_ENCRYPT_KEY の長さが不正です (%d バイト)。16, 24, 32 バイトのいずれかにしてください", keyLen)
	}

	return nil
}

// validatePipelineTimeout は、タイムアウトの三段の大小関係を起動時に確かめます。
//
// ★ PIPELINE_TIMEOUT と dispatch deadline が同時に見えるのはこの package だけなので、
// 不変条件の番人はここに置きます。逆転していると「Cloud Tasks が先に打ち切る →
// 失敗レポートも Slack 通知も残らない」という、いちばん気付きにくい壊れ方をします。
func (c *Config) validatePipelineTimeout() error {
	// 0 以下は無制限。ローカルでの長時間デバッグ用の逃げ道で、本番では既定値が入ります。
	if c.Pipeline.Timeout <= 0 {
		return nil
	}

	if c.Pipeline.Timeout >= TaskDispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%s) は dispatch deadline (%s) より短くしてください。"+
				"長いと Cloud Tasks が先にリクエストを打ち切り、失敗レポートも Slack 通知も残らず、"+
				"review-queue は max_attempts = 1 なので再試行もされません",
			c.Pipeline.Timeout, TaskDispatchDeadline)
	}

	return nil
}
