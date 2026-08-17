package giturl_test

import (
	"testing"

	"github.com/shouni/adk-review/internal/giturl"
)

func TestGetRepositoryPath(t *testing.T) {
	tests := []struct {
		name     string
		inputURL string
		want     string
	}{
		{
			name:     "HTTPS_GitSuffix",
			inputURL: "https://github.com/shouni/go-utils.git",
			want:     "shouni/go-utils",
		},
		{
			name:     "HTTPS_NoGitSuffix",
			inputURL: "https://gitlab.com/group/project",
			want:     "group/project",
		},
		{
			name:     "SSH_Protocol_Colon",
			inputURL: "git@bitbucket.org:team/repository.git",
			want:     "team/repository", // git@ と .git が除去され、: が / に変換される
		},
		{
			name:     "SSH_Protocol_LongPath",
			inputURL: "git@host.net:user/subgroup/repo-name.git",
			want:     "user/subgroup/repo-name",
		},
		{
			name:     "SSH_URLScheme",
			inputURL: "ssh://git@github.com/owner/repo.git",
			want:     "owner/repo", // net/url が処理
		},
		{
			name:     "URL_SubDir",
			inputURL: "https://example.com/owner/repo/subdir",
			want:     "owner/repo/subdir",
		},
		{
			name:     "OnlyHostAndOwner",
			inputURL: "https://github.com/owner",
			want:     "owner",
		},
		{
			name:     "EmptyURL",
			inputURL: "",
			want:     "", // パースエラー時、元のURLがそのまま返される
		},
		{
			name:     "InvalidURL",
			inputURL: "::invalid-url",
			want:     "::invalid-url", // パースエラー時、元のURLがそのまま返される
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := giturl.GetRepositoryPath(tt.inputURL)
			if got != tt.want {
				t.Errorf("GetRepositoryPath(%q) = %q, want %q", tt.inputURL, got, tt.want)
			}
		})
	}
}

// ----------------------------------------------------------------------
// TestGenerateGCSKeyName: GCSキー生成関数のテスト
// ----------------------------------------------------------------------
