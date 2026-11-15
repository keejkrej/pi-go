// Ported from packages/coding-agent/src/cli/session-picker.ts (pi v1.5.2).

package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

type tspFakeTerminal struct {
	mu      sync.Mutex
	onInput func(string)
	writes  []string
	writeCh chan struct{}
}

func tspNewFakeTerminal() *tspFakeTerminal {
	return &tspFakeTerminal{writeCh: make(chan struct{}, 64)}
}

func (t *tspFakeTerminal) Start(onInput func(data string), _ func()) {
	t.mu.Lock()
	t.onInput = onInput
	t.mu.Unlock()
}

func (t *tspFakeTerminal) Stop()                              {}
func (t *tspFakeTerminal) DrainInput(_, _ *int64) error       { return nil }
func (t *tspFakeTerminal) Columns() int                       { return 100 }
func (t *tspFakeTerminal) Rows() int                          { return 30 }
func (t *tspFakeTerminal) KittyProtocolActive() bool          { return false }
func (t *tspFakeTerminal) MoveBy(int)                         {}
func (t *tspFakeTerminal) HideCursor()                        {}
func (t *tspFakeTerminal) ShowCursor()                        {}
func (t *tspFakeTerminal) ClearLine()                         {}
func (t *tspFakeTerminal) ClearFromCursor()                   {}
func (t *tspFakeTerminal) ClearScreen()                       {}
func (t *tspFakeTerminal) SetTitle(string)                    {}
func (t *tspFakeTerminal) SetProgress(bool)                   {}
func (t *tspFakeTerminal) SetProgramStatus(tui.ProgramStatus) {}

func (t *tspFakeTerminal) Write(data string) {
	t.mu.Lock()
	t.writes = append(t.writes, data)
	t.mu.Unlock()
	select {
	case t.writeCh <- struct{}{}:
	default:
	}
}

func (t *tspFakeTerminal) waitForOutput(tb testing.TB, substr string) {
	tb.Helper()
	deadline := time.After(3 * time.Second)
	for {
		t.mu.Lock()
		fn := t.onInput
		var joined strings.Builder
		for _, w := range t.writes {
			joined.WriteString(w)
		}
		matched := fn != nil && len(t.writes) > 0 && (substr == "" || strings.Contains(joined.String(), substr))
		t.mu.Unlock()
		if matched {
			return
		}
		select {
		case <-t.writeCh:
		case <-deadline:
			tb.Fatalf("timed out waiting for session picker output %q", substr)
		}
	}
}

func (t *tspFakeTerminal) send(key string) {
	t.mu.Lock()
	fn := t.onInput
	t.mu.Unlock()
	if fn != nil {
		fn(key)
	}
}

func TestSelectSessionPickAndCancel(t *testing.T) {
	t.Cleanup(func() { _, _ = theme.InitTheme("dark") })
	_, _ = theme.InitTheme("dark")

	now := time.Now()
	sessions := []core.SessionInfo{
		{
			Path:         "/tmp/sessions/sess-1.jsonl",
			Id:           "sess-1",
			Cwd:          "/tmp/proj",
			Created:      now.Add(-time.Hour),
			Modified:     now,
			MessageCount: 2,
			FirstMessage: "first session prompt",
		},
		{
			Path:         "/tmp/sessions/sess-2.jsonl",
			Id:           "sess-2",
			Cwd:          "/tmp/proj",
			Created:      now.Add(-2 * time.Hour),
			Modified:     now.Add(-time.Hour),
			MessageCount: 1,
			FirstMessage: "older session prompt",
		},
	}

	loader := func(_ core.SessionListProgress, _ context.Context) ([]core.SessionInfo, error) {
		return sessions, nil
	}

	t.Run("select_first", func(t *testing.T) {
		term := tspNewFakeTerminal()
		type out struct {
			path *string
			err  error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			p, err := SelectSession(ctx, loader, loader, &SessionPickerOptions{
				AgentDir:         t.TempDir(),
				Terminal:         term,
				DisableThemeInit: true,
			})
			ch <- out{path: p, err: err}
		}()

		term.waitForOutput(t, "first session prompt")
		term.send("\r")
		res := <-ch
		if res.err != nil {
			t.Fatalf("SelectSession error: %v", res.err)
		}
		if res.path == nil || *res.path != "/tmp/sessions/sess-1.jsonl" {
			t.Fatalf("selected path = %v, want %q", res.path, "/tmp/sessions/sess-1.jsonl")
		}
	})

	t.Run("cancel_escape", func(t *testing.T) {
		term := tspNewFakeTerminal()
		type out struct {
			path *string
			err  error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			p, err := SelectSession(ctx, loader, loader, &SessionPickerOptions{
				AgentDir:         t.TempDir(),
				Terminal:         term,
				DisableThemeInit: true,
			})
			ch <- out{path: p, err: err}
		}()

		term.waitForOutput(t, "first session prompt")
		term.send("\x1b")
		res := <-ch
		if res.err != nil || res.path != nil {
			t.Fatalf("cancel got (%v, %v), want (nil, nil)", res.path, res.err)
		}
	})

	t.Run("context_cancel", func(t *testing.T) {
		term := tspNewFakeTerminal()
		type out struct {
			path *string
			err  error
		}
		ch := make(chan out, 1)
		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			p, err := SelectSession(ctx, loader, loader, &SessionPickerOptions{
				AgentDir:         t.TempDir(),
				Terminal:         term,
				DisableThemeInit: true,
			})
			ch <- out{path: p, err: err}
		}()

		term.waitForOutput(t, "")
		cancel()
		res := <-ch
		if !errors.Is(res.err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", res.err)
		}
	})
}
