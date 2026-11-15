package gui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func ttvObj(kvs ...any) *jsonx.Object {
	return jsonx.ObjectOf(kvs...)
}

func ttvIntPtr(n int) *int       { return &n }
func ttvInt64Ptr(n int64) *int64 { return &n }

func TestToolExecutionCardReadWriteAndExpand(t *testing.T) {
	// read tool starts pending, completes with 20 lines and truncation metadata
	args := ttvObj("path", "/tmp/project/main.go", "offset", 1, "limit", 20)
	card := NewToolExecutionCard("tc-read", "read", args, WithToolCwd("/tmp/project"))
	if !card.IsPending() {
		t.Fatal("expected initial pending state")
	}

	tt := ui.NewTester(func(c *ui.Context) {
		card.View(c)
	}, 760, 420)
	if !tt.HasText("read") || !tt.HasText("main.go:1-20") {
		t.Fatalf("expected pending read header, got %v", tt.Texts())
	}

	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("package main // line %02d", i))
	}
	card.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: strings.Join(lines, "\n")}},
		Details: &core.ReadToolDetails{
			Truncation: &core.TruncationResult{
				Truncated:   true,
				OutputLines: 20,
				TotalLines:  100,
				OutputBytes: 400,
				TotalBytes:  2000,
			},
		},
	}, false, ttvInt64Ptr(45))

	tt.Frame()
	if !tt.HasText("45ms") || !tt.HasText("truncated") || !tt.HasText("more lines") {
		t.Fatalf("expected completed read badges and preview truncation, got %v", tt.Texts())
	}
	tt.Click("Expand")
	tt.Frame()
	if !card.IsExpanded() {
		t.Fatal("expected Expand click to expand card")
	}

	// write tool preview
	writeCard := NewToolExecutionCard("tc-write", "write", ttvObj("path", "src/foo.go", "content", "package foo\nfunc Bar() {}\n"))
	writeCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "Successfully wrote 26 bytes"}},
	}, false, ttvInt64Ptr(12))
	ttWrite := ui.NewTester(func(c *ui.Context) {
		writeCard.View(c)
	}, 640, 260)
	if !ttWrite.HasText("write") || !ttWrite.HasText("src/foo.go") || !ttWrite.HasText("package foo") {
		t.Fatalf("expected write header and content, got %v", ttWrite.Texts())
	}
}

func TestToolExecutionCardEditDiffToggle(t *testing.T) {
	patch := "--- a/app.go\n+++ b/app.go\n@@ -1,3 +1,3 @@\n package main\n-var v = 1\n+var v = 2\n"
	editArgs := ttvObj("path", "app.go", "edits", []map[string]any{
		{"oldText": "var v = 1", "newText": "var v = 2"},
	})
	editCard := NewToolExecutionCard("tc-edit", "edit", editArgs, WithToolDiffSideBySide(false))
	editCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "Applied edit"}},
		Details: &core.EditToolDetails{
			Patch:            patch,
			Diff:             "- var v = 1\n+ var v = 2",
			FirstChangedLine: ttvIntPtr(2),
		},
	}, false, ttvInt64Ptr(88))

	tt := ui.NewTester(func(c *ui.Context) {
		editCard.View(c)
	}, 820, 460)
	if !tt.HasText("edit") || !tt.HasText("app.go:L2") || !tt.HasText("Side-by-side") {
		t.Fatalf("expected edit card header and layout toggle, got %v", tt.Texts())
	}
	tt.Click("Side-by-side")
	tt.Frame()
	if !editCard.DiffSideBySide() || !tt.HasText("Inline") {
		t.Fatalf("expected diff layout switched to side-by-side, texts=%v", tt.Texts())
	}
}

func TestToolExecutionCardBashGrepFindLsGenericAndError(t *testing.T) {
	// bash error + ANSI stripping + full log path
	bashCard := NewToolExecutionCard("tc-bash", "bash", ttvObj("command", "go test ./..."))
	bashCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "\x1b[31mFAIL pkg\x1b[0m\n"}},
		Details: &core.BashToolDetails{
			FullOutputPath: tmsgStrPtr("/tmp/full.log"),
			Truncation:     &core.TruncationResult{Truncated: true, OutputLines: 1, TotalLines: 50},
		},
	}, true, ttvInt64Ptr(320))
	ttBash := ui.NewTester(func(c *ui.Context) {
		bashCard.View(c)
	}, 720, 300)
	if !bashCard.IsError() || !ttBash.HasText("FAIL pkg") {
		t.Fatalf("expected error state and stripped ANSI output, got %v", ttBash.Texts())
	}
	if strings.Contains(strings.Join(ttBash.Texts(), ""), "\x1b[31m") {
		t.Fatal("expected ANSI escapes stripped from bash output")
	}

	// grep, find, ls, generic fallback
	grepCard := NewToolExecutionCard("tc-grep", "grep", ttvObj("pattern", "TODO", "path", "codingagent"))
	grepCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "a.go:10: // TODO fix"}},
		Details: &core.GrepToolDetails{MatchLimitReached: ttvIntPtr(100)},
	}, false, nil)

	findCard := NewToolExecutionCard("tc-find", "find", ttvObj("pattern", "*.go"))
	findCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "main.go\nutil.go"}},
	}, false, nil)

	lsCard := NewToolExecutionCard("tc-ls", "ls", ttvObj())
	lsCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "codingagent/\ngui/"}},
	}, false, nil)

	genericCard := NewToolExecutionCard(
		"tc-custom",
		"mcp_lookup",
		ttvObj("query", "widget"),
		WithToolExpanded(true),
	)
	genericCard.Complete(agent.AgentToolResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "found 3 results"}},
		Details: map[string]any{"count": 3},
	}, false, nil)

	ttAll := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			grepCard.View(c)
			findCard.View(c)
			lsCard.View(c)
			genericCard.View(c)
		})
	}, 760, 760)
	allText := strings.Join(ttAll.Texts(), "\n")
	for _, want := range []string{
		"/TODO/ in codingagent", "limit 100",
		"main.go",
		"codingagent/",
		"mcp_lookup", "found 3 results", `"count": 3`,
	} {
		if !strings.Contains(allText, want) {
			t.Fatalf("missing %q in rendered tool cards:\n%s", want, allText)
		}
	}
}
