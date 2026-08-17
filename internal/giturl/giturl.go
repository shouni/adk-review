// Package giturl は、GitリポジトリのURL（HTTPS/SSH）を解析し、画面や通知に出す
// 表示用のリポジトリパスを取り出します。
//
// 元は go-utils/giturl として共有ライブラリに置かれていましたが、利用者が本プロジェクト
// だけであるため internal へ移しました。以前は「どこへクローンするか」「GCS のどのキーへ
// 置くか」を決める関数も持っていましたが、前者は go-review-kit が、後者は
// internal/domain の StorageLayout が担うようになったため、表示用の変換だけが残っています。
package giturl

import (
	"log/slog"
	"net/url"
	"strings"
)

// GetRepositoryPath はリポジトリURLから 'owner/repo-name' の形式のパスを抽出します。
func GetRepositoryPath(repoURL string) string {
	// SSH形式 (git@host:owner/repo.git) を net/url でパース可能な形式に変換
	if strings.HasPrefix(repoURL, "git@") {
		if idx := strings.Index(repoURL, ":"); idx != -1 {
			repoURL = "ssh://" + repoURL[:idx] + "/" + repoURL[idx+1:] // ':' を '/' に置換
		}
	}

	u, err := url.Parse(repoURL)
	if err != nil {
		slog.Warn("リポジトリURLのパースに失敗しました。元のURLをそのまま使用します。", "url", repoURL, "error", err)
		return repoURL // パース失敗時は元のURLを返す
	}

	// パス部分から先頭の '/' と末尾の '.git' を除去
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	return path
}
