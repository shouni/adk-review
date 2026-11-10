package pipeline

import (
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/domain"
)

// toReviewRequest converts a domain.ReviewRequest into the library's review.Request.
func toReviewRequest(req domain.ReviewRequest) review.Request {
	return review.Request{
		JobID:      req.JobID,
		RepoURL:    req.RepoURL,
		Base:       req.BaseBranch,
		Head:       req.FeatureBranch,
		Mode:       req.Mode,
		Model:      req.ModelName,
		StorageURI: req.StorageURI,
		PublicURL:  req.PublicURL,
	}
}
