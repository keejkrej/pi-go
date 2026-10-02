package semver

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Valid []struct {
			V     string `json:"v"`
			Value any    `json:"value"`
		} `json:"valid"`
		ValidRange []struct {
			R     string `json:"r"`
			Value any    `json:"value"`
		} `json:"validRange"`
		Pairs []struct {
			A        string `json:"a"`
			B        string `json:"b"`
			Compare  int    `json:"compare"`
			RCompare int    `json:"rcompare"`
			Gt       bool   `json:"gt"`
			Gtba     bool   `json:"gtba"`
		} `json:"pairs"`
		Throws []struct {
			A string `json:"a"`
			B string `json:"b"`
		} `json:"throws"`
		Satisfies []struct {
			Version string `json:"version"`
			Range   string `json:"range"`
			Value   bool   `json:"value"`
		} `json:"satisfies"`
		MaxSat []struct {
			Versions []string `json:"versions"`
			Range    string   `json:"range"`
			Value    any      `json:"value"`
		} `json:"maxSat"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	fails := 0
	report := func(msg string) {
		fails++
		if fails <= 40 {
			t.Error(msg)
		}
	}
	for _, c := range file.Valid {
		got, ok := Valid(c.V)
		want, wantOK := jsonString(c.Value)
		if ok != wantOK || got != want {
			report(cmp.Diff(map[string]any{"ok": wantOK, "v": want}, map[string]any{"ok": ok, "v": got}) + " valid " + c.V)
		}
	}
	for _, c := range file.ValidRange {
		got, ok := ValidRange(c.R)
		want, wantOK := jsonString(c.Value)
		if ok != wantOK || got != want {
			report(cmp.Diff(map[string]any{"ok": wantOK, "v": want}, map[string]any{"ok": ok, "v": got}) + " range " + c.R)
		}
	}
	for _, c := range file.Pairs {
		got := map[string]any{
			"compare":  Compare(c.A, c.B),
			"rcompare": RCompare(c.A, c.B),
			"gt":       Gt(c.A, c.B),
			"gtba":     Gt(c.B, c.A),
		}
		want := map[string]any{
			"compare":  c.Compare,
			"rcompare": c.RCompare,
			"gt":       c.Gt,
			"gtba":     c.Gtba,
		}
		if diff := cmp.Diff(want, got); diff != "" {
			report(c.A + " vs " + c.B + "\n" + diff)
		}
	}
	// npm throws TypeError on invalid Compare/Gt/RCompare. These bindings return 0/false.
	for _, c := range file.Throws {
		got := map[string]any{
			"compare":  Compare(c.A, c.B),
			"rcompare": RCompare(c.A, c.B),
			"gt":       Gt(c.A, c.B),
			"gtba":     Gt(c.B, c.A),
		}
		want := map[string]any{"compare": 0, "rcompare": 0, "gt": false, "gtba": false}
		if diff := cmp.Diff(want, got); diff != "" {
			report("invalid " + c.A + " vs " + c.B + "\n" + diff)
		}
	}
	for _, c := range file.Satisfies {
		got := Satisfies(c.Version, c.Range)
		if got != c.Value {
			report(cmp.Diff(c.Value, got) + " satisfies " + c.Version + " " + c.Range)
		}
	}
	for _, c := range file.MaxSat {
		got := MaxSatisfying(c.Versions, c.Range)
		want, _ := jsonString(c.Value)
		if got != want {
			report(cmp.Diff(want, got) + " maxSatisfying " + c.Range)
		}
	}
	if fails > 40 {
		t.Errorf("%d mismatches, showed 40", fails)
	}
}

func jsonString(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
