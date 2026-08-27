package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shouni/go-job-kit/jobstatus"

	"github.com/shouni/gcp-kit/negotiate"
)

// errorResponse は、JSON を求めた呼び出し元へ返すエラー本文です。
type errorResponse struct {
	Error string `json:"error"`
}

// wantsJSON は、呼び出し元が JSON を求めているかを返します。
//
// ルートを分けずに Accept で切り替えるのは、同じ取得処理を 2 本持たないためです。
// 画面用と API 用にハンドラを分けると、片方だけ直したときに画面の表示と機械可読な
// 結果が食い違います。5 つの兄弟アプリすべてが同じ形です。
//
// 判定を gcp-kit へ委ねているのは、同時に Vary: Accept を立てさせるためです。
// 同じ URL が Accept で中身を変えるのにキャッシュへ伝えないと、共有キャッシュや
// CDN を挟んだとき JSON を求めたクライアントへ HTML が返りえます。以前は
// 3 アプリが逐語コピーを持ち、3 つとも Vary を落としていました。
func wantsJSON(w http.ResponseWriter, r *http.Request) bool {
	return negotiate.WantsJSON(w, r)
}

// writeJSON は、payload を JSON として書き出します。
func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// ヘッダーは送信済みなので、ここからエラーへ倒すことはできません。記録だけ残します。
		slog.ErrorContext(r.Context(), "レスポンスのエンコードに失敗しました", "error", err)
	}
}

// writeError は、JSON を求められていれば JSON で、そうでなければ text/plain で返します。
//
// JSON 固定にしないのは、画面側の JS がエラー本文を resp.text() で読んでいるためです。
func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if wantsJSON(w, r) {
		writeJSON(w, r, status, errorResponse{Error: message})
		return
	}
	http.Error(w, message, status)
}

// recordErrorStatus は、進行状況の読み出し失敗に対応する HTTP ステータスを返します。
//
// **「まだ無い」と「読めなかった」を分けるのが要点です。** 同一視すると、権限剥奪や
// ストレージ障害が「そんなレビューはありません」として画面にも API にも出て、
// 呼び出し元は再試行すべき場面で諦めます。jobstatus が両者を別のエラーとして返すので、
// そのまま割り当てます（この区別を潰さないことは go-job-kit 側の約束でもあります）。
func recordErrorStatus(err error) int {
	switch {
	case errors.Is(err, jobstatus.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, jobstatus.ErrUnavailable):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
