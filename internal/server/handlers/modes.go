package handlers

import (
	"log/slog"
	"net/http"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/go-serve-kit/respond"
)

// ModeInfo は、選べるレビューモード 1 件です。
//
// 出どころは assets/prompts/*.md の front matter です。呼び出し元にモード名を
// 直書きさせないための一覧で、モードを足しても呼び出し元を変えずに済みます。
type ModeInfo struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	UseWhen   string `json:"use_when"`
	Excerpt   string `json:"excerpt"`
}

// modesResponse は GET /modes の応答です。
type modesResponse struct {
	Modes []ModeInfo `json:"modes"`
}

// HandleModes は、選べるレビューモードの一覧を返します。
//
// このルートだけは HTML を持ちません。画面はフォームの <select> がモード一覧を
// 兼ねているため、人間向けの表示先が既にあります。
func (h *Handler) HandleModes(w http.ResponseWriter, r *http.Request) {
	modes, err := assets.AvailableModes()
	if err != nil {
		// プロンプト資産の破損なので、起動していれば通常は起きません。
		slog.ErrorContext(r.Context(), "レビューモード一覧の読み込みに失敗しました", "error", err)
		respond.ErrorJSON(w, r, http.StatusInternalServerError, "レビューモードを読み込めませんでした。")
		return
	}

	items := make([]ModeInfo, 0, len(modes))
	for _, mode := range modes {
		items = append(items, ModeInfo{
			Key:       mode.Key,
			Label:     mode.DisplayName(),
			Direction: mode.Direction,
			UseWhen:   mode.UseWhen,
			Excerpt:   string(mode.ExcerptKind()),
		})
	}
	respond.JSON(w, r, http.StatusOK, modesResponse{Modes: items})
}
