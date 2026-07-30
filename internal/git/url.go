package git

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// RemoteInfo holds the parsed components of a git remote URL.
type RemoteInfo struct {
	// URL is the raw remote URL, unmodified.
	URL string
	// Host is the remote host with any userinfo and port stripped
	// (e.g. "github.com").
	Host string
	// Org is the owner, group, or namespace: the path between the host and
	// the final segment. For nested namespaces such as "group/subgroup/repo"
	// this captures the full intermediate path ("group/subgroup").
	Org string
	// Repo is the final path segment with any trailing ".git" removed.
	Repo string
	// HTTPUrl is the canonical HTTPS URL: "https://<host>/<org>/<repo>".
	HTTPUrl string
	// SSHUrl is the canonical SCP-like SSH URL: "git@<host>:<org>/<repo>.git".
	SSHUrl string
}

var (
	// schemeRE matches a URL that begins with a scheme (e.g. "https://",
	// "ssh://", "git://").
	schemeRE = regexp.MustCompile(`^[^:]+://`)

	// scpLikeRE matches the SCP-like remote syntax "[user@]host:[port:]path".
	// Ref: https://github.com/git/git/blob/master/Documentation/urls.adoc
	scpLikeRE = regexp.MustCompile(`^(?:(?P<user>[^@]+)@)?(?P<host>[^:\s]+):(?:(?P<port>[0-9]{1,5}):)?(?P<path>[^\\].*)$`)
)

// ParseRemoteURL parses a git remote URL into its structured components.
//
// It supports the "https://", "http://", and "ssh://" schemes as well as the
// SCP-like syntax ("git@host:org/repo.git"). Userinfo (e.g. "user:token@") and
// ports are stripped from the derived host and URLs, and a trailing ".git" is
// removed from the repository name.
func ParseRemoteURL(rawURL string) (*RemoteInfo, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, fmt.Errorf("remote URL is empty")
	}

	host, path, err := splitHostPath(trimmed)
	if err != nil {
		return nil, err
	}

	org, repo, err := splitOrgRepo(path)
	if err != nil {
		return nil, fmt.Errorf("parsing remote URL %q: %w", trimmed, err)
	}

	return &RemoteInfo{
		URL:     trimmed,
		Host:    host,
		Org:     org,
		Repo:    repo,
		HTTPUrl: fmt.Sprintf("https://%s/%s/%s", host, org, repo),
		SSHUrl:  fmt.Sprintf("git@%s:%s/%s.git", host, org, repo),
	}, nil
}

// splitHostPath extracts the host (without userinfo or port) and the repository
// path from a remote URL, handling both scheme-based and SCP-like syntax.
func splitHostPath(rawURL string) (host, path string, err error) {
	if isSCPLike(rawURL) {
		return parseSCP(rawURL)
	}

	u, parseErr := url.Parse(rawURL)
	if parseErr != nil {
		return "", "", fmt.Errorf("parsing remote URL %q: %w", rawURL, parseErr)
	}

	host = u.Hostname()
	if host == "" {
		return "", "", fmt.Errorf("remote URL %q has no host", rawURL)
	}

	return host, u.Path, nil
}

// isSCPLike reports whether rawURL uses the SCP-like syntax
// "[user@]host:path" rather than a scheme-based URL or a local path.
func isSCPLike(rawURL string) bool {
	if schemeRE.MatchString(rawURL) {
		return false
	}
	if !scpLikeRE.MatchString(rawURL) {
		return false
	}
	// Mirror canonical git's url_is_local_not_ssh: when a "/" precedes the
	// first ":", the value is a local path (e.g. "./rel:path"), not SCP.
	if before, _, found := strings.Cut(rawURL, ":"); found && strings.Contains(before, "/") {
		return false
	}
	return true
}

// parseSCP extracts the host (without port) and path from an SCP-like URL.
func parseSCP(rawURL string) (host, path string, err error) {
	m := scpLikeRE.FindStringSubmatch(rawURL)
	if m == nil {
		return "", "", fmt.Errorf("remote URL %q is not valid SCP-like syntax", rawURL)
	}
	host = m[scpLikeRE.SubexpIndex("host")]
	path = m[scpLikeRE.SubexpIndex("path")]
	if host == "" {
		return "", "", fmt.Errorf("remote URL %q has no host", rawURL)
	}
	// Strip a bracketed IPv6 host or trailing port artifacts if present.
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	return host, path, nil
}

// splitOrgRepo divides a repository path into its org (namespace) and repo
// components. The final segment becomes the repo (with any ".git" suffix
// removed) and all preceding segments become the org.
func splitOrgRepo(path string) (org, repo string, err error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "", "", fmt.Errorf("no repository path found")
	}

	segments := strings.Split(trimmed, "/")
	if len(segments) < 2 {
		return "", "", fmt.Errorf("path %q does not contain an org and repo", path)
	}

	repo = strings.TrimSuffix(segments[len(segments)-1], ".git")
	if repo == "" {
		return "", "", fmt.Errorf("path %q has an empty repo segment", path)
	}

	org = strings.Join(segments[:len(segments)-1], "/")
	return org, repo, nil
}
