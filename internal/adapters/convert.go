package adapters

import (
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/internal/domain"
)

// toReviewRequest は domain.ReviewRequest をライブラリの review.Request へ変換します。
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
