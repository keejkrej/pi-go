package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

type tmseMockTUI struct {
	tui.TUI
}

func (m *tmseMockTUI) RequestRender(*bool) {}
func (m *tmseMockTUI) Post(fn func())      { fn() }

type tmseFakeRuntime struct {
	mu       sync.Mutex
	snapshot []*ai.Model
	err      *string
	refresh  func(ctx context.Context, opts *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error)
}

func (f *tmseFakeRuntime) Refresh(ctx context.Context, opts *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
	f.mu.Lock()
	fn := f.refresh
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return ai.ModelsRefreshResult{Errors: omap.NewMap[string, error]()}, nil
}

func (f *tmseFakeRuntime) GetAvailableSnapshot() []*ai.Model {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*ai.Model, len(f.snapshot))
	copy(out, f.snapshot)
	return out
}

func (f *tmseFakeRuntime) GetModel(provider, id string) *ai.Model {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.snapshot {
		if string(m.Provider) == provider && m.Id == id {
			return m
		}
	}
	return nil
}

func (f *tmseFakeRuntime) GetError() *string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func tmseModel(provider, id, name string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     name,
			Provider: ai.ProviderId(provider),
		},
		Reasoning: true,
	}
}

func tmseSelectedModelId(selector *ModelSelectorComponent) string {
	lines := strings.Split(utils.StripAnsi(strings.Join(selector.Render(120), "\n")), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "→") {
			rest := strings.TrimPrefix(line, "→")
			rest = strings.TrimSpace(rest)
			rest = strings.TrimPrefix(rest, "✓")
			rest = strings.TrimSpace(rest)
			parts := strings.SplitN(rest, " ", 2)
			return parts[0]
		}
	}
	return ""
}

func tmseWaitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for model selector condition")
}

