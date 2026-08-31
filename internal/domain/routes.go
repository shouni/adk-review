package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// WorkerTaskPath は、ワーカーがタスクを受け取るパスです。
// 投入側（WorkerTaskURL）と受信側（internal/server のルート登録）の両方が使います。
// 二重に持つと片方だけ変えてもビルドは通り、投入したタスクが全件 404 になります。
const WorkerTaskPath = "/tasks/execute_review"

// WorkerTaskURL は、worker サービスの URL からタスクの配送先 URL を組み立てます。
// WORKER_URL に入れるのはサービスの URL だけで、パスはここで継ぎ足します。
// 末尾が既に WorkerTaskPath の値も、二重に継ぎ足さずそのまま受けます。
func WorkerTaskURL(workerURL string) (string, error) {
	base := strings.TrimSpace(workerURL)
	if base == "" {
		return "", fmt.Errorf("worker URL is empty")
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid worker URL %q: %w", base, err)
	}
	// 末尾スラッシュは落とします。chi は登録どおりのパスでしか一致しません。
	if trimmed := strings.TrimSuffix(parsed.Path, "/"); strings.HasSuffix(trimmed, WorkerTaskPath) {
		parsed.Path = trimmed
		return parsed.String(), nil
	}

	joined, err := url.JoinPath(base, WorkerTaskPath)
	if err != nil {
		return "", fmt.Errorf("invalid worker URL %q: %w", base, err)
	}
	return joined, nil
}
