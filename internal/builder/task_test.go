package builder

import (
	"net/url"
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/domain"
)

// 投入側が組み立てる宛先が、受信側の登録パスと一致すること。
//
// この 2 つはかつて別々のリテラルで、ずれると投入したタスクが全部 404 になり、
// review-queue は max_attempts = 1 なので再試行もされず黙って消えていました。
// 定数を共有したうえで、組み立て結果まで確かめます。
func TestWorkerTaskURLMatchesRegisteredPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workerURL string
		want      string
	}{
		{
			name:      "末尾スラッシュなし",
			workerURL: "https://worker.example.com",
			want:      "https://worker.example.com/tasks/execute_review",
		},
		{
			name:      "末尾スラッシュあり",
			workerURL: "https://worker.example.com/",
			want:      "https://worker.example.com/tasks/execute_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := url.JoinPath(tt.workerURL, domain.TaskExecuteReviewPath)
			if err != nil {
				t.Fatalf("宛先を組み立てられなかった: %v", err)
			}
			if got != tt.want {
				t.Errorf("宛先 = %q, want %q", got, tt.want)
			}
			if !strings.HasSuffix(got, domain.TaskExecuteReviewPath) {
				t.Errorf("宛先 %q が登録パス %q で終わっていない", got, domain.TaskExecuteReviewPath)
			}
		})
	}
}

// パスは絶対パスであること。相対だと JoinPath が WORKER_URL のパス部分に
// 継ぎ足す形になり、宛先が環境ごとにずれます。
func TestTaskExecutePathIsAbsolute(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(domain.TaskExecuteReviewPath, "/") {
		t.Errorf("TaskExecuteReviewPath = %q, 先頭は / であるべき", domain.TaskExecuteReviewPath)
	}
}
