package gui

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func wpRGBAEqual(got, want color.RGBA) bool {
	diff := func(a, b uint8) uint8 {
		if a > b {
			return a - b
		}
		return b - a
	}
	return diff(got.R, want.R) <= 1 &&
		diff(got.G, want.G) <= 1 &&
		diff(got.B, want.B) <= 1 &&
		diff(got.A, want.A) <= 1
}

func wpHexRGBA(t *testing.T, hex string) color.RGBA {
	t.Helper()
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		t.Fatalf("invalid hex color %q", hex)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		t.Fatalf("ParseUint(%q): %v", hex, err)
	}
	return color.RGBA{
		R: uint8((v >> 16) & 0xff),
		G: uint8((v >> 8) & 0xff),
		B: uint8(v & 0xff),
		A: 255,
	}
}

func wpPixelAt(t *testing.T, img *image.RGBA, x, y int) color.RGBA {
	t.Helper()
	if img == nil {
		t.Fatal("Tester.Image() returned nil")
	}
	b := img.Bounds()
	if !image.Pt(x, y).In(b) {
		t.Fatalf("point (%d,%d) outside image bounds %v", x, y, b)
	}
	return img.RGBAAt(x, y)
}

func TestWakuRustSourceSpecParity(t *testing.T) {
	// 1. Verify Go layout constants match canonical Waku measurements.
	wantFloats := []struct {
		name string
		got  float32
		want float32
	}{
		{"wakuSidebarDefaultWidth", wakuSidebarDefaultWidth, 252},
		{"wakuSidebarMinWidth", wakuSidebarMinWidth, 180},
		{"wakuSidebarMaxWidth", wakuSidebarMaxWidth, 420},
		{"wakuRightPanelDefaultWidth", wakuRightPanelDefaultWidth, 460},
		{"wakuRightPanelMinWidth", wakuRightPanelMinWidth, 280},
		{"wakuRightPanelMaxWidth", wakuRightPanelMaxWidth, 1000},
		{"wakuContentMaxWidth", wakuContentMaxWidth, 720},
		{"wakuTrafficLightClearance", wakuTrafficLightClearance, 86},
		{"wakuHeaderHeightPx", wakuHeaderHeightPx, 48},
		{"wakuSidebarActionRowHeight", wakuSidebarActionRowHeight, 32},
		{"wakuSidebarSearchBottomGap", wakuSidebarSearchBottomGap, 10},
		{"wakuSidebarGroupHeaderHeight", wakuSidebarGroupHeaderHeight, 28},
		{"wakuSidebarGroupChildPadding", wakuSidebarGroupChildPadding, 28},
		{"wakuSidebarSessionCardHeight", wakuSidebarSessionCardHeight, 51},
		{"wakuSidebarFooterHeight", wakuSidebarFooterHeight, 40},
		{"wakuWorkspaceFooterHeight", wakuWorkspaceFooterHeight, 28},
		{"wakuComposerRadius", wakuComposerRadius, 13},
		{"wakuUserBubbleMaxWidth", wakuUserBubbleMaxWidth, 540},
		{"wakuUserBubbleRadius", wakuUserBubbleRadius, 12},
	}
	for _, tc := range wantFloats {
		if tc.got != tc.want {
			t.Fatalf("%s = %.1f, want %.1f", tc.name, tc.got, tc.want)
		}
	}

	// 2. Verify Dark & Light theme palettes match Waku theme.rs tokens.
	dark := gui.DarkGuiTheme()
	light := gui.LightGuiTheme()
	colorChecks := []struct {
		name string
		got  ui.Color
		hex  string
	}{
		{"dark.Background", dark.Background, "#1A1A1A"},
		{"dark.Surface", dark.Surface, "#1A1A1A"},
		{"dark.SidebarBg", dark.SidebarBg, "#181818"},
		{"dark.Raised", dark.Raised, "#232323"},
		{"dark.ComposerBg", dark.ComposerBg, "#212121"},
		{"dark.Inset", dark.Inset, "#151515"},
		{"dark.Text", dark.Text, "#E2E2E2"},
		{"dark.TextSecondary", dark.TextSecondary, "#A3A3A3"},
		{"dark.TextTertiary", dark.TextTertiary, "#7D7D7D"},
		{"dark.TextGhost", dark.TextGhost, "#575757"},
		{"dark.Accent", dark.Accent, "#E2795B"},
		{"dark.Inverse", dark.Inverse, "#E7E9EC"},
		{"dark.OnInverse", dark.OnInverse, "#17181C"},
		{"dark.SidebarBorder", dark.SidebarBorder, "#292929"},
		{"dark.Gauge", dark.Gauge, "#3B82F6"},
		{"dark.Success", dark.Success, "#62C987"},
		{"dark.Warning", dark.Warning, "#E0B36A"},
		{"dark.Error", dark.Error, "#E2726A"},

		{"light.Background", light.Background, "#F6F5F6"},
		{"light.Surface", light.Surface, "#F6F5F6"},
		{"light.SidebarBg", light.SidebarBg, "#F3F3F3"},
		{"light.Raised", light.Raised, "#ECECEC"},
		{"light.ComposerBg", light.ComposerBg, "#FFFFFF"},
		{"light.Inset", light.Inset, "#E6E6E6"},
		{"light.Text", light.Text, "#242424"},
		{"light.TextSecondary", light.TextSecondary, "#666666"},
		{"light.TextTertiary", light.TextTertiary, "#858585"},
		{"light.TextGhost", light.TextGhost, "#A4A4A4"},
		{"light.Accent", light.Accent, "#C85F44"},
		{"light.Inverse", light.Inverse, "#202227"},
		{"light.OnInverse", light.OnInverse, "#F8F8F9"},
		{"light.Gauge", light.Gauge, "#2563EB"},
		{"light.Success", light.Success, "#2F8F52"},
		{"light.Warning", light.Warning, "#A66B20"},
		{"light.Error", light.Error, "#C64A42"},
	}
	for _, tc := range colorChecks {
		want := twMustHex(t, tc.hex)
		if tc.got != want {
			t.Fatalf("%s = %+v, want %s (%+v)", tc.name, tc.got, tc.hex, want)
		}
	}

	// 3. When ~/workspace/waku is present locally, verify live source-to-source parity.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	wakuRoot := filepath.Join(homeDir, "workspace", "waku")
	if _, err := os.Stat(wakuRoot); err != nil {
		return
	}

	mustRead := func(rel string) string {
		data, readErr := os.ReadFile(filepath.Join(wakuRoot, rel))
		if readErr != nil {
			t.Fatalf("ReadFile(%s): %v", rel, readErr)
		}
		return string(data)
	}

	appRS := mustRead("src/app.rs")
	sidebarRS := mustRead("src/app/sidebar.rs")
	rightPanelRS := mustRead("src/app/right_panel.rs")
	composerRS := mustRead("src/app/composer.rs")
	componentsRS := mustRead("src/app/components.rs")
	themeRS := mustRead("src/theme.rs")
	localesYML := mustRead("locales/app.yml")

	assertConst := func(src, name string, want float32) {
		re := regexp.MustCompile(`const\s+` + regexp.QuoteMeta(name) + `:\s*f32\s*=\s*([0-9]+(?:\.[0-9]+)?)\s*;`)
		m := re.FindStringSubmatch(src)
		if len(m) != 2 {
			t.Fatalf("missing constant %s in waku source", name)
		}
		v, convErr := strconv.ParseFloat(m[1], 32)
		if convErr != nil {
			t.Fatalf("parse constant %s=%q: %v", name, m[1], convErr)
		}
		if float32(v) != want {
			t.Fatalf("waku %s = %.1f, pi-go wants %.1f", name, float32(v), want)
		}
	}

	assertConst(appRS, "TRAFFIC_LIGHT_CLEARANCE", wakuTrafficLightClearance)
	assertConst(appRS, "CONTENT_MAX_WIDTH", wakuContentMaxWidth)
	assertConst(appRS, "SIDEBAR_MIN_WIDTH", wakuSidebarMinWidth)
	assertConst(appRS, "SIDEBAR_MAX_WIDTH", wakuSidebarMaxWidth)
	assertConst(appRS, "RIGHT_PANEL_MIN_WIDTH", wakuRightPanelMinWidth)
	assertConst(appRS, "RIGHT_PANEL_MAX_WIDTH", wakuRightPanelMaxWidth)
	assertConst(sidebarRS, "SIDEBAR_SESSION_CARD_HEIGHT", wakuSidebarSessionCardHeight)
	assertConst(sidebarRS, "SIDEBAR_ACTION_ROW_HEIGHT", wakuSidebarActionRowHeight)
	assertConst(sidebarRS, "SIDEBAR_SEARCH_BOTTOM_GAP", wakuSidebarSearchBottomGap)
	assertConst(sidebarRS, "SIDEBAR_GROUP_HEADER_HEIGHT", wakuSidebarGroupHeaderHeight)
	assertConst(sidebarRS, "SIDEBAR_GROUP_CHILD_PADDING", wakuSidebarGroupChildPadding)

	for _, pat := range []struct {
		label string
		src   string
		sub   string
	}{
		{"sidebar footer 40px", sidebarRS, ".h(px(40.0))"},
		{"sidebar header 48px", sidebarRS, ".h(px(48.0))"},
		{"right panel header 48px", rightPanelRS, ".h(px(48.0))"},
		{"composer workspace footer 28px", composerRS, ".h(px(28.0))"},
		{"composer radius 13px", composerRS, ".rounded(px(13.0))"},
		{"user bubble max_w 540px", componentsRS, ".max_w(px(540.0))"},
		{"user bubble radius 12px", componentsRS, ".rounded(px(12.0))"},
		{"dark sidebar hex", themeRS, "0x181818"},
		{"light canvas hex", themeRS, "0xF6F5F6"},
		{"light inset hex", themeRS, "0xE6E6E6"},
		{"en Do anything…", localesYML, "Do anything…"},
		{"en This Week", localesYML, "This Week"},
		{"en Sidebar options", localesYML, "Sidebar options"},
		{"en just now", localesYML, "just now"},
	} {
		if !strings.Contains(pat.src, pat.sub) {
			t.Fatalf("expected %s (%q) in waku source", pat.label, pat.sub)
		}
	}
}

