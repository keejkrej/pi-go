package components

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type tteMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tteMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tteMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestToolExecutionGenericFallbackAndExpand(t *testing.T) {
	ui := &tteMockTUI{}
	args := jsonx.ObjectOf("Key", "value")
	comp := NewToolExecutionComponent("my_custom_tool", "call-1", args, nil, nil, ui, "/tmp")
	defer comp.Dispose()

	initialPlain := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(initialPlain, "my_custom_tool") {
		t.Fatalf("expected tool name in fallback call render, got %q", initialPlain)
	}

	var out strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&out, "row-%02d\n", i)
	}
	comp.UpdateResult(ToolExecutionResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: out.String()}},
	}, false)

	collapsed := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(collapsed, "row-10") || strings.Contains(collapsed, "row-15") {
		t.Fatalf("expected 10-line preview when collapsed, got %q", collapsed)
	}
	if !strings.Contains(collapsed, "more lines") {
		t.Fatalf("expected expand hint, got %q", collapsed)
	}

	// Click toggles expansion.
	res := comp.HandleMouse(tui.TuiMouseEvent{
		Type:   tui.TuiMouseEventTypeClick,
		Button: tui.TuiMouseButtonLeft,
		X:      3,
		Y:      2,
		Width:  80,
		Height: len(comp.Render(80)),
	})
	if res == nil || !res.Handled {
		t.Fatalf("expected click on fallback result region to toggle expansion")
	}
	expanded := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(expanded, "row-15") {
		t.Fatalf("expected row-15 after mouse expand, got %q", expanded)
	}

	comp.SetOutputPad(4)
	for i, l := range comp.Render(50) {
		if tui.VisibleWidth(l) > 50 {
			t.Fatalf("line %d exceeded width 50", i)
		}
	}
}

func TestToolExecutionCustomRendererSelfShellAndDeferredArgs(t *testing.T) {
	ui := &tteMockTUI{}
	callRenders := 0
	var seenLastComponent tui.Component

	def := ToolRenderersFromDefinition(&core.ToolDefinition{
		RenderShell: core.ToolRenderShellSelf,
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
			callRenders++
			seenLastComponent = rctx.LastComponent
			return tui.NewText("self-call:"+fmt.Sprint(*args), 0, 0, nil)
		},
		RenderResult: func(res agent.AgentToolResult, opts core.ToolRenderResultOptions, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
			return tui.NewText("self-res:"+fmt.Sprintf("expanded=%v", opts.Expanded), 0, 0, nil)
		},
	})

	comp := NewToolExecutionComponent("self_tool", "call-2", jsonx.ObjectOf("n", 1), nil, def, ui, "/tmp")
	defer comp.Dispose()

	if callRenders != 1 {
		t.Fatalf("expected 1 initial RenderCall, got %d", callRenders)
	}

	// UpdateArgs is deferred until Render.
	comp.UpdateArgs(jsonx.ObjectOf("n", 2))
	comp.UpdateArgs(jsonx.ObjectOf("n", 3))
	if callRenders != 1 {
		t.Fatalf("expected UpdateArgs to defer render until Render(), got %d calls", callRenders)
	}
	lines := comp.Render(60)
	if callRenders != 2 || seenLastComponent == nil {
		t.Fatalf("expected Render to flush pending args and supply LastComponent")
	}
	_ = lines

	comp.MarkExecutionStarted()
	comp.SetArgsComplete()
	comp.UpdateResult(ToolExecutionResult{
		Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
	}, false)

	// Y=0 (top spacer) ignored; Y=1 hits call region and toggles expanded.
	rendered := comp.Render(60)
	if comp.HandleMouse(tui.TuiMouseEvent{Type: tui.TuiMouseEventTypeClick, Button: tui.TuiMouseButtonLeft, Y: 0, Width: 60, Height: len(rendered)}) != nil {
		t.Fatalf("expected spacer click at Y=0 to be ignored")
	}
	if hit := comp.HandleMouse(tui.TuiMouseEvent{Type: tui.TuiMouseEventTypeClick, Button: tui.TuiMouseButtonLeft, Y: 1, Width: 60, Height: len(rendered)}); hit == nil || !hit.Handled {
		t.Fatalf("expected click at Y=1 on self shell to toggle expand")
	}
	if plain := utils.StripAnsi(strings.Join(comp.Render(60), "\n")); !strings.Contains(plain, "expanded=true") {
		t.Fatalf("expected expanded=true after self-shell click, got %q", plain)
	}

	// Built-in renderer lookup for bash works and Dispose stops any timer cleanly.
	bashComp := NewToolExecutionComponent("bash", "call-bash", jsonx.ObjectOf("command", "pwd"), nil, nil, ui, "/tmp")
	_ = bashComp.Render(80)
	bashComp.Dispose()
}
