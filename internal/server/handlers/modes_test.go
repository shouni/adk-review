package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shouni/adk-review/assets"
)

// --- GET /modes ---

func TestHandleModes_ListsEveryMode(t *testing.T) {
	t.Parallel()

	h := buildHandlerWithHistory(t, &recordingHistory{})
	rec := httptest.NewRecorder()
	h.HandleModes(rec, jsonRequest(http.MethodGet, "/modes"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[modesResponse](t, rec)
	byKey := make(map[string]ModeInfo, len(got.Modes))
	for _, m := range got.Modes {
		byKey[m.Key] = m
	}

	for _, key := range []string{"article", "code", "novel"} {
		mode, ok := byKey[key]
		if !ok {
			t.Errorf("mode %q is missing: %+v", key, got.Modes)
			continue
		}
		// With an empty description, callers cannot choose a mode.
		if mode.Label == "" || mode.Direction == "" || mode.UseWhen == "" {
			t.Errorf("%s: the description is missing: %+v", key, mode)
		}
	}

	// excerpt passes the prompt's declaration through unchanged.
	if byKey["code"].Excerpt != string(assets.ExcerptCode) {
		t.Errorf("code excerpt = %q, want %q", byKey["code"].Excerpt, assets.ExcerptCode)
	}
}