func TestWakuChromeAndSidebarGeometryParity(t *testing.T) {
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	fx := tgmNewFixture(t, nil)
	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(context.Background())

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.Frame()

	mustFind := func(label string) ui.Rect {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("expected element %q in frame; texts=%v", label, tt.Texts())
		}
		return r
	}

	// Sidebar topbar 26x26 controls with x >= 86 in top 48px
	for _, lbl := range []string{"Toggle Sidebar", "Previous Session", "Next Session"} {
		r := mustFind(lbl)
		if r.W != 26 || r.H != 26 {
			t.Fatalf("%s size = %.0fx%.0f, want 26x26", lbl, r.W, r.H)
		}
		if r.X < wakuTrafficLightClearance {
			t.Fatalf("%s X = %.1f, want >= %.1f", lbl, r.X, wakuTrafficLightClearance)
		}
		if r.Y+r.H > wakuHeaderHeightPx {
			t.Fatalf("%s bottom = %.1f, want <= %.1f", lbl, r.Y+r.H, wakuHeaderHeightPx)
		}
	}

	// Sidebar primary action rows (32px height inside 252px sidebar)
	for _, lbl := range []string{"New Task", "Search"} {
		r := mustFind(lbl)
		if r.H != wakuSidebarActionRowHeight {
			t.Fatalf("%s height = %.1f, want %.1f", lbl, r.H, wakuSidebarActionRowHeight)
		}
		if r.X+r.W > wakuSidebarDefaultWidth {
			t.Fatalf("%s right edge = %.1f, want <= %.1f", lbl, r.X+r.W, wakuSidebarDefaultWidth)
		}
	}

	// Open Settings 26x26 in bottom-left 40px sidebar footer
	settingsR := mustFind("Open Settings")
	if settingsR.W != 26 || settingsR.H != 26 {
		t.Fatalf("Open Settings size = %.0fx%.0f, want 26x26", settingsR.W, settingsR.H)
	}
	if settingsR.Y < 820-wakuSidebarFooterHeight || settingsR.X > 60 {
		t.Fatalf("Open Settings rect = %+v, want bottom-left footer", settingsR)
	}

	// Toggle Right Panel 26x26 in center header top-right while panel closed
	rpClosed := mustFind("Toggle Right Panel")
	if rpClosed.W != 26 || rpClosed.H != 26 {
		t.Fatalf("Toggle Right Panel size = %.0fx%.0f, want 26x26", rpClosed.W, rpClosed.H)
	}
	if rpClosed.Y+rpClosed.H > wakuHeaderHeightPx || rpClosed.X < 1200 {
		t.Fatalf("Toggle Right Panel closed rect = %+v, want top-right center header", rpClosed)
	}

	// Collapse sidebar -> Toggle Sidebar moves to center header still clearing 86px traffic lights
	if err := tt.Click("Toggle Sidebar"); err != nil {
		t.Fatalf("Click Toggle Sidebar (collapse): %v", err)
	}
	tt.Frame()
	tsCollapsed := mustFind("Toggle Sidebar")
	if tsCollapsed.X < wakuTrafficLightClearance || tsCollapsed.Y+tsCollapsed.H > wakuHeaderHeightPx {
		t.Fatalf("collapsed Toggle Sidebar rect = %+v, want X >= 86 in top 48px", tsCollapsed)
	}
	if err := tt.Click("Toggle Sidebar"); err != nil {
		t.Fatalf("Click Toggle Sidebar (reopen): %v", err)
	}
	tt.Frame()

	// Open Right Panel -> width 460 and Toggle Right Panel moves into right panel header
	if err := tt.Click("Toggle Right Panel"); err != nil {
		t.Fatalf("Click Toggle Right Panel: %v", err)
	}
	tt.Frame()
	changesTab := mustFind("Changes")
	if changesTab.X < 1280-wakuRightPanelDefaultWidth {
		t.Fatalf("Changes tab X = %.1f, expected inside 460px right panel (X >= %v)", changesTab.X, 1280-wakuRightPanelDefaultWidth)
	}
	if err := tt.Click("Toggle Right Panel"); err != nil {
		t.Fatalf("Click Toggle Right Panel (close): %v", err)
	}
	tt.Frame()

	// Before dispatching a prompt, sidebar must NOT list an undispatched "New task" card,
	// while still showing left-aligned Today / project group header + Sidebar options filter button.
	todayEmptyR := mustFind("Today")
	if todayEmptyR.X > 24 {
		t.Fatalf("empty Today header X = %.1f, want left-aligned <= 24", todayEmptyR.X)
	}
	for _, info := range tt.AccessAll() {
		if info.Name == "New task" && info.Bounds.X < wakuSidebarDefaultWidth {
			t.Fatalf("unexpected undispatched 'New task' card inside sidebar: %+v", info.Bounds)
		}
	}

	// Sidebar options popover -> Group by Project -> still shows 'cwd' header without undispatched card.
	if err := tt.Click("Sidebar options"); err != nil {
		t.Fatalf("Click Sidebar options: %v", err)
	}
	tt.Frame()
	for _, opt := range []string{"Group by", "Updated", "Project", "Order by", "Newest", "Oldest"} {
		if !tt.HasText(opt) {
			t.Fatalf("expected Sidebar options item %q, texts=%v", opt, tt.Texts())
		}
	}
	if err := tt.Click("Project"); err != nil {
		t.Fatalf("Click Project option: %v", err)
	}
	tt.Frame()
	if _, ok := tt.Find("cwd"); !ok {
		t.Fatalf("expected empty project header 'cwd', texts=%v", tt.Texts())
	}
	for _, info := range tt.AccessAll() {
		if info.Name == "New task" && info.Bounds.X < wakuSidebarDefaultWidth {
			t.Fatalf("unexpected undispatched 'New task' card under Project grouping: %+v", info.Bounds)
		}
	}

	// Switch back to Updated grouping
	if err := tt.Click("Sidebar options"); err != nil {
		t.Fatalf("Click Sidebar options (2): %v", err)
	}
	tt.Frame()
	if err := tt.Click("Updated"); err != nil {
		t.Fatalf("Click Updated option: %v", err)
	}
	tt.Frame()

	// Send one prompt/assistant turn, confirm started task appears in sidebar, and verify indentation under Project.
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("waku parity response"),
			}, nil),
		},
	})
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Frame()
	tt.Type("hello parity")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "assistant reply visible", func() bool {
		return !mode.Session().IsStreaming() && tt.HasText("waku parity response")
	})

	var hasSidebarCard bool
	tgmWaitUntil(t, mode, tt, 5*time.Second, "started task promoted into sidebar", func() bool {
		for _, info := range tt.AccessAll() {
			if info.Name == "hello parity" && info.Bounds.X < wakuSidebarDefaultWidth {
				hasSidebarCard = true
				return true
			}
		}
		return false
	})
	if !hasSidebarCard {
		t.Fatal("expected dispatched 'hello parity' session card in sidebar")
	}

	// Group by Project -> check started session card indentation under 'cwd' header.
	if err := tt.Click("Sidebar options"); err != nil {
		t.Fatalf("Click Sidebar options (3): %v", err)
	}
	tt.Frame()
	if err := tt.Click("Project"); err != nil {
		t.Fatalf("Click Project option (2): %v", err)
	}
	tt.Frame()
	hdrR, ok := tt.Find("cwd")
	if !ok {
		t.Fatalf("expected project header 'cwd', texts=%v", tt.Texts())
	}
	var cardR ui.Rect
	foundCard := false
	for _, info := range tt.AccessAll() {
		if info.Name == "hello parity" && info.Bounds.X < wakuSidebarDefaultWidth {
			cardR = info.Bounds
			foundCard = true
			break
		}
	}
	if !foundCard {
		t.Fatalf("expected session card 'hello parity' under project header, texts=%v", tt.Texts())
	}
	if cardR.X < hdrR.X+wakuSidebarGroupChildPadding-1 {
		t.Fatalf("project child card X = %.1f should be indented %.0fpx right of header X = %.1f", cardR.X, wakuSidebarGroupChildPadding, hdrR.X)
	}
	if err := tt.Click("Sidebar options"); err != nil {
		t.Fatalf("Click Sidebar options (4): %v", err)
	}
	tt.Frame()
	if err := tt.Click("Updated"); err != nil {
		t.Fatalf("Click Updated option (2): %v", err)
	}
	tt.Frame()
	todayFullR := mustFind("Today")
	if todayFullR.X > 24 {
		t.Fatalf("populated Today header X = %.1f, want left-aligned <= 24", todayFullR.X)
	}

	for _, clutter := range []string{"⌘K", "Sessions", " msgs", "You", "↑", "↓"} {
		if tt.HasText(clutter) {
			t.Fatalf("found non-Waku clutter %q after turn, texts=%v", clutter, tt.Texts())
		}
	}
	if _, ok := tt.Find("Copy message"); !ok {
		t.Fatalf("expected Waku icon-only Copy message button on user/assistant turn, texts=%v", tt.Texts())
	}
}

