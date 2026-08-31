package adkagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
)

// read_file ツールです。作業ディレクトリ配下の 1 ファイルを、行範囲を指定して読みます。
// ツールの登録（説明文つき）は newTools にあります。

type readFileArgs struct {
	Path string `json:"path"`
	// From は読み始める行番号です（1 始まり）。0 や負値は先頭からとみなします。
	From int `json:"from,omitempty"`
	// Lines は読む行数です。0 以下なら From から末尾までです。
	Lines int `json:"lines,omitempty"`
}

type readFileResult struct {
	Content string `json:"content,omitempty"`
	// From / To は実際に返した行の範囲です（1 始まり、To を含む）。
	From int `json:"from,omitempty"`
	To   int `json:"to,omitempty"`
	// TotalLines はファイル全体の行数です。**続きがあるかどうかはこれで分かります。**
	// 無いと、範囲を指定して読んだモデルは「これで全部なのか」を判断できません。
	TotalLines int `json:"total_lines,omitempty"`
	// Truncated は、返す量の上限に当たって末尾を落としたことを示します。
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

// readFileTool は ADK へ渡すハンドラです。functiontool が要求する引数型は agent.Context
// ですが、ツール本体が使うのはその context.Context としての側面（キャンセルとログ）だけです。
// 本体を素の context.Context で受けておくと、テストから agent.Context の実装を用意せずに
// 呼べます。list_files / search_text も同じ形です。
func (t *toolbox) readFileTool(toolCtx agent.Context, args readFileArgs) (readFileResult, error) {
	return t.readFile(toolCtx, args)
}

func (t *toolbox) readFile(toolCtx context.Context, args readFileArgs) (readFileResult, error) {
	if !t.spend() {
		return readFileResult{Error: budgetExhaustedMsg}, nil
	}
	// ★ 範囲も残します。**これが無いと、モデルが範囲指定を使っているのかを後から
	// 確かめられません。** 上限や道具の説明を変えたときに効いたかどうかは、
	// prompt_tokens の増減だけでは「なぜ」まで届きません。
	t.trace(toolCtx, "read_file", "path", args.Path, "from", args.From, "lines", args.Lines)

	path, err := t.resolve(args.Path)
	if err != nil {
		slog.WarnContext(toolCtx, "ツールがパスを解決できませんでした", "tool", "read_file", "path", args.Path, "error", err)
		return readFileResult{Error: err.Error()}, nil
	}

	// path は resolve が root 配下へ閉じ込め済みです（symlink 経由の脱出も実体解決で
	// 弾いています）。ここを可変にしないとツールとして成立しません。
	content, err := os.ReadFile(path) //nolint:gosec // resolve でワークスペース内に限定済み
	if err != nil {
		return readFileResult{Error: fmt.Sprintf("読み込みに失敗しました: %v", err)}, nil
	}

	// テキストかどうかは読み込んだ内容そのもので判定します。先に切り詰めると、
	// 日本語のようなマルチバイト文字が途中で割れて「テキストではない」と誤判定されます
	// （3 バイト文字なら切り口が文字境界に当たるのは 1/3 だけです）。
	if !utf8.Valid(content) {
		return readFileResult{Error: "テキストファイルではありません"}, nil
	}

	body, from, to, total := selectLines(string(content), args.From, args.Lines)

	truncated := false
	if len(body) > maxFileBytes {
		body = body[:truncateAtRune(body, maxFileBytes)]
		truncated = true
		// 落としたぶん、返した範囲の終わりも縮みます。ここを直さないと、モデルは
		// 読めていない行を読んだつもりで次へ進みます。
		//
		// 改行の数が、返し切れた行数です。末尾が行の途中で切れていればその行は
		// 数に入らず、ちょうど改行の直後で切れていれば次の行は 1 バイトも入って
		// いません。どちらも to へ足すと、モデルは次にその先から読むので、
		// 渡していない行が黙って飛ばされます。
		if complete := strings.Count(body, "\n"); complete > 0 {
			to = from + complete - 1
		} else {
			// 1 行が maxFileBytes を超えています。全部は返せないので、この行は
			// 返したことにして先へ進ませます。範囲を空にすると、同じ行を読み直す
			// だけのやり取りが残りの呼び出し回数ぶん繰り返されます。
			to = from
		}
	}
	return readFileResult{
		Content:    body,
		From:       from,
		To:         to,
		TotalLines: total,
		Truncated:  truncated,
	}, nil
}

// selectLines は、1 始まりの行範囲を切り出し、範囲と全体の行数を返します。
//
// 範囲の指定が無ければ全体を返します（従来どおり）。範囲が外れている場合は空を返し、
// 行番号は 0 になります。**呼び出し側が総行数を見て指定し直せる**ようにするためで、
// エラーにはしません。
func selectLines(text string, from, count int) (body string, first, last, total int) {
	lines := strings.Split(text, "\n")
	// 末尾の改行で生まれる空要素は行として数えません。
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total = len(lines)

	if from <= 0 {
		from = 1
	}
	if from > total {
		return "", 0, 0, total
	}

	end := total
	// from+count が桁溢れし得るため、残りの行数と比べる形にします。どちらの値も
	// モデルが書く JSON なので、int の上限に近い値が届きます（足してから比べると
	// 負に折り返し、end が from を下回って切り出しで panic します）。
	if remaining := total - from + 1; count > 0 && count < remaining {
		end = from + count - 1
	}
	return strings.Join(lines[from-1:end], "\n"), from, end, total
}

// truncateAtRune は、limit を超えない範囲で最も後ろの文字境界を返します。
// content は utf8.Valid 済みであることを前提とします。
//
// []byte（read_file の中身）と string（search_text のヒット行）の両方から呼ぶため型
// パラメータにしています。どちらも中身はバイト列で、判定に使うのは添字だけです。
func truncateAtRune[T ~[]byte | ~string](content T, limit int) int {
	if len(content) <= limit {
		return len(content)
	}
	// limit の位置が文字の途中なら、その文字の先頭まで戻します。
	// UTF-8 の継続バイトは 0b10xxxxxx なので、最大 3 バイト戻れば境界に着きます。
	for limit > 0 && content[limit]&0xC0 == 0x80 {
		limit--
	}
	return limit
}
