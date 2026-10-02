// Ported from hosted-git-info@9.0.3 lib/index.js.

package hostedgit

import (
	"errors"
	"regexp"
	"strings"
)

// Options are the hosted-git-info options. Nil fields are unset, so method options fall back to the
// options given to FromUrl.
type Options struct {
	// NoCommittish omits committishes from generated URLs.
	NoCommittish *bool `json:"noCommittish,omitempty"`
	// NoGitPlus strips the `git+` prefix from generated URLs.
	NoGitPlus *bool `json:"noGitPlus,omitempty"`
}

// GitHost is the object hostedGitInfo.fromUrl returns: the host's settings plus the parsed fields.
type GitHost struct {
	// Protocols are the protocols the host accepts (for example "git+ssh:").
	Protocols []string `json:"protocols"`
	Domain    string   `json:"domain"`
	// Treepath, Blobpath and Editpath are "" when the host has none (gist has no treepath or blobpath,
	// sourcehut has no editpath).
	Treepath string `json:"treepath,omitempty"`
	Blobpath string `json:"blobpath,omitempty"`
	Editpath string `json:"editpath,omitempty"`
	// Type is the host name: "github", "bitbucket", "gitlab", "gist" or "sourcehut".
	Type string `json:"type"`
	// User is nil for gists without a user and for shortcuts without one.
	User *string `json:"user"`
	// Auth is "user", "user:password" or ":password" for protocols that carry auth, else nil.
	Auth    *string `json:"auth"`
	Project string  `json:"project"`
	// Committish is nil for a shortcut without a hash. For other URLs it is "" without a hash, and
	// "undefined" for a github `/tree` URL without a committish segment.
	Committish *string `json:"committish"`
	// Default is the default representation: "shortcut", "sshurl", "https", "git" or "http".
	Default string   `json:"default"`
	Opts    *Options `json:"opts"`

	host *hostDef
}

var (
	gitHostsByName     = map[string]*hostDef{}
	gitHostsByDomain   = map[string]string{}
	gitHostsByShortcut = map[string]string{}
)

type protocolInfo struct {
	name string
	auth bool
}

var gitProtocols = map[string]protocolInfo{
	"git+ssh:":   {name: "sshurl"},
	"ssh:":       {name: "sshurl"},
	"git+https:": {name: "https", auth: true},
	"git:":       {auth: true},
	"http:":      {auth: true},
	"https:":     {auth: true},
	"git+http:":  {auth: true},
}

func init() {
	for _, host := range hostOrder {
		gitHostsByName[host.name] = host
		gitHostsByDomain[host.domain] = host.name
		gitHostsByShortcut[host.name+":"] = host.name
		gitProtocols[host.name+":"] = protocolInfo{name: host.name}
	}
}

func newGitHost(hostType string, user, auth *string, project string, committish *string, defaultRepresentation string, opts *Options) *GitHost {
	host := gitHostsByName[hostType]
	if opts == nil {
		opts = &Options{}
	}
	return &GitHost{
		Protocols:  append([]string(nil), host.protocols...),
		Domain:     host.domain,
		Treepath:   host.treepath,
		Blobpath:   host.blobpath,
		Editpath:   host.editpath,
		Type:       hostType,
		User:       user,
		Auth:       auth,
		Project:    project,
		Committish: committish,
		Default:    defaultRepresentation,
		Opts:       opts,
		host:       host,
	}
}

var reGitHTTPProtocol = regexp.MustCompile(`(?:git\+)http:$`)

func unknownHostedUrl(rawURL string) *string {
	parsed, err := NewURL(rawURL)
	if err != nil {
		return nil
	}
	if parsed.Hostname() == "" {
		return nil
	}
	proto := "https:"
	// TS parity: `(?:git\+)http:$` is not optional, so a plain `http:` URL becomes `https:`.
	if reGitHTTPProtocol.MatchString(parsed.Protocol()) {
		proto = "http:"
	}
	path := strings.TrimSuffix(parsed.Pathname(), ".git")
	return new(proto + "//" + parsed.Hostname() + path)
}

