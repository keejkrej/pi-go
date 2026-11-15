package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestWriteTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	tool := CreateWriteTool(dir)

	t.Run("writes file contents and overwrites existing files", func(t *testing.T) {
		res, err := tool.Execute(ctx, "w1", jsonx.ObjectOf("path", "test.txt", "content", "hello world"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ttrTestTextOutput(res); got != "Successfully wrote to test.txt" {
			t.Fatalf("got %q", got)
		}
		data, err := os.ReadFile(filepath.Join(dir, "test.txt"))
		if err != nil || string(data) != "hello world" {
			t.Fatalf("file=(%q, %v)", string(data), err)
		}

		if _, err := tool.Execute(ctx, "w2", jsonx.ObjectOf("path", "test.txt", "content", "updated"), nil); err != nil {
			t.Fatal(err)
		}
		data, _ = os.ReadFile(filepath.Join(dir, "test.txt"))
		if string(data) != "updated" {
			t.Fatalf("got %q, want updated", string(data))
		}
	})

	t.Run("creates nested parent directories automatically", func(t *testing.T) {
		if _, err := tool.Execute(ctx, "w3", jsonx.ObjectOf("path", "a/b/c/deep.txt", "content", "nested"), nil); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "a", "b", "c", "deep.txt"))
		if err != nil || string(data) != "nested" {
			t.Fatalf("file=(%q, %v)", string(data), err)
		}
	})

	t.Run("uses ctx.Cwd when executing via ToolDefinition", func(t *testing.T) {
		altDir := t.TempDir()
		def := CreateWriteToolDefinition(dir)
		if _, err := def.Execute(ctx, "w4", jsonx.ObjectOf("path", "rel.txt", "content", "via ctx"), nil, &ttrMockToolContext{cwd: altDir}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "rel.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected file not created in constructor cwd")
		}
		data, err := os.ReadFile(filepath.Join(altDir, "rel.txt"))
		if err != nil || string(data) != "via ctx" {
			t.Fatalf("expected file in ctx.Cwd, got (%q, %v)", string(data), err)
		}
	})

	t.Run("respects cancelled context", func(t *testing.T) {
		abortCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := tool.Execute(abortCtx, "w5", jsonx.ObjectOf("path", "abort.txt", "content", "x"), nil); err == nil {
			t.Fatalf("expected abort error")
		}
	})
}
