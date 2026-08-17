package builder

import (
	"fmt"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/adk-review/internal/app"
	"github.com/shouni/adk-review/internal/domain"
)

// buildRemoteIO は、 I/O コンポーネントを初期化します。
func buildRemoteIO(storage remoteio.IOFactory) (*app.RemoteIO, error) {
	r, err := storage.InputReader()
	if err != nil {
		return nil, fmt.Errorf("入力リーダーの生成に失敗しました: %w", err)
	}
	w, err := storage.OutputWriter()
	if err != nil {
		return nil, fmt.Errorf("出力ライターの生成に失敗しました: %w", err)
	}
	return &app.RemoteIO{
		Factory: storage,
		Reader:  r,
		Writer:  w,
	}, nil
}

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
