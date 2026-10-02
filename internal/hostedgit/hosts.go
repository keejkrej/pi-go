// Ported from hosted-git-info@9.0.3 lib/hosts.js.

package hostedgit

import (
	"strings"
)

// templateOptions is the `{ ...this, ...this.opts, ...opts }` object the URL templates destructure.
type templateOptions struct {
	typ        string
	domain     string
	treepath   string
	blobpath   string
	editpath   string
	user       *string
	auth       *string
	project    string
	committish *string
	path       string
	fragment   string
	hashformat func(fragment string) string
}

// segments is the result of a host's extract function. committish holds decodeURIComponent's input;
// an undefined committish is "undefined", which is what decodeURIComponent(undefined) returns.
type segments struct {
	user       *string
	project    string
	committish string
}

type template func(o *templateOptions) string

// hostDef is a hosts.js entry merged over the defaults.
type hostDef struct {
	name      string
	protocols []string
	domain    string
	treepath  string
	blobpath  string
	editpath  string

	sshtemplate        template
	sshurltemplate     template
	edittemplate       template
	browsetemplate     template
	browsetreetemplate template
	browseblobtemplate template
	docstemplate       template
	httpstemplate      template
	filetemplate       template
	shortcuttemplate   template
	pathtemplate       template
	tarballtemplate    template
	// bugstemplate returns nil for sourcehut (`bugstemplate: () => null`).
	bugstemplate func(o *templateOptions) *string
	// gittemplate is nil for hosts without one, so `git()` returns null.
	gittemplate template
	extract     func(u *URL) *segments
	hashformat  func(fragment string) string
}

// maybeJoin joins the arguments when every one is truthy (non-empty), else returns "".
func maybeJoin(args ...string) string {
	for _, arg := range args {
		if arg == "" {
			return ""
		}
	}
	return strings.Join(args, "")
}

func maybeEncode(arg string) string {
	if arg == "" {
		return ""
	}
	return encodeURIComponent(arg)
}

func truthy(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// splitLimit is `s.split(sep, limit)`, padded with nil (undefined) entries up to limit.
func splitLimit(s, sep string, limit int) []*string {
	parts := strings.Split(s, sep)
	out := make([]*string, limit)
	for i := 0; i < limit && i < len(parts); i++ {
		out[i] = &parts[i]
	}
	return out
}

func isFalsy(s *string) bool { return s == nil || *s == "" }

func defaultHost() hostDef {
	return hostDef{
		sshtemplate: func(o *templateOptions) string {
			return "git@" + o.domain + ":" + jsString(o.user) + "/" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
		},
		sshurltemplate: func(o *templateOptions) string {
			return "git+ssh://git@" + o.domain + "/" + jsString(o.user) + "/" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
		},
		edittemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project +
				maybeJoin("/", o.editpath, "/", maybeEncode(orString(o.committish, "HEAD")), "/", o.path)
		},
		browsetemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project +
				maybeJoin("/", o.treepath, "/", maybeEncode(truthy(o.committish)))
		},
		browsetreetemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/" + o.treepath + "/" +
				maybeEncode(orString(o.committish, "HEAD")) + "/" + o.path + maybeJoin("#", o.hashformat(o.fragment))
		},
		browseblobtemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/" + o.blobpath + "/" +
				maybeEncode(orString(o.committish, "HEAD")) + "/" + o.path + maybeJoin("#", o.hashformat(o.fragment))
		},
		docstemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project +
				maybeJoin("/", o.treepath, "/", maybeEncode(truthy(o.committish))) + "#readme"
		},
		httpstemplate: func(o *templateOptions) string {
			return "git+https://" + maybeJoin(truthy(o.auth), "@") + o.domain + "/" + jsString(o.user) + "/" + o.project + ".git" +
				maybeJoin("#", truthy(o.committish))
		},
		filetemplate: func(o *templateOptions) string {
			return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/raw/" +
				maybeEncode(orString(o.committish, "HEAD")) + "/" + o.path
		},
		shortcuttemplate: func(o *templateOptions) string {
			return o.typ + ":" + jsString(o.user) + "/" + o.project + maybeJoin("#", truthy(o.committish))
		},
		pathtemplate: func(o *templateOptions) string {
			return jsString(o.user) + "/" + o.project + maybeJoin("#", truthy(o.committish))
		},
		bugstemplate: func(o *templateOptions) *string {
			return new("https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/issues")
		},
		hashformat: formatHashFragment,
	}
}

