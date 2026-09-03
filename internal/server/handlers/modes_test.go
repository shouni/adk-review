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
			t.Errorf("モード %q がありません: %+v", key, got.Modes)
			continue
		}
		// 説明が空だと、呼び出し元はモードを選べません。
		if mode.Label == "" || mode.Direction == "" || mode.UseWhen == "" {
			t.Errorf("%s: 説明が欠けています: %+v", key, mode)
		}
	}

	// excerpt はプロンプト側の宣言をそのまま出します。
	if byKey["code"].Excerpt != string(assets.ExcerptCode) {
		t.Errorf("code の excerpt = %q, want %q", byKey["code"].Excerpt, assets.ExcerptCode)
	}
}
