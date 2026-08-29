package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shouni/go-serve-kit/respond"
)

// 表現を Accept で選ぶ以上、それをキャッシュへ伝える必要があります。
// Vary が無いと、共有キャッシュや CDN を挟んだとき JSON を求めた
// クライアントへ HTML が返りえます。判定を go-serve-kit/respond に
// 委ねているのは、判定と宣言を切り離せなくするためです。
func TestWantsJSONSetsVary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		accept string
		want   bool
	}{
		{"JSON を求めている", "application/json", true},
		{"ブラウザ", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", false},
		{"Accept なし", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()

			if got := respond.WantsJSON(rec, req); got != tt.want {
				t.Errorf("respond.WantsJSON() = %v, want %v", got, tt.want)
			}
			// 判定の結果によらず Vary は必ず立ちます。
			if got := rec.Header().Get("Vary"); got != "Accept" {
				t.Errorf("Vary = %q, want %q", got, "Accept")
			}
		})
	}
}
