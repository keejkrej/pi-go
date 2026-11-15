package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/tui"
)

type tsmsModelState struct {
	id      string
	name    string
	enabled bool
}

func tsmsMakeModel(provider, id, name string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     name,
			Provider: ai.ProviderId(provider),
		},
	}
}

func tsmsMarkerStates(selector *ScopedModelsSelectorComponent, models []tsmsModelState) []bool {
	lines := strings.Split(utils.StripAnsi(strings.Join(selector.Render(120), "\n")), "\n")
	states := make([]bool, len(models))
	for i, model := range models {
		found := ""
		for _, line := range lines {
			if strings.Contains(line, model.id+" [") {
				found = line
				break
			}
		}
		if found == "" {
			return nil
		}
		if js.Len(found) >= 2 {
			states[i] = strings.HasPrefix(js.SliceFrom(found, 2), "✓ ")
		}
	}
	return states
}

func tsmsReset(t *testing.T) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestScopedModelsSelectorComponent(t *testing.T) {
	createSelector := func(t *testing.T, states []tsmsModelState, onPersist ...func([]string)) *ScopedModelsSelectorComponent {
		t.Helper()
		tsmsReset(t)
		const provider = "anthropic"
		var allModels []*ai.Model
		enabledIds := make([]string, 0)
		for _, m := range states {
			allModels = append(allModels, tsmsMakeModel(provider, m.id, m.name))
			if m.enabled {
				enabledIds = append(enabledIds, provider+"/"+m.id)
			}
		}
		var persistFn func([]string)
		if len(onPersist) > 0 {
			persistFn = onPersist[0]
		}
		return NewScopedModelsSelectorComponent(
			ModelsConfig{
				AllModels:       allModels,
				EnabledModelIds: enabledIds,
			},
			ModelsCallbacks{
				OnChange: func(next []string) {
					for i := range states {
						full := provider + "/" + states[i].id
						states[i].enabled = next == nil || slices.Contains(next, full)
					}
				},
				OnPersist: persistFn,
				OnCancel:  func() {},
			},
		)
	}

	t.Run("shows model names that differ from the ID", func(t *testing.T) {
		states := []tsmsModelState{
			{id: "model-a", name: "Model A", enabled: true},
			{id: "model-b", name: "Renamed B", enabled: true},
		}
		sel := createSelector(t, states)
		var rows []string
		for _, line := range strings.Split(utils.StripAnsi(strings.Join(sel.Render(120), "\n")), "\n") {
			if js.Len(line) >= 4 {
				rows = append(rows, strings.TrimRight(js.SliceFrom(line, 4), " "))
			}
		}
		if !slices.Contains(rows, "model-a [anthropic]") || !slices.Contains(rows, "model-b (Renamed B) [anthropic]") {
			t.Fatalf("rows = %v", rows)
		}
	})

	t.Run("marks every model after enabling all", func(t *testing.T) {
		states := []tsmsModelState{
			{id: "model-a", name: "Model A", enabled: true},
			{id: "model-b", name: "Model B", enabled: false},
			{id: "model-c", name: "Model C", enabled: false},
		}
		sel := createSelector(t, states)

		sel.HandleInput("\x01") // Ctrl+A enable all
		for i, st := range states {
			if !st.enabled {
				t.Fatalf("states[%d].enabled = false, want true", i)
			}
		}
		if got := tsmsMarkerStates(sel, states); !slices.Equal(got, []bool{true, true, true}) {
			t.Fatalf("markers = %v, want [true true true]", got)
		}
		if out := utils.StripAnsi(strings.Join(sel.Render(120), "\n")); !strings.Contains(out, "all enabled") {
			t.Fatalf("missing 'all enabled':\n%s", out)
		}
	})

	t.Run("disables only the selected model after enabling all", func(t *testing.T) {
		states := []tsmsModelState{
			{id: "model-a", name: "Model A", enabled: true},
			{id: "model-b", name: "Model B", enabled: false},
			{id: "model-c", name: "Model C", enabled: false},
		}
		sel := createSelector(t, states)

		sel.HandleInput("\x01")
		sel.HandleInput("\r")
		if got := tsmsMarkerStates(sel, states); !slices.Equal(got, []bool{false, true, true}) {
			t.Fatalf("markers = %v, want [false true true]", got)
		}
	})

	t.Run("enables only the selected model after clearing all", func(t *testing.T) {
		states := []tsmsModelState{
			{id: "model-a", name: "Model A", enabled: true},
			{id: "model-b", name: "Model B", enabled: true},
			{id: "model-c", name: "Model C", enabled: true},
		}
		sel := createSelector(t, states)

		sel.HandleInput("\x18") // Ctrl+X clear all
		if got := tsmsMarkerStates(sel, states); !slices.Equal(got, []bool{false, false, false}) {
			t.Fatalf("after clear markers = %v, want [false false false]", got)
		}
		sel.HandleInput("\r")
		if got := tsmsMarkerStates(sel, states); !slices.Equal(got, []bool{true, false, false}) {
			t.Fatalf("after toggle markers = %v, want [true false false]", got)
		}
	})

	t.Run("restores all-enabled state after re-enabling last disabled model", func(t *testing.T) {
		states := []tsmsModelState{
			{id: "model-a", name: "Model A", enabled: true},
			{id: "model-b", name: "Model B", enabled: false},
			{id: "model-c", name: "Model C", enabled: false},
		}
		sel := createSelector(t, states)

		sel.HandleInput("\x01")   // enable all -> nil
		sel.HandleInput("\r")     // disable a; sorted: [b, c, a]
		sel.HandleInput("\x1b[B") // down to c
		sel.HandleInput("\x1b[B") // down to a
		sel.HandleInput("\r")     // re-enable a -> nil

		if got := tsmsMarkerStates(sel, states); !slices.Equal(got, []bool{true, true, true}) {
			t.Fatalf("markers = %v, want [true true true]", got)
		}
		if out := utils.StripAnsi(strings.Join(sel.Render(120), "\n")); !strings.Contains(out, "all enabled") {
			t.Fatalf("missing 'all enabled':\n%s", out)
		}
	})

	t.Run("reorders enabled models, updates models, and persists", func(t *testing.T) {
		tsmsReset(t)
		m1 := tsmsMakeModel("p1", "m1", "M1")
		m2 := tsmsMakeModel("p1", "m2", "M2")
		m3 := tsmsMakeModel("p2", "m3", "M3")
		var persisted [][]string
		status := "Refreshing..."
		sel := NewScopedModelsSelectorComponent(
			ModelsConfig{
				AllModels:       []*ai.Model{m1, m2, m3},
				EnabledModelIds: []string{"p1/m1", "p1/m2"},
				RefreshStatus:   &status,
			},
			ModelsCallbacks{
				OnChange:  func([]string) {},
				OnPersist: func(ids []string) { persisted = append(persisted, ids) },
				OnCancel:  func() {},
			},
		)

		sel.HandleInput("\x1b[B")    // select p1/m2
		sel.HandleInput("\x1b[1;3A") // Alt+Up reorder
		sel.HandleInput("\x13")      // Ctrl+S save
		if len(persisted) != 1 || !slices.Equal(persisted[0], []string{"p1/m2", "p1/m1"}) {
			t.Fatalf("persisted = %v, want [[p1/m2 p1/m1]]", persisted)
		}
		doneMsg := "Refreshed"
		sel.SetRefreshStatus(&doneMsg, "success")
		sel.UpdateModels([]*ai.Model{m1, m2})
		if out := utils.StripAnsi(strings.Join(sel.Render(120), "\n")); !strings.Contains(out, "Refreshed") {
			t.Fatalf("missing refresh status:\n%s", out)
		}
	})
}
