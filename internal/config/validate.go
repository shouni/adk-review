package config

import (
	"fmt"

	"github.com/shouni/netarmor/securenet"
)

// IsSecureServiceURL は、設定された ServiceURL が安全なスキーム (HTTPS など) を使用しているかどうかを確認します。
func (c *Config) IsSecureServiceURL() bool {
	return securenet.IsSecureServiceURL(c.Server.ServiceURL)
}

// ValidateEssentialConfig は設定バリデーションを行います。
//
// 書式の検証（Duration や SERVER_ROLE の解析など）は LoadConfig が済ませているため、
// ここでは値の組み合わせと不足だけを確かめます。役割に関係ない共通項目を先に確かめ、
// 面ごとの設定は担当するプロセスでだけ要求します。worker に OAuth の設定を要求すると、
// 面ごとにサービスを分けた意味（worker の env を最小に保つ）が失われるためです。
//
// **失敗は「起動を止める」ことに意味があります。** ここを通してしまうと、設定漏れは
// 利用者がレビューを投げたあと、しかも AI 呼び出しを終えた保存段階で初めて表面化します。
func (c *Config) ValidateEssentialConfig() error {
	if c.Server.ServiceURL == "" {
		return fmt.Errorf("SERVICE_URL が設定されていません（OAuth のリダイレクト先と履歴リンクの組み立てに使います）")
	}
	if !c.IsSecureServiceURL() {
		return fmt.Errorf("本番環境では SERVICE_URL ('%s') は HTTPS である必要があります", c.Server.ServiceURL)
	}

	if len(c.AI.GeminiModels) == 0 {
		return fmt.Errorf("GEMINI_MODELS が設定されていません（カンマ区切りで複数指定すると、先頭が既定でフォームの選択肢になります）")
	}

	// Gemini は Vertex AI 経由で呼ぶため、両面で要ります。
	if c.GCP.ProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID が設定されていません（Gemini は Vertex AI 経由で呼びます）")
	}

	// **両ロールで要ります。** web は履歴の一覧・詳細・削除に、worker は成果物の保存に使います。
	if c.Storage.GCSBucket == "" {
		return fmt.Errorf("GCS_REVIEW_BUCKET が設定されていません")
	}

	if err := c.validateTimeouts(); err != nil {
		return err
	}

	if c.Server.Role.ServesWeb() {
		if err := c.validateWebConfig(); err != nil {
			return err
		}
	}

	if c.Server.Role.ServesWorker() {
		if err := c.validateWorkerConfig(); err != nil {
			return err
		}
	}

	return nil
}

// validateWebConfig は、Web 面（OAuth・セッション、タスク投入）の設定を検証します。
func (c *Config) validateWebConfig() error {
	// タスクを投入するのは Web 面だけなので、キュー名も投入先も Web 面の要件です。
	if c.Tasks.QueueID == "" {
		return fmt.Errorf("CLOUD_TASKS_QUEUE_ID が設定されていません")
	}
	if c.Tasks.WorkerURL == "" {
		return fmt.Errorf("WORKER_URL が設定されていません")
	}
	// caller SA はタスクを投入する側＝ Web 面の要件です。worker が受け付ける許可リストは
	// ALLOWED_TASK_SERVICE_ACCOUNTS で別に指定します。
	if c.Tasks.CallerServiceAccountEmail == "" {
		return fmt.Errorf("TASK_CALLER_SERVICE_ACCOUNT_EMAIL が設定されていません")
	}

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

// validateWorkerConfig は、Worker 面（Cloud Tasks の受信）の設定を検証します。
func (c *Config) validateWorkerConfig() error {
	if c.Tasks.TaskAudienceURL == "" {
		return fmt.Errorf("TASK_AUDIENCE_URL が設定されていません。Cloud Tasks の OIDC 検証に必須です")
	}
	// 空だと検証器が fail-closed になり、全タスクが失敗し続けます。
	if len(c.Tasks.AllowedServiceAccounts) == 0 {
		return fmt.Errorf("許可する caller SA が 1 件も指定されていません。ALLOWED_TASK_SERVICE_ACCOUNTS を設定してください")
	}
	return nil
}

// validateTimeouts は、タイムアウトの三段の大小関係を起動時に確かめます。
//
// ★ PIPELINE_TIMEOUT と dispatch deadline が同時に見えるのはこの package だけなので、
// 不変条件の番人はここに置きます。逆転していると「Cloud Tasks が先に打ち切る →
// 失敗レポートも Slack 通知も残らない」という、いちばん気付きにくい壊れ方をします。
//
// 三段目（Cloud Run の timeout）はアプリからは見えないので、そちらとの関係は
// インフラ管理リポジトリの precondition が受け持ちます。
func (c *Config) validateTimeouts() error {
	if c.Tasks.DispatchDeadline <= 0 {
		return fmt.Errorf("TASK_DISPATCH_DEADLINE が設定されていません（三段のタイムアウトはデプロイ設定が決めます。例: 10m）")
	}

	// 0 以下は無制限。ローカルでの長時間デバッグ用の逃げ道で、本番では既定値が入ります。
	if c.Pipeline.Timeout <= 0 {
		return nil
	}

	if c.Pipeline.Timeout >= c.Tasks.DispatchDeadline {
		return fmt.Errorf(
			"PIPELINE_TIMEOUT (%s) は TASK_DISPATCH_DEADLINE (%s) より短くしてください。"+
				"長いと Cloud Tasks が先にリクエストを打ち切り、失敗レポートも Slack 通知も残らず、"+
				"review-queue は max_attempts = 1 なので再試行もされません",
			c.Pipeline.Timeout, c.Tasks.DispatchDeadline)
	}

	return nil
}
