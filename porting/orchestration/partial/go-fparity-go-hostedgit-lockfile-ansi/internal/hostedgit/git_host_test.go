package hostedgit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// Vectors in testdata/hosted_git_info_vectors.json were generated with node v24.21.0 from
// hosted-git-info@9.0.3 (fromUrl, every method, parseUrl, fromManifest, and the encodeURIComponent,
// decodeURIComponent and hashformat helpers). Generator script: gen-hg.cjs, not kept.
type hgVectorInfo struct {
	Error  *string `json:"error"`
	Fields *struct {
		Type       string   `json:"type"`
		Domain     string   `json:"domain"`
		Treepath   *string  `json:"treepath"`
		Blobpath   *string  `json:"blobpath"`
		Editpath   *string  `json:"editpath"`
		Protocols  []string `json:"protocols"`
		User       *string  `json:"user"`
		Auth       *string  `json:"auth"`
		Project    string   `json:"project"`
		Committish *string  `json:"committish"`
		Default    string   `json:"default"`
	} `json:"fields"`
	Results [][]json.RawMessage `json:"results"`
	JSON    string              `json:"json"`
}

type hgVectors struct {
	CallSpecs   [][]string `json:"callSpecs"`
	OptVariants []*struct {
		NoCommittish *bool `json:"noCommittish"`
		NoGitPlus    *bool `json:"noGitPlus"`
	} `json:"optVariants"`
	FromUrl []struct {
		Input string        `json:"input"`
		Info  *hgVectorInfo `json:"info"`
	} `json:"fromUrl"`
	WithOpts []struct {
		Input    string        `json:"input"`
		FromOpts int           `json:"fromOpts"`
		Info     *hgVectorInfo `json:"info"`
	} `json:"withOpts"`
	ParseUrl []struct {
		Input string  `json:"input"`
		Href  *string `json:"href"`
	} `json:"parseUrl"`
	FromManifest []struct {
		Manifest any    `json:"manifest"`
		Kind     string `json:"kind"`
		Value    string `json:"value"`
		Type     string `json:"type"`
	} `json:"fromManifest"`
	Helpers []struct {
		Input          string  `json:"input"`
		Encoded        string  `json:"encoded"`
		Decoded        *string `json:"decoded"`
		Hashformat     string  `json:"hashformat"`
		GistHashformat string  `json:"gistHashformat"`
	} `json:"helpers"`
	PrototypeKeys []string `json:"prototypeKeys"`
}

func loadHGVectors(t *testing.T) *hgVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/hosted_git_info_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v hgVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.FromUrl) == 0 || len(v.CallSpecs) == 0 {
		t.Fatal("no vectors")
	}
	return &v
}

func (v *hgVectors) options(i int) *Options {
	o := v.OptVariants[i]
	if o == nil {
		return nil
	}
	return &Options{NoCommittish: o.NoCommittish, NoGitPlus: o.NoGitPlus}
}

// callMethod runs one call spec ([method, path?, fragment?]); the result is a string or nil (null).
func callMethod(info *GitHost, spec []string, opts *Options) *string {
	arg := func(i int) string {
		if i < len(spec) {
			return spec[i]
		}
		return ""
	}
	switch spec[0] {
	case "hash":
		return new(info.Hash())
	case "ssh":
		return new(info.Ssh(opts))
	case "sshurl":
		return new(info.Sshurl(opts))
	case "browse":
		return new(info.Browse(opts))
	case "browsePath":
		return new(info.BrowsePath(arg(1), arg(2), opts))
	case "browseFile":
		return new(info.BrowseFile(arg(1), arg(2), opts))
	case "docs":
		return new(info.Docs(opts))
	case "bugs":
		return info.Bugs(opts)
	case "https":
		return new(info.Https(opts))
	case "git":
		return info.Git(opts)
	case "shortcut":
		return new(info.Shortcut(opts))
	case "path":
		return new(info.Path(opts))
	case "tarball":
		return new(info.Tarball(opts))
	case "file":
		return new(info.File(arg(1), opts))
	case "edit":
		return new(info.Edit(arg(1), opts))
	case "getDefaultRepresentation":
		return new(info.GetDefaultRepresentation())
	case "toString":
		return new(info.ToString(opts))
	}
	panic("unknown method " + spec[0])
}

