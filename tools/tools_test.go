package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// runTool validates raw args against the tool's schema and executes it.
func runTool(t *testing.T, tool ag.AgentTool, args map[string]any) (ag.ToolResult, error) {
	t.Helper()
	tc := &ag.ToolCall{ID: "test-call", Name: tool.Name(), Arguments: args}
	params, err := ag.ValidateToolArguments(tool, tc)
	if err != nil {
		t.Fatalf("ValidateToolArguments(%s) failed: %v", tool.Name(), err)
	}
	return tool.Execute(context.Background(), tc.ID, params, nil)
}

// resultText extracts the joined text content of a ToolResult.
func resultText(t *testing.T, r ag.ToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*ag.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	read := NewReadTool(dir)

	const content = "line one\nline two\nline three\n"
	if _, err := runTool(t, write, map[string]any{"path": "sub/file.txt", "content": content}); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Parent directory should have been created and file written.
	if _, err := os.Stat(filepath.Join(dir, "sub", "file.txt")); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	res, err := runTool(t, read, map[string]any{"path": "sub/file.txt"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	got := resultText(t, res)
	want := strings.TrimRight(content, "\n")
	if got != want {
		t.Fatalf("read round-trip mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestReadOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	read := NewReadTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "f.txt", "content": "a\nb\nc\nd\ne\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	res, err := runTool(t, read, map[string]any{"path": "f.txt", "offset": float64(2), "limit": float64(2)})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got := resultText(t, res); !strings.Contains(got, "b\nc") {
		t.Fatalf("offset/limit window wrong: %q", got)
	}
}

func TestReadNotFound(t *testing.T) {
	dir := t.TempDir()
	read := NewReadTool(dir)
	if _, err := runTool(t, read, map[string]any{"path": "nope.txt"}); err == nil {
		t.Fatal("expected error reading missing file, got nil")
	}
}

func TestEditUniqueMatch(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	edit := NewEditTool(dir)
	read := NewReadTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "e.txt", "content": "hello world\ngoodbye world\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := runTool(t, edit, map[string]any{"path": "e.txt", "old_string": "hello", "new_string": "hi"}); err != nil {
		t.Fatalf("edit failed: %v", err)
	}
	res, err := runTool(t, read, map[string]any{"path": "e.txt"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if got := resultText(t, res); !strings.Contains(got, "hi world") || strings.Contains(got, "hello") {
		t.Fatalf("edit did not apply: %q", got)
	}
}

func TestEditAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	edit := NewEditTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "a.txt", "content": "dup\ndup\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := runTool(t, edit, map[string]any{"path": "a.txt", "old_string": "dup", "new_string": "x"}); err == nil {
		t.Fatal("expected ambiguous-match error, got nil")
	}
}

func TestEditNotFound(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	edit := NewEditTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "n.txt", "content": "abc\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := runTool(t, edit, map[string]any{"path": "n.txt", "old_string": "zzz", "new_string": "x"}); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestEditReplaceAll(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	edit := NewEditTool(dir)
	read := NewReadTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "r.txt", "content": "x x x\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := runTool(t, edit, map[string]any{"path": "r.txt", "old_string": "x", "new_string": "y", "replace_all": true}); err != nil {
		t.Fatalf("replace_all edit failed: %v", err)
	}
	res, _ := runTool(t, read, map[string]any{"path": "r.txt"})
	if got := resultText(t, res); strings.Contains(got, "x") {
		t.Fatalf("replace_all left occurrences: %q", got)
	}
}

func TestGrepFindsLine(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	grep := NewGrepTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "src/main.go", "content": "package main\nfunc target() {}\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	res, err := runTool(t, grep, map[string]any{"pattern": "func target", "include": "*.go"})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	got := resultText(t, res)
	if !strings.Contains(got, "func target") || !strings.Contains(got, "main.go") {
		t.Fatalf("grep did not find line: %q", got)
	}
	if !strings.Contains(got, ":2:") {
		t.Fatalf("grep missing line number: %q", got)
	}
}

func TestGrepIgnoresHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	grep := NewGrepTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": ".git/config", "content": "needle\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	res, err := runTool(t, grep, map[string]any{"pattern": "needle"})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if got := resultText(t, res); strings.Contains(got, "needle") {
		t.Fatalf("grep should have skipped .git: %q", got)
	}
}

func TestFindMatchesFilename(t *testing.T) {
	dir := t.TempDir()
	write := NewWriteTool(dir)
	find := NewFindTool(dir)

	if _, err := runTool(t, write, map[string]any{"path": "pkg/widget.go", "content": "x\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := runTool(t, write, map[string]any{"path": "pkg/notes.txt", "content": "x\n"}); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	res, err := runTool(t, find, map[string]any{"pattern": "*.go"})
	if err != nil {
		t.Fatalf("find failed: %v", err)
	}
	got := resultText(t, res)
	if !strings.Contains(got, "widget.go") {
		t.Fatalf("find did not match filename: %q", got)
	}
	if strings.Contains(got, "notes.txt") {
		t.Fatalf("find matched non-glob file: %q", got)
	}
}

func TestCtxCancellation(t *testing.T) {
	dir := t.TempDir()
	read := NewReadTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tc := &ag.ToolCall{ID: "c", Name: "read", Arguments: map[string]any{"path": "x"}}
	params, err := ag.ValidateToolArguments(read, tc)
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	if _, err := read.Execute(ctx, tc.ID, params, nil); err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
}

// Compile-time check that all constructors yield AgentTool values with the
// expected execution modes.
func TestExecutionModes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		tool ag.AgentTool
		want ag.ExecutionMode
	}{
		{NewReadTool(dir), ag.ExecutionParallel},
		{NewGrepTool(dir), ag.ExecutionParallel},
		{NewFindTool(dir), ag.ExecutionParallel},
		{NewWriteTool(dir), ag.ExecutionSequential},
		{NewEditTool(dir), ag.ExecutionSequential},
	}
	for _, c := range cases {
		if got := c.tool.ExecutionMode(); got != c.want {
			t.Errorf("%s: ExecutionMode = %q, want %q", c.tool.Name(), got, c.want)
		}
	}
}