// FromUrl is hostedGitInfo.fromUrl: it returns nil for URLs that are not on a known git host. Unlike
// the TS version there is no LRU cache, so every call returns a new GitHost. FromUrl also returns nil
// where the TS version throws; use FromUrlChecked to get that error.
func FromUrl(giturl string, opts *Options) *GitHost {
	info, _ := FromUrlChecked(giturl, opts)
	return info
}

// FromUrlChecked is FromUrl that also returns the TypeError the TS version throws when the hostname is
// an Object.prototype property name such as "constructor".
func FromUrlChecked(giturl string, opts *Options) (*GitHost, error) {
	args, err := fromUrl(giturl, opts)
	if err != nil || args == nil {
		return nil, err
	}
	return newGitHost(args.hostType, args.user, args.auth, args.project, args.committish, args.defaultRepresentation, args.opts), nil
}

// ErrNoRepository is the error FromManifest returns when the manifest has no repository URL.
var ErrNoRepository = errors.New("no repository")

// FromManifest is hostedGitInfo.fromManifest for a JSON-decoded package.json. It returns the GitHost for
// the `repository` field; otherwise unknownURL is the `https:` (or `http:` for `git+http:`) URL built
// from it, "" when it does not parse as a URL with a hostname (TS: null). A nil manifest returns
// nothing (TS: undefined); a manifest without a string `repository` or `repository.url` returns
// ErrNoRepository.
func FromManifest(manifest map[string]any, opts *Options) (info *GitHost, unknownURL string, err error) {
	if manifest == nil {
		return nil, "", nil
	}
	rurl := ""
	switch r := manifest["repository"].(type) {
	case string:
		rurl = r
	case map[string]any:
		if u, ok := r["url"].(string); ok {
			rurl = u
		}
	}
	if rurl == "" {
		return nil, "", ErrNoRepository
	}

	if info, err := FromUrlChecked(strings.TrimPrefix(rurl, "git+"), opts); err != nil || info != nil {
		return info, "", err
	}
	unk := unknownHostedUrl(rurl)
	if unk == nil {
		return nil, "", nil
	}
	if info, err := FromUrlChecked(*unk, opts); err != nil || info != nil {
		return info, "", err
	}
	return nil, *unk, nil
}

// ParseUrl is hostedGitInfo.parseUrl: `new URL(url)`, retried after rewriting scp-style URLs such as
// `git@github.com:npm/cli.git`. It returns nil when neither parses.
func ParseUrl(rawURL string) *URL {
	return parseUrl(rawURL, false)
}

// mergeOptions is `{ ...this.opts, ...opts }` restricted to the documented options.
func (g *GitHost) mergeOptions(opts *Options) Options {
	var merged Options
	if g.Opts != nil {
		merged = *g.Opts
	}
	if opts != nil {
		if opts.NoCommittish != nil {
			merged.NoCommittish = opts.NoCommittish
		}
		if opts.NoGitPlus != nil {
			merged.NoGitPlus = opts.NoGitPlus
		}
	}
	return merged
}

func (g *GitHost) templateOptions(opts Options, path, fragment string) *templateOptions {
	o := &templateOptions{
		typ:        g.Type,
		domain:     g.Domain,
		treepath:   g.Treepath,
		blobpath:   g.Blobpath,
		editpath:   g.Editpath,
		user:       g.User,
		auth:       g.Auth,
		project:    g.Project,
		committish: g.Committish,
		// template functions will insert the leading slash themselves
		path:       strings.TrimPrefix(path, "/"),
		fragment:   fragment,
		hashformat: g.host.hashformat,
	}
	if opts.NoCommittish != nil && *opts.NoCommittish {
		o.committish = nil
	}
	return o
}

func noGitPlus(opts Options, result string) string {
	if opts.NoGitPlus != nil && *opts.NoGitPlus && strings.HasPrefix(result, "git+") {
		return result[4:]
	}
	return result
}

// fill is GitHost#fill for templates that always return a string.
func (g *GitHost) fill(tmpl template, opts Options, path, fragment string) string {
	return noGitPlus(opts, tmpl(g.templateOptions(opts, path, fragment)))
}

// Hash is "#" followed by the committish, or "" without one.
func (g *GitHost) Hash() string {
	if isFalsy(g.Committish) {
		return ""
	}
	return "#" + *g.Committish
}

