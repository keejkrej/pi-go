package hostedgit

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// Vectors in testdata/url_vectors.json were generated with node v24.21.0 (`URL`, ada 4.0.0). The inputs
// are the WPT urltestdata.json and setters_tests.json cases plus git URL and IDNA cases; the expected
// values are what Node returned, not the WPT expectations. Generator script: gen-url.cjs, not kept.
type urlSnapshot struct {
	Href     string `json:"href"`
	Protocol string `json:"protocol"`
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Pathname string `json:"pathname"`
	Search   string `json:"search"`
	Hash     string `json:"hash"`
	Origin   string `json:"origin"`
}

type urlParseVector struct {
	Input string  `json:"input"`
	Base  *string `json:"base"`
	OK    bool    `json:"ok"`
	urlSnapshot
}

type urlSetterVector struct {
	Input  string `json:"input"`
	Setter string `json:"setter"`
	Value  string `json:"value"`
	OK     bool   `json:"ok"`
	urlSnapshot
}

func loadURLVectors(t *testing.T) (parse []urlParseVector, set []urlSetterVector) {
	t.Helper()
	data, err := os.ReadFile("testdata/url_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Parse []urlParseVector  `json:"parse"`
		Set   []urlSetterVector `json:"set"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Parse) == 0 || len(v.Set) == 0 {
		t.Fatal("no vectors")
	}
	return v.Parse, v.Set
}

func snapshotURL(u *URL) urlSnapshot {
	return urlSnapshot{
		Href:     u.Href(),
		Protocol: u.Protocol(),
		Username: u.Username(),
		Password: u.Password(),
		Host:     u.Host(),
		Hostname: u.Hostname(),
		Port:     u.Port(),
		Pathname: u.Pathname(),
		Search:   u.Search(),
		Hash:     u.Hash(),
		Origin:   u.Origin(),
	}
}

func TestUrl_ParseMatchesNode(t *testing.T) {
	parse, _ := loadURLVectors(t)
	for _, v := range parse {
		var u *URL
		var err error
		if v.Base == nil {
			u, err = NewURL(v.Input)
		} else {
			u, err = NewURLWithBase(v.Input, *v.Base)
		}
		if v.Base == nil && URLCanParse(v.Input) != v.OK {
			t.Errorf("URLCanParse(%q) = %v, want %v", v.Input, !v.OK, v.OK)
		}
		if !v.OK {
			if err == nil {
				t.Errorf("NewURL(%q, base %v): got %q, want Invalid URL", v.Input, v.Base, u.Href())
			}
			continue
		}
		if err != nil {
			t.Errorf("NewURL(%q, base %v): unexpected error %v, want %q", v.Input, v.Base, err, v.Href)
			continue
		}
		if got := snapshotURL(u); got != v.urlSnapshot {
			t.Errorf("NewURL(%q, base %v):\n got %+v\nwant %+v", v.Input, v.Base, got, v.urlSnapshot)
		}
	}
}

func TestUrl_SettersMatchNode(t *testing.T) {
	_, set := loadURLVectors(t)
	for _, v := range set {
		u, err := NewURL(v.Input)
		if err != nil {
			t.Errorf("NewURL(%q): %v", v.Input, err)
			continue
		}
		var setErr error
		switch v.Setter {
		case "href":
			setErr = u.SetHref(v.Value)
		case "protocol":
			u.SetProtocol(v.Value)
		case "username":
			u.SetUsername(v.Value)
		case "password":
			u.SetPassword(v.Value)
		case "host":
			u.SetHost(v.Value)
		case "hostname":
			u.SetHostname(v.Value)
		case "port":
			u.SetPort(v.Value)
		case "pathname":
			u.SetPathname(v.Value)
		case "search":
			u.SetSearch(v.Value)
		case "hash":
			u.SetHash(v.Value)
		default:
			t.Fatalf("unknown setter %q", v.Setter)
		}
		if (setErr == nil) != v.OK {
			t.Errorf("%q.%s = %q: error %v, want ok %v", v.Input, v.Setter, v.Value, setErr, v.OK)
		}
		if got := snapshotURL(u); got != v.urlSnapshot {
			t.Errorf("%q.%s = %q:\n got %+v\nwant %+v", v.Input, v.Setter, v.Value, got, v.urlSnapshot)
		}
	}
}

func TestUrl_InvalidURLError(t *testing.T) {
	_, err := NewURL("not a url")
	var invalid *InvalidURLError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v", err)
	}
	if invalid.Error() != "Invalid URL" || invalid.Name() != "TypeError" || invalid.Code() != "ERR_INVALID_URL" || invalid.Input != "not a url" {
		t.Fatalf("got %+v", invalid)
	}
	if URLCanParse("not a url") {
		t.Fatal("URLCanParse accepted an invalid URL")
	}
	u, err := NewURL("https://a.com/x")
	if err != nil || u.String() != "https://a.com/x" || u.ToJSON() != "https://a.com/x" {
		t.Fatalf("String/ToJSON: %v %v", u, err)
	}
}
