package domain

import "github.com/shouni/go-remote-io/remoteio"

const (
	// reviewPrefix は、レビュージョブが並ぶオブジェクトプレフィックスです。
	reviewPrefix = "reviews/"
	// statusFileName は、進行状況と一覧用メタのファイル名です。
	statusFileName = "status.json"
	// reportFileName は、レビュー結果全文のファイル名です。
	reportFileName = "report.json"
)

// StorageLayout は、GCS 上のオブジェクト名の決め方を 1 箇所に集めたものです。
//
// 1 ジョブ分のオブジェクトはジョブ ID のプレフィックス配下へまとめます。バケット直下に
// 散らばっていると、履歴を消すたびに「このジョブが何を作ったか」を人が数え上げることに
// なるためです。プレフィックスにまとめておけば、消す側は中身を知らないまま一括で消せます。
//
// 状態ファイル（status.json）を成果物と同居させるのは意図的です。履歴一覧は reviews/ 直下の
// プレフィックスを列挙して作るため、同居させておくと、まだ実行中のジョブや失敗したジョブも
// そのまま一覧に並びます。レビューでは「依頼したのに履歴から消えている」ほうが困るので、
// 成果物を持たないジョブも見えるようにしています。
type StorageLayout struct {
	bucket string
}

// NewStorageLayout は、指定バケットの StorageLayout を返します。
func NewStorageLayout(bucket string) StorageLayout {
	return StorageLayout{bucket: bucket}
}

// ReviewPrefixURI は、履歴一覧が列挙するプレフィックスの URI を返します。
func (l StorageLayout) ReviewPrefixURI() string {
	return remoteio.BuildGCSURI(l.bucket, reviewPrefix)
}

// JobPrefixURI は、1 ジョブ分のオブジェクトを収めるプレフィックスの URI を返します。
// 履歴の削除はこのプレフィックス単位で行えます。
func (l StorageLayout) JobPrefixURI(jobID string) string {
	return remoteio.BuildGCSURI(l.bucket, JobPrefix(jobID))
}

// StatusURI は、進行状況と一覧用メタ（status.json）の URI を返します。
func (l StorageLayout) StatusURI(jobID string) string {
	return remoteio.BuildGCSURI(l.bucket, JobPrefix(jobID)+statusFileName)
}

// ReportURI は、レビュー結果全文（report.json）の URI を返します。
//
// 一覧用メタと別のオブジェクトにするのは、履歴一覧が行数分だけ status.json を読むためです。
// 指摘の全文を同じファイルへ入れると、一覧の読み取り量が指摘件数に比例して増えます。
func (l StorageLayout) ReportURI(jobID string) string {
	return remoteio.BuildGCSURI(l.bucket, JobPrefix(jobID)+reportFileName)
}

// JobPrefix は、1 ジョブ分の相対オブジェクトプレフィックスを返します。
func JobPrefix(jobID string) string {
	return reviewPrefix + jobID + "/"
}