func TestWakuFramebufferPixelParity(t *testing.T) {
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	fx := tgmNewFixture(t, nil)
	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(context.Background())

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	attR, ok := tt.Find("Attach clipboard image")
	if !ok {
		t.Fatalf("expected Attach clipboard image button in composer, texts=%v", tt.Texts())
	}
	composerProbeX := int(attR.X + attR.W + 40)
	composerProbeY := int(attR.Y + attR.H/2)

	assertModePixels := func(label, sidebarHex, canvasHex, composerHex string) {
		t.Helper()
		img := tt.Image()
		wantSidebar := wpHexRGBA(t, sidebarHex)
		wantCanvas := wpHexRGBA(t, canvasHex)
		wantComposer := wpHexRGBA(t, composerHex)

		if got := wpPixelAt(t, img, 120, 790); !wpRGBAEqual(got, wantSidebar) {
			t.Fatalf("%s sidebar pixel (120,790) = %+v, want %s (%+v)", label, got, sidebarHex, wantSidebar)
		}
		// No 1px border line above 40px sidebar footer (y=780)
		for _, y := range []int{779, 780, 781} {
			if got := wpPixelAt(t, img, 120, y); !wpRGBAEqual(got, wantSidebar) {
				t.Fatalf("%s expected borderless sidebar footer at (120,%d): got %+v, want %s", label, y, got, sidebarHex)
			}
		}

		if got := wpPixelAt(t, img, 640, 200); !wpRGBAEqual(got, wantCanvas) {
			t.Fatalf("%s canvas pixel (640,200) = %+v, want %s (%+v)", label, got, canvasHex, wantCanvas)
		}
		// No 1px border line under 48px center header (y=48)
		for _, y := range []int{47, 48, 49} {
			if got := wpPixelAt(t, img, 640, y); !wpRGBAEqual(got, wantCanvas) {
				t.Fatalf("%s expected borderless center header at (640,%d): got %+v, want %s", label, y, got, canvasHex)
			}
		}

		if got := wpPixelAt(t, img, composerProbeX, composerProbeY); !wpRGBAEqual(got, wantComposer) {
			t.Fatalf("%s composer pixel (%d,%d) = %+v, want %s (%+v)", label, composerProbeX, composerProbeY, got, composerHex, wantComposer)
		}
	}

	assertModePixels("dark", "#181818", "#1A1A1A", "#212121")

	mode.toggleQuickThemeDarkLight()
	tt.SetDark(false)
	tt.Frame()

	assertModePixels("light", "#F3F3F3", "#F6F5F6", "#FFFFFF")
}

