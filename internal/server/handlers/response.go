package handlers

import (
	"errors"
	"net/http"

	"github.com/shouni/go-job-kit/jobstatus"
)

// recordErrorStatus returns the HTTP status for a failure to read the progress.
//
// The point is to separate "not there yet" from "could not be read". Conflated, a revoked
// permission or a storage outage would reach both the screen and the API as "no such review",
// and callers would give up where they should retry. jobstatus returns the two as different
// errors, so they are mapped as is (keeping the distinction is also go-job-kit's promise).
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
