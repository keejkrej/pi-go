// Ported from hosted-git-info@9.0.3 lib/from-url.js.

package hostedgit

import (
	"slices"
	"strings"
)

// isGitHubShorthand looks for github shorthand inputs, such as npm/cli. The index comparisons are
// relative, so byte offsets give the same answers as the UTF-16 offsets of the TS version.
func isGitHubShorthand(arg string) bool {
	// it cannot contain whitespace before the first #
	// it cannot start with a / because that's probably an absolute file path
	// but it must include a slash since repos are username/repository
	// it cannot start with a . because that's probably a relative file path
	// it cannot start with an @ because that's a scoped package if it passes the other tests
	// it cannot contain a : before a # because that tells us that there's a protocol
	// a second / may not exist before a #
	firstHash := strings.IndexByte(arg, '#')
	firstSlash := strings.IndexByte(arg, '/')
	secondSlash := strings.IndexByte(arg[firstSlash+1:], '/')
	if secondSlash > -1 {
		secondSlash += firstSlash + 1
	}
	firstColon := strings.IndexByte(arg, ':')
	firstSpace := strings.IndexFunc(arg, isJSWhitespace)
	firstAt := strings.IndexByte(arg, '@')

	spaceOnlyAfterHash := firstSpace == -1 || (firstHash > -1 && firstSpace > firstHash)
	atOnlyAfterHash := firstAt == -1 || (firstHash > -1 && firstAt > firstHash)
	colonOnlyAfterHash := firstColon == -1 || (firstHash > -1 && firstColon > firstHash)
	secondSlashOnlyAfterHash := secondSlash == -1 || (firstHash > -1 && secondSlash > firstHash)
	hasSlash := firstSlash > 0
	// if a # is found, what we really want to know is that the character
	// immediately before # is not a /
	var doesNotEndWithSlash bool
	if firstHash > -1 {
		doesNotEndWithSlash = firstHash == 0 || arg[firstHash-1] != '/'
	} else {
		doesNotEndWithSlash = !strings.HasSuffix(arg, "/")
	}
	doesNotStartWithDot := !strings.HasPrefix(arg, ".")

	return spaceOnlyAfterHash && hasSlash && doesNotEndWithSlash &&
		doesNotStartWithDot && atOnlyAfterHash && colonOnlyAfterHash &&
		secondSlashOnlyAfterHash
}

// objectPrototypeKeys are the Object.prototype property names; looking one up in the TS byDomain map
// finds the inherited property instead of a host.
var objectPrototypeKeys = []string{
	"constructor", "__defineGetter__", "__defineSetter__", "hasOwnProperty", "__lookupGetter__",
	"__lookupSetter__", "isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf", "__proto__",
	"toLocaleString",
}

type hostArgs struct {
	hostType              string
	user                  *string
	auth                  *string
	project               string
	committish            *string
	defaultRepresentation string
	opts                  *Options
}

func fromUrl(giturl string, opts *Options) (*hostArgs, error) {
	if giturl == "" {
		return nil, nil
	}

	correctedURL := giturl
	if isGitHubShorthand(giturl) {
		correctedURL = "github:" + giturl
	}
	parsed := parseUrl(correctedURL, true)
	if parsed == nil {
		return nil, nil
	}

	gitHostShortcut := gitHostsByShortcut[parsed.Protocol()]
	hostname := strings.TrimPrefix(parsed.Hostname(), "www.")
	gitHostDomain := gitHostsByDomain[hostname]
	if gitHostShortcut == "" && gitHostDomain == "" {
		if slices.Contains(objectPrototypeKeys, hostname) {
			// TS parity: byDomain[hostname] is the inherited property, so gitHosts[...] is undefined and reading
			// `.protocols` throws.
			return nil, &TypeError{Message: "Cannot read properties of undefined (reading 'protocols')"}
		}
		return nil, nil
	}
	gitHostName := gitHostShortcut
	if gitHostName == "" {
		gitHostName = gitHostDomain
	}

	gitHostInfo := gitHostsByName[gitHostName]
	var auth *string
	username, password := parsed.Username(), parsed.Password()
	if gitProtocols[parsed.Protocol()].auth && (username != "" || password != "") {
		a := username
		if password != "" {
			a += ":" + password
		}
		auth = &a
	}

	var committish, user *string
	project := ""
	defaultRepresentation := ""

	if gitHostShortcut != "" {
		pathname := strings.TrimPrefix(parsed.Pathname(), "/")
		// we ignore auth for shortcuts, so just trim it out
		if firstAt := strings.IndexByte(pathname, '@'); firstAt > -1 {
			pathname = pathname[firstAt+1:]
		}

		if lastSlash := strings.LastIndexByte(pathname, '/'); lastSlash > -1 {
			u, err := decodeURIComponent(pathname[:lastSlash])
			if err != nil {
				return nil, nil
			}
			// we want nulls only, never empty strings
			if u != "" {
				user = &u
			}
			p, err := decodeURIComponent(pathname[lastSlash+1:])
			if err != nil {
				return nil, nil
			}
			project = p
		} else {
			p, err := decodeURIComponent(pathname)
			if err != nil {
				return nil, nil
			}
			project = p
		}

		project = strings.TrimSuffix(project, ".git")

		if hash := parsed.Hash(); hash != "" {
			c, err := decodeURIComponent(hash[1:])
			if err != nil {
				return nil, nil
			}
			committish = &c
		}

		defaultRepresentation = "shortcut"
	} else {
		if !slices.Contains(gitHostInfo.protocols, parsed.Protocol()) {
			return nil, nil
		}

		segments := gitHostInfo.extract(parsed)
		if segments == nil {
			return nil, nil
		}

		if segments.user != nil && *segments.user != "" {
			u, err := decodeURIComponent(*segments.user)
			if err != nil {
				return nil, nil
			}
			user = &u
		} else {
			user = segments.user
		}
		p, err := decodeURIComponent(segments.project)
		if err != nil {
			return nil, nil
		}
		project = p
		c, err := decodeURIComponent(segments.committish)
		if err != nil {
			return nil, nil
		}
		committish = &c
		defaultRepresentation = gitProtocols[parsed.Protocol()].name
		if defaultRepresentation == "" {
			defaultRepresentation = strings.TrimSuffix(parsed.Protocol(), ":")
		}
	}

	return &hostArgs{
		hostType:              gitHostName,
		user:                  user,
		auth:                  auth,
		project:               project,
		committish:            committish,
		defaultRepresentation: defaultRepresentation,
		opts:                  opts,
	}, nil
}
