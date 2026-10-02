package ignore

import (
	"encoding/json"
	"os"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type behaviorFile struct {
	Results []struct {
		Name     string   `json:"name"`
		Patterns []string `json:"patterns"`
		Raw      string   `json:"raw"`
		Paths    []struct {
			Path         string `json:"path"`
			Ignores      *bool  `json:"ignores"`
			IgnoresError string `json:"ignoresError"`
			Test         *struct {
				Ignored   bool   `json:"ignored"`
				Unignored bool   `json:"unignored"`
				HasRule   bool   `json:"hasRule"`
				Pattern   string `json:"pattern"`
				Negative  bool   `json:"negative"`
			} `json:"test"`
			TestError string `json:"testError"`
		} `json:"paths"`
		Filtered    []string `json:"filtered"`
		FilterError string   `json:"filterError"`
	} `json:"results"`
	PanicResults []struct {
		Path    string `json:"path"`
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	} `json:"panicResults"`
}

func TestBehavior(t *testing.T) {
	raw, err := os.ReadFile("testdata/behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var file behaviorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for _, c := range file.Results {
		g := New()
		if c.Raw != "" {
			g.Add(c.Raw)
		} else {
			g.Add(c.Patterns...)
		}
		paths := make([]string, len(c.Paths))
		for i, p := range c.Paths {
			paths[i] = p.Path
			gotIgn, ignErr := catchBool(func() bool { return g.Ignores(p.Path) })
			if p.IgnoresError != "" {
				if ignErr != p.IgnoresError {
					t.Errorf("%s Ignores(%q) error %q want %q", c.Name, p.Path, ignErr, p.IgnoresError)
				}
			} else if ignErr != "" || p.Ignores == nil || gotIgn != *p.Ignores {
				t.Errorf("%s Ignores(%q)=%v err=%q want %v", c.Name, p.Path, gotIgn, ignErr, p.Ignores)
			}
			res, testErr := catchTest(func() TestResult { return g.Test(p.Path) })
			if p.TestError != "" {
				if testErr != p.TestError {
					t.Errorf("%s Test(%q) error %q want %q", c.Name, p.Path, testErr, p.TestError)
				}
				continue
			}
			if testErr != "" || p.Test == nil {
				t.Errorf("%s Test(%q) err=%q", c.Name, p.Path, testErr)
				continue
			}
			got := map[string]any{"ignored": res.Ignored, "unignored": res.Unignored, "hasRule": res.Rule != nil}
			want := map[string]any{"ignored": p.Test.Ignored, "unignored": p.Test.Unignored, "hasRule": p.Test.HasRule}
			if res.Rule != nil {
				got["pattern"] = res.Rule.Pattern
				got["negative"] = res.Rule.Negative
				want["pattern"] = p.Test.Pattern
				want["negative"] = p.Test.Negative
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("%s Test(%q) %s", c.Name, p.Path, diff)
			}
		}
		filtered, ferr := catchStrings(func() []string { return g.Filter(paths) })
		if c.FilterError != "" {
			if ferr != c.FilterError {
				t.Errorf("%s filter error %q want %q", c.Name, ferr, c.FilterError)
			}
			continue
		}
		if ferr != "" {
			t.Errorf("%s filter panic %s", c.Name, ferr)
			continue
		}
		if diff := cmp.Diff(c.Filtered, filtered); diff != "" {
			t.Errorf("%s filter %s", c.Name, diff)
		}
	}
	g := New()
	g.Add("*")
	for _, p := range file.PanicResults {
		_, err := catchBool(func() bool { return g.Ignores(p.Path) })
		if p.OK {
			if err != "" {
				t.Errorf("Ignores(%q) unexpected %s", p.Path, err)
			}
			continue
		}
		if err != p.Message {
			t.Errorf("Ignores(%q) %q want %q", p.Path, err, p.Message)
		}
	}
}

func catchBool(fn func() bool) (v bool, msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = sprintPanic(r)
		}
	}()
	return fn(), ""
}

func catchTest(fn func() TestResult) (v TestResult, msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = sprintPanic(r)
		}
	}()
	return fn(), ""
}

func catchStrings(fn func() []string) (v []string, msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = sprintPanic(r)
		}
	}()
	return fn(), ""
}

func sprintPanic(r any) string {
	switch v := r.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return ""
	}
}

func TestWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("win32 path rules")
	}
	g := New()
	g.Add("*")
	_, err := catchBool(func() bool { return g.Ignores("C:/foo") })
	if err != "path should be a `path.relative()`d string, but got \"C:/foo\"" {
		t.Fatalf("drive: %q", err)
	}
	if !g.Ignores(`foo\bar`) {
		t.Fatal(`foo\bar should be ignored after slash conversion`)
	}
	if !g.Ignores(`foo\bar\baz`) {
		t.Fatal("nested backslash")
	}
	if !g.Ignores(`\\?\C:\foo`) {
		t.Fatal("verbatim path is relative and matched as a single segment")
	}
}
