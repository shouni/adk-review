package domain

import "github.com/shouni/go-remote-io/remoteio"

const (
	// reviewPrefix is the object prefix under which review jobs are listed.
	reviewPrefix = "reviews/"
	// statusFileName is the file name of the progress and list metadata.
	statusFileName = "status.json"
	// reportFileName is the file name of the full review result.
	reportFileName = "report.json"
)

// StorageLayout gathers how GCS object names are decided in one place.
//
// A job's objects are gathered under its job ID prefix. Scattered at the bucket root, every
// history deletion would mean someone enumerating "what this job made". Gathered under a
// prefix, the deleter removes them all without knowing what is inside.
//
// Keeping the status file (status.json) next to the artifacts is deliberate. The history list
// is built by enumerating the prefixes directly under reviews/, so with them together, jobs
// still running or failed appear in the list as well. For reviews, "requested but missing from
// the history" is the worse problem, so jobs without artifacts are kept visible.
type StorageLayout struct {
	bucket string
}

// NewStorageLayout returns the StorageLayout for the given bucket.
func NewStorageLayout(bucket string) StorageLayout {
	return StorageLayout{bucket: bucket}
}

// ReviewPrefixURI returns the URI of the prefix the history list enumerates.
func (l StorageLayout) ReviewPrefixURI() string {
	return remoteio.BuildURI(remoteio.SchemeGCS, l.bucket, reviewPrefix)
}

// JobPrefixURI returns the URI of the prefix holding one job's objects.
// History is deleted per prefix.
func (l StorageLayout) JobPrefixURI(jobID string) string {
	return remoteio.BuildURI(remoteio.SchemeGCS, l.bucket, JobPrefix(jobID))
}

// StatusURI returns the URI of the progress and list metadata (status.json).
func (l StorageLayout) StatusURI(jobID string) string {
	return remoteio.BuildURI(remoteio.SchemeGCS, l.bucket, JobPrefix(jobID)+statusFileName)
}

// ReportURI returns the URI of the full review result (report.json).
//
// It is a separate object from the list metadata because the history list reads status.json
// once per row. With the full findings in the same file, the list's reads would grow with the
// number of findings.
func (l StorageLayout) ReportURI(jobID string) string {
	return remoteio.BuildURI(remoteio.SchemeGCS, l.bucket, JobPrefix(jobID)+reportFileName)
}

// JobPrefix returns one job's relative object prefix.
func JobPrefix(jobID string) string {
	return reviewPrefix + jobID + "/"
}
