package handlers

import (
	"errors"
	"net/http"

	"github.com/shouni/go-job-kit/jobstatus"
)

// errorResponse は、JSON 固定のエンドポイントが返すエラー本文です。
//
// 表現を出し分けるルートは negotiate.Error を使います（相手が JSON を求めていなければ
// text/plain を返します）。ここに残しているのは、画面から開かれることのない
// JSON 専用ルートで、常に同じ本文の形を返す必要があるためです。
type errorResponse struct {
	Error string `json:"error"`
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
