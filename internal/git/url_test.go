package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name        string
		rawURL      string
		wantHost    string
		wantOrg     string
		wantRepo    string
		wantHTTPUrl string
		wantSSHUrl  string
	}{
		{
			name:        "https with .git suffix",
			rawURL:      "https://github.com/acme/widgets.git",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "https without .git suffix",
			rawURL:      "https://github.com/acme/widgets",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "https with trailing slash",
			rawURL:      "https://github.com/acme/widgets/",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "http scheme",
			rawURL:      "http://internal.example.com/acme/widgets.git",
			wantHost:    "internal.example.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://internal.example.com/acme/widgets",
			wantSSHUrl:  "git@internal.example.com:acme/widgets.git",
		},
		{ //nolint:gosec // G101: test-only fake credentials, not a real secret
			name:        "https with userinfo is stripped from host",
			rawURL:      "https://user:token@github.example.com/acme/widgets.git",
			wantHost:    "github.example.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.example.com/acme/widgets",
			wantSSHUrl:  "git@github.example.com:acme/widgets.git",
		},
		{
			name:        "https with port is stripped from host",
			rawURL:      "https://github.example.com:8443/acme/widgets.git",
			wantHost:    "github.example.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.example.com/acme/widgets",
			wantSSHUrl:  "git@github.example.com:acme/widgets.git",
		},
		{
			name:        "scp-like syntax",
			rawURL:      "git@github.com:acme/widgets.git",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "scp-like syntax without .git",
			rawURL:      "git@github.com:acme/widgets",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "scp-like syntax with port",
			rawURL:      "git@github.com:22:acme/widgets.git",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "ssh scheme",
			rawURL:      "ssh://git@github.com/acme/widgets.git",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "ssh scheme with port",
			rawURL:      "ssh://git@github.com:22/acme/widgets.git",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
		{
			name:        "nested namespace captures full intermediate path",
			rawURL:      "https://gitlab.com/group/subgroup/widgets.git",
			wantHost:    "gitlab.com",
			wantOrg:     "group/subgroup",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://gitlab.com/group/subgroup/widgets",
			wantSSHUrl:  "git@gitlab.com:group/subgroup/widgets.git",
		},
		{
			name:        "nested namespace scp-like",
			rawURL:      "git@gitlab.com:group/subgroup/widgets.git",
			wantHost:    "gitlab.com",
			wantOrg:     "group/subgroup",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://gitlab.com/group/subgroup/widgets",
			wantSSHUrl:  "git@gitlab.com:group/subgroup/widgets.git",
		},
		{
			name:        "leading whitespace is trimmed",
			rawURL:      "  https://github.com/acme/widgets.git\n",
			wantHost:    "github.com",
			wantOrg:     "acme",
			wantRepo:    "widgets",
			wantHTTPUrl: "https://github.com/acme/widgets",
			wantSSHUrl:  "git@github.com:acme/widgets.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseRemoteURL(tt.rawURL)
			require.NoError(t, err)
			require.NotNil(t, info)

			assert.Equal(t, tt.wantHost, info.Host, "host")
			assert.Equal(t, tt.wantOrg, info.Org, "org")
			assert.Equal(t, tt.wantRepo, info.Repo, "repo")
			assert.Equal(t, tt.wantHTTPUrl, info.HTTPUrl, "httpUrl")
			assert.Equal(t, tt.wantSSHUrl, info.SSHUrl, "sshUrl")
		})
	}
}

func TestParseRemoteURL_PreservesRawURL(t *testing.T) {
	raw := "  git@github.com:acme/widgets.git  "
	info, err := ParseRemoteURL(raw)
	require.NoError(t, err)
	assert.Equal(t, "git@github.com:acme/widgets.git", info.URL)
}

func TestParseRemoteURL_Errors(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		wantErr string
	}{
		{
			name:    "empty string",
			rawURL:  "",
			wantErr: "empty",
		},
		{
			name:    "whitespace only",
			rawURL:  "   ",
			wantErr: "empty",
		},
		{
			name:    "scheme URL with no host",
			rawURL:  "https:///acme/widgets.git",
			wantErr: "no host",
		},
		{
			name:    "no org segment",
			rawURL:  "https://github.com/widgets.git",
			wantErr: "org and repo",
		},
		{
			name:    "scp-like with no org segment",
			rawURL:  "git@github.com:widgets.git",
			wantErr: "org and repo",
		},
		{
			name:    "no path at all",
			rawURL:  "https://github.com",
			wantErr: "no repository path found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRemoteURL(tt.rawURL)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestIsSCPLike(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		want   bool
	}{
		{"scp-like", "git@github.com:acme/widgets.git", true},
		{"scp-like no user", "github.com:acme/widgets.git", true},
		{"https scheme", "https://github.com/acme/widgets.git", false},
		{"ssh scheme", "ssh://git@github.com/acme/widgets.git", false},
		{"local relative path with colon", "./relative:path", false},
		{"local absolute path with colon", "/abs/with:colon/file", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSCPLike(tt.rawURL))
		})
	}
}
