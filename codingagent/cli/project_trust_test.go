// Ported from packages/coding-agent/src/cli/project-trust.ts (pi v1.5.2).

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type tptFakeTerminal struct {
	mu      sync.Mutex
	onInput func(string)
	writes  []string
	writeCh chan struct{}
}

func tptNewFakeTerminal() *tptFakeTerminal {
	return &tptFakeTerminal{writeCh: make(chan struct{}, 32)}
}

func (t *tptFakeTerminal) Start(onInput func(data string), _ func()) {
	t.mu.Lock()
	t.onInput = onInput
	t.mu.Unlock()
}

func (t *tptFakeTerminal) Stop()                              {}
func (t *tptFakeTerminal) DrainInput(_, _ *int64) error       { return nil }
func (t *tptFakeTerminal) Columns() int                       { return 100 }
func (t *tptFakeTerminal) Rows() int                          { return 30 }
func (t *tptFakeTerminal) KittyProtocolActive() bool          { return false }
func (t *tptFakeTerminal) MoveBy(int)                         {}
func (t *tptFakeTerminal) HideCursor()                        {}
func (t *tptFakeTerminal) ShowCursor()                        {}
func (t *tptFakeTerminal) ClearLine()                         {}
func (t *tptFakeTerminal) ClearFromCursor()                   {}
func (t *tptFakeTerminal) ClearScreen()                       {}
func (t *tptFakeTerminal) SetTitle(string)                    {}
func (t *tptFakeTerminal) SetProgress(bool)                   {}
func (t *tptFakeTerminal) SetProgramStatus(tui.ProgramStatus) {}

func (t *tptFakeTerminal) Write(data string) {
	t.mu.Lock()
	t.writes = append(t.writes, data)
	t.mu.Unlock()
	select {
	case t.writeCh <- struct{}{}:
	default:
	}
}

func (t *tptFakeTerminal) waitReadyAndSend(tb testing.TB, key string) {
	tb.Helper()
	deadline := time.After(3 * time.Second)
	for {
		t.mu.Lock()
		fn := t.onInput
		ready := fn != nil && len(t.writes) > 0
		t.mu.Unlock()
		if ready {
			fn(key)
			return
		}
		select {
		case <-t.writeCh:
		case <-deadline:
			tb.Fatal("timed out waiting for trust prompt render")
		}
	}
}

func TestResolveStartupProjectTrustPolicies(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	// settings.json in .pi triggers HasTrustRequiringProjectResources(cwd).
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := core.SettingsManagerInMemory(&core.Settings{})

	trustTrue := true
	trusted, err := ResolveStartupProjectTrust(context.Background(), PromptProjectTrustOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		TrustOverride:   &trustTrue,
		HasUI:           false,
	})
	if err != nil || !trusted {
		t.Fatalf("TrustOverride=true got (%v, %v), want (true, nil)", trusted, err)
	}
	if !sm.IsProjectTrusted() {
		t.Fatal("expected SettingsManager.SetProjectTrusted(true) called")
	}

	trustFalse := false
	trusted, err = ResolveStartupProjectTrust(context.Background(), PromptProjectTrustOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		TrustOverride:   &trustFalse,
		HasUI:           false,
	})
	if err != nil || trusted {
		t.Fatalf("TrustOverride=false got (%v, %v), want (false, nil)", trusted, err)
	}
	if sm.IsProjectTrusted() {
		t.Fatal("expected SettingsManager.SetProjectTrusted(false) called")
	}

	// Default policy "ask" with HasUI=false defaults to untrusted without prompting.
	askPolicy := core.DefaultProjectTrustAsk
	smAsk := core.SettingsManagerInMemory(&core.Settings{DefaultProjectTrust: &askPolicy})
	trusted, err = ResolveStartupProjectTrust(context.Background(), PromptProjectTrustOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: smAsk,
		Mode:            core.ExtensionModePrint,
		HasUI:           false,
	})
	if err != nil || trusted {
		t.Fatalf("ask+noUI got (%v, %v), want (false, nil)", trusted, err)
	}

	// Default policy "always" trusts without prompting even when HasUI=false.
	alwaysPolicy := core.DefaultProjectTrustAlways
	smAlways := core.SettingsManagerInMemory(&core.Settings{DefaultProjectTrust: &alwaysPolicy})
	trusted, err = ResolveStartupProjectTrust(context.Background(), PromptProjectTrustOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: smAlways,
		Mode:            core.ExtensionModePrint,
		HasUI:           false,
	})
	if err != nil || !trusted {
		t.Fatalf("always+noUI got (%v, %v), want (true, nil)", trusted, err)
	}
}

func TestResolveStartupProjectTrustInteractivePromptAndNotify(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	var errBuf bytes.Buffer
	uiCtx := NewCliProjectTrustUI(&errBuf, nil)
	uiCtx.Notify("project warning", "warning")
	if errBuf.String() != "warning: project warning\n" {
		t.Fatalf("Notify output = %q, want %q", errBuf.String(), "warning: project warning\n")
	}

	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "settings.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	askPolicy := core.DefaultProjectTrustAsk
	sm := core.SettingsManagerInMemory(&core.Settings{DefaultProjectTrust: &askPolicy})
	term := tptNewFakeTerminal()

	type result struct {
		trusted bool
		err     error
	}
	ch := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		trusted, err := ResolveStartupProjectTrust(ctx, PromptProjectTrustOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: sm,
			Mode:            core.ExtensionModeTui,
			HasUI:           true,
			ErrOut:          &errBuf,
			UiOptions: &StartupUiOptions{
				AgentDir:               agentDir,
				Terminal:               term,
				DisableThemeController: true,
			},
		})
		ch <- result{trusted: trusted, err: err}
	}()

	// Option 0 in GetProjectTrustOptions is trust/yes; confirm with Enter.
	term.waitReadyAndSend(t, "\r")
	res := <-ch
	if res.err != nil || !res.trusted {
		t.Fatalf("interactive ResolveStartupProjectTrust = (%v, %v), want (true, nil)", res.trusted, res.err)
	}
}