func tmseReset(t *testing.T, userBindings tui.KeybindingsConfig) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(userBindings).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestModelSelectorComponent(t *testing.T) {
	t.Run("keeps the current model marked while browsing", func(t *testing.T) {
		tmseReset(t, nil)
		cur := tmseModel("anthropic", "current-model", "Current Model")
		other := tmseModel("anthropic", "browsed-model", "Browsed Model")
		rt := &tmseFakeRuntime{snapshot: []*ai.Model{cur, other}}

		sel := mseNewModelSelectorWithRuntime(&tmseMockTUI{}, cur, rt, nil, func(*ai.Model) {}, func() {}, nil, nil, nil)
		defer sel.Dispose()

		getRow := func(id string) string {
			for _, line := range strings.Split(utils.StripAnsi(strings.Join(sel.Render(120), "\n")), "\n") {
				if strings.Contains(line, id+" [") {
					return strings.TrimRight(line, " ")
				}
			}
			return ""
		}

		if got := getRow("current-model"); got != "→ ✓ current-model [anthropic]" {
			t.Fatalf("current row = %q, want %q", got, "→ ✓ current-model [anthropic]")
		}
		sel.HandleInput("\x1b[B")
		if got := getRow("current-model"); got != "  ✓ current-model [anthropic]" {
			t.Fatalf("after down current row = %q, want %q", got, "  ✓ current-model [anthropic]")
		}
		if got := getRow("browsed-model"); got != "→   browsed-model [anthropic]" {
			t.Fatalf("after down browsed row = %q, want %q", got, "→   browsed-model [anthropic]")
		}
	})

	t.Run("shows model names that differ from the ID", func(t *testing.T) {
		tmseReset(t, nil)
		m1 := tmseModel("openrouter", "minimax/minimax-m3", "MiniMax M3 [parasail]")
		m2 := tmseModel("openrouter", "claude-sonnet-4-5", "Claude Sonnet 4.5")
		rt := &tmseFakeRuntime{snapshot: []*ai.Model{m1, m2}}

		sel := mseNewModelSelectorWithRuntime(&tmseMockTUI{}, nil, rt, nil, func(*ai.Model) {}, func() {}, nil, nil, nil)
		defer sel.Dispose()

		var rows []string
		for _, line := range strings.Split(utils.StripAnsi(strings.Join(sel.Render(120), "\n")), "\n") {
			if js.Len(line) >= 4 {
				rows = append(rows, strings.TrimRight(js.SliceFrom(line, 4), " "))
			}
		}
		if !slices.Contains(rows, "minimax/minimax-m3 (MiniMax M3 [parasail]) [openrouter]") {
			t.Fatalf("missing distinct model name row in %v", rows)
		}
		if !slices.Contains(rows, "claude-sonnet-4-5 [openrouter]") {
			t.Fatalf("missing restated model ID row in %v", rows)
		}
	})

	t.Run("moves by one page with remapped selection page keys", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		up := tui.KeyId("alt+v")
		down := tui.KeyId("ctrl+v")
		cfg.Set(string(tui.KeybindingTuiSelectPageUp), &tui.KeybindingKeys{Single: &up})
		cfg.Set(string(tui.KeybindingTuiSelectPageDown), &tui.KeybindingKeys{Single: &down})
		tmseReset(t, cfg)

		var models []*ai.Model
		for i := 0; i < 12; i++ {
			models = append(models, tmseModel("openai", fmt.Sprintf("model-%02d", i), fmt.Sprintf("Model %02d", i)))
		}
		rt := &tmseFakeRuntime{snapshot: models}
		var chosen *ai.Model
		sel := mseNewModelSelectorWithRuntime(&tmseMockTUI{}, nil, rt, nil, func(m *ai.Model) { chosen = m }, func() {}, nil, nil, nil)
		defer sel.Dispose()

		sel.HandleInput("\x16") // Ctrl+V
		sel.HandleInput("\x16")
		sel.HandleInput("\x1bv") // Alt+V
		sel.HandleInput("\r")
		if chosen == nil || chosen.Id != "model-01" {
			t.Fatalf("chosen = %+v, want model-01", chosen)
		}
	})

	t.Run("uses the configured save binding", func(t *testing.T) {
		cfg := omap.NewMap[string, *tui.KeybindingKeys]()
		saveKey := tui.KeyId("ctrl+r")
		cfg.Set(string(core.KeybindingAppModelsSave), &tui.KeybindingKeys{Single: &saveKey})
		tmseReset(t, cfg)

		cur := tmseModel("openai", "gpt-5", "GPT 5")
		rt := &tmseFakeRuntime{snapshot: []*ai.Model{cur}}
		var saved []*ai.Model
		sel := mseNewModelSelectorWithRuntime(
			&tmseMockTUI{},
			cur,
			rt,
			nil,
			func(*ai.Model) {},
			func() {},
			nil,
			func(m *ai.Model) { saved = append(saved, m) },
			&DefaultModelRef{Provider: "openai", Id: "gpt-5"},
		)
		defer sel.Dispose()

		rendered := utils.StripAnsi(strings.Join(sel.Render(120), "\n"))
		if !strings.Contains(rendered, "Ctrl+R to set as default") {
			t.Fatalf("missing Ctrl+R hint:\n%s", rendered)
		}
		sel.HandleInput("\x13") // Ctrl+S (unbound)
		if len(saved) != 0 {
			t.Fatalf("saved after unbound Ctrl+S = %v, want empty", saved)
		}
		sel.HandleInput("\x12") // Ctrl+R
		if len(saved) != 1 || saved[0].Id != "gpt-5" {
			t.Fatalf("saved after Ctrl+R = %v, want [gpt-5]", saved)
		}
	})

	t.Run("lists every catalog that failed to refresh", func(t *testing.T) {
		tmseReset(t, nil)
		rt := &tmseFakeRuntime{
			snapshot: []*ai.Model{tmseModel("openai", "gpt-5", "GPT 5")},
			refresh: func(context.Context, *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
				errs := omap.NewMap[string, error]()
				errs.Set("openai", errors.New("unavailable"))
				errs.Set("anthropic", errors.New("unavailable"))
				return ai.ModelsRefreshResult{Errors: errs}, nil
			},
		}
		sel := mseNewModelSelectorWithRuntime(&tmseMockTUI{}, rt.snapshot[0], rt, nil, func(*ai.Model) {}, func() {}, nil, nil, nil)
		defer sel.Dispose()

		tmseWaitUntil(t, func() bool {
			return strings.Contains(
				utils.StripAnsi(strings.Join(sel.Render(120), "\n")),
				"Could not refresh 2 model catalogs (openai, anthropic); showing cached models.",
			)
		})
	})

	openWithPendingRefresh := func(t *testing.T) (*tmseFakeRuntime, *ModelSelectorComponent, func()) {
		t.Helper()
		tmseReset(t, nil)
		m1 := tmseModel("test", "alpha-1", "Alpha 1")
		m2 := tmseModel("test", "alpha-2", "Alpha 2")
		m3 := tmseModel("test", "alpha-3", "Alpha 3")
		m4 := tmseModel("test", "beta-1", "Beta 1")

		gate := make(chan struct{})
		rt := &tmseFakeRuntime{
			snapshot: []*ai.Model{m1, m2, m3, m4},
			refresh: func(ctx context.Context, _ *ai.ModelsRefreshOptions) (ai.ModelsRefreshResult, error) {
				select {
				case <-gate:
					return ai.ModelsRefreshResult{Errors: omap.NewMap[string, error]()}, nil
				case <-ctx.Done():
					return ai.ModelsRefreshResult{Aborted: true, Errors: omap.NewMap[string, error]()}, nil
				}
			},
		}
		sel := mseNewModelSelectorWithRuntime(&tmseMockTUI{}, m1, rt, nil, func(*ai.Model) {}, func() {}, nil, nil, nil)
		finish := func() {
			close(gate)
			tmseWaitUntil(t, func() bool {
				return strings.Contains(utils.StripAnsi(strings.Join(sel.Render(120), "\n")), "Model catalogs refreshed.")
			})
		}
		return rt, sel, finish
	}

	t.Run("refresh preserves browsed model, active search, shifting rows, and fallback", func(t *testing.T) {
		_, sel, finish := openWithPendingRefresh(t)
		sel.HandleInput("\x1b[B")
		sel.HandleInput("\x1b[B")
		finish()
		if got := tmseSelectedModelId(sel); got != "alpha-3" {
			t.Fatalf("selected after refresh = %q, want alpha-3", got)
		}
		sel.Dispose()

		_, sel2, finish2 := openWithPendingRefresh(t)
		for _, ch := range "alpha" {
			sel2.HandleInput(string(ch))
		}
		sel2.HandleInput("\x1b[B")
		browsed := tmseSelectedModelId(sel2)
		finish2()
		if got := tmseSelectedModelId(sel2); got != browsed || browsed == "alpha-1" {
			t.Fatalf("after filtered refresh selected = %q, want %q (!= alpha-1)", got, browsed)
		}
		sel2.Dispose()

		rt3, sel3, finish3 := openWithPendingRefresh(t)
		sel3.HandleInput("\x1b[B")
		rt3.mu.Lock()
		rt3.snapshot = append([]*ai.Model{tmseModel("test", "added-model", "Added Model")}, rt3.snapshot...)
		rt3.mu.Unlock()
		finish3()
		if got := tmseSelectedModelId(sel3); got != "alpha-2" {
			t.Fatalf("after shift selected = %q, want alpha-2", got)
		}
		sel3.HandleInput("\x1b[A")
		sel3.HandleInput("\x1b[A")
		if got := tmseSelectedModelId(sel3); got != "alpha-1" {
			t.Fatalf("after moving up twice selected = %q, want alpha-1", got)
		}
		sel3.Dispose()

		rt4, sel4, finish4 := openWithPendingRefresh(t)
		sel4.HandleInput("\x1b[B")
		rt4.mu.Lock()
		var remaining []*ai.Model
		for _, m := range rt4.snapshot {
			if m.Id != "alpha-2" {
				remaining = append(remaining, m)
			}
		}
		rt4.snapshot = remaining
		rt4.mu.Unlock()
		finish4()
		if got := tmseSelectedModelId(sel4); got != "alpha-1" {
			t.Fatalf("after disappearance fallback selected = %q, want alpha-1", got)
		}
		sel4.Dispose()
	})

	t.Run("resets selection to top match on filter typing and toggles scope", func(t *testing.T) {
		tmseReset(t, nil)
		m1 := tmseModel("test", "alpha-one", "Alpha One")
		m2 := tmseModel("test", "beta-two", "Beta Two")
		rt := &tmseFakeRuntime{snapshot: []*ai.Model{m1, m2}}

		sel := mseNewModelSelectorWithRuntime(
			&tmseMockTUI{},
			m2,
			rt,
			[]core.ScopedModel{{Model: m2}},
			func(*ai.Model) {},
			func() {},
			nil,
			nil,
			nil,
		)
		defer sel.Dispose()

		if got := tmseSelectedModelId(sel); got != "beta-two" {
			t.Fatalf("initial scoped selected = %q, want beta-two", got)
		}
		sel.HandleInput("\t") // switch to all
		for _, ch := range "alpha" {
			sel.HandleInput(string(ch))
		}
		if got := tmseSelectedModelId(sel); got != "alpha-one" {
			t.Fatalf("filtered top match = %q, want alpha-one", got)
		}
	})
}
