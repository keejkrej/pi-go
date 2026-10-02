package jsdiff_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsdiff"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Expected values in this file were produced with diff@8.0.4 under node.

const (
	piOld = "package main\n\nfunc main() {\n\tprintln(\"a\")\n}\n"
	piNew = "package main\n\nfunc main() {\n\tprintln(\"b\")\n\tprintln(\"c\")\n}\n"
)

func mustStringify(t *testing.T, v any) string {
	t.Helper()
	s, err := jsonx.Stringify(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// generateUnifiedPatch in packages/coding-agent/src/core/tools/edit-diff.ts.
func TestJsdiff_PiUnifiedPatch(t *testing.T) {
	context := 4
	got := jsdiff.CreateTwoFilesPatch("main.go", "main.go", piOld, piNew, nil, nil, &jsdiff.PatchOptions{
		Context:       &context,
		HeaderOptions: &jsdiff.FileHeadersOnly,
	})
	want := "--- main.go\n+++ main.go\n@@ -1,5 +1,6 @@\n package main\n \n func main() {\n-\tprintln(\"a\")\n+\tprintln(\"b\")\n+\tprintln(\"c\")\n }\n"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// generateDiffString (edit-diff.ts) and renderIntraLineDiff (components/diff.ts).
func TestJsdiff_PiLineAndWordDiff(t *testing.T) {
	lines := mustStringify(t, jsdiff.DiffLines(piOld, piNew, nil))
	wantLines := `[{"count":3,"added":false,"removed":false,"value":"package main\n\nfunc main() {\n"},{"count":1,"added":false,"removed":true,"value":"\tprintln(\"a\")\n"},{"count":2,"added":true,"removed":false,"value":"\tprintln(\"b\")\n\tprintln(\"c\")\n"},{"count":1,"added":false,"removed":false,"value":"}\n"}]`
	if lines != wantLines {
		t.Fatalf("DiffLines:\n got %s\nwant %s", lines, wantLines)
	}
	words := mustStringify(t, jsdiff.DiffWords("\tprintln(\"a\")", "\tprintln(\"bb\")", nil))
	wantWords := `[{"count":3,"added":false,"removed":false,"value":"\tprintln(\""},{"count":1,"added":false,"removed":true,"value":"a"},{"count":1,"added":true,"removed":false,"value":"bb"},{"count":2,"added":false,"removed":false,"value":"\")"}]`
	if words != wantWords {
		t.Fatalf("DiffWords:\n got %s\nwant %s", words, wantWords)
	}
}

func TestJsdiff_ChangeJSONKeyOrder(t *testing.T) {
	c := jsdiff.Change{Value: "x<", Removed: true, Count: 1}
	want := `{"count":1,"added":false,"removed":true,"value":"x<"}`
	if got := mustStringify(t, c); got != want {
		t.Fatalf("jsonx: got %s want %s", got, want)
	}
	b, err := json.Marshal(jsdiff.Change{Value: "x", Added: true, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); got != `{"count":2,"added":true,"removed":false,"value":"x"}` {
		t.Fatalf("encoding/json: got %s", got)
	}
	ac := jsdiff.ArrayChange[int]{Added: true, Count: 0}
	if got := mustStringify(t, ac); got != `{"count":0,"added":true,"removed":false,"value":[]}` {
		t.Fatalf("ArrayChange: got %s", got)
	}
	dmp := jsdiff.ConvertChangesToDMP([]jsdiff.Change{{Value: "a", Added: true}, {Value: "b", Removed: true}, {Value: " "}})
	if got := mustStringify(t, dmp); got != "[[1,\"a\"],[-1,\"b\"],[0,\" \"]]" {
		t.Fatalf("DMP: got %s", got)
	}
}

func TestJsdiff_NilOnAbort(t *testing.T) {
	zero := 0
	if got := jsdiff.DiffLines("a\n", "b\n", &jsdiff.Options{MaxEditLength: &zero}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	// Identical inputs finish before the edit-length loop, like TS.
	if got := jsdiff.DiffLines("a\n", "a\n", &jsdiff.Options{MaxEditLength: &zero}); got == nil || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	if got := jsdiff.DiffLines("", "", nil); got == nil || len(got) != 0 {
		t.Fatalf("empty diff: got %#v, want empty non-nil", got)
	}
	if got := jsdiff.CreateTwoFilesPatch("a", "a", "x\n", "y\n", nil, nil, &jsdiff.PatchOptions{Options: jsdiff.Options{MaxEditLength: &zero}}); got != "" {
		t.Fatalf("patch: got %q", got)
	}
	// A timeout that has already expired aborts a diff that needs at least one edit.
	var big strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&big, "line %d\n", i)
	}
	expired := int64(-1)
	if got := jsdiff.DiffLines(big.String(), "other\n"+big.String()+"x\n", &jsdiff.Options{Timeout: &expired}); got != nil {
		t.Fatalf("expired timeout: got %d changes", len(got))
	}
}

func TestJsdiff_SegmenterGranularity(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || fmt.Sprint(r) != `The segmenter passed must have a granularity of "word"` {
			t.Fatalf("recover() = %v", r)
		}
	}()
	jsdiff.DiffWords("a", "b", &jsdiff.Options{IntlSegmenter: graphemeSegmenter{}})
}

type graphemeSegmenter struct{}

func (graphemeSegmenter) Granularity() string       { return "grapheme" }
func (graphemeSegmenter) Segment(s string) []string { return strings.Split(s, "") }

func TestJsdiff_NewlineIsTokenPatchPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || fmt.Sprint(r) != "newlineIsToken may not be used with patch-generation functions, only with diffing functions" {
			t.Fatalf("recover() = %v", r)
		}
	}()
	jsdiff.NewStructuredPatch("a", "a", "x\n", "y\n", nil, nil, &jsdiff.PatchOptions{Options: jsdiff.Options{NewlineIsToken: true}})
}

