// Package assets は、プロンプトテンプレート等を埋め込みリソースとして提供します。
package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/shouni/go-prompt-kit/frontmatter"
	"github.com/shouni/go-prompt-kit/resource"
	"go.yaml.in/yaml/v3"
)

const promptDir = "prompts"

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

// Engine は、レビューモードが使うレビューエンジンです。
type Engine string

// レビューエンジンの種別です。
const (
	// EngineSingle は、差分だけを見る単発のレビューです。
	EngineSingle Engine = "single"
	// EngineAgent は、作業ディレクトリをツールで調べるエージェント型のレビューです。
	EngineAgent Engine = "agent"
)

// ExcerptStyle は、指摘の引用（excerpt / suggestion）を画面でどう見せるかです。
//
// モードによって引用の中身が別物になります。code なら等幅で読むソースコードですが、
// novel / article では日本語の地の文です。同じ体裁で出すと、原稿の引用がコードに見えます。
type ExcerptStyle string

// 引用の見せ方です。
const (
	// ExcerptProse は、地の文としての引用です（本文フォント・明るい背景）。
	ExcerptProse ExcerptStyle = "prose"
	// ExcerptCode は、ソースコードとしての引用です（等幅・暗い背景）。
	ExcerptCode ExcerptStyle = "code"
)

// ModeMetadata は、プロンプト冒頭の front matter に書くモードの説明です。
// 兄弟アプリと同じ方式で、**説明の置き場をプロンプト自身にします。**
//
// 画面側に一覧を持たせない理由は、モードの追加が prompts/<mode>.md を置くだけで
// 済む仕組みだからです。説明を別ファイルに分けると、モードを足した人が説明を
// 書き忘れても誰も気付かず、選択肢だけが増えていきます。
type ModeMetadata struct {
	// Label は選択肢に出す名前です。キー（ファイル名）は英字なので、日本語の表示名を
	// 別に持ちます。
	Label string `yaml:"label"`
	// Direction は何をレビューするモードなのかの一行説明です。
	Direction string `yaml:"direction"`
	// UseWhen は、どういう対象のときに選ぶかです。
	UseWhen string `yaml:"use_when"`
	// Engine は、そのモードを実行するレビューエンジンです。空なら EngineSingle 扱いです。
	//
	// **どのモードをどう実行するかはプロンプト資産側の宣言です。** 依頼のたびに選ぶ
	// 性質のものではないため、フォームには出しません。
	Engine Engine `yaml:"engine"`
	// Excerpt は、指摘の引用を画面でどう見せるかです。空なら ExcerptProse 扱いです。
	//
	// engine と同じく**モードを足す人がプロンプト側で宣言します。** 画面側に
	// 「code なら等幅」といった一覧を持たせると、モードを足したときに
	// prompts/<mode>.md を置くだけでは済まなくなります。
	//
	// 既定を prose にしているのは、このアプリの主対象が原稿だからです。宣言を忘れた
	// モードがコード扱いで表示されるより、地の文として出るほうが被害が小さくなります。
	Excerpt ExcerptStyle `yaml:"excerpt"`
}

// Mode は、フォームに出す 1 モードです。
type Mode struct {
	// Key は prompts/<key>.md のファイル名で、そのまま worker へ渡る値です。
	Key string
	ModeMetadata
}

// DisplayName は選択肢に表示する名前です。
//
// front matter が無いプロンプトを置いてもキーで表示され、**選択肢自体は消えません。**
// 説明の書き忘れで動くはずのモードが画面から消えるほうが困るためです。
func (m Mode) DisplayName() string {
	if m.Label != "" {
		return m.Label
	}
	return m.Key
}

// EngineKind は、そのモードを実行するエンジンを返します。
func (m Mode) EngineKind() Engine {
	if m.Engine == "" {
		return EngineSingle
	}
	return m.Engine
}

// ExcerptKind は、そのモードの引用の見せ方を返します。
func (m Mode) ExcerptKind() ExcerptStyle {
	if m.Excerpt == "" {
		return ExcerptProse
	}
	return m.Excerpt
}

// promptSet は、プロンプトの本文と、front matter から組み立てたモード情報です。
type promptSet struct {
	bodies map[string]string
	modes  map[string]Mode
}

