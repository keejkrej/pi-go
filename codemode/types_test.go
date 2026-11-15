package codemode_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

func ttypesStrPtr(s string) *string { return &s }

func TestToCodemodeIdentifier(t *testing.T) {
	cases := map[string]string{
		"":                "_",
		"mcp__docs__find": "mcp__docs__find",
		"my-tool":         "my_tool",
		"123abc":          "_23abc",
		"$foo_1":          "$foo_1",
		"a.b/c:d":         "a_b_c_d",
	}
	for in, want := range cases {
		if got := codemode.ToCodemodeIdentifier(in); got != want {
			t.Fatalf("ToCodemodeIdentifier(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadQuickJSWasm(t *testing.T) {
	mod1, err := codemode.LoadQuickJSWasm(nil)
	if err != nil {
		t.Fatalf("LoadQuickJSWasm(nil): %v", err)
	}
	if mod1 == nil {
		t.Fatal("expected non-nil module")
	}
	empty := ""
	mod2, err := codemode.LoadQuickJSWasm(&empty)
	if err != nil {
		t.Fatalf("LoadQuickJSWasm(&empty): %v", err)
	}
	if mod1 != mod2 {
		t.Fatalf("expected cached embedded module instance")
	}

	missing := filepath.Join(t.TempDir(), "missing.wasm")
	if _, err := codemode.LoadQuickJSWasm(&missing); err == nil {
		t.Fatal("expected error for missing wasm file")
	}
	// Verify failed load is not cached and retries on next call (still fails cleanly).
	if _, err := codemode.LoadQuickJSWasm(&missing); err == nil {
		t.Fatal("expected retried missing wasm load to fail")
	}

	invalid := filepath.Join(t.TempDir(), "bad.wasm")
	if err := os.WriteFile(invalid, []byte("not wasm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codemode.LoadQuickJSWasm(&invalid); err == nil {
		t.Fatal("expected error for invalid wasm bytes")
	}
}

func TestCodemodeOutputItemAndResultRoundTrip(t *testing.T) {
	itemsJSON := `[{"type":"text","text":"hi"},{"type":"image","data":"AQID","mimeType":"image/png"},{"type":"audio","extra":1}]`
	var list codemode.CodemodeOutputItemList
	if err := jsonx.Unmarshal([]byte(itemsJSON), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	txt, ok := list[0].(*codemode.CodemodeTextOutput)
	if !ok || txt.Text != "hi" {
		t.Fatalf("list[0] = %#v", list[0])
	}
	img, ok := list[1].(*codemode.CodemodeImageOutput)
	if !ok || img.Data != "AQID" || img.MimeType != "image/png" {
		t.Fatalf("list[1] = %#v", list[1])
	}
	unk, ok := list[2].(*codemode.UnknownCodemodeOutputItem)
	if !ok || unk.Raw == nil {
		t.Fatalf("list[2] = %#v", list[2])
	}
	reMarshaled, err := jsonx.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if string(reMarshaled) != itemsJSON {
		t.Fatalf("round-trip = %s, want %s", reMarshaled, itemsJSON)
	}

	swSet := omap.NewMap[string, any]()
	swSet.Set("z", 1)
	swSet.Set("a", map[string]any{"nested": true})
	succ := &codemode.CodemodeSuccess{
		Ok:     true,
		Value:  jsonx.Null[any](),
		Output: codemode.CodemodeOutputItemList{txt},
		Calls:  []codemode.CodemodeCall{{Name: "echo", Status: codemode.CodemodeCallStatusOk, DurationMs: 5}},
		StoreWrites: codemode.CodemodeStoreWrites{
			Set:    swSet,
			Delete: []string{"old"},
		},
	}
	succBytes, err := jsonx.Marshal(succ)
	if err != nil {
		t.Fatal(err)
	}
	decodedSucc, err := codemode.UnmarshalCodemodeResult(succBytes)
	if err != nil {
		t.Fatal(err)
	}
	gotSucc, ok := decodedSucc.(*codemode.CodemodeSuccess)
	if !ok || !gotSucc.Ok || gotSucc.Value.IsZero() || !gotSucc.Value.IsNull() {
		t.Fatalf("gotSucc = %#v", decodedSucc)
	}
	if keys := gotSucc.StoreWrites.Set.Keys(); len(keys) != 2 || keys[0] != "z" || keys[1] != "a" {
		t.Fatalf("key order not preserved: %v", keys)
	}

	absentJSON := `{"ok":true,"output":[],"calls":[],"storeWrites":{"set":{},"delete":[]}}`
	decodedAbsent, err := codemode.UnmarshalCodemodeResult([]byte(absentJSON))
	if err != nil {
		t.Fatal(err)
	}
	if s := decodedAbsent.(*codemode.CodemodeSuccess); !s.Value.IsZero() {
		t.Fatalf("expected absent Value to have IsZero() == true, got %+v", s.Value)
	}

	failRes := &codemode.CodemodeFailure{
		Ok: false,
		Error: codemode.CodemodeError{
			Kind:    codemode.CodemodeErrorKindScript,
			Name:    ttypesStrPtr("RangeError"),
			Message: "boom",
		},
		Output: codemode.CodemodeOutputItemList{},
		Calls:  []codemode.CodemodeCall{},
	}
	failBytes, err := jsonx.Marshal(failRes)
	if err != nil {
		t.Fatal(err)
	}
	decodedFail, err := codemode.UnmarshalCodemodeResult(failBytes)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := decodedFail.(*codemode.CodemodeFailure); !ok || f.Error.Message != "boom" {
		t.Fatalf("gotFail = %#v", decodedFail)
	}

	decodedUnk, err := codemode.UnmarshalCodemodeResult([]byte(`{"weird":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := decodedUnk.(*codemode.UnknownCodemodeResult); !ok || u.Raw == nil {
		t.Fatalf("expected UnknownCodemodeResult, got %#v", decodedUnk)
	}
}
