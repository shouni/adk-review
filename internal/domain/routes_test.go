package domain_test

import (
	"strings"
	"testing"

	"github.com/shouni/adk-review/internal/domain"
)

// 配送先 URL の組み立て。期待値のパスは domain の定数を使わずリテラルで書きます。
// 定数で書くとパスを変えてもこのテストが緑のままになり、ルート登録側とのずれを
// 検出できなくなるためです。
func TestWorkerTaskURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		service string
		want    string
	}{
		{name: "サービス URL に継ぎ足す", service: "https://worker.example.com", want: "https://worker.example.com/tasks/execute_review"},
		{name: "末尾スラッシュを重ねない", service: "https://worker.example.com/", want: "https://worker.example.com/tasks/execute_review"},
		{name: "前後の空白を落とす", service: "  https://worker.example.com  ", want: "https://worker.example.com/tasks/execute_review"},
		{name: "ベースパスの下に付ける", service: "https://worker.example.com/base", want: "https://worker.example.com/base/tasks/execute_review"},
		{name: "クエリの前に差し込む", service: "https://worker.example.com?q=1", want: "https://worker.example.com/tasks/execute_review?q=1"},
		{name: "フラグメントの前に差し込む", service: "https://worker.example.com#frag", want: "https://worker.example.com/tasks/execute_review#frag"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.WorkerTaskURL(tt.service)
			if err != nil {
				t.Fatalf("WorkerTaskURL(%q) error = %v", tt.service, err)
			}
			if got != tt.want {
				t.Fatalf("WorkerTaskURL(%q) = %q, want %q", tt.service, got, tt.want)
			}
		})
	}
}

// ap-infra が WORKER_URL にパスまで書いていた頃の値を、そのまま受けられること。
// 二重に継ぎ足すと /tasks/execute_review/tasks/execute_review になり、投入したタスクが全件 404 に
// なります。コードと Terraform の入れ替え順に関係なく動くための保険です。
func TestWorkerTaskURLAcceptsURLThatAlreadyHasPath(t *testing.T) {
	t.Parallel()

	const want = "https://worker.example.com/tasks/execute_review"
	for _, in := range []string{
		"https://worker.example.com/tasks/execute_review",
		"https://worker.example.com/tasks/execute_review/",
		"  https://worker.example.com/tasks/execute_review  ",
	} {
		got, err := domain.WorkerTaskURL(in)
		if err != nil {
			t.Fatalf("WorkerTaskURL(%q) error = %v", in, err)
		}
		if got != want {
			t.Fatalf("WorkerTaskURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkerTaskURLRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "   ", "http://[::1", "https://worker.example.com/%zz"} {
		if _, err := domain.WorkerTaskURL(in); err == nil {
			t.Fatalf("WorkerTaskURL(%q) error = nil, want error", in)
		}
	}
}

// パスは絶対パスであること。相対だと JoinPath が WORKER_URL のパス部分に継ぎ足す形に
// なり、宛先が環境ごとにずれます。
func TestWorkerTaskPathIsAbsolute(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(domain.WorkerTaskPath, "/") {
		t.Fatalf("WorkerTaskPath = %q, 先頭は / であるべき", domain.WorkerTaskPath)
	}
}