func TestWakuSidebarMultiProjectSwitchInvariance(t *testing.T) {
	ctx := context.Background()
	tempRoot := t.TempDir()
	agentDir := filepath.Join(tempRoot, "agent")
	sessionsRoot := filepath.Join(agentDir, "sessions")
	for _, d := range []string{agentDir, sessionsRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(utils.EnvSessionDir, sessionsRoot)

	projAlpha := filepath.Join(tempRoot, "work", "projAlpha")
	projBeta := filepath.Join(tempRoot, "work", "projBeta")
	projGamma := filepath.Join(tempRoot, "work", "projGamma")
	for _, d := range []string{projAlpha, projBeta, projGamma} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	now := time.Now()
	seedSession := func(cwd, customDir, firstPrompt string, age time.Duration) string {
		dir := customDir
		if dir == "" {
			dir = core.GetDefaultSessionDir(cwd)
		}
		sm, err := core.SessionManagerCreate(cwd, dir)
		if err != nil {
			t.Fatalf("SessionManagerCreate(%s): %v", cwd, err)
		}
		ts := now.Add(-age).UnixMilli()
		if _, err := sm.AppendMessage(&ai.UserMessage{
			Role:      "user",
			Content:   ai.UserMessageContent{Text: tgmStrPtr(firstPrompt)},
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("AppendMessage(user): %v", err)
		}
		if _, err := sm.AppendMessage(&ai.AssistantMessage{
			Role:      "assistant",
			Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: "ack " + firstPrompt}},
			Timestamp: ts,
		}); err != nil {
			t.Fatalf("AppendMessage(assistant): %v", err)
		}
		filePtr := sm.GetSessionFile()
		if filePtr == nil || *filePtr == "" {
			t.Fatalf("expected non-empty session file for %s", cwd)
		}
		file := *filePtr
		mt := now.Add(-age)
		if err := os.Chtimes(file, mt, mt); err != nil {
			t.Fatalf("Chtimes(%s): %v", file, err)
		}
		return file
	}

	var alphaActive string
	for i := 0; i < 5; i++ {
		p := seedSession(projAlpha, "", "alpha-task-"+strconv.Itoa(i), time.Duration(30+i*15)*time.Minute)
		if i == 0 {
			alphaActive = p
		}
	}
	var beta2h string
	for i := 0; i < 4; i++ {
		age := time.Duration(i+1) * time.Hour
		if i == 1 {
			age = 2*time.Hour + 5*time.Minute
		}
		p := seedSession(projBeta, "", "beta-task-"+strconv.Itoa(i), age)
		if i == 1 {
			beta2h = p
		}
	}
	for i := 0; i < 3; i++ {
		seedSession(projGamma, "", "gamma-task-"+strconv.Itoa(i), time.Duration(24+i*12)*time.Hour)
	}

	smInit, err := core.SessionManagerOpen(alphaActive)
	if err != nil {
		t.Fatalf("SessionManagerOpen(alphaActive): %v", err)
	}
	authStorage := core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	rt, err := core.CreateAgentSessionRuntime(ctx, func(c context.Context, args core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		services, svcErr := core.CreateAgentSessionServices(c, &core.CreateAgentSessionServicesOptions{
			Cwd:         args.Cwd,
			AgentDir:    args.AgentDir,
			AuthStorage: authStorage,
			ResourceLoaderOptions: &core.DefaultResourceLoaderOptions{
				NoExtensions:      true,
				NoSkills:          true,
				NoPromptTemplates: true,
				NoThemes:          true,
			},
		})
		if svcErr != nil {
			return nil, svcErr
		}
		res, sessErr := core.CreateAgentSessionFromServices(c, core.CreateAgentSessionFromServicesOptions{
			Services:       services,
			SessionManager: args.SessionManager,
		})
		if sessErr != nil {
			return nil, sessErr
		}
		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *res,
			Services:                 services,
			Diagnostics:              services.Diagnostics,
		}, nil
	}, core.CreateAgentSessionRuntimeOptions{
		Cwd:            projAlpha,
		AgentDir:       agentDir,
		SessionManager: smInit,
	})
	if err != nil {
		t.Fatalf("CreateAgentSessionRuntime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Dispose(context.Background()) })

	mode := NewGuiMode(rt, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	listed1 := mode.refreshSidebarSessionsSync(ctx)
	tt.Frame()
	if len(listed1) != 12 {
		t.Fatalf("initial session count = %d, want 12", len(listed1))
	}
	for _, grpName := range []string{"Today", "Yesterday"} {
		if grpR, ok := tt.Find(grpName); ok && grpR.X > 24 {
			t.Fatalf("sidebar date header %q X = %.1f, want left-aligned <= 24", grpName, grpR.X)
		}
	}

	// Switch to 2h-old projBeta session -> count must stay 12 and Modified age must stay "2h".
	res, err := rt.SwitchSession(ctx, beta2h)
	if err != nil || (res != nil && res.Cancelled) {
		t.Fatalf("SwitchSession(beta2h): err=%v res=%+v", err, res)
	}
	mode.handleSessionReplaced()
	listed2 := mode.refreshSidebarSessionsSync(ctx)
	tt.Frame()
	if len(listed2) != 12 {
		t.Fatalf("post-switch session count = %d, want 12 (sidebar must not collapse to single project)", len(listed2))
	}

	seenProj := map[string]int{}
	var switchedInfo *core.SessionInfo
	for i := range listed2 {
		seenProj[filepath.Base(listed2[i].Cwd)]++
		if wakuResolvePath(listed2[i].Path) == wakuResolvePath(beta2h) {
			cp := listed2[i]
			switchedInfo = &cp
		}
	}
	if seenProj["projAlpha"] != 5 || seenProj["projBeta"] != 4 || seenProj["projGamma"] != 3 {
		t.Fatalf("unexpected project counts after switch: %+v", seenProj)
	}
	if switchedInfo == nil {
		t.Fatal("switched beta2h session missing from listed2")
	}
	if age := wakuFormatShortAge(switchedInfo.Modified); age != "2h" {
		t.Fatalf("switched beta2h Modified age = %q, want %q (must not bump to just now)", age, "2h")
	}
	if !tt.HasText("projAlpha") || !tt.HasText("projBeta") || !tt.HasText("projGamma") {
		t.Fatalf("expected all 3 project names visible in sidebar after switch, texts=%v", tt.Texts())
	}

	// External --session-dir outside utils.GetSessionsDir() merges cleanly without dropping the 12 default sessions.
	extDir := filepath.Join(tempRoot, "external-sessions")
	extPath := seedSession(projAlpha, extDir, "external-task", 5*time.Hour)
	resExt, err := rt.SwitchSession(ctx, extPath)
	if err != nil || (resExt != nil && resExt.Cancelled) {
		t.Fatalf("SwitchSession(extPath): err=%v res=%+v", err, resExt)
	}
	mode.handleSessionReplaced()
	listed3 := mode.refreshSidebarSessionsSync(ctx)
	tt.Frame()
	if len(listed3) != 13 {
		t.Fatalf("external --session-dir session count = %d, want 13", len(listed3))
	}
}

func TestWakuTerminalAndActivityWidthClampedTo720(t *testing.T) {
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	unbrokenPolicy := `{"policy":"` + strings.Repeat("x", 4000) + `"}`
	bashTool := &agent.AgentTool{
		Name:        "bash",
		Label:       "bash",
		Description: "Runs a shell command",
		Parameters:  typebox.Object(nil),
		Execute: func(ctx context.Context, toolCallId string, params any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
			out := "drwx------@ 53 jack staff 1696 Nov 15 22:48 Ambient\nAmbient.app\n"
			if obj, ok := params.(*jsonx.Object); ok {
				if cmd := tvArgStr(obj, "command"); strings.Contains(cmd, "long-policy") {
					out = "header-before\n" + unbrokenPolicy + "\nfooter-after\n"
				}
			}
			return agent.AgentToolResult{
				Content: ai.ContentList{&ai.TextContent{Type: "text", Text: out}},
			}, nil
		},
	}

	fx := tgmNewFixture(t, &tgmFixtureOpts{
		tools:        []*agent.AgentTool{bashTool},
		defaultTools: []string{"read"},
	})

	cmd1Args := jsonx.NewObject()
	cmd1Args.Set("command", "ls -la ~/Library/Application\\ Support/Ambient/ "+strings.Repeat("a", 280))
	cmd2Args := jsonx.NewObject()
	cmd2Args.Set("command", "cat long-policy.json "+strings.Repeat("b", 280))

	stopTool := ai.StopReasonToolUse
	fx.faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking(strings.Repeat("T", 3500)),
				providers.FauxToolCall("bash", cmd1Args, nil),
				providers.FauxToolCall("bash", cmd2Args, nil),
			}, &providers.FauxAssistantMessageOptions{
				StopReason: &stopTool,
			}),
		},
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("terminal overflow regression check complete"),
			}, nil),
		},
	})

	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(context.Background())

	tt := ui.NewTester(mode.GUI().View, 1440, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	// Execute a !!/bash command with a 4,000-char unbroken output line to exercise BashExecutionCard.
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor: %v", err)
	}
	tt.Frame()
	tt.Type("!!printf '%04000d' 0")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "user bash card finished", func() bool {
		return !mode.Session().IsBashRunning() && tt.HasText("exit 0")
	})

	// Run the multi-tool turn containing 3.5KB unbroken Thinking and 4KB unbroken bash JSON output.
	if err := tt.Click("Prompt editor"); err != nil {
		t.Fatalf("Click Prompt editor (2): %v", err)
	}
	tt.Frame()
	tt.Type("inspect long outputs")
	tt.Key(0, ui.KeyEnter)
	tgmWaitUntil(t, mode, tt, 5*time.Second, "multi-tool turn complete", func() bool {
		return !mode.Session().IsStreaming() &&
			tt.HasText("terminal overflow regression check complete") &&
			tt.HasText("bash") &&
			tt.HasText("header-before")
	})

	// Expand the 3,500-char thinking block too so its unconstrained line would try to stretch layout.
	if tt.HasText("Thinking") {
		_ = tt.Click("Thinking")
		tt.Frame()
	}

	// Centered 720px column inside [257..1440] spans [488.5..1208.5].
	// Verify every "Expand", "Copy", and "Copy message" button stays within the 720px column.
	const colMaxRight float32 = 1212
	var checkedButtons int
	var bashActionRects []ui.Rect
	for _, info := range tt.AccessAll() {
		if info.Name == "Expand" || info.Name == "Copy" || info.Name == "Copy message" {
			checkedButtons++
			rightEdge := info.Bounds.X + info.Bounds.W
			if rightEdge > colMaxRight || info.Bounds.X < 480 {
				t.Fatalf("button %q bounds %+v overflowed 720px transcript column [488..1209]", info.Name, info.Bounds)
			}
			if info.Name == "Expand" || info.Name == "Copy" {
				bashActionRects = append(bashActionRects, info.Bounds)
			}
		}
	}
	if checkedButtons < 3 {
		t.Fatalf("expected at least 3 action buttons (Expand/Copy/Copy message) in transcript, got %d; texts=%v", checkedButtons, tt.Texts())
	}

	// Verify framebuffer pixels in the right gutter outside the 720px column remain pure canvas (#1A1A1A).
	img := tt.Image()
	wantCanvas := wpHexRGBA(t, "#1A1A1A")
	probeYs := []int{120, 180, 240, 300, 360, 420, 480}
	for _, r := range bashActionRects {
		py := int(r.Y + r.H/2)
		if py >= 60 && py < 620 {
			probeYs = append(probeYs, py)
		}
	}
	for _, px := range []int{1230, 1300, 1400} {
		for _, py := range probeYs {
			got := wpPixelAt(t, img, px, py)
			if !wpRGBAEqual(got, wantCanvas) {
				t.Fatalf("right gutter pixel (%d,%d) = %+v, want #1A1A1A (%+v) — card leaked past 720px column", px, py, got, wantCanvas)
			}
		}
	}

	// Verify no left vertical turn-body rail: left margin outside the true 720px column [488.5..1208.5]
	// stays pure #1A1A1A and tool cards start flush at the 720px column edge (bash title < 530px).
	for _, px := range []int{470, 476, 482, 485} {
		for _, py := range []int{140, 200, 260, 320, 380, 440} {
			got := wpPixelAt(t, img, px, py)
			if !wpRGBAEqual(got, wantCanvas) {
				t.Fatalf("left margin pixel (%d,%d) = %+v, want #1A1A1A (%+v) — unexpected left vertical line", px, py, got, wantCanvas)
			}
		}
	}
	if bashTitleR, ok := tt.Find("bash"); ok {
		if bashTitleR.X > 530 {
			t.Fatalf("bash tool title X = %.1f, expected flush in 720px column without left rail indentation (< 530)", bashTitleR.X)
		}
	} else {
		t.Fatalf("expected bash tool title in frame, texts=%v", tt.Texts())
	}
}

