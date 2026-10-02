package minimatch

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type mmCase struct {
	Pattern   string     `json:"pattern"`
	Platform  string     `json:"platform"`
	NoCase    bool       `json:"nocase"`
	Dot       bool       `json:"dot"`
	MatchBase bool       `json:"matchBase"`
	NoBrace   bool       `json:"nobrace"`
	NoExt     bool       `json:"noext"`
	Comment   bool       `json:"comment"`
	Empty     bool       `json:"empty"`
	Negate    bool       `json:"negate"`
	Set       [][]string `json:"set"`
	Paths     []struct {
		P string `json:"p"`
		M bool   `json:"m"`
	} `json:"paths"`
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []mmCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	fails := 0
	report := func(msg string) {
		fails++
		if fails <= 30 {
			t.Error(msg)
		}
	}
	for _, c := range file.Cases {
		opts := Options{
			NoCase:    c.NoCase,
			Dot:       c.Dot,
			MatchBase: c.MatchBase,
			NoBrace:   c.NoBrace,
			NoExt:     c.NoExt,
			Platform:  c.Platform,
		}
		comment, empty, negate, rows := partsDebug(c.Pattern, opts)
		if comment != c.Comment || empty != c.Empty || negate != c.Negate {
			report(cmp.Diff(map[string]bool{"comment": c.Comment, "empty": c.Empty, "negate": c.Negate},
				map[string]bool{"comment": comment, "empty": empty, "negate": negate}) + " pattern " + c.Pattern)
		}
		if diff := cmp.Diff(c.Set, rows); diff != "" {
			report("set " + c.Pattern + " " + optsLabel(opts) + "\n" + diff)
		}
		for _, p := range c.Paths {
			got := Match(p.P, c.Pattern, opts)
			if got != p.M {
				report(cmp.Diff(p.M, got) + "\npath " + p.P + " pattern " + c.Pattern + " " + optsLabel(opts))
			}
		}
	}
	if fails > 30 {
		t.Errorf("%d mismatches, showed 30", fails)
	}
}

func optsLabel(o Options) string {
	return strings.Join([]string{
		"platform=" + o.Platform,
		boolOpt("nocase", o.NoCase),
		boolOpt("dot", o.Dot),
		boolOpt("matchBase", o.MatchBase),
		boolOpt("nobrace", o.NoBrace),
		boolOpt("noext", o.NoExt),
	}, " ")
}

func boolOpt(name string, v bool) string {
	if v {
		return name
	}
	return ""
}

func TestOverlongPattern(t *testing.T) {
	p := strings.Repeat("a", 1024*64+1)
	if Match("a", p, Options{Platform: "posix"}) {
		t.Fatal("overlong pattern matched")
	}
}
