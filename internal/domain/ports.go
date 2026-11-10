// Package domain defines the core domain models and interfaces (ports) of the review workflow.
package domain

import (
	"context"
)

// Pipeline processes one review request to the end.
//
// The implementation is internal/pipeline's Runner (the ACL over go-review-kit's pipeline),
// held by the worker role only.
type Pipeline interface {
	Execute(ctx context.Context, payload ReviewRequest) error
}
