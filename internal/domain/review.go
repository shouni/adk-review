package domain

// ReviewRequest は Web 層で受け取り、Cloud Tasks でワーカーに渡す入力モデルです。
// 外部ライブラリの型に依存しないよう、このリポジトリ側で定義します。
type ReviewRequest struct {
	// JobID は投入時に採番する識別子です。成果物と進行状況の置き場所を決め、
	// 履歴一覧の 1 行に対応します。
	JobID         string `json:"job_id"`
	RepoURL       string `json:"repo_url"`
	BaseBranch    string `json:"base_branch"`
	FeatureBranch string `json:"feature_branch"`
	Mode          string `json:"mode"`
	ModelName     string `json:"model_name"`
	StorageURI    string `json:"storage_uri"`
	PublicURL     string `json:"public_url"`
}
