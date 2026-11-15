package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type toaselMockTUI struct {
	tui.TUI
}

func (m *toaselMockTUI) RequestRender(*bool) {}
func (m *toaselMockTUI) Post(fn func())      { fn() }

func toaselStrPtr(s string) *string { return &s }

func TestOAuthSelectorComponent(t *testing.T) {
	_, _ = theme.InitTheme("dark", false)
	origKb := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(origKb)
	})

	t.Run("renders status indicators accurately", func(t *testing.T) {
		selNone := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{Id: "google", Name: "Google", AuthType: ai.AuthTypeApiKey}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		outNone := utils.StripAnsi(strings.Join(selNone.Render(120), "\n"))
		if !strings.Contains(outNone, "not configured") || strings.Contains(outNone, "✓ configured") {
			t.Fatalf("unexpected output for unconfigured provider:\n%s", outNone)
		}

		selOAuth := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{
				Id:       "anthropic",
				Name:     "Anthropic",
				AuthType: ai.AuthTypeApiKey,
				Status:   &ai.AuthCheck{Type: ai.AuthTypeOAuth, Source: toaselStrPtr("OAuth")},
			}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		if out := utils.StripAnsi(strings.Join(selOAuth.Render(120), "\n")); !strings.Contains(out, "subscription configured") {
			t.Fatalf("expected subscription configured:\n%s", out)
		}

		selEnv := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{
				Id:       "openai",
				Name:     "OpenAI",
				AuthType: ai.AuthTypeApiKey,
				Status:   &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: toaselStrPtr("OPENAI_API_KEY")},
			}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		outEnv := utils.StripAnsi(strings.Join(selEnv.Render(120), "\n"))
		if !strings.Contains(outEnv, "✓ env: OPENAI_API_KEY") || strings.Contains(outEnv, "not configured") {
			t.Fatalf("unexpected env status output:\n%s", outEnv)
		}

		selModelsKey := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{
				Id:       "local-proxy",
				Name:     "local-proxy",
				AuthType: ai.AuthTypeApiKey,
				Status:   &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: toaselStrPtr("key in models.json")},
			}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		if out := utils.StripAnsi(strings.Join(selModelsKey.Render(120), "\n")); !strings.Contains(out, "✓ key in models.json") {
			t.Fatalf("missing key in models.json:\n%s", out)
		}

		selModelsCmd := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{
				Id:       "op-proxy",
				Name:     "op-proxy",
				AuthType: ai.AuthTypeApiKey,
				Status:   &ai.AuthCheck{Type: ai.AuthTypeApiKey, Source: toaselStrPtr("command in models.json")},
			}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		if out := utils.StripAnsi(strings.Join(selModelsCmd.Render(120), "\n")); !strings.Contains(out, "✓ command in models.json") {
			t.Fatalf("missing command in models.json:\n%s", out)
		}
	})

	t.Run("shows preferred partner brand style only on selected row", func(t *testing.T) {
		th := theme.CurrentTheme()
		ui := &toaselMockTUI{}
		sel := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{
				{Id: "radius", Name: "Radius", AuthType: ai.AuthTypeOAuth},
				{Id: "openrouter", Name: "OpenRouter", AuthType: ai.AuthTypeApiKey},
			},
			func(string, ai.AuthType) {},
			func() {},
			nil,
			ui,
		)
		defer sel.Dispose()

		out := strings.Join(sel.Render(120), "\n")
		if strings.Contains(out, th.Fg(theme.FgAccent, "Radius")) {
			t.Fatalf("expected brand style instead of static accent for Radius:\n%q", out)
		}
		if !strings.Contains(out, th.Fg(theme.FgText, "OpenRouter")) {
			t.Fatalf("expected FgText for unselected OpenRouter:\n%q", out)
		}
		if !strings.Contains(utils.StripAnsi(out), "→ Radius") {
			t.Fatalf("missing selected Radius row:\n%s", utils.StripAnsi(out))
		}

		sel.HandleInput("\x1b[B")
		moved := strings.Join(sel.Render(120), "\n")
		if !strings.Contains(moved, th.Fg(theme.FgText, "Radius")) {
			t.Fatalf("expected FgText for unselected Radius:\n%q", moved)
		}
		if strings.Contains(moved, th.Fg(theme.FgAccent, "OpenRouter")) {
			t.Fatalf("expected brand style instead of static accent for OpenRouter:\n%q", moved)
		}
		if !strings.Contains(utils.StripAnsi(moved), "→ OpenRouter") {
			t.Fatalf("missing selected OpenRouter row:\n%s", utils.StripAnsi(moved))
		}
	})

	t.Run("keeps accent style without TUI and marks preferred partners with star", func(t *testing.T) {
		th := theme.CurrentTheme()
		selNoUI := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{{Id: "openrouter", Name: "OpenRouter", AuthType: ai.AuthTypeApiKey}},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		if out := strings.Join(selNoUI.Render(120), "\n"); !strings.Contains(out, th.Fg(theme.FgAccent, "OpenRouter")) {
			t.Fatalf("expected accent style without UI:\n%q", out)
		}

		selStar := NewOAuthSelectorComponent(
			"login",
			[]AuthSelectorProvider{
				{Id: "radius", Name: "Radius", AuthType: ai.AuthTypeOAuth},
				{Id: "openrouter", Name: "OpenRouter", AuthType: ai.AuthTypeApiKey},
				{Id: "anthropic", Name: "Anthropic", AuthType: ai.AuthTypeApiKey},
			},
			func(string, ai.AuthType) {},
			func() {},
			nil,
		)
		out := utils.StripAnsi(strings.Join(selStar.Render(120), "\n"))
		for _, want := range []string{
			"→ Radius ✦ [subscription]",
			"  OpenRouter ✦ [API key]",
			"  Anthropic [API key]",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("output missing %q:\n%s", want, out)
			}
		}
	})
}