func ptrString(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func optString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// topLevelKeys returns the keys of a JSON object in document order.
func topLevelKeys(t *testing.T, data []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// checkJSON compares json.Marshal(info) with JSON.stringify(info): same values and key order.
func checkJSON(t *testing.T, label string, info *GitHost, want string) {
	t.Helper()
	got, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) || !slices.Equal(topLevelKeys(t, got), topLevelKeys(t, []byte(want))) {
		t.Errorf("%s: JSON\n got %s\nwant %s", label, got, want)
	}
}

func checkInfo(t *testing.T, v *hgVectors, label string, input string, fromOpts *Options, want *hgVectorInfo, methodOpts []int) {
	t.Helper()
	info, err := FromUrlChecked(input, fromOpts)
	if plain := FromUrl(input, fromOpts); (plain == nil) != (info == nil) {
		t.Errorf("%s: FromUrl and FromUrlChecked disagree", label)
	}
	switch {
	case want == nil:
		if info != nil || err != nil {
			t.Errorf("%s: got %v, %v; want nil", label, info, err)
		}
		return
	case want.Error != nil:
		var typeErr *TypeError
		if !errors.As(err, &typeErr) || "TypeError: "+typeErr.Error() != *want.Error || info != nil {
			t.Errorf("%s: got %v, %v; want %s", label, info, err, *want.Error)
		}
		return
	}
	if err != nil || info == nil {
		t.Errorf("%s: got %v, %v; want %+v", label, info, err, *want.Fields)
		return
	}
	f := want.Fields
	if info.Type != f.Type || info.Domain != f.Domain || info.Treepath != optString(f.Treepath) || info.Blobpath != optString(f.Blobpath) ||
		info.Editpath != optString(f.Editpath) || !slices.Equal(info.Protocols, f.Protocols) || ptrString(info.User) != ptrString(f.User) ||
		ptrString(info.Auth) != ptrString(f.Auth) || info.Project != f.Project || ptrString(info.Committish) != ptrString(f.Committish) ||
		info.Default != f.Default {
		t.Errorf("%s: fields\n got type %q domain %q tree %q blob %q edit %q protocols %v user %s auth %s project %q committish %s default %q\nwant %+v",
			label, info.Type, info.Domain, info.Treepath, info.Blobpath, info.Editpath, info.Protocols, ptrString(info.User),
			ptrString(info.Auth), info.Project, ptrString(info.Committish), info.Default, *f)
	}
	checkJSON(t, label, info, want.JSON)
	for oi, results := range want.Results {
		opts := v.options(methodOpts[oi])
		for ci, raw := range results {
			spec := v.CallSpecs[ci]
			got := callMethod(info, spec, opts)
			var wantStr *string
			var wantErr struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &wantStr); err != nil {
				if err := json.Unmarshal(raw, &wantErr); err != nil {
					t.Fatal(err)
				}
				// sourcehut bugs() with noGitPlus throws in TS (null.startsWith); Go returns nil.
				if got != nil {
					t.Errorf("%s: %v opts %d: got %q, want nil for TS %s", label, spec, methodOpts[oi], *got, wantErr.Error)
				}
				continue
			}
			if ptrString(got) != ptrString(wantStr) {
				t.Errorf("%s: %v opts %d: got %s, want %s", label, spec, methodOpts[oi], ptrString(got), ptrString(wantStr))
			}
		}
	}
}

func TestGitHost_FromUrlMatchesHostedGitInfo(t *testing.T) {
	v := loadHGVectors(t)
	for _, c := range v.FromUrl {
		checkInfo(t, v, "fromUrl("+c.Input+")", c.Input, nil, c.Info, []int{0})
	}
}

func TestGitHost_OptionsMatchHostedGitInfo(t *testing.T) {
	v := loadHGVectors(t)
	all := make([]int, len(v.OptVariants))
	for i := range all {
		all[i] = i
	}
	for _, c := range v.WithOpts {
		checkInfo(t, v, "fromUrl("+c.Input+", opts "+string(rune('0'+c.FromOpts))+")", c.Input, v.options(c.FromOpts), c.Info, all)
	}
}

