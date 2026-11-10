package adapters

import (
	"fmt"
	"os"
	"path/filepath"

	"uuid"

	gitsource "github.com/shouni/go-review-kit/git"
	"github.com/shouni/go-review-kit/review"
)

// baseRepoDirName is the name of the directory that collects the clones.
const baseRepoDirName = "reviewer-repos"

// NewDiffSourceFactory builds a review.DiffSourceFactory.
//
// The go-git implementation (GoGit) is chosen because this service runs on Cloud Run, where
// instances are disposable. It needs no git binary, and Close removes the whole working
// directory, so a missed cleanup does not stay behind as used disk space.
func NewDiffSourceFactory(sshKeyPath string) (review.DiffSourceFactory, error) {
	root := filepath.Join(os.TempDir(), baseRepoDirName)

	factory, err := gitsource.NewGoGitFactory(root,
		gitsource.WithSSHKey(sshKeyPath),
		gitsource.WithDirNamer(uniqueRepoDirName),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build the DiffSourceFactory: %w", err)
	}
	return factory, nil
}

// uniqueRepoDirName returns a clone directory name unique to each run.
//
// The default name is derived from the repository URL alone, so concurrent reviews of the
// same repository fight over one directory. GoGit deletes the working directory on Close, so
// the cleanup of whichever finishes first pulls the ground from under the one still taking
// its diff. A name unique to each run avoids that race.
func uniqueRepoDirName(repoURL string) string {
	return gitsource.RepoDirName(repoURL) + "-" + uuid.NewV4().String()
}
