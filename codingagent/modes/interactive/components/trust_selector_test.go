package components

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func ttrstselBoolPtr(v bool) *bool {
	return &v
}

func ttrstselReset(t *testing.T) {
	t.Helper()
	_, _ = theme.InitTheme("dark", false)
	orig := tui.GetKeybindings()
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
	t.Cleanup(func() {
		tui.SetKeybindings(orig)
	})
}

func TestTrustSelector(t *testing.T) {
	t.Run("keeps the saved trusted decision marked while browsing", func(t *testing.T) {
		ttrstselReset(t)
		selector := NewTrustSelectorComponent(TrustSelectorOptions{
			Cwd:            "/project",
			SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/project", Decision: true},
			ProjectTrusted: true,
			OnSelect:       func(TrustSelection) {},
			OnCancel:       func() {},
		})

		output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
		for _, want := range []string{
			"Saved decision: trusted (/project)",
			"Current session: trusted",
			"→ ✓ Trust",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("output missing %q:\n%s", want, output)
			}
		}

		selector.HandleInput("\x1b[B")
		output = utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
		if !strings.Contains(output, "✓ Trust") {
			t.Fatalf("output missing checkmark on Trust:\n%s", output)
		}
		if !strings.Contains(output, "→   Trust parent folder (/)") {
			t.Fatalf("output missing selected parent option:\n%s", output)
		}
		if strings.Contains(output, "✓ Do not trust") {
			t.Fatalf("unexpected checkmark on Do not trust:\n%s", output)
		}
	})

	t.Run("selects a trust decision", func(t *testing.T) {
		ttrstselReset(t)
		var got *TrustSelection
		selector := NewTrustSelectorComponent(TrustSelectorOptions{
			Cwd:            "/project",
			SavedDecision:  nil,
			ProjectTrusted: false,
			OnSelect: func(sel TrustSelection) {
				s := sel
				got = &s
			},
			OnCancel: func() {},
		})

		selector.HandleInput("\n")
		want := TrustSelection{
			Trusted: true,
			Updates: []core.ProjectTrustUpdate{{Path: "/project", Decision: ttrstselBoolPtr(true)}},
		}
		if got == nil || got.Trusted != want.Trusted || len(got.Updates) != len(want.Updates) ||
			got.Updates[0].Path != want.Updates[0].Path || *got.Updates[0].Decision != *want.Updates[0].Decision {
			t.Fatalf("got = %+v, want %+v", got, want)
		}
	})

	t.Run("labels saved ancestor decisions as inherited", func(t *testing.T) {
		ttrstselReset(t)
		selector := NewTrustSelectorComponent(TrustSelectorOptions{
			Cwd:            "/parent/project/nested",
			SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/parent", Decision: true},
			ProjectTrusted: true,
			OnSelect:       func(TrustSelection) {},
			OnCancel:       func() {},
		})

		output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
		if !strings.Contains(output, "Saved decision: trusted (inherited from /parent)") {
			t.Fatalf("output missing inherited label:\n%s", output)
		}
	})

	t.Run("adds a trust parent option", func(t *testing.T) {
		ttrstselReset(t)
		var got *TrustSelection
		selector := NewTrustSelectorComponent(TrustSelectorOptions{
			Cwd:            "/parent/project",
			SavedDecision:  &core.ProjectTrustStoreEntry{Path: "/parent", Decision: true},
			ProjectTrusted: true,
			OnSelect: func(sel TrustSelection) {
				s := sel
				got = &s
			},
			OnCancel: func() {},
		})

		output := utils.StripAnsi(strings.Join(selector.Render(120), "\n"))
		for _, want := range []string{
			"Saved decision: trusted (inherited from /parent)",
			"✓ Trust parent folder (/parent)",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("output missing %q:\n%s", want, output)
			}
		}

		selector.HandleInput("\n")
		if got == nil || !got.Trusted || len(got.Updates) != 2 ||
			got.Updates[0].Path != "/parent" || got.Updates[0].Decision == nil || !*got.Updates[0].Decision ||
			got.Updates[1].Path != "/parent/project" || got.Updates[1].Decision != nil {
			t.Fatalf("got = %+v, want trusted parent update + clear child update", got)
		}
	})

	t.Run("cancels on escape", func(t *testing.T) {
		ttrstselReset(t)
		cancelled := false
		selector := NewTrustSelectorComponent(TrustSelectorOptions{
			Cwd:            "/project",
			SavedDecision:  nil,
			ProjectTrusted: false,
			OnSelect:       func(TrustSelection) {},
			OnCancel:       func() { cancelled = true },
		})

		selector.HandleInput("\x1b")
		if !cancelled {
			t.Fatal("expected cancel callback")
		}
		_ = slices.Compare([]int{1}, []int{1})
	})
}
