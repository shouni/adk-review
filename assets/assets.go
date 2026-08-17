// Package assets は、プロンプトテンプレート等を埋め込みリソースとして提供します。
package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/shouni/go-prompt-kit/resource"
)

const (
	promptDir             = "prompts"
	modeDescriptionPrefix = "<!-- mode-description:"
	enginePrefix          = "<!-- engine:"
	metadataSuffix        = "-->"
)

// Engine は、レビューモードが使うレビューエンジンです。
type Engine string

// レビューエンジンの種別です。
const (
	// EngineSingle は、差分だけを見る単発のレビューです（既定）。
	EngineSingle Engine = "single"
	// EngineAgent は、作業ディレクトリを調べるエージェント型のレビューです。
	EngineAgent Engine = "agent"
)

var (
	// promptFiles はプロンプトテンプレートです。ディレクトリ内は現在プロンプトのみのため、
	// ファイル名のprefixは不要です（ファイル名がそのままモード名になります）。
	//go:embed prompts/*.md
	promptFiles embed.FS

	// partialFiles は、複数のプロンプトモードで共有するテキスト断片です。
	// prompts/ とは別ディレクトリに置き、レビューモードの一覧には含めません。
	//go:embed partials/*.md
	partialFiles embed.FS

	// Templates は、HTMLテンプレートです。
	//go:embed templates/*.html
	Templates embed.FS

	// StaticFiles は、ブラウザへ配信するJavaScriptなどの静的ファイルを保持します。
	//go:embed static
	StaticFiles embed.FS
)

// loadPrompts は、埋め込みプロンプトの解析結果を最初の呼び出しで1度だけ構築します。
//
// AvailableModes と IsValidMode はリクエストのたびに呼ばれるため遅延初期化しますが、
// 二重チェックロックを手で書く必要はありません。読み込み元は埋め込みアセットで、
// 失敗するとすれば「モード名の衝突」のように毎回同じ結果になるものだけなので、
// エラーごとキャッシュして再試行しないのが正しい挙動です。
//
// 返すマップは呼び出し側で共有されます。書き換えないでください
// （LoadPrompts と AvailableModes は、いずれも新しい入れ物へ写して返します）。
var loadPrompts = sync.OnceValues(func() (map[string]promptTemplate, error) {
	files, err := resource.Load(promptFiles, promptDir)
	if err != nil {
		return nil, err
	}

	parsed := make(map[string]promptTemplate, len(files))
	for mode, body := range files {
		description, engine, promptBody, err := parsePromptMetadata(mode, body)
		if err != nil {
			return nil, err
		}
		parsed[mode] = promptTemplate{
			body:        promptBody,
			description: description,
			engine:      engine,
		}
	}
	return parsed, nil
})

type promptTemplate struct {
	body        string
	description string
	engine      Engine
}

// ReviewMode は、フォームに表示するレビューモードのメタデータです。
type ReviewMode struct {
	Name        string
	Description string
}

// LoadPrompts は埋め込まれたプロンプトの本文をモード名で引けるマップとして返します。
func LoadPrompts() (map[string]string, error) {
	cached, err := loadPrompts()
	if err != nil {
		return nil, err
	}

	prompts := make(map[string]string, len(cached))
	for mode, prompt := range cached {
		prompts[mode] = prompt.body
	}
	return prompts, nil
}

// LoadFindingsFormat は、レビュー指摘のJSONフォーマットを説明する共通テキストを読み込みます。
// 全レビューモードのプロンプトで共有され、AIの構造化出力(findings配列)のスキーマに
// 対応する項目を説明します。
func LoadFindingsFormat() (string, error) {
	return loadPartial("findings_format.md")
}

// LoadVerdictFormat は、判定結果のJSONフォーマット(verdictオブジェクト)を説明する
// 共通テキストを読み込みます。
func LoadVerdictFormat() (string, error) {
	return loadPartial("verdict_format.md")
}

func loadPartial(name string) (string, error) {
	b, err := partialFiles.ReadFile("partials/" + name)
	if err != nil {
		return "", fmt.Errorf("共有テンプレート '%s' の読み込みに失敗: %w", name, err)
	}
	return string(b), nil
}

// AvailableModes は、埋め込まれたレビュープロンプトから利用可能なモード名を返します。
func AvailableModes() ([]ReviewMode, error) {
	cached, err := loadPrompts()
	if err != nil {
		return nil, err
	}

	modes := make([]ReviewMode, 0, len(cached))
	for mode, prompt := range cached {
		modes = append(modes, ReviewMode{
			Name:        mode,
			Description: prompt.description,
		})
	}

	sort.Slice(modes, func(i, j int) bool {
		return modes[i].Name < modes[j].Name
	})
	return modes, nil
}

