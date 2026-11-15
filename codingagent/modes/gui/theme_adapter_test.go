package gui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/gui"
	"github.com/keejkrej/pi-go/tui"
)

func ttaRestoreTheme(t *testing.T, prev *theme.Theme) {
	t.Helper()
	if prev != nil {
		theme.SetThemeInstance(prev)
	}
}

func TestThemeAdapterDarkLightAndCustomJson(t *testing.T) {
	prev := theme.CurrentTheme()
	defer ttaRestoreTheme(t, prev)

	darkTh, err := theme.GetThemeByName("dark")
	if err != nil || darkTh == nil {
		t.Fatalf("GetThemeByName(dark) failed: %v", err)
	}
	darkGui := BuildGuiTheme(darkTh)
	if !darkGui.Dark || darkGui.Name != "dark" {
		t.Fatalf("expected dark theme metadata, got Dark=%v Name=%q", darkGui.Dark, darkGui.Name)
	}
	if darkGui.Accent == ui.Transparent || darkGui.Text == ui.Transparent || darkGui.BashMode == ui.Transparent {
		t.Fatal("expected resolved non-transparent dark semantic colors")
	}

	lightTh, err := theme.GetThemeByName("light")
	if err != nil || lightTh == nil {
		t.Fatalf("GetThemeByName(light) failed: %v", err)
	}
	lightGui := BuildGuiTheme(lightTh)
	if lightGui.Dark || lightGui.Name != "light" {
		t.Fatalf("expected light theme metadata, got Dark=%v Name=%q", lightGui.Dark, lightGui.Name)
	}
	if lightGui.Background == darkGui.Background {
		t.Fatal("expected light theme background to differ from dark theme background")
	}

	// Custom theme with overridden tokens
	rgbAccent, _ := tui.NewRgbColor(255, 85, 0)
	rgbUserBg, _ := tui.NewRgbColor(20, 30, 50)
	custom := &theme.Theme{
		Name:       "custom-test",
		Appearance: tui.TerminalColorSchemeDark,
		Colors: map[string]tui.Color{
			theme.FgAccent:       rgbAccent,
			theme.BgUserMessage:  rgbUserBg,
			theme.FgThinkingHigh: rgbAccent,
		},
	}
	customSet := SyncUIThemes(custom)
	if customSet.Gui.Accent != ui.RGB(255, 85, 0) {
		t.Fatalf("expected custom accent #ff5500, got %+v", customSet.Gui.Accent)
	}
	if customSet.Gui.UserMessageBg != ui.RGB(20, 30, 50) {
		t.Fatalf("expected custom userMessageBg, got %+v", customSet.Gui.UserMessageBg)
	}
	if ThinkingLevelColor(customSet.Gui, agent.ThinkingLevel("high")) != ui.RGB(255, 85, 0) {
		t.Fatal("expected ThinkingLevelColor to reflect overridden high color")
	}
	if customSet.UI == nil || customSet.Markdown == nil || customSet.Editor == nil {
		t.Fatal("expected UI/Markdown/Editor sub-themes to be populated")
	}

	if got := ThemeTokenToUIColor(custom, theme.FgAccent, ui.RGB(1, 2, 3)); got != ui.RGB(255, 85, 0) {
		t.Fatalf("ThemeTokenToUIColor mismatch: %+v", got)
	}
	if got := ThemeTokenToUIColor(custom, "missing-token", ui.RGB(1, 2, 3)); got != ui.RGB(1, 2, 3) {
		t.Fatalf("expected fallback color, got %+v", got)
	}
}

func TestThemeAdapterHostSyncAndBindChanges(t *testing.T) {
	prev := theme.CurrentTheme()
	defer ttaRestoreTheme(t, prev)

	host := gui.New()
	var updates int
	unsub := BindThemeChanges(host, func(ts *ThemeSet) {
		if ts != nil && ts.Gui != nil {
			updates++
		}
	})
	defer unsub()

	res, ts := ApplyThemeToHost(host, "light")
	if !res.Success || ts == nil || ts.Gui.Dark {
		t.Fatalf("ApplyThemeToHost(light) failed: %+v", res)
	}
	if host.Theme().Dark {
		t.Fatal("expected host theme to become light")
	}
	if updates == 0 {
		t.Fatal("expected BindThemeChanges listener to be notified")
	}

	// Verify tester renders cleanly with applied theme
	host.SetRootView(func(c *ui.Context) {
		th := gui.ResolveGuiTheme(c, nil)
		ui.Text(c, th.Name).TextColor(th.Text)
	})
	tt := ui.NewTester(host.View, 300, 120)
	if !tt.HasText("light") {
		t.Fatalf("expected light theme name rendered, got %v", tt.Texts())
	}

	if got := ResolveThemeForSetting("light/dark", true); got != "dark" {
		t.Fatalf("ResolveThemeForSetting(true) = %q, want dark", got)
	}
	if got := ResolveThemeForSetting("light/dark", false); got != "light" {
		t.Fatalf("ResolveThemeForSetting(false) = %q, want light", got)
	}
}