func TestWakuModelSelectorAndComposerReasoningPopoverParity(t *testing.T) {
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	fx := tgmNewFixture(t, nil)
	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	// Switch to reasoning-capable model faux-2 so Reasoning popover exposes Low/Medium/High.
	faux2 := fx.runtime.CurrentSession().ModelRegistry().Find(fx.provider, "faux-2")
	if faux2 == nil {
		t.Fatalf("expected faux-2 reasoning model in registry")
	}
	if err := fx.runtime.CurrentSession().SetModel(context.Background(), faux2); err != nil {
		t.Fatalf("SetModel(faux-2): %v", err)
	}

	mode.refreshSidebarSessionsSync(context.Background())

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	chipR, ok := tt.Find("Select Thinking Chip")
	if !ok {
		t.Fatalf("expected Select Thinking Chip in composer, texts=%v", tt.Texts())
	}
	if err := tt.Click("Select Thinking Chip"); err != nil {
		t.Fatalf("Click Select Thinking Chip: %v", err)
	}
	tt.Frame()

	if mode.GUI().HasOverlay() {
		t.Fatal("composer reasoning selector should open as an anchored popover, not a centered modal overlay")
	}
	popR, ok := tt.Find("Reasoning")
	if !ok || !tt.HasText("Default") {
		t.Fatalf("expected anchored Reasoning popover with Default badge, texts=%v", tt.Texts())
	}
	if popR.Y >= chipR.Y {
		t.Fatalf("Reasoning popover header Y = %.1f should be above composer chip Y = %.1f", popR.Y, chipR.Y)
	}

	imgPop := tt.Image()
	bPop := imgPop.Bounds()
	for py := max(bPop.Min.Y, int(popR.Y)); py < min(bPop.Max.Y, int(popR.Y)+120); py++ {
		for px := max(bPop.Min.X, int(popR.X)); px < min(bPop.Max.X, int(popR.X)+200); px++ {
			col := wpPixelAt(t, imgPop, px, py)
			if col.R > 180 && int(col.R)-int(col.B) > 50 {
				t.Fatalf("unexpected orange fill inside Reasoning popover at (%d,%d): %+v", px, py, col)
			}
		}
	}

	if err := tt.Click("High"); err != nil {
		t.Fatalf("Click High in Reasoning popover: %v", err)
	}
	tt.Frame()
	if mode.Session().ThinkingLevel() != ai.ModelThinkingLevelHigh {
		t.Fatalf("expected ThinkingLevel=high after popover selection, got %v", mode.Session().ThinkingLevel())
	}
	if tt.HasText("Reasoning") {
		t.Fatal("expected Reasoning popover to close after selection")
	}

	// Verify ModelSelectorDialog active row has zero coral-orange fill and high text contrast.
	mode.ShowModelSelector()
	tt.Frame()
	if !mode.GUI().HasOverlay() || !tt.HasText("Select Model") {
		t.Fatalf("expected Select Model overlay, texts=%v", tt.Texts())
	}

	var activeRow ui.Rect
	var foundRow bool
	for _, info := range tt.AccessAll() {
		if strings.HasPrefix(info.Name, "✓ ") && info.Bounds.W > 120 {
			activeRow = info.Bounds
			foundRow = true
			break
		}
	}
	if !foundRow {
		t.Fatalf("expected active model row prefixed with check mark, access=%v", tt.Texts())
	}

	imgModal := tt.Image()
	maxSampleX := int(activeRow.X + activeRow.W*0.72)
	bgCounts := make(map[color.RGBA]int)
	for py := int(activeRow.Y) + 4; py < int(activeRow.Y+activeRow.H)-4; py++ {
		for px := int(activeRow.X) + 8; px < maxSampleX; px++ {
			col := wpPixelAt(t, imgModal, px, py)
			if col.R > 180 && int(col.R)-int(col.B) > 50 {
				t.Fatalf("unreadable orange fill in selected model row at (%d,%d): %+v", px, py, col)
			}
			bgCounts[col]++
		}
	}
	var dominant color.RGBA
	var maxCount int
	for col, n := range bgCounts {
		if n > maxCount {
			maxCount = n
			dominant = col
		}
	}
	if dominant.R > 75 || dominant.G > 75 || dominant.B > 75 {
		t.Fatalf("unexpectedly bright dominant background in selected model row: %+v", dominant)
	}
	darkTheme := gui.DarkGuiTheme()
	bgUI := ui.RGB(dominant.R, dominant.G, dominant.B)
	if ratio := gui.ContrastRatio(bgUI, darkTheme.TextSecondary); ratio < 4.5 {
		t.Fatalf("contrast between selected row bg %+v and TextSecondary %+v = %.2f, want >= 4.5", dominant, darkTheme.TextSecondary, ratio)
	}
}

