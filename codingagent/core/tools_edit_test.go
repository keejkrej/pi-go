package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestEditTool_ExecutionAndPrepareArguments(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tool := CreateEditTool(dir)
	def := CreateEditToolDefinition(dir)

	t.Run("replaces single text block and includes diff details", func(t *testing.T) {
		p := filepath.Join(dir, "single.txt")
		if err := os.WriteFile(p, []byte("line 1\nline 2\nline 3\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := tool.Execute(ctx, "e1", jsonx.ObjectOf(
			"path", "single.txt",
			"edits", []any{jsonx.ObjectOf("oldText", "line 2", "newText", "LINE TWO")},
		), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "Successfully replaced 1 block(s) in single.txt." {
			t.Fatalf("got %q", got)
		}
		data, _ := os.ReadFile(p)
		if string(data) != "line 1\nLINE TWO\nline 3\n" {
			t.Fatalf("content=%q", string(data))
		}
		details, ok := res.Details.(*EditToolDetails)
		if !ok || details == nil || !strings.Contains(details.Diff, "LINE TWO") || !strings.Contains(details.Patch, "-line 2") {
			t.Fatalf("unexpected details: %#v", res.Details)
		}
	})

	t.Run("replaces multiple disjoint edits atomically without partial failure", func(t *testing.T) {
		p := filepath.Join(dir, "multi.txt")
		orig := "first\nmiddle\nsecond\n"
		if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(ctx, "e2", jsonx.ObjectOf(
			"path", "multi.txt",
			"edits", []any{
				jsonx.ObjectOf("oldText", "first", "newText", "FIRST"),
				jsonx.ObjectOf("oldText", "missing", "newText", "X"),
			},
		), nil); err == nil {
			t.Fatalf("expected failure when one edit fails")
		}
		afterFail, _ := os.ReadFile(p)
		if string(afterFail) != orig {
			t.Fatalf("expected atomic failure, got %q", string(afterFail))
		}

		res, err := tool.Execute(ctx, "e3", jsonx.ObjectOf(
			"path", "multi.txt",
			"edits", []any{
				jsonx.ObjectOf("oldText", "first", "newText", "FIRST"),
				jsonx.ObjectOf("oldText", "second", "newText", "SECOND"),
			},
		), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "Successfully replaced 2 block(s) in multi.txt." {
			t.Fatalf("got %q", got)
		}
		afterOk, _ := os.ReadFile(p)
		if string(afterOk) != "FIRST\nmiddle\nSECOND\n" {
			t.Fatalf("got %q", string(afterOk))
		}
	})

	t.Run("fails on missing file with ENOENT, duplicate occurrence, or empty edits", func(t *testing.T) {
		if _, err := tool.Execute(ctx, "e4", jsonx.ObjectOf(
			"path", "missing.txt",
			"edits", []any{jsonx.ObjectOf("oldText", "a", "newText", "b")},
		), nil); err == nil || !strings.Contains(err.Error(), "ENOENT") {
			t.Fatalf("expected ENOENT error, got %v", err)
		}

		p := filepath.Join(dir, "dup.txt")
		if err := os.WriteFile(p, []byte("dup\ndup\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(ctx, "e5", jsonx.ObjectOf(
			"path", "dup.txt",
			"edits", []any{jsonx.ObjectOf("oldText", "dup", "newText", "x")},
		), nil); err == nil || !strings.Contains(err.Error(), "2 occurrences") {
			t.Fatalf("expected duplicate error, got %v", err)
		}

		if _, err := tool.Execute(ctx, "e6", jsonx.ObjectOf(
			"path", "dup.txt",
			"edits", []any{},
		), nil); err == nil {
			t.Fatalf("expected empty edits error")
		}
	})

	t.Run("preserves UTF-8 BOM and CRLF endings", func(t *testing.T) {
		p := filepath.Join(dir, "crlf.txt")
		if err := os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte("one\r\ntwo\r\nthree\r\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Execute(ctx, "e7", jsonx.ObjectOf(
			"path", "crlf.txt",
			"edits", []any{jsonx.ObjectOf("oldText", "two", "newText", "TWO\nEXTRA")},
		), nil); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(raw), "\uFEFF") {
			t.Fatalf("expected UTF-8 BOM preserved")
		}
		want := "\uFEFFone\r\nTWO\r\nEXTRA\r\nthree\r\n"
		if string(raw) != want {
			t.Fatalf("got %q, want %q", string(raw), want)
		}
	})

	t.Run("PrepareArguments folds legacy oldText/newText and parses stringified edits while keeping schema clean", func(t *testing.T) {
		if def.RenderShell != ToolRenderShellSelf {
			t.Fatalf("expected ToolRenderShellSelf, got %q", def.RenderShell)
		}
		if propsRaw, ok := def.Parameters.Get("properties"); ok {
			if props, ok := propsRaw.(*jsonx.Object); ok && props != nil {
				if _, has := props.Get("oldText"); has {
					t.Fatalf("legacy oldText must not appear in public schema")
				}
				if _, has := props.Get("newText"); has {
					t.Fatalf("legacy newText must not appear in public schema")
				}
			}
		}

		// Legacy fold + execute
		legacy := def.PrepareArguments(jsonx.ObjectOf(
			"path", "single.txt",
			"oldText", "LINE TWO",
			"newText", "line two",
		))
		if _, err := tool.Execute(ctx, "e8", legacy, nil); err != nil {
			t.Fatalf("prepared legacy args execute failed: %v", err)
		}

		// JSON stringified edits
		strEdits := def.PrepareArguments(jsonx.ObjectOf(
			"path", "single.txt",
			"edits", `[{"oldText":"line two","newText":"reverted"}]`,
		))
		obj, ok := strEdits.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object, got %T", strEdits)
		}
		arr, has := obj.GetArray("edits")
		if !has || len(arr) != 1 {
			t.Fatalf("expected parsed edits array, got %#v", arr)
		}

		// Non-object pass-through
		if got := def.PrepareArguments("garbage"); got != "garbage" {
			t.Fatalf("got %#v, want garbage", got)
		}
		if got := def.PrepareArguments(nil); got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})
}
