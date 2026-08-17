package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	gitsource "github.com/shouni/go-review-kit/git"
	"github.com/shouni/go-review-kit/review"
)

// baseRepoDirName は、クローン先を集めるディレクトリ名です。
const baseRepoDirName = "reviewer-repos"

// NewDiffSourceFactory は review.DiffSourceFactory を構築します。
//
// go-git 実装（GoGit）を選ぶのは、本サービスが Cloud Run 上で動き、インスタンスが
// 使い捨てられるためです。git バイナリを必要とせず、Close で作業ディレクトリごと
// 消えるので、後始末の取りこぼしがそのまま容量として残りません。
func NewDiffSourceFactory(sshKeyPath string) (review.DiffSourceFactory, error) {
	root := filepath.Join(os.TempDir(), baseRepoDirName)

	factory, err := gitsource.NewGoGitFactory(root,
		gitsource.WithSSHKey(sshKeyPath),
		gitsource.WithDirNamer(uniqueRepoDirName),
	)
	if err != nil {
		return nil, fmt.Errorf("DiffSourceFactoryの構築に失敗しました: %w", err)
	}
	return factory, nil
}

// uniqueRepoDirName は、実行ごとに重複しないクローン先の名前を返します。
//
// 既定の命名はリポジトリURLだけから決まるため、同じリポジトリのレビューが同時に走ると
// 同じディレクトリを奪い合います。GoGit は Close で作業ディレクトリを削除するので、
// 先に終わった側の後始末が、まだ差分を取っている側の足元を消してしまいます。
// 実行ごとに固有の名前にして、その競合を避けます。
func uniqueRepoDirName(repoURL string) string {
	return gitsource.RepoDirName(repoURL) + "-" + uuid.New().String()
}
