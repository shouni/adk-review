package server

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrossOriginErrorHandler_ReturnsForbiddenPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/submit_review", nil)
	w := httptest.NewRecorder()

	crossOriginErrorHandler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("unexpected content type: got %q want %q", got, "text/html; charset=utf-8")
	}
	body := w.Body.String()
	if !strings.Contains(body, "送信元が許可されていません") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestWriteCrossOriginErrorResponse_FallsBackToPlainTextOnTemplateError(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/submit_review", nil)
	w := httptest.NewRecorder()

	brokenTemplate := template.Must(template.New("wrong-layout.html").Parse("ignored"))

	writeCrossOriginErrorResponse(w, req, brokenTemplate)

	if w.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusForbidden)
	}
	if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("unexpected content type: got %q want %q", got, "text/plain; charset=utf-8")
	}
	body := w.Body.String()
	if !strings.Contains(body, "送信元を確認できなかったため") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// ヘッダーを持たないサーバー間呼び出しが CrossOriginProtection を素通りすること。
//
// **ここが黙って変わると、ap-mcp からの投入だけが 403 になります。** 画面は動いたままなので
// 気付きにくく、原因も「CSRF らしきもの」としか見えません。Go の実装は
// 「Sec-Fetch-Site も Origin も無いリクエストは非ブラウザとみなして許可」する仕様で、
// M2M クライアントはどちらも送りません。その前提をここで固定します。
func TestCrossOriginProtection_AllowsHeaderlessPost(t *testing.T) {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(crossOriginErrorHandler())

	var reached bool
	handler := protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/submit_review", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer dummy")
	// Sec-Fetch-Site も Origin も付けません（Go の HTTP クライアントと同じ形）。

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !reached {
		t.Fatalf("ヘッダー無しの POST が弾かれました: status = %d", w.Code)
	}
	if w.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusAccepted)
	}
}

// ブラウザからのクロスサイト POST は従来どおり弾かれること。
func TestCrossOriginProtection_RejectsCrossSitePost(t *testing.T) {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(crossOriginErrorHandler())

	handler := protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/submit_review", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: got %d want %d", w.Code, http.StatusForbidden)
	}
}