// Ssh is the scp-style URL, for example `git@github.com:npm/cli.git`.
func (g *GitHost) Ssh(opts *Options) string {
	return g.fill(g.host.sshtemplate, g.mergeOptions(opts), "", "")
}

// Sshurl is the `git+ssh://` URL.
func (g *GitHost) Sshurl(opts *Options) string {
	return g.fill(g.host.sshurltemplate, g.mergeOptions(opts), "", "")
}

// Browse is `info.browse(opts)`: the repository's web page.
func (g *GitHost) Browse(opts *Options) string {
	return g.fill(g.host.browsetemplate, g.mergeOptions(opts), "", "")
}

// BrowsePath is `info.browse(path, fragment, opts)`: the web page of path in the tree view. An empty
// fragment is the same as none.
func (g *GitHost) BrowsePath(path, fragment string, opts *Options) string {
	return g.fill(g.host.browsetreetemplate, g.mergeOptions(opts), path, fragment)
}

// BrowseFile is `info.browseFile(path, fragment, opts)`: like BrowsePath, but uses the blob view on hosts
// that have one, which does not redirect to a specific commit. An empty fragment is the same as none.
func (g *GitHost) BrowseFile(path, fragment string, opts *Options) string {
	return g.fill(g.host.browseblobtemplate, g.mergeOptions(opts), path, fragment)
}

// Docs is the repository's README page.
func (g *GitHost) Docs(opts *Options) string {
	return g.fill(g.host.docstemplate, g.mergeOptions(opts), "", "")
}

// Bugs is the issue tracker URL; nil for sourcehut, which has none.
func (g *GitHost) Bugs(opts *Options) *string {
	merged := g.mergeOptions(opts)
	result := g.host.bugstemplate(g.templateOptions(merged, "", ""))
	// The TS version throws a TypeError (null.startsWith) for the null sourcehut result with noGitPlus;
	// Go returns nil in both cases.
	if result == nil {
		return nil
	}
	return new(noGitPlus(merged, *result))
}

// Https is the `git+https://` URL (`https://` for sourcehut).
func (g *GitHost) Https(opts *Options) string {
	return g.fill(g.host.httpstemplate, g.mergeOptions(opts), "", "")
}

// Git is the `git://` URL; nil for hosts without one (only github and gist have one).
func (g *GitHost) Git(opts *Options) *string {
	if g.host.gittemplate == nil {
		return nil
	}
	return new(g.fill(g.host.gittemplate, g.mergeOptions(opts), "", ""))
}

// Shortcut is the `<type>:<user>/<project>` form.
func (g *GitHost) Shortcut(opts *Options) string {
	return g.fill(g.host.shortcuttemplate, g.mergeOptions(opts), "", "")
}

// Path is the `<user>/<project>` form.
func (g *GitHost) Path(opts *Options) string {
	return g.fill(g.host.pathtemplate, g.mergeOptions(opts), "", "")
}

// Tarball is the tarball download URL; it always includes the committish (default HEAD).
func (g *GitHost) Tarball(opts *Options) string {
	merged := g.mergeOptions(opts)
	merged.NoCommittish = new(false)
	return g.fill(g.host.tarballtemplate, merged, "", "")
}

// File is the raw download URL of path.
func (g *GitHost) File(path string, opts *Options) string {
	return g.fill(g.host.filetemplate, g.mergeOptions(opts), path, "")
}

// Edit is the web edit URL of path.
func (g *GitHost) Edit(path string, opts *Options) string {
	return g.fill(g.host.edittemplate, g.mergeOptions(opts), path, "")
}

// GetDefaultRepresentation returns Default.
func (g *GitHost) GetDefaultRepresentation() string {
	return g.Default
}

// ToString is the URL in the default representation; Sshurl when that has no method ("http").
func (g *GitHost) ToString(opts *Options) string {
	switch g.Default {
	case "shortcut":
		return g.Shortcut(opts)
	case "sshurl":
		return g.Sshurl(opts)
	case "https":
		return g.Https(opts)
	case "git":
		if git := g.Git(opts); git != nil {
			return *git
		}
		return ""
	}
	return g.Sshurl(opts)
}

// String is ToString(nil).
func (g *GitHost) String() string {
	return g.ToString(nil)
}
