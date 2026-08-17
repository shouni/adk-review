package domain

import (
	"strings"
	"testing"
)

const testBucket = "review-bucket"
const testJobID = "20260810-213000-a1b2c3d4"

func TestStorageLayoutURIs(t *testing.T) {
	layout := NewStorageLayout(testBucket)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "一覧が列挙するプレフィックス",
			got:  layout.ReviewPrefixURI(),
			want: "gs://review-bucket/reviews/",
		},
		{
			name: "ジョブのプレフィックス",
			got:  layout.JobPrefixURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/",
		},
		{
			name: "進行状況",
			got:  layout.StatusURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/status.json",
		},
		{
			name: "レビュー結果",
			got:  layout.ReportURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/report.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// 1 ジョブ分のオブジェクトはすべてジョブのプレフィックス配下に収まります。
// 外れると、履歴の削除（プレフィックスの一括削除）で消し残しが出ます。
func TestStorageLayoutKeepsJobObjectsUnderOnePrefix(t *testing.T) {
	layout := NewStorageLayout(testBucket)
	prefix := layout.JobPrefixURI(testJobID)

	for _, uri := range []string{layout.StatusURI(testJobID), layout.ReportURI(testJobID)} {
		if !strings.HasPrefix(uri, prefix) {
			t.Errorf("%q が %q の配下にありません", uri, prefix)
		}
	}
}

// 状態ファイルはジョブのプレフィックス配下に置きます。履歴一覧は reviews/ 直下の
// プレフィックスを列挙して作るため、ここを外すと成果物を持たないジョブ
// （実行中・失敗・スキップ）が一覧から消えます。
func TestStatusLivesUnderTheListedPrefix(t *testing.T) {
	layout := NewStorageLayout(testBucket)

	if !strings.HasPrefix(layout.StatusURI(testJobID), layout.ReviewPrefixURI()) {
		t.Error("状態ファイルが一覧の列挙対象から外れています")
	}
}

func TestJobPrefix(t *testing.T) {
	if got, want := JobPrefix(testJobID), "reviews/"+testJobID+"/"; got != want {
		t.Errorf("JobPrefix() = %q, want %q", got, want)
	}
}
