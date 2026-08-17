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