// stripGitSuffix is `if (project && project.endsWith('.git')) project = project.slice(0, -4)`.
func stripGitSuffix(project string) string {
	return strings.TrimSuffix(project, ".git")
}

func newGitHub() *hostDef {
	h := defaultHost()
	h.name = "github"
	// First two are insecure and generally shouldn't be used any more, but
	// they are still supported.
	h.protocols = []string{"git:", "http:", "git+ssh:", "git+https:", "ssh:", "https:"}
	h.domain = "github.com"
	h.treepath = "tree"
	h.blobpath = "blob"
	h.editpath = "edit"
	h.filetemplate = func(o *templateOptions) string {
		return "https://" + maybeJoin(truthy(o.auth), "@") + "raw.githubusercontent.com/" + jsString(o.user) + "/" + o.project + "/" +
			maybeEncode(orString(o.committish, "HEAD")) + "/" + o.path
	}
	h.gittemplate = func(o *templateOptions) string {
		return "git://" + maybeJoin(truthy(o.auth), "@") + o.domain + "/" + jsString(o.user) + "/" + o.project + ".git" +
			maybeJoin("#", truthy(o.committish))
	}
	h.tarballtemplate = func(o *templateOptions) string {
		return "https://codeload." + o.domain + "/" + jsString(o.user) + "/" + o.project + "/tar.gz/" + maybeEncode(orString(o.committish, "HEAD"))
	}
	h.extract = func(u *URL) *segments {
		parts := splitLimit(u.Pathname(), "/", 5)
		user, project, typ, committish := parts[1], parts[2], parts[3], parts[4]
		if !isFalsy(typ) && *typ != "tree" {
			return nil
		}
		if isFalsy(typ) {
			committish = new(strings.TrimPrefix(u.Hash(), "#"))
		}
		if !isFalsy(project) {
			project = new(stripGitSuffix(*project))
		}
		if isFalsy(user) || isFalsy(project) {
			return nil
		}
		// TS parity: a `/tree` path without a committish segment yields decodeURIComponent(undefined).
		c := "undefined"
		if committish != nil {
			c = *committish
		}
		return &segments{user: user, project: *project, committish: c}
	}
	return &h
}

func newBitbucket() *hostDef {
	h := defaultHost()
	h.name = "bitbucket"
	h.protocols = []string{"git+ssh:", "git+https:", "ssh:", "https:"}
	h.domain = "bitbucket.org"
	h.treepath = "src"
	h.blobpath = "src"
	h.editpath = "?mode=edit"
	h.edittemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project +
			maybeJoin("/", o.treepath, "/", maybeEncode(orString(o.committish, "HEAD")), "/", o.path, o.editpath)
	}
	h.tarballtemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/get/" + maybeEncode(orString(o.committish, "HEAD")) + ".tar.gz"
	}
	h.extract = func(u *URL) *segments {
		parts := splitLimit(u.Pathname(), "/", 4)
		user, project, aux := parts[1], parts[2], parts[3]
		if aux != nil && *aux == "get" {
			return nil
		}
		if !isFalsy(project) {
			project = new(stripGitSuffix(*project))
		}
		if isFalsy(user) || isFalsy(project) {
			return nil
		}
		return &segments{user: user, project: *project, committish: strings.TrimPrefix(u.Hash(), "#")}
	}
	return &h
}

func newGitLab() *hostDef {
	h := defaultHost()
	h.name = "gitlab"
	h.protocols = []string{"git+ssh:", "git+https:", "ssh:", "https:"}
	h.domain = "gitlab.com"
	h.treepath = "tree"
	h.blobpath = "tree"
	h.editpath = "-/edit"
	h.tarballtemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/api/v4/projects/" + maybeEncode(jsString(o.user)+"/"+o.project) +
			"/repository/archive.tar.gz?sha=" + maybeEncode(orString(o.committish, "HEAD"))
	}
	h.extract = func(u *URL) *segments {
		path := u.Pathname()
		if path != "" {
			path = path[1:]
		}
		if strings.Contains(path, "/-/") || strings.Contains(path, "/archive.tar.gz") {
			return nil
		}
		parts := strings.Split(path, "/")
		project := stripGitSuffix(parts[len(parts)-1])
		user := strings.Join(parts[:len(parts)-1], "/")
		if user == "" || project == "" {
			return nil
		}
		return &segments{user: &user, project: project, committish: strings.TrimPrefix(u.Hash(), "#")}
	}
	return &h
}