func TestGitHost_ParseUrlMatchesHostedGitInfo(t *testing.T) {
	v := loadHGVectors(t)
	for _, c := range v.ParseUrl {
		got := ParseUrl(c.Input)
		var href *string
		if got != nil {
			href = new(got.Href())
		}
		if ptrString(href) != ptrString(c.Href) {
			t.Errorf("parseUrl(%q) = %s, want %s", c.Input, ptrString(href), ptrString(c.Href))
		}
	}
}

func TestGitHost_FromManifestMatchesHostedGitInfo(t *testing.T) {
	v := loadHGVectors(t)
	for _, c := range v.FromManifest {
		manifest, _ := c.Manifest.(map[string]any)
		info, unknown, err := FromManifest(manifest, nil)
		var kind, value, typ string
		switch {
		case errors.Is(err, ErrNoRepository):
			kind, value = "error", "Error: "+err.Error()
		case err != nil:
			kind, value = "error", "TypeError: "+err.Error()
		case info != nil:
			kind, value, typ = "info", info.String(), info.Type
		case unknown != "":
			kind, value = "string", unknown
		case manifest == nil:
			kind = "undefined"
		default:
			kind = "null"
		}
		if kind != c.Kind || value != c.Value || typ != c.Type {
			t.Errorf("fromManifest(%v) = %s %q %q, want %s %q %q", c.Manifest, kind, value, typ, c.Kind, c.Value, c.Type)
		}
	}
}

func TestGitHost_HelpersMatchJS(t *testing.T) {
	v := loadHGVectors(t)
	gist := gitHostsByName["gist"]
	for _, c := range v.Helpers {
		if got := encodeURIComponent(c.Input); got != c.Encoded {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", c.Input, got, c.Encoded)
		}
		decoded, err := decodeURIComponent(c.Input)
		if c.Decoded == nil {
			var uriErr *URIError
			if !errors.As(err, &uriErr) || uriErr.Name() != "URIError" {
				t.Errorf("decodeURIComponent(%q) = %q, %v; want URIError", c.Input, decoded, err)
			}
		} else if err != nil || decoded != *c.Decoded {
			t.Errorf("decodeURIComponent(%q) = %q, %v; want %q", c.Input, decoded, err, *c.Decoded)
		}
		if got := formatHashFragment(c.Input); got != c.Hashformat {
			t.Errorf("formatHashFragment(%q) = %q, want %q", c.Input, got, c.Hashformat)
		}
		if got := gist.hashformat(c.Input); got != c.GistHashformat {
			t.Errorf("gist hashformat(%q) = %q, want %q", c.Input, got, c.GistHashformat)
		}
	}
	if !slices.Equal(objectPrototypeKeys, v.PrototypeKeys) {
		t.Errorf("objectPrototypeKeys = %v, want %v", objectPrototypeKeys, v.PrototypeKeys)
	}
}

func TestGitHost_OptionsFallBackToFromUrlOptions(t *testing.T) {
	info := FromUrl("github:npm/cli#v1", &Options{NoCommittish: new(true)})
	if got := info.Https(nil); got != "git+https://github.com/npm/cli.git" {
		t.Fatalf("Https(nil) = %q", got)
	}
	if got := info.Https(&Options{NoCommittish: new(false)}); got != "git+https://github.com/npm/cli.git#v1" {
		t.Fatalf("Https(noCommittish false) = %q", got)
	}
	if got := info.Tarball(nil); got != "https://codeload.github.com/npm/cli/tar.gz/v1" {
		t.Fatalf("Tarball(nil) = %q", got)
	}
	if got := info.String(); got != "github:npm/cli" {
		t.Fatalf("String() = %q", got)
	}
}

// GitHost methods are used from several goroutines; hash fragment formatting must not share casing state.
func TestGitHost_ConcurrentHashFormatting(t *testing.T) {
	info := FromUrl("github:npm/cli", nil)
	want := info.BrowsePath("README.md", "Ünïcode İ Heading ΣΑΣ", nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				if got := info.BrowsePath("README.md", "Ünïcode İ Heading ΣΑΣ", nil); got != want {
					t.Errorf("got %q, want %q", got, want)
					return
				}
			}
		})
	}
	wg.Wait()
}
