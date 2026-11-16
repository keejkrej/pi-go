// Ported from packages/coding-agent/test/codemode-renderer.test.ts (pi v1.5.2).

package codemodeext_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type tcrDummyTUI struct{ tui.TUI }

func (*tcrDummyTUI) RequestRender(*bool) {}

func tcrStrPtr(s string) *string       { return &s }
func tcrFloat64Ptr(v float64) *float64 { return &v }
func tcrInt64Ptr(v int64) *int64       { return &v }

func tcrInitDarkTheme(t *testing.T) *theme.Theme {
	t.Helper()
	th, err := theme.InitTheme("dark", false)
	if err != nil {
		t.Fatalf("InitTheme(dark): %v", err)
	}
	return th
}

func tcrRenderResult(
	t *testing.T,
	th *theme.Theme,
	result agent.AgentToolResult,
	isError bool,
	expanded bool,
	width int,
) string {
	t.Helper()
	rctx := core.ToolRenderContext{
		KeybindingHints:  core.DefaultKeybindingHints(),
		Args:             jsonx.ObjectOf("code", ""),
		ToolCallId:       "call",
		Invalidate:       func() {},
		State:            map[string]any{},
		Cwd:              "/",
		ExecutionStarted: true,
		ArgsComplete:     true,
		IsPartial:        false,
		Expanded:         expanded,
		ShowImages:       false,
		IsError:          isError,
		OutputPad:        1,
	}
	comp := codemodeext.CodemodeRenderers.RenderResult(
		result,
		core.ToolRenderResultOptions{Expanded: expanded, IsPartial: false},
		th,
		rctx,
	)
	if comp == nil {
		t.Fatal("RenderResult returned nil component")
	}
	rawLines := comp.Render(width)
	stripped := tui.StripTerminalSequences(strings.Join(rawLines, "\n"))
	lines := strings.Split(stripped, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func TestCodemodeRenderer(t *testing.T) {
	th := tcrInitDarkTheme(t)

	t.Run("hides the script header and shows the output", func(t *testing.T) {
		text := tcrRenderResult(t, th, agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "Script completed\nWall time 0.1 seconds\nOutput:\n"},
				&ai.TextContent{Type: "text", Text: "hello"},
			},
			Details: codemodeext.CodemodeToolDetails{
				Calls: []codemodeext.CodemodeNestedCall{
					{
						Id:         "call/1",
						Name:       "read",
						Args:       `{"path":"a"}`,
						Status:     codemodeext.CodemodeNestedCallOk,
						DurationMs: tcrFloat64Ptr(5),
					},
				},
			},
		}, false, true, 200)
		want := "✓ read {\"path\":\"a\"} 5ms\n\nhello"
		if text != want {
			t.Fatalf("got:\n%s\nwant:\n%s", text, want)
		}
	})

	t.Run("shows the cost of model calls and their total", func(t *testing.T) {
		text := tcrRenderResult(t, th, agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "Script completed\nWall time 0.1 seconds\nOutput:\n"},
			},
			Details: codemodeext.CodemodeToolDetails{
				Calls: []codemodeext.CodemodeNestedCall{
					{
						Id:         "call/models.classify/1",
						Name:       "models.classify",
						Args:       "scorer/judge",
						Status:     codemodeext.CodemodeNestedCallOk,
						DurationMs: tcrFloat64Ptr(5),
						Cost:       tcrFloat64Ptr(0.000012936),
					},
					{
						Id:         "call/models.classify/2",
						Name:       "models.classify",
						Args:       "scorer/judge",
						Status:     codemodeext.CodemodeNestedCallOk,
						DurationMs: tcrFloat64Ptr(5),
						Cost:       tcrFloat64Ptr(0.02),
					},
					{
						Id:         "call/models.classify/3",
						Name:       "models.classify",
						Args:       "scorer/judge",
						Status:     codemodeext.CodemodeNestedCallOk,
						DurationMs: tcrFloat64Ptr(5),
					},
				},
			},
		}, false, true, 200)
		want := strings.Join([]string{
			"✓ models.classify scorer/judge 5ms $0.000013",
			"✓ models.classify scorer/judge 5ms $0.02",
			"✓ models.classify scorer/judge 5ms",
			"Model calls: $0.02",
		}, "\n")
		if text != want {
			t.Fatalf("got:\n%s\nwant:\n%s", text, want)
		}
	})

	t.Run("shows results without a header, such as rejected options", func(t *testing.T) {
		text := tcrRenderResult(t, th, agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "The @options line must be followed by JavaScript source"},
			},
			Details: nil,
		}, true, true, 200)
		want := "The @options line must be followed by JavaScript source"
		if text != want {
			t.Fatalf("got %q, want %q", text, want)
		}
	})

	t.Run("limits collapsed output to wrapped lines, not logical lines", func(t *testing.T) {
		text := tcrRenderResult(t, th, agent.AgentToolResult{
			Content: ai.ContentList{
				&ai.TextContent{Type: "text", Text: "Script completed\nWall time 0.1 seconds\nOutput:\n"},
				&ai.TextContent{Type: "text", Text: strings.Repeat("x", 1000)},
			},
			Details: codemodeext.CodemodeToolDetails{
				Calls:          []codemodeext.CodemodeNestedCall{},
				FullOutputPath: tcrStrPtr("/tmp/out.txt"),
			},
		}, false, false, 50)
		lines := strings.Split(text, "\n")
		if len(lines) != 7 {
			t.Fatalf("expected 7 lines, got %d:\n%s", len(lines), text)
		}
		line50 := strings.Repeat("x", 50)
		for i := 0; i < 5; i++ {
			if lines[i] != line50 {
				t.Fatalf("line %d = %q, want 50 x's", i, lines[i])
			}
		}
		if !regexp.MustCompile(`^\.\.\. \(15 more lines,`).MatchString(lines[5]) {
			t.Fatalf("line 5 = %q, want hidden count hint", lines[5])
		}
		if lines[6] != "Full output: /tmp/out.txt" {
			t.Fatalf("line 6 = %q, want %q", lines[6], "Full output: /tmp/out.txt")
		}
	})

	t.Run("shows the elapsed time while the script runs and the total once it finished", func(t *testing.T) {
		renderers := codemodeext.CodemodeRenderers
		comp := components.NewToolExecutionComponent(
			"codemode",
			"call",
			jsonx.ObjectOf("code", ""),
			nil,
			&renderers,
			&tcrDummyTUI{},
			"/",
		)
		defer comp.Dispose()

		rendered := func() string {
			return tui.StripTerminalSequences(strings.Join(comp.Render(120), "\n"))
		}

		comp.MarkExecutionStarted()
		comp.UpdateResult(components.ToolExecutionResult{
			Content: ai.ContentList{},
			Details: codemodeext.CodemodeToolDetails{Calls: []codemodeext.CodemodeNestedCall{}},
			IsError: false,
		}, true)
		running := rendered()
		if !strings.Contains(running, "Elapsed ") {
			t.Fatalf("expected running render to contain Elapsed, got:\n%s", running)
		}

		// Also verify exact 3.0s elapsed formatting through ToolTimerState.
		threeSecsAgo := time.Now().Add(-3 * time.Second)
		directComp := codemodeext.CodemodeRenderers.RenderResult(
			agent.AgentToolResult{
				Content: ai.ContentList{},
				Details: codemodeext.CodemodeToolDetails{Calls: []codemodeext.CodemodeNestedCall{}},
			},
			core.ToolRenderResultOptions{Expanded: true, IsPartial: true},
			th,
			core.ToolRenderContext{
				KeybindingHints:  core.DefaultKeybindingHints(),
				State:            &core.ToolTimerState{StartedAt: &threeSecsAgo},
				ExecutionStarted: true,
				IsPartial:        true,
			},
		)
		directText := tui.StripTerminalSequences(strings.Join(directComp.Render(120), "\n"))
		if !strings.Contains(directText, "Elapsed 3.0s") {
			t.Fatalf("expected direct partial render to contain Elapsed 3.0s, got:\n%s", directText)
		}

		comp.UpdateResult(components.ToolExecutionResult{
			Content:    ai.ContentList{},
			Details:    codemodeext.CodemodeToolDetails{Calls: []codemodeext.CodemodeNestedCall{}},
			IsError:    false,
			DurationMs: tcrInt64Ptr(5100),
		}, false)
		done := rendered()
		if !strings.Contains(done, "Took 5.1s") {
			t.Fatalf("expected finished render to contain Took 5.1s, got:\n%s", done)
		}
		if strings.Contains(done, "Elapsed") {
			t.Fatalf("expected finished render not to contain Elapsed, got:\n%s", done)
		}
	})
}
