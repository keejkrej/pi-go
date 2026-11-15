// Ported from packages/coding-agent/src/utils/git.ts (pi v1.5.2).

package utils

import (
	neturl "net/url"
	"regexp"
	"strings"
)

// GitSource represents a parsed git package specifier.
type GitSource struct {
	Type   string
	Repo   string
	Host   string
	Path   string
	Ref    string
	Pinned bool
}

var (
	gitProtocolPrefixRe = regexp.MustCompile(`(?i)^(https?|ssh|git)://`)
	gitScpLikeRe        = regexp.MustCompile(`^([^@:/\\\s]+)@([^:/\\\s]+):(.+)$`)
)

func gitSplitRef(rawPath string) (cleanPath, ref string) {
	idx := strings.IndexByte(rawPath, '@')
	if idx < 0 {
		return rawPath, ""
	}
	return rawPath[:idx], rawPath[idx+1:]
}

func gitHasUnsafeComponent(raw string) bool {
	if raw == "" || strings.ContainsRune(raw, '\x00') || strings.ContainsRune(raw, '\\') || strings.HasPrefix(raw, "/") {
		return true
	}
	if gitHasDotDotSegment(raw) {
		return true
	}
	decoded, err := neturl.PathUnescape(raw)
	if err != nil {
		return true
	}
	if strings.ContainsRune(decoded, '\x00') || strings.ContainsRune(decoded, '\\') || strings.HasPrefix(decoded, "/") {
		return true
	}
	return gitHasDotDotSegment(decoded)
}

func gitHasDotDotSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func gitHasTwoPathSegments(normalizedPath string) bool {
	if normalizedPath == "" {
		return false
	}
	parts := strings.Split(normalizedPath, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}

func gitIsValidHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " \t\r\n@\\") {
		return false
	}
	return strings.ContainsRune(host, '.') || strings.EqualFold(host, "localhost")
}

// ParseGitUrl parses a git source specifier (protocol URL or `git:`-prefixed URL/shorthand)
// into a GitSource, returning nil when invalid or unsafe.
func ParseGitUrl(source string) *GitSource {
	if strings.ContainsRune(source, '\x00') {
		return nil
	}
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil
	}

	hasGitPrefix := strings.HasPrefix(trimmed, "git:")
	urlStr := trimmed
	if hasGitPrefix {
		urlStr = strings.TrimSpace(trimmed[len("git:"):])
	}
	if urlStr == "" || strings.ContainsRune(urlStr, '\x00') {
		return nil
	}

	// 1. Explicit protocol URL (http://, https://, ssh://, git:// — case-insensitive).
	if gitProtocolPrefixRe.MatchString(urlStr) {
		schemeIdx := strings.Index(urlStr, "://")
		authStart := schemeIdx + len("://")
		slashOffset := strings.IndexByte(urlStr[authStart:], '/')
		if slashOffset < 0 {
			return nil
		}
		pathStart := authStart + slashOffset
		cleanSubPath, ref := gitSplitRef(urlStr[pathStart+1:])
		if strings.ContainsRune(ref, '\x00') {
			return nil
		}
		repoWithoutRef := urlStr[:pathStart+1] + cleanSubPath
		parsed, err := neturl.Parse(repoWithoutRef)
		if err != nil || parsed == nil || parsed.Host == "" {
			return nil
		}
		host := parsed.Hostname()
		if host == "" {
			return nil
		}
		if gitHasUnsafeComponent(cleanSubPath) {
			return nil
		}
		normalizedPath := strings.TrimSuffix(cleanSubPath, ".git")
		if gitHasUnsafeComponent(normalizedPath) || !gitHasTwoPathSegments(normalizedPath) {
			return nil
		}
		return &GitSource{
			Type:   "git",
			Repo:   repoWithoutRef,
			Host:   host,
			Path:   normalizedPath,
			Ref:    ref,
			Pinned: ref != "",
		}
	}

	// Reject any other protocol scheme (for example git+ssh://, git+https://, file://).
	if strings.Contains(urlStr, "://") {
		return nil
	}

	// Shorthand and SCP forms require the `git:` prefix.
	if !hasGitPrefix {
		return nil
	}

	// 2. SCP-style `git:<user>@<host>:<path>[@ref]`.
	if m := gitScpLikeRe.FindStringSubmatch(urlStr); len(m) == 4 {
		user, host, rawRest := m[1], m[2], m[3]
		if !gitIsValidHost(host) {
			return nil
		}
		cleanPath, ref := gitSplitRef(rawRest)
		if strings.ContainsRune(ref, '\x00') || gitHasUnsafeComponent(cleanPath) {
			return nil
		}
		normalizedPath := strings.TrimSuffix(cleanPath, ".git")
		if gitHasUnsafeComponent(normalizedPath) || !gitHasTwoPathSegments(normalizedPath) {
			return nil
		}
		return &GitSource{
			Type:   "git",
			Repo:   user + "@" + host + ":" + cleanPath,
			Host:   host,
			Path:   normalizedPath,
			Ref:    ref,
			Pinned: ref != "",
		}
	}

	// 3. Shorthand `git:<host>/<path>[@ref]`.
	slashIdx := strings.IndexByte(urlStr, '/')
	if slashIdx <= 0 {
		return nil
	}
	hostPort := urlStr[:slashIdx]
	hostOnly := hostPort
	if colonIdx := strings.IndexByte(hostPort, ':'); colonIdx >= 0 {
		hostOnly = hostPort[:colonIdx]
	}
	if !gitIsValidHost(hostOnly) || strings.ContainsRune(hostPort, '@') {
		return nil
	}
	cleanPath, ref := gitSplitRef(urlStr[slashIdx+1:])
	if strings.ContainsRune(ref, '\x00') || gitHasUnsafeComponent(cleanPath) {
		return nil
	}
	normalizedPath := strings.TrimSuffix(cleanPath, ".git")
	if gitHasUnsafeComponent(normalizedPath) || !gitHasTwoPathSegments(normalizedPath) {
		return nil
	}
	return &GitSource{
		Type:   "git",
		Repo:   "https://" + hostPort + "/" + cleanPath,
		Host:   hostOnly,
		Path:   normalizedPath,
		Ref:    ref,
		Pinned: ref != "",
	}
}
