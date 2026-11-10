package builder

import (
	"github.com/shouni/go-job-kit/jobstatus"
	"github.com/shouni/go-remote-io/remoteio"

	"github.com/shouni/adk-review/internal/domain"
)

// buildStatusStore initialises reading and writing of the job progress.
//
// Placement is left to UnderJobDir so it sits under the same job directory as the artifacts.
// Deleting history is then one prefix deletion, and no status file is left behind.
func buildStatusStore(store remoteio.Store, layout domain.StorageLayout) domain.StatusStore {
	return jobstatus.NewStore[domain.JobStatus](
		store,
		jobstatus.UnderJobDir(layout.ReviewPrefixURI()),
	)
}
