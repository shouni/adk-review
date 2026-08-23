package domain

import (
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// CommandReview は、ジョブの種類を表す jobstatus.Status.Command の値です。
// 本サービスのジョブは今のところレビューだけですが、記録の読み手が種類を判別できるよう
// 明示しておきます。
const CommandReview = "review"

// JobStatus は、1 レビュージョブの進行状況と、履歴一覧に出すメタ情報です。
//
// jobstatus.Status を埋め込んでいるため、JSON はフラットに展開されます（入れ子の
// ペイロードを挟むと保存済みの status.json が読めなくなります）。
//
// 一覧が 1 行あたり 1 オブジェクトの読み取りで済むよう、表示に要る項目はすべてここへ
// 持たせます。指摘の全文だけは行数に比例して重くなるため、report.json 側に置きます。
type JobStatus struct {
	jobstatus.Status

	// Outcome は、レビューそのものの結末です。
	//
	// jobstatus.State（queued / running / succeeded / failed）はジョブの生死しか表せません。
	// 「差分が無くてレビューしなかった」はジョブとしては正常終了なので succeeded ですが、
	// 成果物は無く判定もありません。一覧でこの違いを見分けるために別の値として持ちます。
	Outcome review.Status `json:"outcome,omitempty"`

	RepoURL       string `json:"repo_url,omitempty"`
	BaseBranch    string `json:"base_branch,omitempty"`
	FeatureBranch string `json:"feature_branch,omitempty"`
	Mode          string `json:"mode,omitempty"`
	ModelName     string `json:"model_name,omitempty"`

	// Decision はレビュー全体の判定です。成功時のみ入ります。
	Decision review.Decision `json:"decision,omitempty"`
	// ReportURI はレビュー結果全文（report.json）の URI です。成功時のみ入ります。
	ReportURI string `json:"report_uri,omitempty"`
}

// HasReport は、レビュー結果全文が保存されているかどうかを返します。
func (s JobStatus) HasReport() bool {
	return s.ReportURI != ""
}

// Deletable は、履歴から削除してよい状態かどうかを返します。
//
// queued / running を消せないようにしているのは、消したあとでワーカーが status.json を
// 書き戻し、プレフィックスが復活するためです。結果として、中身の無い行が履歴に残ります。
// failed を許すのは、review-queue が max_attempts = 1 で再試行が来ないためです。
func (s JobStatus) Deletable() bool {
	return s.State == jobstatus.StateSucceeded || s.State == jobstatus.StateFailed
}

// carryOverFrom は、前回の記録から引き継ぐべきサービス固有フィールドを移します。
//
// ワーカーは状態が変わるたびにタスクの内容から JobStatus を組み立て直すため、投入時に
// しか分からない情報（Outcome や Decision のような後から確定する値ではなく、タスクに
// 含まれない値）を素直に書くと失われます。共通フィールドの引き継ぎは jobstatus.Recorder が
// 行うので、ここではこの型が足した分だけを扱います。
func (s *JobStatus) carryOverFrom(prev *JobStatus) {
	if prev == nil {
		return
	}
	if s.ReportURI == "" {
		s.ReportURI = prev.ReportURI
	}
	if s.Decision == "" {
		s.Decision = prev.Decision
	}
	if s.Outcome == "" {
		s.Outcome = prev.Outcome
	}
}

// CarryOverExtras は、jobstatus.Recorder.Record へ渡す引き継ぎ関数です。
func CarryOverExtras(next, prev *JobStatus) {
	next.carryOverFrom(prev)
}

// NewQueuedStatus は、タスク投入時に記録する JobStatus を組み立てます。
func NewQueuedStatus(req ReviewRequest) JobStatus {
	return newStatus(req, jobstatus.StateQueued)
}

// NewRunningStatus は、ワーカーが処理を開始したときに記録する JobStatus を組み立てます。
func NewRunningStatus(req ReviewRequest) JobStatus {
	return newStatus(req, jobstatus.StateRunning)
}

// NewFailedStatus は、失敗を記録する JobStatus を組み立てます。
func NewFailedStatus(req ReviewRequest, err error) JobStatus {
	status := newStatus(req, jobstatus.StateFailed)
	status.Outcome = review.StatusFailed
	if err != nil {
		status.Error = err.Error()
	}
	return status
}

// NewSucceededStatus は、レビューが完了したことを記録する JobStatus を組み立てます。
// スキップも「ジョブとしては正常終了」として succeeded で記録し、Outcome で区別します。
func NewSucceededStatus(req ReviewRequest, outcome review.Status) JobStatus {
	status := newStatus(req, jobstatus.StateSucceeded)
	status.Outcome = outcome
	return status
}

func newStatus(req ReviewRequest, state jobstatus.State) JobStatus {
	return JobStatus{
		Status: jobstatus.Status{
			JobID:   req.JobID,
			Command: CommandReview,
			State:   state,
		},
		RepoURL:       req.RepoURL,
		BaseBranch:    req.BaseBranch,
		FeatureBranch: req.FeatureBranch,
		Mode:          req.Mode,
		ModelName:     req.ModelName,
	}
}
