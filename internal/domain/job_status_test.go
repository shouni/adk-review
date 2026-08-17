package domain

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

func testRequest() ReviewRequest {
	return ReviewRequest{
		JobID:         testJobID,
		RepoURL:       "git@github.com:org/repo.git",
		BaseBranch:    "main",
		FeatureBranch: "develop",
		Mode:          "code",
		ModelName:     "gemini-2.5-pro",
		StorageURI:    "gs://review-bucket/reviews/" + testJobID + "/report.json",
	}
}

func TestNewStatusByState(t *testing.T) {
	req := testRequest()

	tests := []struct {
		name        string
		status      JobStatus
		wantState   jobstatus.State
		wantOutcome review.Status
	}{
		{"受付", NewQueuedStatus(req), jobstatus.StateQueued, ""},
		{"実行中", NewRunningStatus(req), jobstatus.StateRunning, ""},
		{"失敗", NewFailedStatus(req, errors.New("boom")), jobstatus.StateFailed, review.StatusFailed},
		{"完了", NewSucceededStatus(req, review.StatusSucceeded), jobstatus.StateSucceeded, review.StatusSucceeded},
		{"スキップ", NewSucceededStatus(req, review.StatusSkipped), jobstatus.StateSucceeded, review.StatusSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.status.State != tt.wantState {
				t.Errorf("State = %q, want %q", tt.status.State, tt.wantState)
			}
			if tt.status.Outcome != tt.wantOutcome {
				t.Errorf("Outcome = %q, want %q", tt.status.Outcome, tt.wantOutcome)
			}
			if tt.status.RepoURL != req.RepoURL || tt.status.FeatureBranch != req.FeatureBranch {
				t.Errorf("リクエストの内容が引き継がれていません: %+v", tt.status)
			}
			if tt.status.Command != CommandReview {
				t.Errorf("Command = %q, want %q", tt.status.Command, CommandReview)
			}
		})
	}
}

// スキップはジョブとしては正常終了です。State だけでは完了と区別できないため、
// Outcome で見分けられる必要があります。
func TestSkippedIsSucceededJobWithDistinctOutcome(t *testing.T) {
	skipped := NewSucceededStatus(testRequest(), review.StatusSkipped)

	if skipped.State != jobstatus.StateSucceeded {
		t.Fatalf("State = %q, want succeeded", skipped.State)
	}
	if skipped.Outcome == review.StatusSucceeded {
		t.Fatal("スキップと完了が区別できません")
	}
	if skipped.HasReport() {
		t.Error("スキップに成果物があることになっています")
	}
}

func TestNewFailedStatusRecordsCause(t *testing.T) {
	status := NewFailedStatus(testRequest(), review.WrapStep(review.StepReview, errors.New("timeout")))

	if status.Error == "" {
		t.Fatal("失敗理由が記録されていません")
	}
	if status.HasReport() {
		t.Error("失敗に成果物があることになっています")
	}
}

// ワーカーは状態が変わるたびにタスクから組み立て直すため、後から確定した値
// （結果の場所・判定・結末）は引き継がないと失われます。
func TestCarryOverExtras(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.ReportURI = "gs://review-bucket/reviews/" + testJobID + "/report.json"
	prev.Decision = review.DecisionMinor

	next := NewRunningStatus(testRequest())
	CarryOverExtras(&next, &prev)

	if next.ReportURI != prev.ReportURI {
		t.Errorf("ReportURI = %q, want %q", next.ReportURI, prev.ReportURI)
	}
	if next.Decision != prev.Decision {
		t.Errorf("Decision = %q, want %q", next.Decision, prev.Decision)
	}
	if next.Outcome != prev.Outcome {
		t.Errorf("Outcome = %q, want %q", next.Outcome, prev.Outcome)
	}
}

// 今回の記録に値があるなら、それを前回の値で上書きしてはいけません。
func TestCarryOverExtrasKeepsNewerValues(t *testing.T) {
	prev := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	prev.Decision = review.DecisionNone

	next := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	next.Decision = review.DecisionBlocker

	CarryOverExtras(&next, &prev)

	if next.Decision != review.DecisionBlocker {
		t.Errorf("Decision = %q, want %q", next.Decision, review.DecisionBlocker)
	}
}

func TestCarryOverExtrasWithoutPrevious(t *testing.T) {
	next := NewRunningStatus(testRequest())
	CarryOverExtras(&next, nil)

	if next.ReportURI != "" || next.Decision != "" {
		t.Errorf("前回の記録が無いのに値が入っています: %+v", next)
	}
}

// 埋め込みにより JSON はフラットに展開されます。入れ子になると、保存済みの
// status.json が読めなくなります。
func TestJobStatusJSONIsFlat(t *testing.T) {
	status := NewSucceededStatus(testRequest(), review.StatusSucceeded)
	status.Decision = review.DecisionMinor

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("エンコードに失敗: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("デコードに失敗: %v", err)
	}

	for _, key := range []string{"job_id", "state", "command", "repo_url", "base_branch", "feature_branch", "outcome", "decision"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("トップレベルに %s がありません: %s", key, data)
		}
	}
	if _, nested := raw["Status"]; nested {
		t.Errorf("共通フィールドが入れ子になっています: %s", data)
	}
}

// 空の項目は出力しません。実行中のジョブの status.json に空の判定が並ぶと、
// 読み手が「判定なし」と「未確定」を区別できなくなります。
func TestJobStatusOmitsEmptyFields(t *testing.T) {
	data, err := json.Marshal(NewRunningStatus(testRequest()))
	if err != nil {
		t.Fatalf("エンコードに失敗: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("デコードに失敗: %v", err)
	}

	for _, key := range []string{"outcome", "decision", "report_uri", "error"} {
		if _, ok := raw[key]; ok {
			t.Errorf("未確定の %s が出力されています: %s", key, data)
		}
	}
}