func TestWakuFullWindowSettingsViewParity(t *testing.T) {
	t.Setenv(utils.EnvSessionDir, t.TempDir())

	fx := tgmNewFixture(t, nil)
	mode := NewGuiMode(fx.runtime, &GuiModeOptions{
		SkipWindow:          true,
		InitialThemeSetting: tgmStrPtr("dark"),
	})
	t.Cleanup(func() { mode.Stop() })

	mode.refreshSidebarSessionsSync(context.Background())

	tt := ui.NewTester(mode.GUI().View, 1280, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 48, Left: 86})
	tt.SetVibrancy(false)
	tt.SetDark(true)
	tt.Frame()

	if err := tt.Click("Open Settings"); err != nil {
		t.Fatalf("Click Open Settings: %v", err)
	}
	tt.Frame()

	if !mode.SettingsOpen() || mode.GUI().HasOverlay() {
		t.Fatalf("expected full-window Settings view (settingsOpen=%v hasOverlay=%v)", mode.SettingsOpen(), mode.GUI().HasOverlay())
	}
	for _, want := range []string{"Back", "Search Settings", "General", "Appearance", "Providers", "Behavior", "Local by default", "Auto-Compact"} {
		if !tt.HasText(want) {
			t.Fatalf("expected Waku Settings element %q, texts=%v", want, tt.Texts())
		}
	}

	swR, ok := tt.Find("Toggle Auto-Compact")
	if !ok {
		t.Fatalf("expected Toggle Auto-Compact switch, texts=%v", tt.Texts())
	}
	if swR.W != 36 || swR.H != 20 {
		t.Fatalf("Toggle Auto-Compact size = %.0fx%.0f, want 36x20", swR.W, swR.H)
	}

	img := tt.Image()
	var sawMonochromeOn bool
	for py := int(swR.Y) + 1; py < int(swR.Y+swR.H)-1; py++ {
		for px := int(swR.X) + 1; px < int(swR.X+swR.W)-1; px++ {
			col := wpPixelAt(t, img, px, py)
			if col.R > 180 && int(col.R)-int(col.B) > 50 {
				t.Fatalf("unexpected orange toggle switch pixel at (%d,%d): %+v", px, py, col)
			}
			if col.R > 200 && col.G > 200 && col.B > 200 {
				sawMonochromeOn = true
			}
		}
	}
	if !sawMonochromeOn {
		t.Fatal("expected light monochrome pixels on enabled WakuSwitch track")
	}

	if err := tt.Click("Toggle Auto-Compact"); err != nil {
		t.Fatalf("Click Toggle Auto-Compact: %v", err)
	}
	tt.Frame()
	if mode.Session().AutoCompactionEnabled() {
		t.Fatal("expected Auto-Compact disabled after clicking WakuSwitch")
	}

	if err := tt.Click("Appearance"); err != nil {
		t.Fatalf("Click Appearance tab: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Theme") || !tt.HasText("Hide Thinking Blocks") {
		t.Fatalf("expected Appearance settings cards, texts=%v", tt.Texts())
	}

	if err := tt.Click("Back"); err != nil {
		t.Fatalf("Click Back: %v", err)
	}
	tt.Frame()
	if mode.SettingsOpen() || !tt.HasText("What should we build in cwd?") {
		t.Fatalf("expected return to workspace after Back (open=%v, texts=%v)", mode.SettingsOpen(), tt.Texts())
	}
}
