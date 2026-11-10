package handlers

import (
	"net/http"

	"github.com/shouni/adk-review/assets"
	"github.com/shouni/go-serve-kit/respond"
)

// ModeInfo is one selectable review mode.
//
// Its source is the front matter of assets/prompts/*.md. The list keeps callers from
// hard-coding mode names, so adding a mode needs no change on their side.
type ModeInfo struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Direction string `json:"direction"`
	UseWhen   string `json:"use_when"`
	Excerpt   string `json:"excerpt"`
}

// modesResponse is the response of GET /modes.
type modesResponse struct {
	Modes []ModeInfo `json:"modes"`
}

// HandleModes returns the list of selectable review modes.
//
// This route alone has no HTML. On screen the form's <select> doubles as the mode list, so a
// place to show it to people already exists.
func (h *Handler) HandleModes(w http.ResponseWriter, r *http.Request) {
	modes, err := assets.AvailableModes()
	if err != nil {
		// This means broken prompt assets, which normally cannot happen once started.
		respond.ServerErrorJSON(w, r, http.StatusInternalServerError, err, "failed to load the review modes")
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
