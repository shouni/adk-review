package domain

// ReviewRequest is the input model received by the web layer and passed to the worker through
// Cloud Tasks. It is defined in this repository so it does not depend on an external library's types.
type ReviewRequest struct {
	// JobID is the identifier minted at submission. It decides where the artifacts and progress
	// are stored and corresponds to one row of the history list.
	JobID         string `json:"job_id"`
	RepoURL       string `json:"repo_url"`
	BaseBranch    string `json:"base_branch"`
	FeatureBranch string `json:"feature_branch"`
	Mode          string `json:"mode"`
	ModelName     string `json:"model_name"`

	StorageURI string `json:"storage_uri"`
	PublicURL  string `json:"public_url"`
}