// IsValidMode は、指定されたモード名に対応するプロンプトファイルが存在するか確認します。
func IsValidMode(mode string) bool {
	cached, err := loadPrompts()
	if err != nil {
		slog.Error("failed to load prompts for validation", "error", err)
		return false
	}

	_, ok := cached[mode]
	return ok
}

// EngineFor \u306f\u3001\u30e2\u30fc\u30c9\u304c\u4f7f\u3046\u30ec\u30d3\u30e5\u30fc\u30a8\u30f3\u30b8\u30f3\u3092\u8fd4\u3057\u307e\u3059\u3002
// \u30e1\u30bf\u30c7\u30fc\u30bf\u3067\u6307\u5b9a\u3055\u308c\u3066\u3044\u306a\u3044\u30e2\u30fc\u30c9\u306f EngineSingle \u3067\u3059\u3002
func EngineFor(mode string) (Engine, error) {
	cached, err := loadPrompts()
	if err != nil {
		return "", err
	}

	prompt, ok := cached[mode]
	if !ok {
		return "", fmt.Errorf("\u672a\u77e5\u306e\u30ec\u30d3\u30e5\u30fc\u30e2\u30fc\u30c9\u3067\u3059: %q", mode)
	}
	return prompt.engine, nil
}

// parsePromptMetadata \u306f\u3001\u30d7\u30ed\u30f3\u30d7\u30c8\u5192\u982d\u306e\u30e1\u30bf\u30c7\u30fc\u30bf\u30b3\u30e1\u30f3\u30c8\u3092\u53d6\u308a\u51fa\u3057\u307e\u3059\u3002
//
// \u5bfe\u5fdc\u3059\u308b\u306e\u306f mode-description \u3068 engine \u306e 2 \u7a2e\u985e\u3067\u3001\u3069\u3061\u3089\u3082\u4efb\u610f\u30fb\u9806\u4e0d\u540c\u3067\u3059\u3002
// engine \u306e\u6307\u5b9a\u30df\u30b9\u306f\u8d77\u52d5\u6642\uff08\u521d\u56de\u30ed\u30fc\u30c9\u6642\uff09\u306b\u843d\u3068\u3057\u307e\u3059\u3002\u5b9f\u884c\u6642\u306b\u30d5\u30a9\u30fc\u30eb\u30d0\u30c3\u30af\u3059\u308b\u3068\u3001
// \u30a8\u30fc\u30b8\u30a7\u30f3\u30c8\u3067\u52d5\u304b\u3059\u3064\u3082\u308a\u306e\u30e2\u30fc\u30c9\u304c\u9ed9\u3063\u3066\u5358\u767a\u3067\u52d5\u304d\u7d9a\u3051\u308b\u305f\u3081\u3067\u3059\u3002
func parsePromptMetadata(mode, body string) (description string, engine Engine, promptBody string, err error) {
	description = mode
	engine = EngineSingle
	rest := strings.TrimLeft(body, "\ufeff \t\r\n")

	for {
		switch {
		case strings.HasPrefix(rest, modeDescriptionPrefix):
			value, remainder, ok := cutMetadata(rest, modeDescriptionPrefix)
			if !ok {
				return description, engine, rest, nil
			}
			if value != "" {
				description = value
			}
			rest = remainder

		case strings.HasPrefix(rest, enginePrefix):
			value, remainder, ok := cutMetadata(rest, enginePrefix)
			if !ok {
				return description, engine, rest, nil
			}
			switch Engine(value) {
			case EngineSingle, EngineAgent:
				engine = Engine(value)
			default:
				return "", "", "", fmt.Errorf("\u30d7\u30ed\u30f3\u30d7\u30c8 %q \u306e engine \u6307\u5b9a\u304c\u4e0d\u6b63\u3067\u3059: %q\uff08single \u304b agent\uff09", mode, value)
			}
			rest = remainder

		default:
			return description, engine, rest, nil
		}
	}
}

// cutMetadata \u306f\u3001prefix \u3067\u59cb\u307e\u308b\u30e1\u30bf\u30c7\u30fc\u30bf\u30b3\u30e1\u30f3\u30c8 1 \u3064\u3092\u5024\u3068\u6b8b\u308a\u306b\u5206\u3051\u307e\u3059\u3002
func cutMetadata(s, prefix string) (value, rest string, ok bool) {
	end := strings.Index(s, metadataSuffix)
	if end < len(prefix) {
		return "", "", false
	}
	value = strings.TrimSpace(s[len(prefix):end])
	rest = strings.TrimLeft(s[end+len(metadataSuffix):], " \t\r\n")
	return value, rest, true
}
