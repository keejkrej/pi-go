// Ported from packages/coding-agent/src/cli/config-selector.ts (pi v1.5.2).

package cli

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tcsFakeTerminal struct {
	mu      sync.Mutex
	onInput func(string)
	writes  []string
	writeCh chan struct{}
}

func tcsNewFakeTerminal() *tcsFakeTerminal {
	return &tcsFakeTerminal{writeCh: make(chan struct{}, 32)}
}

func (t *tcsFakeTerminal) Start(onInput func(data string), _ func()) {
	t.mu.Lock()
	t.onInput = onInput
	t.mu.Unlock()
}

func (t *tcsFakeTerminal) Stop()                              {}
func (t *tcsFakeTerminal) DrainInput(_, _ *int64) error       { return nil }
func (t *tcsFakeTerminal) Columns() int                       { return 100 }
func (t *tcsFakeTerminal) Rows() int                          { return 30 }
func (t *tcsFakeTerminal) KittyProtocolActive() bool          { return false }
func (t *tcsFakeTerminal) MoveBy(int)                         {}
func (t *tcsFakeTerminal) HideCursor()                        {}
func (t *tcsFakeTerminal) ShowCursor()                        {}
func (t *tcsFakeTerminal) ClearLine()                         {}
func (t *tcsFakeTerminal) ClearFromCursor()                   {}
func (t *tcsFakeTerminal) ClearScreen()                       {}
func (t *tcsFakeTerminal) SetTitle(string)                    {}
func (t *tcsFakeTerminal) SetProgress(bool)                   {}
func (t *tcsFakeTerminal) SetProgramStatus(tui.ProgramStatus) {}

func (t *tcsFakeTerminal) Write(data string) {
	t.mu.Lock()
	t.writes = append(t.writes, data)
	t.mu.Unlock()
	select {
	case t.writeCh <- struct{}{}:
	default:
	}
}

func (t *tcsFakeTerminal) waitReadyAndSend(tb testing.TB, key string) {
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
			tb.Fatal("timed out waiting for config selector render")
		}
	}
}

func TestSelectConfigCloseExitAndCancel(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	cwd := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "agent")
	sm := core.SettingsManagerInMemory(&core.Settings{})
	scoped := components.ScopedResolvedPaths{
		Global: core.ResolvedPaths{
			Skills: []core.ResolvedResource{
				{
					Path:    filepath.Join(agentDir, "skills", "demo", "SKILL.md"),
					Enabled: true,
					Metadata: utils.PathMetadata{
						Source:  "user",
						Scope:   utils.SourceScopeUser,
						Origin:  utils.SourceOriginTopLevel,
						BaseDir: agentDir,
					},
				},
			},
		},
	}

	t.Run("close_escape", func(t *testing.T) {
		term := tcsNewFakeTerminal()
		errCh := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			errCh <- SelectConfig(ctx, scoped, sm, cwd, agentDir, components.ConfigWriteScopeGlobal, false, &ConfigSelectorOptions{
				Terminal:         term,
				DisableThemeInit: true,
			})
		}()

		term.waitReadyAndSend(t, "\x1b")
		if err := <-errCh; err != nil {
			t.Fatalf("SelectConfig close error: %v", err)
		}
	})

	t.Run("exit_ctrl_c", func(t *testing.T) {
		term := tcsNewFakeTerminal()
		errCh := make(chan error, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			errCh <- SelectConfig(ctx, scoped, sm, cwd, agentDir, components.ConfigWriteScopeGlobal, true, &ConfigSelectorOptions{
				Terminal:         term,
				DisableThemeInit: true,
			})
		}()

		term.waitReadyAndSend(t, "\x03")
		if err := <-errCh; !errors.Is(err, ErrConfigSelectorExit) {
			t.Fatalf("SelectConfig exit = %v, want ErrConfigSelectorExit", err)
		}
	})

	t.Run("context_cancel", func(t *testing.T) {
		term := tcsNewFakeTerminal()
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			errCh <- SelectConfig(ctx, scoped, sm, cwd, agentDir, components.ConfigWriteScopeGlobal, false, &ConfigSelectorOptions{
				Terminal:         term,
				DisableThemeInit: true,
			})
		}()

		term.waitReadyAndSend(t, "")
		cancel()
		if err := <-errCh; !errors.Is(err, context.Canceled) {
			t.Fatalf("SelectConfig cancel = %v, want context.Canceled", err)
		}
	})
}
