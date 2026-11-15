// Ported from packages/coding-agent/src/cli/startup-ui.ts & test/first-time-setup.test.ts (pi v1.5.2).

package cli

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

type tsuFakeTerminal struct {
	mu      sync.Mutex
	cols    int
	rows    int
	onInput func(string)
	writes  []string
	writeCh chan struct{}
	stopped bool
}

func tsuNewFakeTerminal() *tsuFakeTerminal {
	return &tsuFakeTerminal{
		cols:    100,
		rows:    30,
		writeCh: make(chan struct{}, 32),
	}
}

func (t *tsuFakeTerminal) Start(onInput func(data string), _ func()) {
	t.mu.Lock()
	t.onInput = onInput
	t.mu.Unlock()
}

func (t *tsuFakeTerminal) Stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
}

func (t *tsuFakeTerminal) DrainInput(_, _ *int64) error { return nil }

func (t *tsuFakeTerminal) Write(data string) {
	t.mu.Lock()
	t.writes = append(t.writes, data)
	t.mu.Unlock()
	select {
	case t.writeCh <- struct{}{}:
	default:
	}
}

func (t *tsuFakeTerminal) Columns() int              { return t.cols }
func (t *tsuFakeTerminal) Rows() int                 { return t.rows }
func (t *tsuFakeTerminal) KittyProtocolActive() bool { return false }
func (t *tsuFakeTerminal) MoveBy(int)                {}
func (t *tsuFakeTerminal) HideCursor()               {}
func (t *tsuFakeTerminal) ShowCursor()               {}
func (t *tsuFakeTerminal) ClearLine()                {}
func (t *tsuFakeTerminal) ClearFromCursor()          {}
func (t *tsuFakeTerminal) ClearScreen()              {}
func (t *tsuFakeTerminal) SetTitle(string)           {}
func (t *tsuFakeTerminal) SetProgress(bool)          {}
func (t *tsuFakeTerminal) SetProgramStatus(tui.ProgramStatus) {
}

func (t *tsuFakeTerminal) waitReady(tb testing.TB) {
	tb.Helper()
	deadline := time.After(3 * time.Second)
	for {
		t.mu.Lock()
		ready := t.onInput != nil && len(t.writes) > 0
		t.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-t.writeCh:
		case <-deadline:
			tb.Fatal("timed out waiting for fake terminal start/render")
		}
	}
}

func (t *tsuFakeTerminal) send(keys ...string) {
	t.mu.Lock()
	fn := t.onInput
	t.mu.Unlock()
	if fn == nil {
		return
	}
	for _, k := range keys {
		fn(k)
		time.Sleep(15 * time.Millisecond)
	}
}

