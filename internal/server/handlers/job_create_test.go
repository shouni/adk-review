package handlers

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// --- POST /jobs (JSON) ---

func newJSONSubmitRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req
}

func TestHandleJobCreate_AcceptsJSON(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleJobCreate(rec, newJSONSubmitRequest(`{
		"repo_url": "git@github.com:org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro"
	}`))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202\n%s", rec.Code, rec.Body.String())
	}

	got := decodeJSON[submitResponse](t, rec)
	if got.JobID != testJobID {
		t.Errorf("job_id = %q, want %q", got.JobID, testJobID)
	}
	wantURL := "https://service.example.com/jobs/" + testJobID
	if got.DetailURL != wantURL {
		t.Errorf("detail_url = %q, want %q", got.DetailURL, wantURL)
	}
	// 本文を読まなくてもポーリング先が分かるように、Location にも同じ URL を載せます。
	if loc := rec.Header().Get("Location"); loc != wantURL {
		t.Errorf("Location = %q, want %q", loc, wantURL)
	}
	if !enq.called || enq.received.Mode != "code" {
		t.Errorf("キューへ渡った内容が違います: %+v", enq.received)
	}
}

// 呼び出し元に保存先を決めさせないこと。
//
// storage_uri を受け付けると、成果物をバケット内の任意のパスへ書かせられます。
// 黙って捨てるのではなくエラーにするのは、送った側が効いたと思い込まないためです。
func TestHandleJobCreate_RejectsCallerSuppliedStorageURI(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleJobCreate(rec, newJSONSubmitRequest(`{
		"repo_url": "git@github.com:org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro",
		"storage_uri": "gs://other-bucket/anywhere.json"
	}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	if enq.called {
		t.Error("不正な入力なのにキューへ投入されました")
	}
}

func TestHandleJobCreate_JSONValidationError(t *testing.T) {
	t.Parallel()

	h, enq, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleJobCreate(rec, newJSONSubmitRequest(`{
		"repo_url": "https://github.com/org/repo.git",
		"base_branch": "main",
		"feature_branch": "develop",
		"mode": "code",
		"model_name": "gemini-2.5-pro"
	}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	// 画面ではなく理由だけを返します（HTML が混ざっていないこと）。
	if got := decodeJSON[errorBody](t, rec); got.Error == "" {
		t.Error("エラー本文が空です")
	}
	if enq.called {
		t.Error("不正な入力なのにキューへ投入されました")
	}
}

// フォーム投入は JSON を求めない限り従来どおり HTML を返すこと。
func TestHandleJobCreate_FormStillRendersHTML(t *testing.T) {
	t.Parallel()

	h, _, _, _ := buildTestHandler(t, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleJobCreate(rec, newSubmitRequest(validFormBody()))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func newSubmitRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func validFormBody() string {
	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-2.5-flash")
	return v.Encode()
}

func TestHandleJobCreate_ParseError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()
	h.HandleJobCreate(w, newSubmitRequest("%zz"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on parse error")
	}
}

func TestHandleJobCreate_ValidationError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "invalid-mode")
	v.Set("model_name", "gemini-2.5-flash")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
}

func TestHandleJobCreate_ValidationErrorPreservesSelectedModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "invalid-url")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
	body := html.UnescapeString(w.Body.String())
	if !strings.Contains(body, `<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`) {
		t.Fatalf("selected gemini model should be preserved, body=%s", body)
	}
}