// TS formatPatch decrements the start of empty ranges in place.
func TestJsdiff_FormatPatchMutatesLikeTS(t *testing.T) {
	p := jsdiff.NewStructuredPatch("a", "a", "", "y\n", nil, nil, nil)
	if got := jsdiff.FormatPatch(p, &jsdiff.OmitHeaders); got != "@@ -0,0 +1,1 @@\n+y\n" {
		t.Fatalf("first: %q", got)
	}
	if got := jsdiff.FormatPatch(p, &jsdiff.OmitHeaders); got != "@@ --1,0 +1,1 @@\n+y\n" {
		t.Fatalf("second: %q", got)
	}
}

func TestJsdiff_ApplyPatchListErrors(t *testing.T) {
	if _, _, err := jsdiff.ApplyPatchList("x", nil, nil); err == nil || err.Error() != "Cannot read properties of undefined (reading 'hunks')" {
		t.Fatalf("empty list: %v", err)
	}
	p := jsdiff.NewStructuredPatch("a", "a", "x\n", "y\n", nil, nil, nil)
	if _, _, err := jsdiff.ApplyPatchList("x", []*jsdiff.StructuredPatch{p, p}, nil); err == nil || err.Error() != "applyPatch only works with a single input." {
		t.Fatalf("two patches: %v", err)
	}
	got, ok, err := jsdiff.ApplyPatchStructured("x\n", p, nil)
	if err != nil || !ok || got != "y\n" {
		t.Fatalf("structured: %q %v %v", got, ok, err)
	}
	// The patch is not modified by line-ending conversion.
	got, ok, err = jsdiff.ApplyPatchStructured("x\r\n", p, nil)
	if err != nil || !ok || got != "y\r\n" || p.Hunks[0].Lines[0] != "-x" {
		t.Fatalf("crlf: %q %v %v %q", got, ok, err, p.Hunks[0].Lines)
	}
}

