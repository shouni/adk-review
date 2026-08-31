// Package giturl は、Git リポジトリの URL（HTTPS / SSH）から、画面や通知に出す
// 表示用のリポジトリパスを取り出します。
//
// 扱うのは表示だけです。クローン先は go-review-kit が、保存先は
// internal/domain の StorageLayout が決めます。
package giturl

import (
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

	// 解釈できない URL はそのまま返します。握り潰しではありません。呼び出し元は
	// テンプレート関数と通知の本文で、どちらも「元の URL を出す」が正しい結末です。
	// 記録も残しません。この関数は context を受け取れる位置にないので、書けば job_id の
	// 付かない行が 1 本増えるだけで、誰のどのレビューの話か追えません。
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL
	}

	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	return path
}
