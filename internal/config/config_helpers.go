package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shouni/go-utils/envutil"
	"github.com/shouni/go-utils/text"
	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL は、設定されたServiceURLが安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.ServiceURL)
}

// ValidateEssentialConfig は設定バリデーションを行います。
//
// 役割に関係ない共通項目を先に確かめ、Web 面の設定は担当するプロセスでだけ要求します。
// worker に OAuth の設定を要求すると、面ごとにサービスを分けた意味（worker の env を
// 最小に保つ）が失われるためです。
func (c *Config) ValidateEssentialConfig() error {
	if c.roleErr != nil {
		return fmt.Errorf("SERVER_ROLE: %w", c.roleErr)
	}

	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.ServiceURL)
	}

	if len(c.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS が設定されていません（カンマ区切りで複数指定すると、先頭が既定でフォームの選択肢になります）")
	}

	if err := c.validatePipelineTimeout(); err != nil {
		return err
	}

	if c.agentMaxToolCallsErr != nil {
		return fmt.Errorf("AGENT_MAX_TOOL_CALLS の解析に失敗しました: %w", c.agentMaxToolCallsErr)
	}

	if c.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	return nil
}

// validateWebConfig は、Web 面（OAuth・セッション）の設定を検証します。
func (c *Config) validateWebConfig() error {
	if c.GoogleClientID == "" || c.GoogleClientSecret == "" || c.SessionSecret == "" {
		return fmt.Errorf("google OAuth 関連の設定（ClientID, ClientSecret, SessionSecret）が不足しています")
	}

	if len(c.AllowedEmails) == 0 && len(c.AllowedDomains) == 0 {
		return fmt.Errorf("許可されたメールアドレスまたはドメインが一つも設定されていません（認可リストが空です）")
	}

	if c.SessionEncryptKey == "" {
		return fmt.Errorf("SESSION_ENCRYPT_KEY が設定されていません。セキュアな運用のために必須です")
	}

	// SessionEncryptKey の長さチェック (AES要件: 16, 24, 32 bytes)
	keyLen := len(c.SessionEncryptKey)
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
	if c.pipelineTimeoutErr != nil {
		return fmt.Errorf("PIPELINE_TIMEOUT の解析に失敗しました: %w（例: '25m', '1500s'）", c.pipelineTimeoutErr)
	}

	// 0 以下は無制限。ローカルでの長時間デバッグ用の逃げ道で、本番では既定値が入ります。
	if c.PipelineTimeout <= 0 {
		return nil
	}

	if c.PipelineTimeout >= TaskDispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%s) は dispatch deadline (%s) より短くしてください。"+
				"長いと Cloud Tasks が先にリクエストを打ち切り、失敗レポートも Slack 通知も残らず、"+
				"review-queue は max_attempts = 1 なので再試行もされません",
			c.PipelineTimeout, TaskDispatchDeadline)
	}

	return nil
}

// getEnv は環境変数を取得し、存在しない場合はデフォルト値を返します。
func getEnv(key string, defaultValue string) string {
	return envutil.GetEnv(key, defaultValue)
}

// parseCommaSeparatedList はカンマ区切りの文字列をパースしてスライスを返します。
func parseCommaSeparatedList(value string) []string {
	return text.ParseCommaSeparatedList(value)
}

// parseIntEnv は整数の環境変数を読みます。
// 未設定なら既定値を返し、書式が不正なら既定値とエラーの両方を返します
// （parseDurationEnv と同じ「黙って既定値へ落とさない」方針です）。
func parseIntEnv(key string, defaultValue int) (int, error) {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue, fmt.Errorf("%s=%q: %w", key, raw, err)
	}
	return n, nil
}

// parseDurationEnv は Duration 形式の環境変数を読みます（例: "25m", "1500s"）。
// 未設定なら既定値を返し、書式が不正なら既定値とエラーの両方を返します。
// 不正値を黙って既定値へ落とすと、設定したつもりの値が効かないまま動き続けるため、
// 呼び出し側（ValidateEssentialConfig）が起動時に落とせるようにエラーを持ち帰ります。
func parseDurationEnv(key string, defaultValue time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return defaultValue, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultValue, fmt.Errorf("%s=%q: %w", key, raw, err)
	}
	return d, nil
}
