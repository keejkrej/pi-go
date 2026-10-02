package jsre

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestGoldenPi runs regexps that pi builds (tui autocomplete and CJK
// punctuation, ANSI stripping, color parsing, MCP auth headers, paste
// markers, search) against Node's results.
func TestGoldenPi(t *testing.T) {
	data, err := os.ReadFile("testdata/golden/pi.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Cases []struct {
			Pattern string `json:"pattern"`
			Flags   string `json:"flags"`
			Input   string `json:"input"`
			Test    bool   `json:"test"`
			All     []struct {
				Index  int       `json:"index"`
				Groups []*string `json:"groups"`
			} `json:"all"`
			Replace string    `json:"replace"`
			Split   []*string `json:"split"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Cases {
		re, err := Compile(c.Pattern, c.Flags)
		if err != nil {
			t.Errorf("Compile(%q, %q): %v", c.Pattern, c.Flags, err)
			continue
		}
		name := fmt.Sprintf("/%s/%s on %q", c.Pattern, c.Flags, c.Input)
		if got := re.Test(c.Input); got != c.Test {
			t.Errorf("%s: Test = %v, want %v", name, got, c.Test)
		}
		all := re.MatchAll(c.Input)
		if len(all) != len(c.All) {
			t.Errorf("%s: %d matches, want %d", name, len(all), len(c.All))
		} else {
			for i, m := range all {
				w := c.All[i]
				got := m.Captures()
				if m.Index != w.Index || len(got) != len(w.Groups) {
					t.Errorf("%s: match %d at %d with %d groups, want %d with %d", name, i, m.Index, len(got), w.Index, len(w.Groups))
					continue
				}
				for j := range got {
					if strp(got[j]) != strp(w.Groups[j]) {
						t.Errorf("%s: match %d group %d = %q, want %q", name, i, j, strp(got[j]), strp(w.Groups[j]))
					}
				}
			}
		}
		if got := re.Replace(c.Input, "<$&>"); got != c.Replace {
			t.Errorf("%s: Replace = %q, want %q", name, got, c.Replace)
		}
		want := make([]string, len(c.Split))
		for i, p := range c.Split {
			want[i] = strp(p)
			if p == nil {
				want[i] = ""
			}
		}
		if got := re.Split(c.Input, -1); !slices.Equal(got, want) {
			t.Errorf("%s: Split = %q, want %q", name, got, want)
		}
	}
}

func TestCompileErrors(t *testing.T) {
	_, err := Compile("a(", "g")
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("Compile error %T, want *SyntaxError", err)
	}
	if se.Name() != "SyntaxError" || se.Error() != "Invalid regular expression: /a(/g: Unterminated group" {
		t.Errorf("got %s: %q", se.Name(), se.Error())
	}
	if _, err := Compile("a", "gg"); err == nil || err.Error() != "Invalid flags supplied to RegExp constructor 'gg'" {
		t.Errorf("duplicate flags: %v", err)
	}
	if _, err := Compile("a", "v"); !errors.Is(err, ErrUnicodeSets) {
		t.Errorf("v flag: %v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustCompile did not panic")
		}
	}()
	MustCompile("[", "")
}

func TestRegexpAccessors(t *testing.T) {
	re := MustCompile("a/b", "yusmigd")
	if re.Flags() != "dgimsuy" || re.String() != `/a\/b/dgimsuy` || re.Source() != `a\/b` {
		t.Errorf("Flags %q String %q Source %q", re.Flags(), re.String(), re.Source())
	}
	if !re.Global() || !re.IgnoreCase() || !re.Multiline() || !re.DotAll() || !re.Unicode() || !re.Sticky() || !re.HasIndices() {
		t.Error("flag accessors")
	}
	if got := MustCompile("", "").String(); got != "/(?:)/" {
		t.Errorf("empty String = %q", got)
	}
	named := MustCompile(`(?<y>\d+)-(x)-(?<m>\d+)|(?<y>z)`, "")
	if named.NumCaptures() != 4 || !slices.Equal(named.GroupNames(), []string{"y", "m"}) {
		t.Errorf("NumCaptures %d GroupNames %q", named.NumCaptures(), named.GroupNames())
	}
	if MustCompile("(a)", "").GroupNames() != nil {
		t.Error("GroupNames without names")
	}
}

func TestExecIndices(t *testing.T) {
	// "😀" is two UTF-16 units.
	s := "😀ab😀b"
	re := MustCompile("b", "g")
	var got []int
	for from := 0; ; {
		m := re.Exec(s, from)
		if m == nil {
			break
		}
		got = append(got, m.Index, m.End())
		from = m.End()
	}
	if !slices.Equal(got, []int{3, 4, 6, 7}) {
		t.Errorf("global Exec indices %v", got)
	}
	if m := MustCompile("b", "").Exec(s, 5); m == nil || m.Index != 3 {
		t.Errorf("non-global Exec must ignore from: %v", m)
	}
	if m := MustCompile("b", "y").Exec(s, 3); m == nil || m.Index != 3 {
		t.Error("sticky Exec at 3")
	}
	if m := MustCompile("b", "y").Exec(s, 2); m != nil {
		t.Error("sticky Exec at 2 matched")
	}
	if m := MustCompile("", "g").Exec(s, 8); m != nil {
		t.Error("Exec beyond the end matched")
	}
	if m := MustCompile("", "g").Exec(s, 7); m == nil || m.Index != 7 {
		t.Error("Exec at the end")
	}
	// With /u an index inside a pair moves back to the pair start.
	if m := MustCompile(".", "gu").Exec(s, 1); m == nil || m.Index != 0 || m.String() != "😀" {
		t.Errorf("unicode Exec from 1: %v", m)
	}
	// Without /u a match can split a pair; the halves become U+FFFD.
	m := MustCompile(".", "g").Exec(s, 1)
	if m == nil || m.Index != 1 || m.String() != string(rune(0xFFFD)) {
		t.Errorf("non-unicode Exec from 1: %v", m)
	}
}

func TestMatchGroups(t *testing.T) {
	re := MustCompile(`(?<word>\w+)(?:-(?<num>\d+))?(x)?`, "d")
	m := re.Exec("  abc-12 ", 0)
	if m == nil {
		t.Fatal("no match")
	}
	if m.NumGroups() != 4 || m.String() != "abc-12" || m.Group(1) != "abc" || m.Group(2) != "12" || m.Group(9) != "" {
		t.Errorf("groups %d %q %q %q", m.NumGroups(), m.String(), m.Group(1), m.Group(2))
	}
	if v, ok := m.GroupOK(3); ok || v != "" {
		t.Error("group 3 participated")
	}
	if s, e := m.GroupIndex(2); s != 6 || e != 8 {
		t.Errorf("GroupIndex(2) = %d, %d", s, e)
	}
	if s, e := m.GroupIndex(3); s != -1 || e != -1 {
		t.Errorf("GroupIndex(3) = %d, %d", s, e)
	}
	if m.Named("word") != "abc" || m.Named("nope") != "" {
		t.Error("Named")
	}
	ng := m.NamedGroups()
	if len(ng) != 2 || *ng["word"] != "abc" || *ng["num"] != "12" {
		t.Errorf("NamedGroups %v", ng)
	}
	m = re.Exec("abc", 0)
	if ng := m.NamedGroups(); ng["num"] != nil {
		t.Error("undefined named group must be nil")
	}
	if v, ok := m.NamedOK("num"); ok || v != "" {
		t.Error("NamedOK of undefined group")
	}
	// Duplicate names: the participating group wins.
	dup := MustCompile(`(?<n>a)|(?<n>b)`, "")
	if got := dup.Exec("b", 0).Named("n"); got != "b" {
		t.Errorf("duplicate name = %q", got)
	}
}

func TestStringHelpers(t *testing.T) {
	re := MustCompile(`(\d)(\d)?`, "g")
	if got := re.MatchStrings("a1b23"); !slices.Equal(got, []string{"1", "23"}) {
		t.Errorf("global MatchStrings %q", got)
	}
	if got := MustCompile(`(\d)(\d)?`, "").MatchStrings("a1b23"); !slices.Equal(got, []string{"1", "1", ""}) {
		t.Errorf("MatchStrings %q", got)
	}
	if MustCompile("z", "g").MatchStrings("abc") != nil {
		t.Error("MatchStrings without a match")
	}
	if got := MustCompile("b", "").Search("😀ab"); got != 3 {
		t.Errorf("Search = %d", got)
	}
	if got := MustCompile("z", "").Search("ab"); got != -1 {
		t.Errorf("Search = %d", got)
	}
	ui := MustCompile("", "u")
	if ui.AdvanceIndex("😀a", 0) != 2 || ui.AdvanceIndex("😀a", 2) != 3 || ui.AdvanceIndex("😀a", 1) != 2 {
		t.Error("AdvanceIndex /u")
	}
	if MustCompile("", "").AdvanceIndex("😀a", 0) != 1 {
		t.Error("AdvanceIndex")
	}
	if got := MustCompile("", "gu").MatchAll("😀a"); len(got) != 3 || got[1].Index != 2 {
		t.Errorf("MatchAll /u empty matches: %d", len(got))
	}
	if got := MustCompile("", "g").MatchAll("😀a"); len(got) != 4 {
		t.Errorf("MatchAll empty matches: %d", len(got))
	}
	if got := MustCompile("a", "y").MatchAll("aaba"); len(got) != 2 {
		t.Errorf("sticky MatchAll: %d", len(got))
	}
}

func TestReplace(t *testing.T) {
	cases := []struct{ pattern, flags, s, repl, want string }{
		{`(\w+)\s(\w+)`, "", "John Smith", "$2, $1", "Smith, John"},
		{`o`, "", "foo", "0", "f0o"},
		{`o`, "g", "foo", "0", "f00"},
		{`(?<first>\w)`, "g", "ab", "[$<first>]", "[a][b]"},
		{`b`, "", "abc", "$$-$&-$`-$'", "a$-b-a-cc"},
		{`x*`, "g", "ab", "-", "-a-b-"},
		{`(a)?b`, "g", "ab b", "[$1]", "[a] []"},
	}
	for _, c := range cases {
		if got := MustCompile(c.pattern, c.flags).Replace(c.s, c.repl); got != c.want {
			t.Errorf("%q.replace(/%s/%s, %q) = %q, want %q", c.s, c.pattern, c.flags, c.repl, got, c.want)
		}
	}
	got := MustCompile(`\d+`, "g").ReplaceFunc("a1b22c", func(m *Match) string {
		return fmt.Sprintf("<%s@%d>", m.String(), m.Index)
	})
	if got != "a<1@1>b<22@3>c" {
		t.Errorf("ReplaceFunc = %q", got)
	}
	if got := MustCompile(`z`, "g").ReplaceFunc("abc", func(*Match) string { return "!" }); got != "abc" {
		t.Errorf("ReplaceFunc without match = %q", got)
	}
}

func TestSplit(t *testing.T) {
	cases := []struct {
		pattern, s string
		limit      int
		want       []string
	}{
		{`,`, "a,b,,c", -1, []string{"a", "b", "", "c"}},
		{`,`, "a,b,,c", 2, []string{"a", "b"}},
		{`,`, "a,b", 0, []string{}},
		{`(,)`, "a,b", -1, []string{"a", ",", "b"}},
		{`(x)?,`, "a,b", -1, []string{"a", "", "b"}},
		{``, "a😀", -1, []string{"a", string(rune(0xFFFD)), string(rune(0xFFFD))}},
		{`\s*`, "a b", -1, []string{"a", "b"}},
		{`a`, "", -1, []string{""}},
		{`a*`, "", -1, []string{}},
	}
	for _, c := range cases {
		if got := MustCompile(c.pattern, "").Split(c.s, c.limit); !slices.Equal(got, c.want) {
			t.Errorf("%q.split(/%s/, %d) = %q, want %q", c.s, c.pattern, c.limit, got, c.want)
		}
	}
	if got := MustCompile(``, "u").Split("a😀", -1); !slices.Equal(got, []string{"a", "😀"}) {
		t.Errorf("unicode split = %q", got)
	}
}

// A Go string with invalid UTF-8 is matched as if each invalid byte were
// U+FFFD; returned substrings keep the original bytes.
func TestInvalidUTF8(t *testing.T) {
	s := "a\xffb"
	repl := "\\" + "uFFFD"
	for _, flags := range []string{"", "u"} {
		m := MustCompile(repl+"(b)", flags).Exec(s, 0)
		if m == nil || m.Index != 1 || m.End() != 3 || m.String() != "\xffb" || m.Group(1) != "b" {
			t.Errorf("flags %q: %v", flags, m)
		}
		if got := MustCompile(".", "g"+flags).Replace(s, "."); got != "..." {
			t.Errorf("flags %q: Replace = %q", flags, got)
		}
	}
}

// Lone surrogate code units (without /u, halves of astral characters) are
// matched like any other unit.
func TestSurrogateUnits(t *testing.T) {
	lead, trail := "\\"+"uD83D", "\\"+"uDE00"
	cases := []struct {
		pattern, flags, s string
		index, end        int
	}{
		{lead, "", "x😀", 1, 2},
		{trail, "", "x😀", 2, 3},
		{"[" + lead + "-" + "\\" + "uD83E]", "", "x😀", 1, 2},
		{"[" + lead + trail + "]+", "", "x😀", 1, 3},
		{lead + trail, "u", "x😀", 1, 3},
		{"[^a]", "", "😀", 0, 1},
		{"[^a]", "u", "😀", 0, 2},
		{`\S\S`, "", "😀", 0, 2},
		{`^[😀]$`, "u", "😀", 0, 2},
	}
	for _, c := range cases {
		m := MustCompile(c.pattern, c.flags).Exec(c.s, 0)
		if m == nil || m.Index != c.index || m.End() != c.end {
			t.Errorf("/%s/%s on %q: %v, want [%d,%d)", c.pattern, c.flags, c.s, m, c.index, c.end)
		}
	}
	for _, c := range []struct{ pattern, flags, s string }{
		{lead, "u", "😀"},
		{trail, "u", "😀"},
		{`^[😀]$`, "", "😀"},
	} {
		if m := MustCompile(c.pattern, c.flags).Exec(c.s, 0); m != nil {
			t.Errorf("/%s/%s on %q matched at %d", c.pattern, c.flags, c.s, m.Index)
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	re := MustCompile(`(?<k>\w+)=(?<v>[^;]*)`, "g")
	inputs := []string{"a=1;b=2", "x=😀;y=", strings.Repeat("k=v;", 50)}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			for j := range 50 {
				s := inputs[(i+j)%len(inputs)]
				want := strings.Count(s, "=")
				if got := len(re.MatchAll(s)); got != want {
					t.Errorf("%q: %d matches, want %d", s, got, want)
					return
				}
				re.Replace(s, "$<v>=$<k>")
				re.Split(s, -1)
			}
		})
	}
	wg.Wait()
}
