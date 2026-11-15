package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

func twMustHex(t *testing.T, hex string) ui.Color {
	t.Helper()
	col, ok := gui.ParseHexColor(hex)
	if !ok {
		t.Fatalf("ParseHexColor(%q) failed", hex)
	}
	return col
}

func TestWakuThemePaletteNoLegacyPurple(t *testing.T) {
	prev := theme.CurrentTheme()
	defer ttaRestoreTheme(t, prev)

	legacyPurple1 := twMustHex(t, "#26233a")
	legacyPurple2 := twMustHex(t, "#1f1d2e")

	darkCanvas := twMustHex(t, "#1a1a1a")
	darkSidebar := twMustHex(t, "#181818")
	darkRaised := twMustHex(t, "#232323")
	darkComposer := twMustHex(t, "#212121")
	darkInset := twMustHex(t, "#151515")
	darkAccent := twMustHex(t, "#e2795b")

	lightCanvas := twMustHex(t, "#f6f5f6")
	lightSidebar := twMustHex(t, "#f3f3f3")
	lightRaised := twMustHex(t, "#ececec")
	lightComposer := twMustHex(t, "#ffffff")
	lightAccent := twMustHex(t, "#c85f44")

	darkTh, err := theme.GetThemeByName("dark")
	if err != nil || darkTh == nil {
		t.Fatalf("GetThemeByName(dark) failed: %v", err)
	}
	lightTh, err := theme.GetThemeByName("light")
	if err != nil || lightTh == nil {
		t.Fatalf("GetThemeByName(light) failed: %v", err)
	}

	cases := []struct {
		name      string
		th        *theme.Theme
		wantDark  bool
		bg        ui.Color
		sidebarBg ui.Color
		raised    ui.Color
		composer  ui.Color
		accent    ui.Color
	}{
		{"dark", darkTh, true, darkCanvas, darkSidebar, darkRaised, darkComposer, darkAccent},
		{"empty-dark", &theme.Theme{Name: "", Appearance: tui.TerminalColorSchemeDark}, true, darkCanvas, darkSidebar, darkRaised, darkComposer, darkAccent},
		{"system-dark", &theme.Theme{Name: "system", Appearance: tui.TerminalColorSchemeDark}, true, darkCanvas, darkSidebar, darkRaised, darkComposer, darkAccent},
		{"light", lightTh, false, lightCanvas, lightSidebar, lightRaised, lightComposer, lightAccent},
		{"system-light", &theme.Theme{Name: "system", Appearance: tui.TerminalColorSchemeLight}, false, lightCanvas, lightSidebar, lightRaised, lightComposer, lightAccent},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildGuiTheme(tc.th)
			if got == nil {
				t.Fatal("BuildGuiTheme returned nil")
			}
			if got.Dark != tc.wantDark {
				t.Fatalf("Dark = %v, want %v", got.Dark, tc.wantDark)
			}
			if got.Background == legacyPurple1 || got.Background == legacyPurple2 ||
				got.UserMessageBg == legacyPurple1 || got.UserMessageBg == legacyPurple2 ||
				got.SidebarBg == legacyPurple1 || got.SidebarBg == legacyPurple2 {
				t.Fatalf("legacy TUI purple leaked into %s theme: bg=%v userBg=%v sidebar=%v",
					tc.name, got.Background, got.UserMessageBg, got.SidebarBg)
			}
			if got.Background != tc.bg {
				t.Fatalf("Background = %+v, want %+v", got.Background, tc.bg)
			}
			if got.SidebarBg != tc.sidebarBg {
				t.Fatalf("SidebarBg = %+v, want %+v", got.SidebarBg, tc.sidebarBg)
			}
			if got.Raised != tc.raised {
				t.Fatalf("Raised = %+v, want %+v", got.Raised, tc.raised)
			}
			if got.ComposerBg != tc.composer {
				t.Fatalf("ComposerBg = %+v, want %+v", got.ComposerBg, tc.composer)
			}
			if got.Accent != tc.accent {
				t.Fatalf("Accent = %+v, want %+v", got.Accent, tc.accent)
			}
			if tc.wantDark && got.Inset != darkInset {
				t.Fatalf("dark Inset = %+v, want %+v", got.Inset, darkInset)
			}
		})
	}
}

