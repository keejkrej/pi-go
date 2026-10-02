package hostedgit

import (
	"encoding/json"
	"os"
	"testing"
)

// testdata/hosted_git_info_fuzz.json is a differential corpus generated with node v24.21.0 and
// hosted-git-info@9.0.3 (generator scripts: gen-fuzz.cjs and compact.cjs, not kept). Inputs are random
// concatenations of 1-8 tokens (schemes and shortcuts, known and unknown hosts, Object.prototype names,
// IPv4/IPv6 hosts, IDNA and full-width labels, percent escapes, separators, path words, whitespace and
// control characters). Each record is [input, error, info, url, parseUrl]:
//   - error: String(err) when fromUrl throws, else null
//   - info: null, or [type, user, auth, project, committish, default, toString(), https(), browse(), ssh(),
//     tarball(), file("a/b c.js"), browse("/x/y.md", "Heading One!")]
//   - url: null when `new URL(input)` throws, else [href, hostname, pathname, hash, username, password,
//     port, search]
//   - parseUrl: hostedGitInfo.parseUrl(input)?.href ?? null
type tfcRecord struct {
	Input string
	Error *string
	Info  []*string
	URL   []string
	Parse *string
}

func (r *tfcRecord) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	fields := []any{&r.Input, &r.Error, &r.Info, &r.URL, &r.Parse}
	for i, f := range fields {
		if err := json.Unmarshal(raw[i], f); err != nil {
			return err
		}
	}
	return nil
}

func TestFuzzCorpus_MatchesHostedGitInfo(t *testing.T) {
	data, err := os.ReadFile("testdata/hosted_git_info_fuzz.json")
	if err != nil {
		t.Fatal(err)
	}
	var records []tfcRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("no records")
	}
	for _, r := range records {
		u, err := NewURL(r.Input)
		switch {
		case (err == nil) != (r.URL != nil):
			t.Errorf("new URL(%q): err %v, want ok %v", r.Input, err, r.URL != nil)
		case err == nil:
			got := []string{u.Href(), u.Hostname(), u.Pathname(), u.Hash(), u.Username(), u.Password(), u.Port(), u.Search()}
			for i := range got {
				if got[i] != r.URL[i] {
					t.Errorf("new URL(%q): field %d = %q, want %q", r.Input, i, got[i], r.URL[i])
					break
				}
			}
		}

		var parsed *string
		if p := ParseUrl(r.Input); p != nil {
			parsed = new(p.Href())
		}
		if ptrString(parsed) != ptrString(r.Parse) {
			t.Errorf("parseUrl(%q) = %s, want %s", r.Input, ptrString(parsed), ptrString(r.Parse))
		}

		info, err := FromUrlChecked(r.Input, nil)
		if r.Error != nil {
			if err == nil || "TypeError: "+err.Error() != *r.Error {
				t.Errorf("fromUrl(%q): err %v, want %s", r.Input, err, *r.Error)
			}
			continue
		}
		if err != nil || (info == nil) != (r.Info == nil) {
			t.Errorf("fromUrl(%q) = %v, %v; want info %v", r.Input, info, err, r.Info != nil)
			continue
		}
		if info == nil {
			continue
		}
		got := []string{info.Type, ptrString(info.User), ptrString(info.Auth), info.Project, ptrString(info.Committish), info.Default,
			info.String(), info.Https(nil), info.Browse(nil), info.Ssh(nil), info.Tarball(nil), info.File("a/b c.js", nil),
			info.BrowsePath("/x/y.md", "Heading One!", nil)}
		for i := range got {
			if got[i] != ptrString(r.Info[i]) {
				t.Errorf("fromUrl(%q): field %d = %q, want %s", r.Input, i, got[i], ptrString(r.Info[i]))
				break
			}
		}
	}
}