func TestFirstTimeSetupEligibility(t *testing.T) {
	t.Setenv("CI", "")

	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sm := core.SettingsManagerInMemory(&core.Settings{})

	if !ShouldRunFirstTimeSetup(sm, agentDir, nil, true) {
		t.Fatal("expected onboarding when settings.json does not exist")
	}

	if ShouldRunFirstTimeSetup(sm, agentDir, nil, false) {
		t.Fatal("expected onboarding disabled in non-interactive mode")
	}

	explicit := "dark"
	if ShouldRunFirstTimeSetup(sm, agentDir, &explicit, true) {
		t.Fatal("expected onboarding skipped when --theme is passed")
	}

	t.Setenv("CI", "true")
	if ShouldRunFirstTimeSetup(sm, agentDir, nil, true) {
		t.Fatal("expected onboarding skipped when CI env var is set")
	}
	t.Setenv("CI", "")

	settingsPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	smEmpty := core.SettingsManagerCreate(t.TempDir(), agentDir)
	if !ShouldRunFirstTimeSetup(smEmpty, agentDir, nil, true) {
		t.Fatal("expected onboarding when settings.json has empty global settings")
	}

	for _, raw := range []string{
		`{"theme":"dark"}`,
		`{"enableAnalytics":false}`,
		`{"lastChangelogVersion":"1.0.0"}`,
	} {
		if err := os.WriteFile(settingsPath, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		smPop := core.SettingsManagerCreate(t.TempDir(), agentDir)
		if ShouldRunFirstTimeSetup(smPop, agentDir, nil, true) {
			t.Fatalf("expected onboarding skipped for settings %s", raw)
		}
	}
}

func TestRunFirstTimeSetupSubmitAndCancel(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	t.Run("submit", func(t *testing.T) {
		agentDir := filepath.Join(t.TempDir(), "agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sm := core.SettingsManagerCreate(t.TempDir(), agentDir)
		term := tsuNewFakeTerminal()

		type resPair struct {
			summary FirstTimeSetupSummary
			err     error
		}
		ch := make(chan resPair, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			summary, err := RunFirstTimeSetup(ctx, sm, &StartupUiOptions{
				AgentDir:               agentDir,
				Terminal:               term,
				DisableThemeController: true,
			})
			ch <- resPair{summary: summary, err: err}
		}()

		term.waitReady(t)
		// Step 1: confirm default theme; Step 2: confirm default analytics choice.
		term.send("\r", "\r")

		out := <-ch
		if out.err != nil {
			t.Fatalf("RunFirstTimeSetup error: %v", out.err)
		}
		if !out.summary.Completed || out.summary.Result == nil {
			t.Fatalf("expected completed setup summary, got %+v", out.summary)
		}
		data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
		if err != nil {
			t.Fatalf("expected settings.json persisted: %v", err)
		}
		var saved map[string]any
		if err := jsonx.Unmarshal(data, &saved); err != nil {
			t.Fatalf("invalid saved settings JSON: %v", err)
		}
		if saved["theme"] == nil || saved["enableAnalytics"] == nil {
			t.Fatalf("expected theme and enableAnalytics saved, got %v", saved)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		agentDir := filepath.Join(t.TempDir(), "agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sm := core.SettingsManagerCreate(t.TempDir(), agentDir)
		term := tsuNewFakeTerminal()

		type resPair struct {
			summary FirstTimeSetupSummary
			err     error
		}
		ch := make(chan resPair, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			summary, err := RunFirstTimeSetup(ctx, sm, &StartupUiOptions{
				AgentDir:               agentDir,
				Terminal:               term,
				DisableThemeController: true,
			})
			ch <- resPair{summary: summary, err: err}
		}()

		term.waitReady(t)
		term.send("\x1b")

		out := <-ch
		if out.err != nil {
			t.Fatalf("RunFirstTimeSetup cancel error: %v", out.err)
		}
		if out.summary.Completed {
			t.Fatalf("expected uncompleted setup summary on cancel, got %+v", out.summary)
		}
	})
}

func TestShowStartupSelectConfirmAndInput(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	// Empty options short-circuits without UI.
	emptyRes, err := ShowStartupSelect(context.Background(), "Empty", nil, nil)
	if err != nil || emptyRes != nil {
		t.Fatalf("empty ShowStartupSelect = (%v, %v), want (nil, nil)", emptyRes, err)
	}

	t.Run("select_second_option", func(t *testing.T) {
		term := tsuNewFakeTerminal()
		type out struct {
			val *string
			err error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			v, err := ShowStartupSelect(ctx, "Pick one", []string{"alpha", "beta"}, nil, &StartupUiOptions{
				Terminal:               term,
				DisableThemeController: true,
			})
			ch <- out{val: v, err: err}
		}()

		term.waitReady(t)
		term.send("\x1b[B", "\r")
		res := <-ch
		if res.err != nil || res.val == nil || *res.val != "beta" {
			t.Fatalf("got (%v, %v), want \"beta\"", res.val, res.err)
		}
	})

	t.Run("confirm_yes", func(t *testing.T) {
		term := tsuNewFakeTerminal()
		type out struct {
			ok  bool
			err error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			ok, err := ShowStartupConfirm(ctx, "Trust?", "Confirm action", &StartupUiOptions{
				Terminal:               term,
				DisableThemeController: true,
			})
			ch <- out{ok: ok, err: err}
		}()

		term.waitReady(t)
		term.send("\r")
		res := <-ch
		if res.err != nil || !res.ok {
			t.Fatalf("got (%v, %v), want (true, nil)", res.ok, res.err)
		}
	})

	t.Run("input_submit", func(t *testing.T) {
		term := tsuNewFakeTerminal()
		type out struct {
			val *string
			err error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			v, err := ShowStartupInput(ctx, "Enter value", nil, &StartupUiOptions{
				Terminal:               term,
				DisableThemeController: true,
			})
			ch <- out{val: v, err: err}
		}()

		term.waitReady(t)
		term.send("h", "i", "\r")
		res := <-ch
		if res.err != nil || res.val == nil || *res.val != "hi" {
			t.Fatalf("got (%v, %v), want \"hi\"", res.val, res.err)
		}
	})
}

func TestInitStartupThemes(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })

	sm := core.SettingsManagerInMemory(&core.Settings{})
	explicit := "dark"
	ctrl, err := InitStartupThemes(sm, nil, &explicit)
	if err != nil {
		t.Fatalf("InitStartupThemes error: %v", err)
	}
	if ctrl != nil {
		defer ctrl.Dispose()
	}
	if got := theme.CurrentTheme().Name; got != "dark" {
		t.Fatalf("active theme = %q, want %q", got, "dark")
	}
}
