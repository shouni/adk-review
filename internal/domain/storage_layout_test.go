package domain

import (
	"strings"
	"testing"
)

const testBucket = "review-bucket"
const testJobID = "20260810-213000-a1b2c3d4"

func TestStorageLayoutURIs(t *testing.T) {
	layout := NewStorageLayout(testBucket)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "prefix the list enumerates",
			got:  layout.ReviewPrefixURI(),
			want: "gs://review-bucket/reviews/",
		},
		{
			name: "job prefix",
			got:  layout.JobPrefixURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/",
		},
		{
			name: "progress",
			got:  layout.StatusURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/status.json",
		},
		{
			name: "review result",
			got:  layout.ReportURI(testJobID),
			want: "gs://review-bucket/reviews/" + testJobID + "/report.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// Every object of a job sits under the job's prefix.
// Anything outside would be left behind when the history is deleted (one prefix deletion).
func TestStorageLayoutKeepsJobObjectsUnderOnePrefix(t *testing.T) {
	layout := NewStorageLayout(testBucket)
	prefix := layout.JobPrefixURI(testJobID)

	for _, uri := range []string{layout.StatusURI(testJobID), layout.ReportURI(testJobID)} {
		if !strings.HasPrefix(uri, prefix) {
			t.Errorf("%q is not under %q", uri, prefix)
		}
	}
}

// The status file sits under the job's prefix. The history list is built by enumerating the
// prefixes directly under reviews/, so moving it out would drop jobs without artifacts
// (running, failed, skipped) from the list.
func TestStatusLivesUnderTheListedPrefix(t *testing.T) {
	layout := NewStorageLayout(testBucket)

	if !strings.HasPrefix(layout.StatusURI(testJobID), layout.ReviewPrefixURI()) {
		t.Error("the status file is outside what the list enumerates")
	}
}

func TestJobPrefix(t *testing.T) {
	if got, want := JobPrefix(testJobID), "reviews/"+testJobID+"/"; got != want {
		t.Errorf("JobPrefix() = %q, want %q", got, want)
	}
}
