package eastasian_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/internal/eastasian"
)

// vectors.json is produced by testdata/gen.mjs (see testdata/README.md): runs of identical
// [type, width, width with ambiguousAsWide] over every code point up to `end`.
type vectors struct {
	End   rune    `json:"end"`
	Runs  [][]any `json:"runs"`
	Extra [][]any `json:"extra"`
}

type expectation struct {
	typ   eastasian.WidthType
	w, wa int
}

func decodeRow(t *testing.T, row []any) (rune, expectation) {
	t.Helper()
	if len(row) != 4 {
		t.Fatalf("bad row %v", row)
	}
	return rune(row[0].(float64)), expectation{eastasian.WidthType(row[1].(string)), int(row[2].(float64)), int(row[3].(float64))}
}

func check(t *testing.T, r rune, want expectation) bool {
	t.Helper()
	got := expectation{eastasian.EastAsianWidthType(r), eastasian.Width(r, false), eastasian.Width(r, true)}
	if got != want {
		t.Errorf("U+%04X: got %+v, want %+v", r, got, want)
		return false
	}
	return true
}

func TestEastAsian_MatchesNodeForEveryCodePoint(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, row := range v.Runs {
		start, want := decodeRow(t, row)
		end := v.End
		if i+1 < len(v.Runs) {
			next, _ := decodeRow(t, v.Runs[i+1])
			end = next - 1
		}
		for r := start; r <= end; r++ {
			if !check(t, r, want) {
				failures++
				if failures > 20 {
					t.Fatal("too many failures")
				}
			}
		}
	}
	for _, row := range v.Extra {
		r, want := decodeRow(t, row)
		check(t, r, want)
	}
}

func TestEastAsian_ReadmeExamples(t *testing.T) {
	if got := eastasian.Width('字', false); got != 2 {
		t.Errorf("Width(字) = %d, want 2", got)
	}
	if got := eastasian.EastAsianWidthType('字'); got != eastasian.WidthTypeWide {
		t.Errorf("EastAsianWidthType(字) = %q, want wide", got)
	}
	if got := eastasian.Width('⛣', false); got != 1 {
		t.Errorf("Width(⛣) = %d, want 1", got)
	}
	if got := eastasian.Width('⛣', true); got != 2 {
		t.Errorf("Width(⛣, ambiguousAsWide) = %d, want 2", got)
	}
}