// loadModes は、埋め込みプロンプトの本文と front matter の解析を最初の呼び出しで
// 1度だけ行います。本文（LoadPrompts）とモード情報（AvailableModes / EngineFor）は
// 別の入口ですが、出どころは同じディレクトリです。
//
// 失敗するとすれば front matter の書式や engine の値のように毎回同じ結果になるものだけ
// なので、エラーごとキャッシュして再試行しません。
//
// 返すマップは呼び出し側で共有されます。書き換えないでください。
var loadModes = sync.OnceValues(func() (promptSet, error) {
	raw, err := resource.Load(promptFiles, promptDir)
	if err != nil {
		return promptSet{}, err
	}

	bodies, fronts := frontmatter.SplitMap(raw)

	// **黙って無視しません。** 書き間違えた説明が空欄になるだけだと、画面を開くまで
	// 気付けません。
	metas, err := frontmatter.DecodeMap[ModeMetadata](fronts, yaml.Unmarshal)
	if err != nil {
		return promptSet{}, fmt.Errorf("prompts の front matter が読めません: %w", err)
	}

	modes := make(map[string]Mode, len(bodies))
	for key := range bodies {
		meta := metas[key]

		// engine の綴り違いは起動時に落とします。実行時に既定へ落とすと、
		// エージェントで動かすつもりのモードが黙って単発で動き続けます。
		switch meta.Engine {
		case "", EngineSingle, EngineAgent:
		default:
			return promptSet{}, fmt.Errorf("プロンプト %q の engine 指定が不正です: %q（single か agent）", key, meta.Engine)
		}

		// 綴り違いは engine と同じく起動時に落とします。実行時に既定へ落とすと、
		// コードとして見せるつもりのモードが黙って地の文で表示され続けます。
		switch meta.Excerpt {
		case "", ExcerptProse, ExcerptCode:
		default:
			return promptSet{}, fmt.Errorf("プロンプト %q の excerpt 指定が不正です: %q（prose か code）", key, meta.Excerpt)
		}

		modes[key] = Mode{Key: key, ModeMetadata: meta}
	}
	return promptSet{bodies: bodies, modes: modes}, nil
})

// LoadPrompts は埋め込まれたプロンプトの**本文だけ**を読み込みます。
//
// front matter は説明であってプロンプトではないので、ここで落とします。
// 残したまま渡すと YAML が指示文の先頭に紛れ込みます。
func LoadPrompts() (map[string]string, error) {
	set, err := loadModes()
	if err != nil {
		return nil, err
	}
	return maps.Clone(set.bodies), nil
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

// LoadFindingPolicy は、全レビューモードで共通の指摘の方針を読み込みます。
//
// 行番号の算出・軽微な指摘のまとめ方・出力言語は、モードが変わっても同じ決まりです。
// モードごとのプロンプトに写しを置くと、直したつもりが 1 モードだけ古いまま残ります。
// モード固有の方針（何を優先するか、何に踏み込まないか）は各プロンプトに残します。
func LoadFindingPolicy() (string, error) {
	policy, err := loadPartial("finding_policy.md")
	if err != nil {
		return "", err
	}
	// 末尾の改行を落とします。この断片は箇条書きの**途中**に差し込まれるので、残すと
	// 空行が入り、後ろに続くモード固有の方針が別のリストとして分かれて見えます。
	return strings.TrimRight(policy, "\n"), nil
}

func loadPartial(name string) (string, error) {
	b, err := partialFiles.ReadFile("partials/" + name)
	if err != nil {
		return "", fmt.Errorf("共有テンプレート '%s' の読み込みに失敗: %w", name, err)
	}
	return string(b), nil
}

// AvailableModes は、埋め込まれたレビュープロンプトから利用可能なモードをキー順に返します。
//
// 並びを固定するのは、map の走査順がそのまま選択肢の順になると、
// 描画のたびに並びが変わるためです。
func AvailableModes() ([]Mode, error) {
	set, err := loadModes()
	if err != nil {
		return nil, err
	}

	modes := make([]Mode, 0, len(set.modes))
	for _, mode := range set.modes {
		modes = append(modes, mode)
	}

	sort.Slice(modes, func(i, j int) bool { return modes[i].Key < modes[j].Key })
	return modes, nil
}

// IsValidMode は、指定されたモード名に対応するプロンプトファイルが存在するか確認します。
func IsValidMode(mode string) bool {
	set, err := loadModes()
	if err != nil {
		slog.Error("failed to load prompts for validation", "error", err)
		return false
	}

	_, ok := set.modes[mode]
	return ok
}

// ResolveEngine は、モードの既定と依頼ごとの上書き指定から、実行するエンジンを決めます。
//
// 受付時（フォーム）と実行時（ワーカー）の両方から呼びます。判定を 2 か所に書くと、
// 画面に出した内容と実際に走るエンジンが食い違う余地が生まれるためです。
func ResolveEngine(mode, override string) (Engine, error) {
	if override != "" {
		switch Engine(override) {
		case EngineSingle, EngineAgent:
			return Engine(override), nil
		default:
			return "", fmt.Errorf("未知のレビューエンジンです: %q（single か agent）", override)
		}
	}
	return EngineFor(mode)
}

// ExcerptStyleFor は、モードの引用の見せ方を返します。
//
// **未知のモードでもエラーにしません。** 呼ぶのは履歴の詳細ページで、履歴には
// 過去に実行したモード名がそのまま残ります。プロンプトを消したり改名したりしたあとに
// 過去のレビューを開けなくなるのは、表示の都合として重すぎます。
func ExcerptStyleFor(mode string) ExcerptStyle {
	set, err := loadModes()
	if err != nil {
		return ExcerptProse
	}

	m, ok := set.modes[mode]
	if !ok {
		return ExcerptProse
	}
	return m.ExcerptKind()
}

// EngineFor は、モードを実行するレビューエンジンを返します。
func EngineFor(mode string) (Engine, error) {
	set, err := loadModes()
	if err != nil {
		return "", err
	}

	m, ok := set.modes[mode]
	if !ok {
		return "", fmt.Errorf("未知のレビューモードです: %q", mode)
	}
	return m.EngineKind(), nil
}
