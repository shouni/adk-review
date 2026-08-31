package adkagent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/agent"
)

// search_text ツールです。作業ディレクトリ配下のテキストファイルを横断して検索します。
// ツールの登録（説明文つき）は newTools にあります。

type searchTextArgs struct {
	Query string `json:"query"`
	// Context は、ヒット行の前後に添える行数です（0〜maxSearchContext、既定 0）。
	//
	// 既定を 0 にしているのは、広い検索で総量の上限に先に当たると、**添えた文脈のぶんだけ
	// ヒットそのものが落ちる**ためです。1 行では判断できないと分かっている検索でだけ指定します。
	Context int `json:"context,omitempty"`
}

type searchTextResult struct {
	// Hits の各要素は "path:line: 行の内容" 形式です。
	Hits      []string `json:"hits,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (t *toolbox) searchTextTool(toolCtx agent.Context, args searchTextArgs) (searchTextResult, error) {
	return t.searchText(toolCtx, args)
}

func (t *toolbox) searchText(toolCtx context.Context, args searchTextArgs) (searchTextResult, error) {
	if !t.spend() {
		return searchTextResult{Error: budgetExhaustedMsg}, nil
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return searchTextResult{Error: "query が空です"}, nil
	}
	if len(query) > maxQueryBytes {
		return searchTextResult{Error: fmt.Sprintf("query が長すぎます（%d バイト、上限 %d バイト）", len(query), maxQueryBytes)}, nil
	}

	ctxLines := min(max(args.Context, 0), maxSearchContext)
	t.trace(toolCtx, "search_text", "query", query, "context", ctxLines)

	run := searchRun{root: t.root, lowered: strings.ToLower(query), ctxLines: ctxLines}
	if err := filepath.WalkDir(t.root, run.walkFunc(toolCtx)); err != nil {
		if cerr := cancelErr(toolCtx, "search_text"); cerr != nil {
			return searchTextResult{}, cerr
		}
		return searchTextResult{Error: fmt.Sprintf("検索に失敗しました: %v", err)}, nil
	}
	return searchTextResult{Hits: run.hits, Truncated: run.truncated}, nil
}

// searchRun は、1 回の search_text で積み上がる状態です。
//
// ヒット数も返却量も検索全体でひとつの上限なので、ファイルをまたいで持ち回る必要が
// あります。WalkDir のコールバックへ閉じ込めると、ひとつの関数が状態の管理・ディレクトリ
// 走査・行の切り出しを兼ねることになるため、状態のほうを型にして走査を分けています。
type searchRun struct {
	root     string
	lowered  string // 小文字化した検索語。大文字小文字を区別しません。
	ctxLines int

	hits        []string
	resultBytes int
	matches     int
	scanned     int
	truncated   bool
}

// walkFunc は、root 配下を走査してヒットを積む WalkDir のコールバックを返します。
//
// ここで見るのはファイル単位の足切り（種別・サイズ・文字コード・走査数）だけで、
// 中身の走査は scanFile が持ちます。
func (s *searchRun) walkFunc(ctx context.Context) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// キャンセルはファイル単位で見ます。1 ファイルは maxSearchByteLen で頭打ちなので、
		// 1 回のコールバックが締切を大きく踏み越えることはありません。
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		// ★ 通常ファイル以外（symlink・FIFO・デバイス）は開きません。**os.ReadFile は
		// symlink を辿るため、ここを通すと root 外のファイルの中身がモデルの入力に入ります。**
		// d.Info() が返すのはリンク自身の情報なので、下のサイズ検査も素通りします
		// （read_file は resolve で塞いでいますが、こちらは WalkDir のパスを直接開きます）。
		if !d.Type().IsRegular() {
			return nil
		}
		// 走査したファイル数でも打ち切ります。ヒットが 1 件も無いとヒット上限が効かず、
		// 1 レビューぶんのツール予算をリポジトリの全走査に何度も使ってしまうためです。
		s.scanned++
		if s.scanned > maxSearchFiles {
			s.truncated = true
			return filepath.SkipAll
		}

		info, err := d.Info()
		if err != nil || info.Size() > maxSearchByteLen {
			return nil
		}
		// path は WalkDir が s.root 配下から渡す通常ファイルのパスです。
		content, err := os.ReadFile(path) //nolint:gosec // 走査元が root 配下の通常ファイルに限定済み
		if err != nil || !utf8.Valid(content) {
			return nil
		}

		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		return s.scanFile(filepath.ToSlash(rel), string(content))
	}
}

// scanFile は 1 ファイルの中身を走査し、ヒットと（指定があれば）その前後の行を積みます。
//
// 上限に当たったときに filepath.SkipAll を返すのは、続きを読んでも載せられないためです。
func (s *searchRun) scanFile(relPath, content string) error {
	// emit は 1 行を結果へ載せます。載せられなければ false を返します。
	//
	// 既に載せた行は飛ばします。ヒットが近いと前後の文脈が重なるためで、
	// 潰さないと同じ行が何度も並んで総量の上限を無駄に食います。
	lastEmitted := 0
	emit := func(no int, line string) bool {
		if no <= lastEmitted {
			return true
		}
		hit, cut := formatHit(relPath, no, line)
		if cut {
			s.truncated = true
		}
		// 総量の上限に当たったら、その 1 件は載せずに打ち切ります。
		if s.resultBytes+len(hit) > maxSearchResultBytes {
			s.truncated = true
			return false
		}
		s.resultBytes += len(hit)
		s.hits = append(s.hits, hit)
		lastEmitted = no
		return true
	}

	// strings.Lines は行を切り出しながら回すので、大きなファイルでも
	// 全行ぶんのスライスを一度に確保しません。**前後の文脈も、直前の数行を
	// 持ち回るだけで足ります**（全行を配列にする必要はありません）。
	var before []bufferedLine
	remainingAfter := 0
	lineNo := 0

	for raw := range strings.Lines(content) {
		lineNo++
		// 落とすのは改行だけです。TrimSpace までやるとインデントが消え、
		// モデルから見て行の位置関係（ネストの深さ、箇条書きの階層）が読めなくなります。
		line := strings.TrimRight(raw, "\r\n")

		switch {
		case strings.Contains(strings.ToLower(line), s.lowered):
			if s.matches >= maxSearchHits {
				s.truncated = true
				return filepath.SkipAll
			}
			s.matches++
			for _, b := range before {
				if !emit(b.no, b.text) {
					return filepath.SkipAll
				}
			}
			before = before[:0]
			if !emit(lineNo, line) {
				return filepath.SkipAll
			}
			remainingAfter = s.ctxLines

		case remainingAfter > 0:
			if !emit(lineNo, line) {
				return filepath.SkipAll
			}
			remainingAfter--

		case s.ctxLines > 0:
			if len(before) == s.ctxLines {
				before = before[1:]
			}
			before = append(before, bufferedLine{no: lineNo, text: line})
		}
	}
	return nil
}

// bufferedLine は、ヒットの手前で持ち回る 1 行です。
type bufferedLine struct {
	no   int
	text string
}

// formatHit は "path:line: 行の内容" 形式のヒットを組み立て、行を切り詰めたかを返します。
// 切り詰めは文字境界で行い、切ったことが分かるよう末尾に省略記号を付けます。
func formatHit(rel string, lineNo int, line string) (string, bool) {
	if len(line) <= maxHitBytes {
		return fmt.Sprintf("%s:%d: %s", rel, lineNo, line), false
	}
	return fmt.Sprintf("%s:%d: %s…", rel, lineNo, line[:truncateAtRune(line, maxHitBytes)]), true
}
