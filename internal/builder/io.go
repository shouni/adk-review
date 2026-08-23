package builder

import (
	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/domain"
)

// buildStatusStore は、ジョブ進行状況の読み書きを初期化します。
//
// 配置を UnderJobDir に委ねているのは、成果物と同じジョブディレクトリ配下へ置くためです。
// 履歴の削除がプレフィックスの一括削除で済み、状態ファイルの消し漏れが起きません。
func buildStatusStore(rio *app.RemoteIO, layout domain.StorageLayout) domain.StatusStore {
	return jobstatus.NewStore[domain.JobStatus](
		rio.Reader,
		rio.Writer,
		jobstatus.UnderJobDir(layout.ReviewPrefixURI()),
	)
}
