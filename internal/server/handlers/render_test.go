package handlers

import (
	"testing"

	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-review-kit/review"
)

// 判定ごとの表示が全種類そろっていること。
//
// レビューツールとして、ブロッカーが「—」や薄いグレーで出るのはいちばん痛い壊れ方です。
// 1 つの判定しか踏まないテストだと、他の分岐が壊れても気付けません。
func TestDecisionBadgeCoversAllDecisions(t *testing.T) {
	t.Parallel()

	for _, decision := range review.Decisions() {
		t.Run(string(decision), func(t *testing.T) {
			t.Parallel()

			b := decisionBadge(decision)
			if b == unknownDecisionBadge {
				t.Fatalf("%q の表示が未定義のまま", decision)
			}
			if b.label == "" || b.class == "" {
				t.Fatalf("%q の表示が空: %+v", decision, b)
			}
		})
	}

	if got := decisionBadge("なにかの新しい判定"); got != unknownDecisionBadge {
		t.Errorf("未知の判定 = %+v, want %+v", got, unknownDecisionBadge)
	}
}

// ブロッカーは危険色で出ること。判定の重さが色に出ないと一覧で見落とします。
func TestDecisionBlockerIsDanger(t *testing.T) {
	t.Parallel()

	if got := decisionClass(review.DecisionBlocker); got != "text-bg-danger" {
		t.Errorf("ブロッカーのクラス = %q, want text-bg-danger", got)
	}
	if got := decisionLabel(review.DecisionBlocker); got == "—" {
		t.Error("ブロッカーの表示名が未定義扱いになっている")
	}
}

// severityClass は decisionClass へ委譲しますが、型が違うので別関数です。
// 委譲が外れていないことを確かめます。
func TestSeverityClassMatchesDecisionClass(t *testing.T) {
	t.Parallel()

	for _, severity := range review.Severities() {
		if got, want := severityClass(severity), decisionClass(review.Decision(severity)); got != want {
			t.Errorf("severityClass(%q) = %q, want %q", severity, got, want)
		}
	}
}

// 進行状況の表示が全状態そろっていること。
func TestStateBadgeCoversAllStates(t *testing.T) {
	t.Parallel()

	states := []jobstatus.State{
		jobstatus.StateQueued,
		jobstatus.StateRunning,
		jobstatus.StateFailed,
		jobstatus.StateSucceeded,
	}
	for _, state := range states {
		b := stateBadge(state, review.StatusSucceeded)
		if b == unknownStateBadge {
			t.Errorf("%q の表示が未定義のまま", state)
		}
	}

	// スキップは succeeded だが完了とは別扱いにします。
	skipped := stateBadge(jobstatus.StateSucceeded, review.StatusSkipped)
	done := stateBadge(jobstatus.StateSucceeded, review.StatusSucceeded)
	if skipped == done {
		t.Error("スキップと完了が同じ表示になっている")
	}

	if got := stateBadge("なにかの新しい状態", review.StatusSucceeded); got != unknownStateBadge {
		t.Errorf("未知の状態 = %+v, want %+v", got, unknownStateBadge)
	}
}

// 引用の整形。モデルが付けてくるコードフェンスと前後の空行を落とすこと。
func TestCodeText(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   string
		want string
	}{
		"言語付きフェンスを剥がす": {
			in:   "```go\nfunc main() {}\n```",
			want: "func main() {}",
		},
		"言語なしフェンスを剥がす": {
			in:   "```\nhello\n```",
			want: "hello",
		},
		"前後の空行を落とす": {
			in:   "\n\n    アキラは剣を抜いた。\n\n",
			want: "    アキラは剣を抜いた。",
		},
		"インデントは残す": {
			in:   "if x {\n\treturn nil\n}",
			want: "if x {\n\treturn nil\n}",
		},
		"閉じていないフェンスは内容として残す": {
			in:   "```go\nfunc main() {}",
			want: "```go\nfunc main() {}",
		},
		"途中のフェンスは残す": {
			in:   "見出し\n```go\ncode\n```\n続き",
			want: "見出し\n```go\ncode\n```\n続き",
		},
		"CRLF を LF に揃える": {
			in:   "a\r\nb",
			want: "a\nb",
		},
		"空文字": {in: "", want: ""},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := codeText(tt.in); got != tt.want {
				t.Errorf("codeText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// 引用のクラスはモードで分かれること。判定の出どころはプロンプトの front matter です。
func TestExcerptClass(t *testing.T) {
	t.Parallel()

	if got := excerptClass("code"); got != codeBlockClass {
		t.Errorf("excerptClass(code) = %q, want %q", got, codeBlockClass)
	}
	for _, mode := range []string{"novel", "article", "消えたモード", ""} {
		if got := excerptClass(mode); got != quoteBlockClass {
			t.Errorf("excerptClass(%q) = %q, want %q", mode, got, quoteBlockClass)
		}
	}
}

// 差分の大きさは MAX_DIFF_BYTES（既定 320 KiB）と見比べる値なので、桁を数えずに
// 読める形にすること。
func TestByteSize(t *testing.T) {
	t.Parallel()

	tests := map[int]string{
		0:       "0 B",
		512:     "512 B",
		1024:    "1 KiB",
		327680:  "320 KiB",
		491186:  "480 KiB",
		1 << 20: "1.0 MiB",
	}
	for in, want := range tests {
		if got := byteSize(in); got != want {
			t.Errorf("byteSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestDurationText(t *testing.T) {
	t.Parallel()

	tests := map[int64]string{
		0:       "0.0 秒",
		39_000:  "39.0 秒",
		152_000: "2 分 32 秒",
	}
	for in, want := range tests {
		if got := durationText(in); got != want {
			t.Errorf("durationText(%d) = %q, want %q", in, got, want)
		}
	}
}