func TestJsdiff_ApplyPatches(t *testing.T) {
	uniDiff := "Index: a\n--- a\n+++ a\n@@ -1 +1 @@\n-1\n+2\nIndex: b\n--- b\n+++ b\n@@ -1 +1 @@\n-3\n+4\n"
	files := map[string]string{"a": "1\n", "b": "x\n"}
	var log []string
	var completeErr error
	completed := 0
	err := jsdiff.ApplyPatches(uniDiff, &jsdiff.ApplyPatchesOptions{
		LoadFile: func(index *jsdiff.StructuredPatch, callback func(err error, data string)) {
			log = append(log, "load "+index.OldFileName)
			callback(nil, files[index.OldFileName])
		},
		Patched: func(index *jsdiff.StructuredPatch, content string, ok bool, callback func(err error)) {
			log = append(log, fmt.Sprintf("patched %s %q %v", index.NewFileName, content, ok))
			callback(nil)
		},
		Complete: func(err error) {
			completed++
			completeErr = err
		},
	})
	if err != nil || completeErr != nil || completed != 1 {
		t.Fatalf("err=%v complete=%v completed=%d", err, completeErr, completed)
	}
	want := []string{"load a", `patched a "2\n" true`, "load b", `patched b "" false`}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Fatalf("log %q, want %q", log, want)
	}

	// An error from LoadFile ends the run.
	loadErr := errors.New("boom")
	completed = 0
	err = jsdiff.ApplyPatches(uniDiff, &jsdiff.ApplyPatchesOptions{
		LoadFile: func(_ *jsdiff.StructuredPatch, callback func(err error, data string)) { callback(loadErr, "") },
		Patched: func(*jsdiff.StructuredPatch, string, bool, func(error)) {
			t.Fatal("patched called")
		},
		Complete: func(err error) {
			completed++
			completeErr = err
		},
	})
	if err != nil || completeErr != loadErr || completed != 1 {
		t.Fatalf("err=%v complete=%v completed=%d", err, completeErr, completed)
	}

	// A malformed patch fails before any callback.
	err = jsdiff.ApplyPatches("@@ -1 +1 @@\n-a\n+b\ngarbage\n", &jsdiff.ApplyPatchesOptions{})
	if err == nil || err.Error() != `Unknown line 4 "garbage"` {
		t.Fatalf("parse error: %v", err)
	}
}

func TestJsdiff_CanonicalizeCircular(t *testing.T) {
	circ := jsonx.ObjectOf("b", 1.0)
	circ.Set("self", circ)
	got, err := jsdiff.Canonicalize(circ, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj := got.(*jsonx.Object)
	self, _ := obj.Get("self")
	if obj == circ || self != obj || strings.Join(obj.Keys(), ",") != "b,self" {
		t.Fatalf("canonical %v keys %v", obj, obj.Keys())
	}
	_, err = jsdiff.DiffJson(circ, jsonx.NewObject(), nil)
	want := "Converting circular structure to JSON\n    --> starting at object with constructor 'Object'\n    --- property 'self' closes the circle"
	if err == nil || err.Error() != want {
		t.Fatalf("DiffJson err %q", err)
	}
}

func TestJsdiff_DiffJsonTypedValues(t *testing.T) {
	type item struct {
		Z int    `json:"z"`
		A string `json:"a"`
	}
	got, err := jsdiff.DiffJson(item{Z: 1, A: "x"}, map[string]any{"a": "x", "z": 2.0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"count":2,"added":false,"removed":false,"value":"{\n  \"a\": \"x\",\n"},{"count":1,"added":false,"removed":true,"value":"  \"z\": 1\n"},{"count":1,"added":true,"removed":false,"value":"  \"z\": 2\n"},{"count":1,"added":false,"removed":false,"value":"}"}]`
	if s := mustStringify(t, got); s != want {
		t.Fatalf("got %s\nwant %s", s, want)
	}
}

func TestJsdiff_CustomDiff(t *testing.T) {
	// A Diff with every hook left nil is the base class: a character diff.
	d := &jsdiff.Diff{}
	got := mustStringify(t, d.Diff("ab", "ac", nil))
	want := `[{"count":1,"added":false,"removed":false,"value":"a"},{"count":1,"added":false,"removed":true,"value":"b"},{"count":1,"added":true,"removed":false,"value":"c"}]`
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestJsdiff_DiffArraysComparator(t *testing.T) {
	got := jsdiff.DiffArrays([]int{1, 2, 3}, []int{11, 12, 4}, &jsdiff.ArrayOptions[int]{
		Comparator: func(l, r int) bool { return l%10 == r%10 },
	})
	want := `[{"count":2,"added":false,"removed":false,"value":[11,12]},{"count":1,"added":false,"removed":true,"value":[3]},{"count":1,"added":true,"removed":false,"value":[4]}]`
	if s := mustStringify(t, got); s != want {
		t.Fatalf("got %s", s)
	}
}
