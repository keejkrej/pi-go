package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type tehFakeRenderedComponent struct {
	lines []string
}

func (c *tehFakeRenderedComponent) Render(_ int) []string { return c.lines }
func (c *tehFakeRenderedComponent) Invalidate()           {}

func TestCreateToolHtmlRendererAndExportSessionToHtml(t *testing.T) {
	renderer := CreateToolHtmlRenderer(ToolHtmlRendererDeps{
		GetToolRenderers: func(name string) *ToolRenderers {
			if name != "my_tool" {
				return nil
			}
			return &ToolRenderers{
				RenderCall: func(_ *jsonx.Object, _ *theme.Theme, _ ToolRenderContext) tui.Component {
					return &tehFakeRenderedComponent{lines: []string{"\x1b[1mcall my_tool\x1b[0m"}}
				},
				RenderResult: func(_ agent.AgentToolResult, opts ToolRenderResultOptions, _ *theme.Theme, _ ToolRenderContext) tui.Component {
					if opts.Expanded {
						return &tehFakeRenderedComponent{lines: []string{"", "expanded line", ""}}
					}
					return &tehFakeRenderedComponent{lines: []string{"collapsed line"}}
				},
			}
		},
		Cwd: "/tmp",
	})

	callHtml := renderer.RenderCall("tc1", "my_tool", map[string]any{"x": 1})
	if callHtml == nil || !strings.Contains(*callHtml, "call my_tool") {
		t.Fatalf("unexpected RenderCall HTML: %v", callHtml)
	}
	resHtml := renderer.RenderResult("tc1", "my_tool", ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}, nil, false)
	if resHtml == nil || resHtml.Collapsed == nil || !strings.Contains(*resHtml.Collapsed, "collapsed line") || resHtml.Expanded == nil || !strings.Contains(*resHtml.Expanded, "expanded line") || strings.Contains(*resHtml.Expanded, "&nbsp;") {
		t.Fatalf("unexpected RenderResult HTML: %+v", resHtml)
	}

	tmp := t.TempDir()
	sm, err := SessionManagerCreate(tmp, tmp)
	if err != nil {
		t.Fatalf("SessionManagerCreate failed: %v", err)
	}
	args := jsonx.NewObject()
	args.Set("x", 1)
	if _, err := sm.AppendMessage(&ai.AssistantMessage{
		Role:    "assistant",
		Content: ai.ContentList{&ai.ToolCall{Type: "toolCall", Id: "tc1", Name: "my_tool", Arguments: args}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendMessage(&ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "tc1",
		ToolName:   "my_tool",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "done"}},
	}); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(tmp, "out.html")
	written, err := ExportSessionToHtml(sm, &agent.AgentState{SystemPrompt: "Be helpful"}, &ExportOptions{
		OutputPath:   &outPath,
		ToolRenderer: renderer,
	})
	if err != nil {
		t.Fatalf("ExportSessionToHtml failed: %v", err)
	}
	if written != outPath {
		t.Fatalf("expected outputPath %q, got %q", outPath, written)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "{{SESSION_DATA}}") || !strings.Contains(string(data), "<html") {
		t.Fatalf("exported HTML appears unpopulated:\n%s", string(data)[:min(len(data), 400)])
	}

	// Test ExportFromFile as well.
	fromFileOut := filepath.Join(tmp, "fromfile.html")
	gotFromFile, err := ExportFromFile(*sm.GetSessionFile(), &ExportOptions{OutputPath: &fromFileOut})
	if err != nil {
		t.Fatalf("ExportFromFile failed: %v", err)
	}
	if gotFromFile != fromFileOut {
		t.Fatalf("expected %q, got %q", fromFileOut, gotFromFile)
	}
}