func newGist() *hostDef {
	h := defaultHost()
	h.name = "gist"
	h.protocols = []string{"git:", "git+ssh:", "git+https:", "ssh:", "https:"}
	h.domain = "gist.github.com"
	h.editpath = "edit"
	h.sshtemplate = func(o *templateOptions) string {
		return "git@" + o.domain + ":" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
	}
	h.sshurltemplate = func(o *templateOptions) string {
		return "git+ssh://git@" + o.domain + "/" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
	}
	h.edittemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + maybeJoin("/", maybeEncode(truthy(o.committish))) + "/" + o.editpath
	}
	h.browsetemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + o.project + maybeJoin("/", maybeEncode(truthy(o.committish)))
	}
	h.browsetreetemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + o.project + maybeJoin("/", maybeEncode(truthy(o.committish))) + maybeJoin("#", o.hashformat(o.path))
	}
	h.browseblobtemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + o.project + maybeJoin("/", maybeEncode(truthy(o.committish))) + maybeJoin("#", o.hashformat(o.path))
	}
	h.docstemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + o.project + maybeJoin("/", maybeEncode(truthy(o.committish)))
	}
	h.httpstemplate = func(o *templateOptions) string {
		return "git+https://" + o.domain + "/" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
	}
	h.filetemplate = func(o *templateOptions) string {
		return "https://gist.githubusercontent.com/" + jsString(o.user) + "/" + o.project + "/raw" +
			maybeJoin("/", maybeEncode(truthy(o.committish))) + "/" + o.path
	}
	h.shortcuttemplate = func(o *templateOptions) string {
		return o.typ + ":" + o.project + maybeJoin("#", truthy(o.committish))
	}
	h.pathtemplate = func(o *templateOptions) string {
		return o.project + maybeJoin("#", truthy(o.committish))
	}
	h.bugstemplate = func(o *templateOptions) *string {
		return new("https://" + o.domain + "/" + o.project)
	}
	h.gittemplate = func(o *templateOptions) string {
		return "git://" + o.domain + "/" + o.project + ".git" + maybeJoin("#", truthy(o.committish))
	}
	h.tarballtemplate = func(o *templateOptions) string {
		return "https://codeload.github.com/gist/" + o.project + "/tar.gz/" + maybeEncode(orString(o.committish, "HEAD"))
	}
	h.extract = func(u *URL) *segments {
		parts := splitLimit(u.Pathname(), "/", 4)
		user, project, aux := parts[1], parts[2], parts[3]
		if aux != nil && *aux == "raw" {
			return nil
		}
		if isFalsy(project) {
			if isFalsy(user) {
				return nil
			}
			project = user
			user = nil
		}
		return &segments{user: user, project: stripGitSuffix(*project), committish: strings.TrimPrefix(u.Hash(), "#")}
	}
	h.hashformat = func(fragment string) string {
		if fragment == "" {
			return ""
		}
		return "file-" + formatHashFragment(fragment)
	}
	return &h
}

func newSourcehut() *hostDef {
	h := defaultHost()
	h.name = "sourcehut"
	h.protocols = []string{"git+ssh:", "https:"}
	h.domain = "git.sr.ht"
	h.treepath = "tree"
	h.blobpath = "tree"
	h.filetemplate = func(o *templateOptions) string {
		committish := maybeEncode(truthy(o.committish))
		if committish == "" {
			committish = "HEAD"
		}
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/blob/" + committish + "/" + o.path
	}
	h.httpstemplate = func(o *templateOptions) string {
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + maybeJoin("#", truthy(o.committish))
	}
	h.tarballtemplate = func(o *templateOptions) string {
		committish := maybeEncode(truthy(o.committish))
		if committish == "" {
			committish = "HEAD"
		}
		return "https://" + o.domain + "/" + jsString(o.user) + "/" + o.project + "/archive/" + committish + ".tar.gz"
	}
	h.bugstemplate = func(*templateOptions) *string { return nil }
	h.extract = func(u *URL) *segments {
		parts := splitLimit(u.Pathname(), "/", 4)
		user, project, aux := parts[1], parts[2], parts[3]
		// tarball url
		if aux != nil && *aux == "archive" {
			return nil
		}
		if !isFalsy(project) {
			project = new(stripGitSuffix(*project))
		}
		if isFalsy(user) || isFalsy(project) {
			return nil
		}
		return &segments{user: user, project: *project, committish: strings.TrimPrefix(u.Hash(), "#")}
	}
	return &h
}

// hostOrder is the Object.entries order of hosts.js.
var hostOrder = []*hostDef{newGitHub(), newBitbucket(), newGitLab(), newGist(), newSourcehut()}
