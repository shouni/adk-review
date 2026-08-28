package handlers

import (
	"errors"
	"net/http"

	"github.com/shouni/go-job-kit/jobstatus"
)

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