func TestWakuThreePaneShellAndToggles(t *testing.T) {
	fx := tgmNewFixture(t, nil)
	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow: true,
	})
	t.Cleanup(func() { mode.Stop() })

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.Frame()

	// Empty state and Waku sidebar landmarks
	for _, want := range []string{
		"New Task",
		"Search",
		"Collapse Sidebar",
		"Toggle Right Panel",
		"Ask anything, @ to mention files, / for commands",
		"Review changes",
		"Explain codebase",
	} {
		if !tt.HasText(want) {
			t.Fatalf("expected initial Waku shell text %q, texts=%v", want, tt.Texts())
		}
	}

	// Collapse left sidebar -> Toggle Sidebar appears in center header
	if err := tt.Click("Collapse Sidebar"); err != nil {
		t.Fatalf("Click Collapse Sidebar: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Toggle Sidebar") {
		t.Fatalf("expected Toggle Sidebar button in header after collapse, texts=%v", tt.Texts())
	}
	if tt.HasText("Collapse Sidebar") {
		t.Fatalf("expected sidebar collapsed, texts=%v", tt.Texts())
	}

	// Re-open left sidebar
	if err := tt.Click("Toggle Sidebar"); err != nil {
		t.Fatalf("Click Toggle Sidebar: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Collapse Sidebar") {
		t.Fatalf("expected sidebar reopened with Collapse Sidebar button, texts=%v", tt.Texts())
	}

	// Open right panel -> Changes tab active, switch to Tree tab
	if err := tt.Click("Toggle Right Panel"); err != nil {
		t.Fatalf("Click Toggle Right Panel: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Changes") || !tt.HasText("Tree") || !tt.HasText("Working tree clean") {
		t.Fatalf("expected right panel Changes view, texts=%v", tt.Texts())
	}

	if err := tt.Click("Tree"); err != nil {
		t.Fatalf("Click Tree tab: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Open Full Tree") {
		t.Fatalf("expected Tree tab contents in right panel, texts=%v", tt.Texts())
	}

	// Toggle appearance quick switcher in sidebar footer
	wasDark := mode.currentGuiTheme().Dark
	if err := tt.Click("Toggle Appearance"); err != nil {
		t.Fatalf("Click Toggle Appearance: %v", err)
	}
	tt.Frame()
	if mode.currentGuiTheme().Dark == wasDark {
		t.Fatalf("expected Toggle Appearance to flip Dark from %v", wasDark)
	}
}

func TestWakuWorkedForClusterFoldAndComposerControls(t *testing.T) {
	sampleTool := &agent.AgentTool{
		Name:        "waku_probe",
		Label:       "Waku Probe",
		Description: "Inspects sample item",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "probe output body"}},
			}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools: []*agent.AgentTool{sampleTool},
	})

	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("checking files carefully"),
				providers.FauxToolCall("waku_probe", jsonx.NewObject(), nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("final waku answer"),
			}, nil),
		},
	})

	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow: true,
	})
	t.Cleanup(func() { mode.Stop() })

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.Frame()

	tt.Type("inspect project")
	tt.Key(0, ui.KeyEnter)

	tgmWaitUntil(t, mode, tt, 5*time.Second, "Worked cluster and final prose", func() bool {
		return !mode.Session().IsStreaming() &&
			tt.HasText("final waku answer") &&
			tt.HasText("waku_probe") &&
			tt.HasText("Worked")
	})

	var workedLbl string
	for _, txt := range tt.Texts() {
		if strings.HasPrefix(txt, "Worked") {
			workedLbl = txt
			break
		}
	}
	if workedLbl == "" {
		t.Fatalf("expected Worked header text in %v", tt.Texts())
	}

	// Fold "Worked ..." cluster: hides tool activity card while preserving final prose
	if err := tt.Click(workedLbl); err != nil {
		t.Fatalf("Click %q: %v", workedLbl, err)
	}
	tt.Frame()
	if tt.HasText("waku_probe") {
		t.Fatalf("expected waku_probe hidden when turn cluster is folded, texts=%v", tt.Texts())
	}
	if !tt.HasText("final waku answer") {
		t.Fatalf("expected final prose to remain visible when folded, texts=%v", tt.Texts())
	}

	// Click again to expand cluster
	if err := tt.Click(workedLbl); err != nil {
		t.Fatalf("Click %q to re-expand: %v", workedLbl, err)
	}
	tt.Frame()
	if !tt.HasText("waku_probe") {
		t.Fatalf("expected waku_probe visible again after re-expanding, texts=%v", tt.Texts())
	}

	// Open Context Window popover via footer gauge
	if err := tt.Click("Context Window Usage"); err != nil {
		t.Fatalf("Click Context Window Usage: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Context Window") || !tt.HasText("Compact Now") {
		t.Fatalf("expected Context Window popover visible, texts=%v", tt.Texts())
	}
	// Close popover by clicking gauge again
	_ = tt.Click("Context Window Usage")
	tt.Frame()

	// Open Cmd+K Command Palette
	mode.ShowCommandPalette()
	tt.Frame()
	if !mode.GUI().HasOverlay() ||
		!tt.HasText("Command Palette") ||
		!tt.HasText("Quick actions & slash commands") ||
		!tt.HasText("New Task") ||
		!tt.HasText("/model") {
		t.Fatalf("expected Command Palette items visible, texts=%v", tt.Texts())
	}
}
