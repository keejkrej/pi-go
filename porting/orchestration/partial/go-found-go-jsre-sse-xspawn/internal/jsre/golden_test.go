package jsre

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"unicode"
)

// testdata/golden/*.json were generated with Node v24.21.0 (V8 13.6); see
// testdata/golden/README.md.

type goldenExecResult struct {
	Index   int          `json:"index"`
	End     int          `json:"end"`
	Groups  []*string    `json:"groups"`
	Named   [][2]*string `json:"named"`
	Indices [][]int      `json:"indices"`
}

type goldenFile struct {
	Errors []struct {
		Pattern string  `json:"pattern"`
		Flags   string  `json:"flags"`
		Error   *string `json:"error"`
	} `json:"errors"`
	Exec []struct {
		Pattern string            `json:"pattern"`
		Flags   string            `json:"flags"`
		Input   string            `json:"input"`
		From    int               `json:"from"`
		Result  *goldenExecResult `json:"result"`
	} `json:"exec"`
	Replace []struct {
		Pattern string `json:"pattern"`
		Flags   string `json:"flags"`
		Input   string `json:"input"`
		Repl    string `json:"repl"`
		Result  string `json:"result"`
	} `json:"replace"`
	Split []struct {
		Pattern string    `json:"pattern"`
		Flags   string    `json:"flags"`
		Input   string    `json:"input"`
		Limit   int       `json:"limit"`
		Result  []*string `json:"result"`
	} `json:"split"`
	Source []struct {
		Pattern string `json:"pattern"`
		Flags   string `json:"flags"`
		Source  string `json:"source"`
		String  string `json:"string"`
	} `json:"source"`
}

func loadGolden(t *testing.T) *goldenFile {
	t.Helper()
	data, err := os.ReadFile("testdata/golden/regexp.json")
	if err != nil {
		t.Fatal(err)
	}
	var g goldenFile
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	return &g
}

func strp(s *string) string {
	if s == nil {
		return "<undefined>"
	}
	return *s
}

func TestGoldenErrors(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Errors {
		re, err := Compile(c.Pattern, c.Flags)
		switch {
		case c.Error == nil && err != nil:
			t.Errorf("Compile(%q, %q): unexpected error %v", c.Pattern, c.Flags, err)
		case c.Error != nil && err == nil:
			t.Errorf("Compile(%q, %q): want error %q", c.Pattern, c.Flags, *c.Error)
		case c.Error != nil && err.Error() != *c.Error:
			if err == ErrUnicodeSets {
				continue
			}
			t.Errorf("Compile(%q, %q):\n got %q\nwant %q", c.Pattern, c.Flags, err.Error(), *c.Error)
		case c.Error == nil:
			// Every valid pattern must translate to a valid regexp2 program.
			re.program()
		}
	}
}

func TestGoldenExec(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Exec {
		re, err := Compile(c.Pattern, c.Flags)
		if err != nil {
			t.Errorf("Compile(%q, %q): %v", c.Pattern, c.Flags, err)
			continue
		}
		m := re.Exec(c.Input, c.From)
		name := "/" + c.Pattern + "/" + c.Flags
		if c.Result == nil {
			if m != nil {
				t.Errorf("%s.exec(%q) from %d: got match %q at %d, want null", name, c.Input, c.From, m.String(), m.Index)
			}
			continue
		}
		if m == nil {
			t.Errorf("%s.exec(%q) from %d: got null, want match at %d", name, c.Input, c.From, c.Result.Index)
			continue
		}
		if m.Index != c.Result.Index || m.End() != c.Result.End {
			t.Errorf("%s.exec(%q) from %d: got [%d,%d), want [%d,%d)", name, c.Input, c.From, m.Index, m.End(), c.Result.Index, c.Result.End)
		}
		got := m.Captures()
		if len(got) != len(c.Result.Groups) {
			t.Errorf("%s.exec(%q): got %d groups, want %d", name, c.Input, len(got), len(c.Result.Groups))
			continue
		}
		for i := range got {
			if strp(got[i]) != strp(c.Result.Groups[i]) {
				t.Errorf("%s.exec(%q) from %d: group %d = %q, want %q", name, c.Input, c.From, i, strp(got[i]), strp(c.Result.Groups[i]))
			}
		}
		for _, kv := range c.Result.Named {
			v, ok := m.NamedOK(*kv[0])
			want := strp(kv[1])
			gotv := "<undefined>"
			if ok {
				gotv = v
			}
			if gotv != want {
				t.Errorf("%s.exec(%q): groups.%s = %q, want %q", name, c.Input, *kv[0], gotv, want)
			}
		}
		if c.Result.Named == nil && m.NamedGroups() != nil {
			t.Errorf("%s.exec(%q): unexpected named groups", name, c.Input)
		}
		for i, idx := range c.Result.Indices {
			s, e := m.GroupIndex(i)
			if idx == nil {
				if s != -1 || e != -1 {
					t.Errorf("%s.exec(%q): indices[%d] = [%d,%d], want undefined", name, c.Input, i, s, e)
				}
			} else if s != idx[0] || e != idx[1] {
				t.Errorf("%s.exec(%q): indices[%d] = [%d,%d], want %v", name, c.Input, i, s, e, idx)
			}
		}
	}
}

func TestGoldenReplace(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Replace {
		re := MustCompile(c.Pattern, c.Flags)
		if got := re.Replace(c.Input, c.Repl); got != c.Result {
			t.Errorf("%q.replace(/%s/%s, %q) = %q, want %q", c.Input, c.Pattern, c.Flags, c.Repl, got, c.Result)
		}
	}
}

func TestGoldenSplit(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Split {
		re := MustCompile(c.Pattern, c.Flags)
		want := make([]string, len(c.Result))
		for i, p := range c.Result {
			if p != nil {
				want[i] = *p
			}
		}
		if got := re.Split(c.Input, c.Limit); !slices.Equal(got, want) {
			t.Errorf("%q.split(/%s/%s, %d) = %q, want %q", c.Input, c.Pattern, c.Flags, c.Limit, got, want)
		}
	}
}

func TestGoldenSource(t *testing.T) {
	g := loadGolden(t)
	for _, c := range g.Source {
		re := MustCompile(c.Pattern, c.Flags)
		if re.Source() != c.Source || re.String() != c.String {
			t.Errorf("Compile(%q, %q): source %q string %q, want %q %q", c.Pattern, c.Flags, re.Source(), re.String(), c.Source, c.String)
		}
	}
}

// TestCaseClassesGolden checks the non-Unicode table and Go's simple case
// folding orbits against the classes observed in V8.
func TestCaseClassesGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/golden/caseclasses.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		NonUnicode [][]rune `json:"nonUnicode"`
		Unicode    [][]rune `json:"unicode"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	check := func(classes [][]rune, unicodeMode bool, limit rune) {
		want := map[rune][]rune{}
		for _, cls := range classes {
			for _, c := range cls {
				want[c] = cls
			}
		}
		for c := rune(0); c <= limit; c++ {
			w := want[c]
			if w == nil {
				w = []rune{c}
			}
			got := slices.Clone(charClosure(c, unicodeMode))
			slices.Sort(got)
			if !slices.Equal(got, w) {
				t.Errorf("unicode=%v: closure(%U) = %U, want %U", unicodeMode, c, got, w)
			}
		}
	}
	check(v.NonUnicode, false, 0xFFFF)
	check(v.Unicode, true, unicode.MaxRune)
}