func TestHandleJobCreate_ValidationErrorPreservesFormValues(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "invalid-url")
	v.Set("base_branch", "release/2026-04")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "novel")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on validation error")
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`name="repo_url" class="form-control"
                   placeholder="例: git@github.com:user/repo.git"
                   value="invalid-url"`,
		`name="base_branch" class="form-control"
                       value="release/2026-04"`,
		`name="feature_branch" class="form-control"
                       value="feature/new-ui"`,
		`<option value="novel" selected>novel (小説原稿レビュー)</option>`,
		`<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("form value should be preserved: want %q body=%s", want, body)
		}
	}
}

func TestHandleJobCreate_InvalidModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v := url.Values{}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "main")
	v.Set("feature_branch", "feature/new-ui")
	v.Set("mode", "code")
	v.Set("model_name", "gemini-invalid")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called on invalid gemini model")
	}
}

// ジョブ ID を採番できなければ保存先も閲覧先も決まらないため、投入まで進みません。
func TestHandleJobCreate_JobIDError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, errors.New("entropy failure"), nil)
	w := httptest.NewRecorder()
	h.HandleJobCreate(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", w.Code)
	}
	if enq.called {
		t.Fatal("enqueue should not be called when job id assignment fails")
	}
}

func TestHandleJobCreate_EnqueueError(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, errors.New("queue unavailable"))
	w := httptest.NewRecorder()
	h.HandleJobCreate(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called before returning 503")
	}
}

func TestHandleJobCreate_Success(t *testing.T) {
	h, enq, store, history := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()
	h.HandleJobCreate(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called on success")
	}
	if enq.received.ModelName != "gemini-2.5-flash" {
		t.Fatalf("unexpected model name: %s", enq.received.ModelName)
	}
	if enq.received.JobID != testJobID {
		t.Fatalf("unexpected job id: %s", enq.received.JobID)
	}

	// 保存先も閲覧先もジョブ ID から決まります。
	wantURI := "gs://bucket-a/reviews/" + testJobID + "/report.json"
	if enq.received.StorageURI != wantURI {
		t.Fatalf("storage uri = %s, want %s", enq.received.StorageURI, wantURI)
	}
	wantURL := "https://service.example.com/jobs/" + testJobID
	if enq.received.PublicURL != wantURL {
		t.Fatalf("public url = %s, want %s", enq.received.PublicURL, wantURL)
	}
	if body := w.Body.String(); !strings.Contains(body, wantURL) {
		t.Fatalf("response should include the detail URL, body=%q", body)
	}

	// 受付が履歴に残り、一覧のキャッシュが捨てられていること。
	if len(store.saved) != 1 {
		t.Fatalf("記録件数 = %d, want 1", len(store.saved))
	}
	if got := store.saved[0]; got.State != "queued" || got.QueuedAt.IsZero() {
		t.Fatalf("記録内容が想定と違います: %+v", got)
	}
	if history.invalidated != 1 {
		t.Fatalf("キャッシュ破棄の回数 = %d, want 1", history.invalidated)
	}
}

// 記録に失敗しても投入は成立しているため、受付は成功として返します。
func TestHandleJobCreate_StatusRecordFailureStillAccepts(t *testing.T) {
	h, enq, store, history := buildTestHandler(t, nil, nil)
	store.err = errors.New("gcs unavailable")

	w := httptest.NewRecorder()
	h.HandleJobCreate(w, newSubmitRequest(validFormBody()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called")
	}
	if history.invalidated != 0 {
		t.Fatal("記録に失敗したらキャッシュは捨てない想定です")
	}
}

func TestHandleJobCreate_SuccessPreservesFormValues(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v, err := url.ParseQuery(validFormBody())
	if err != nil {
		t.Fatalf("failed to parse valid form body: %v", err)
	}
	v.Set("repo_url", "git@github.com:org/repo.git")
	v.Set("base_branch", "release/2026-04")
	v.Set("feature_branch", "feature/completion-form")
	v.Set("mode", "article")
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if !enq.called {
		t.Fatal("enqueue should be called on success")
	}
	body := html.UnescapeString(w.Body.String())
	for _, want := range []string{
		`name="repo_url" class="form-control"
                   placeholder="例: git@github.com:user/repo.git"
                   value="git@github.com:org/repo.git"`,
		`name="base_branch" class="form-control"
                       value="release/2026-04"`,
		`name="feature_branch" class="form-control"
                       value="feature/completion-form"`,
		`<option value="article" selected>article (技術記事レビュー)</option>`,
		`<option value="gemini-2.5-pro" selected>gemini-2.5-pro</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("form value should be preserved: want %q body=%s", want, body)
		}
	}
}

func TestHandleJobCreate_UsesSelectedModelName(t *testing.T) {
	h, enq, _, _ := buildTestHandler(t, nil, nil)
	w := httptest.NewRecorder()

	v, err := url.ParseQuery(validFormBody())
	if err != nil {
		t.Fatalf("failed to parse valid form body: %v", err)
	}
	v.Set("model_name", "gemini-2.5-pro")

	h.HandleJobCreate(w, newSubmitRequest(v.Encode()))

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", w.Code)
	}
	if enq.received.ModelName != "gemini-2.5-pro" {
		t.Fatalf("unexpected model name: %s", enq.received.ModelName)
	}
}
