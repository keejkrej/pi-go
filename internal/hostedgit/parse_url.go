// Ported from hosted-git-info@9.0.3 lib/parse-url.js.

package hostedgit

import (
	"strings"
)

// lastIndexOfBefore is the last index of char before the first beforeChar (anywhere when there is none).
func lastIndexOfBefore(str string, char, beforeChar byte) int {
	if startPosition := strings.IndexByte(str, beforeChar); startPosition > -1 {
		return strings.LastIndexByte(str[:startPosition], char)
	}
	return strings.LastIndexByte(str, char)
}

// safeURL is `new URL(u)` returning nil instead of throwing.
func safeURL(u string) *URL {
	parsed, err := NewURL(u)
	if err != nil {
		// this fn should never throw
		return nil
	}
	return parsed
}

// correctProtocol accepts input like git:github.com:user/repo and inserts the // after the first :
func correctProtocol(arg string) string {
	firstColon := strings.IndexByte(arg, ':')
	proto := arg[:firstColon+1]
	if _, ok := gitProtocols[proto]; ok {
		return arg
	}

	if firstColon > -1 && strings.HasPrefix(arg[firstColon:], "://") {
		// If arg is given as <foo>://<bar>, then this is already a valid URL.
		return arg
	}

	firstAt := strings.IndexByte(arg, '@')
	if firstAt > -1 {
		if firstAt > firstColon {
			// URL has the form of <foo>:<bar>@<baz>. Assume this is a git+ssh URL.
			return "git+ssh://" + arg
		}
		// URL has the form 'git@github.com:npm/hosted-git-info.git'.
		return arg
	}

	// Correct <foo>:<bar> to <foo>://<bar>
	return arg[:firstColon+1] + "//" + arg[firstColon+1:]
}

// correctUrl attempts to correct an scp style url so that it will parse with `new URL()`
func correctUrl(giturl string) string {
	// ignore @ that come after the first hash since the denotes the start
	// of a committish which can contain @ characters
	firstAt := lastIndexOfBefore(giturl, '@', '#')
	// ignore colons that come after the hash since that could include colons such as:
	// git@github.com:user/package-2#semver:^1.0.0
	lastColonBeforeHash := lastIndexOfBefore(giturl, ':', '#')

	if lastColonBeforeHash > firstAt {
		// the last : comes after the first @ (or there is no @)
		// like it would in:
		// proto://hostname.com:user/repo
		// username@hostname.com:user/repo
		// :password@hostname.com:user/repo
		// username:password@hostname.com:user/repo
		// proto://username@hostname.com:user/repo
		// proto://:password@hostname.com:user/repo
		// proto://username:password@hostname.com:user/repo
		// then we replace the last : with a / to create a valid path
		giturl = giturl[:lastColonBeforeHash] + "/" + giturl[lastColonBeforeHash+1:]
	}

	if lastIndexOfBefore(giturl, ':', '#') == -1 && !strings.Contains(giturl, "//") {
		// we have no : at all
		// as it would be in:
		// username@hostname.com/user/repo
		// then we prepend a protocol
		giturl = "git+ssh://" + giturl
	}

	return giturl
}

// parseUrl is parse-url.js; withProtocols is whether the protocols table is passed (fromUrl passes it,
// the exported parseUrl does not).
func parseUrl(giturl string, withProtocols bool) *URL {
	withProtocol := giturl
	if withProtocols {
		withProtocol = correctProtocol(giturl)
	}
	if u := safeURL(withProtocol); u != nil {
		return u
	}
	return safeURL(correctUrl(withProtocol))
}
