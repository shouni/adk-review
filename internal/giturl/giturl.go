// Package giturl extracts, from a Git repository URL (HTTPS / SSH), the repository path
// shown on screen and in notifications.
//
// It is for display only. go-review-kit decides the clone location, and internal/domain's
// StorageLayout decides where results are stored.
package giturl

import (
	"net/url"
	"strings"
)

// GetRepositoryPath extracts a path of the form 'owner/repo-name' from a repository URL.
func GetRepositoryPath(repoURL string) string {
	// net/url cannot parse the scp form (git@host:owner/repo.git), so it is turned into the
	// ssh:// form first.
	if strings.HasPrefix(repoURL, "git@") {
		if idx := strings.Index(repoURL, ":"); idx != -1 {
			repoURL = "ssh://" + repoURL[:idx] + "/" + repoURL[idx+1:]
		}
	}

	// An unparsable URL is returned as is. That is not swallowing the error: the callers are a
	// template function and the notification body, where "show the original URL" is the right
	// outcome. Nothing is logged either. This function is in no position to receive a context,
	// so a log line would lack a job_id and could not be traced to anyone's review.
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL
	}

	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	return path
}
