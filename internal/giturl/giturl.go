// Package giturl は、Git リポジトリの URL（HTTPS / SSH）から、画面や通知に出す
// 表示用のリポジトリパスを取り出します。
//
// **扱うのは表示だけです。** クローン先は go-review-kit が、保存先は
// internal/domain の StorageLayout が決めます。
package giturl

import (
	"log/slog"
	"net/url"
	"strings"
)

// GetRepositoryPath はリポジトリURLから 'owner/repo-name' の形式のパスを抽出します。
func GetRepositoryPath(repoURL string) string {
	// scp 形式（git@host:owner/repo.git）は net/url が解釈できないため、
	// ssh:// の形へ寄せてから渡します。
	if strings.HasPrefix(repoURL, "git@") {
		if idx := strings.Index(repoURL, ":"); idx != -1 {
			repoURL = "ssh://" + repoURL[:idx] + "/" + repoURL[idx+1:]
		}
	}

	u, err := url.Parse(repoURL)
	if err != nil {
		slog.Warn("リポジトリURLのパースに失敗しました。元のURLをそのまま使用します。", "url", repoURL, "error", err)
		return repoURL
	}

	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	return path
}
