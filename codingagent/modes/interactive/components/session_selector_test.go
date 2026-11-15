// Ported from packages/coding-agent/test/session-selector-path-delete.test.ts and
// packages/coding-agent/test/session-selector-rename.test.ts (pi v1.5.2).

package components

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

const (
	tssCtrlD         = "\x04"
	tssCtrlR         = "\x1b[114;5u"
	tssCtrlBackspace = "\x1b[127;5u"
)

func tssInitEnv(t *testing.T) {
	t.Helper()
	theme.InitTheme("dark", false)
	tui.SetKeybindings(core.NewKeybindingsManager(nil).KeybindingsManager)
}

func tssStrPtr(s string) *string { return &s }

func tssBoolPtr(b bool) *bool { return &b }

func tssWaitUntil(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func tssMakeSession(id string, opts ...func(*core.SessionInfo)) core.SessionInfo {
	s := core.SessionInfo{
		Path:            "/tmp/" + id + ".jsonl",
		Id:              id,
		Cwd:             "",
		Created:         time.Unix(0, 0),
		Modified:        time.Unix(0, 0),
		MessageCount:    1,
		FirstMessage:    "hello",
		AllMessagesText: "hello",
	}
	for _, fn := range opts {
		fn(&s)
	}
	return s
}

func tssCreateSymlinkedSessionPaths(t *testing.T) (parentAliasA, parentAliasB, childAliasB string) {
	t.Helper()
	baseDir := t.TempDir()
	realDir := filepath.Join(baseDir, "real")
	aliasADir := filepath.Join(baseDir, "alias-a")
	aliasBDir := filepath.Join(baseDir, "alias-b")
	sharedDir := filepath.Join(realDir, "sessions")
	for _, dir := range []string{sharedDir, aliasADir, aliasBDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	aliasASessions := filepath.Join(aliasADir, "sessions")
	aliasBSessions := filepath.Join(aliasBDir, "sessions")
	if err := os.Symlink(sharedDir, aliasASessions); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sharedDir, aliasBSessions); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "parent.jsonl"), []byte("parent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedDir, "child.jsonl"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(aliasASessions, "parent.jsonl"),
		filepath.Join(aliasBSessions, "parent.jsonl"),
		filepath.Join(aliasBSessions, "child.jsonl")
}

func tssNewSelectorAndWait(
	t *testing.T,
	currentLoader, allLoader SessionsLoader,
	opts *SessionSelectorOptions,
	currentSessionFilePath ...string,
) *SessionSelectorComponent {
	t.Helper()
	var currentDone atomic.Bool
	wrappedCurrent := func(onProgress core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error) {
		defer currentDone.Store(true)
		return currentLoader(onProgress, ctx)
	}
	sel := NewSessionSelectorComponent(
		wrappedCurrent,
		allLoader,
		func(string) {},
		func() {},
		func() {},
		func() {},
		opts,
		currentSessionFilePath...,
	)
	t.Cleanup(sel.Dispose)
	tssWaitUntil(t, "initial current load", func() bool {
		return currentDone.Load() && !sel.headerLoading()
	})
	return sel
}

func (s *SessionSelectorComponent) headerLoading() bool {
	s.mu.Lock()
	isLoading := s.currentLoad != nil
	s.mu.Unlock()
	return isLoading
}

func TestSessionSelectorPathDelete(t *testing.T) {
	tssInitEnv(t)
	kb := core.NewKeybindingsManager(nil)

	t.Run("does not treat Ctrl+Backspace as delete when search query is non-empty", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a"), tssMakeSession("b")}
		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		var changes []string
		list.OnDeleteConfirmationChange = func(path *string) {
			if path == nil {
				changes = append(changes, "")
			} else {
				changes = append(changes, *path)
			}
		}

		list.HandleInput("a")
		list.HandleInput(tssCtrlBackspace)

		if len(changes) != 0 {
			t.Fatalf("changes = %v, want empty", changes)
		}
	})

	t.Run("enters confirmation mode on Ctrl+D even with a non-empty search query", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a"), tssMakeSession("b")}
		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		var changes []string
		list.OnDeleteConfirmationChange = func(path *string) {
			if path == nil {
				changes = append(changes, "")
			} else {
				changes = append(changes, *path)
			}
		}

		list.HandleInput("a")
		list.HandleInput(tssCtrlD)

		if !slices.Equal(changes, []string{sessions[0].Path}) {
			t.Fatalf("changes = %v, want [%q]", changes, sessions[0].Path)
		}
	})

	t.Run("enters confirmation mode on Ctrl+Backspace when search query is empty", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a"), tssMakeSession("b")}
		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		var changes []string
		list.OnDeleteConfirmationChange = func(path *string) {
			if path == nil {
				changes = append(changes, "nil")
			} else {
				changes = append(changes, *path)
			}
		}
		var deletedPath string
		list.OnDeleteSession = func(sessionPath string) {
			deletedPath = sessionPath
		}

		list.HandleInput(tssCtrlBackspace)
		if !slices.Equal(changes, []string{sessions[0].Path}) {
			t.Fatalf("changes = %v, want [%q]", changes, sessions[0].Path)
		}

		list.HandleInput("\r")
		if !slices.Equal(changes, []string{sessions[0].Path, "nil"}) {
			t.Fatalf("changes = %v", changes)
		}
		if deletedPath != sessions[0].Path {
			t.Fatalf("deletedPath = %q, want %q", deletedPath, sessions[0].Path)
		}
	})

	t.Run("does not switch scope back to All when All load resolves after toggling back to Current", func(t *testing.T) {
		currentSessions := []core.SessionInfo{tssMakeSession("current")}
		releaseAll := make(chan struct{})
		allDone := make(chan struct{})
		var allCalls atomic.Int32

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) {
				return currentSessions, nil
			},
			func(_ core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error) {
				allCalls.Add(1)
				defer close(allDone)
				select {
				case <-releaseAll:
					return []core.SessionInfo{tssMakeSession("all")}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		list.HandleInput("\t")
		tssWaitUntil(t, "all load started", func() bool { return allCalls.Load() == 1 })
		list.HandleInput("\t")

		close(releaseAll)
		<-allDone
		time.Sleep(15 * time.Millisecond)

		if got := allCalls.Load(); got != 1 {
			t.Fatalf("allCalls = %d, want 1", got)
		}
		output := strings.Join(sel.Render(120), "\n")
		if !strings.Contains(output, "Resume Session (Current Folder)") || strings.Contains(output, "Resume Session (All)") {
			t.Fatalf("unexpected header state:\n%s", output)
		}
	})

	t.Run("does not start redundant All loads when toggling scopes while All is already loading", func(t *testing.T) {
		currentSessions := []core.SessionInfo{tssMakeSession("current")}
		allSessions := []core.SessionInfo{tssMakeSession("all")}
		releaseAll := make(chan struct{})
		progressSent := make(chan struct{})
		var allCalls atomic.Int32

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) {
				return currentSessions, nil
			},
			func(onProgress core.SessionListProgress, ctx context.Context) ([]core.SessionInfo, error) {
				allCalls.Add(1)
				if onProgress != nil {
					onProgress(1, 2, allSessions)
				}
				close(progressSent)
				select {
				case <-releaseAll:
					return allSessions, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		list.HandleInput("\t")
		<-progressSent
		list.HandleInput("\t")
		list.HandleInput("\t")

		if got := allCalls.Load(); got != 1 {
			t.Fatalf("allCalls = %d, want 1", got)
		}
		selected := sel.GetSessionList().GetSelectedSessionPath()
		if selected == nil || *selected != allSessions[0].Path {
			t.Fatalf("selected = %v, want %q", selected, allSessions[0].Path)
		}
		if output := strings.Join(sel.Render(120), "\n"); !strings.Contains(output, "Loading") {
			t.Fatalf("expected Loading indicator:\n%s", output)
		}

		close(releaseAll)
		tssWaitUntil(t, "all load finish", func() bool {
			return !strings.Contains(strings.Join(sel.Render(120), "\n"), "Loading")
		})
	})

	t.Run("threads sessions when parent and child paths use different symlink aliases", func(t *testing.T) {
		parentAliasA, parentAliasB, childAliasB := tssCreateSymlinkedSessionPaths(t)
		sessions := []core.SessionInfo{
			tssMakeSession("parent", func(s *core.SessionInfo) {
				s.Path = parentAliasB
				s.Name = tssStrPtr("Parent")
				s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			}),
			tssMakeSession("child", func(s *core.SessionInfo) {
				s.Path = childAliasB
				s.ParentSessionPath = tssStrPtr(parentAliasA)
				s.Name = tssStrPtr("Child")
				s.Modified = time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
			}),
		}

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		output := utils.StripAnsi(strings.Join(sel.Render(120), "\n"))
		if !strings.Contains(output, "Parent") || !strings.Contains(output, "└─ Child") {
			t.Fatalf("expected threaded output:\n%s", output)
		}
	})

	t.Run("sorts threaded sessions by latest activity in their subtree", func(t *testing.T) {
		parentOne := tssMakeSession("parent-one", func(s *core.SessionInfo) {
			s.Name = tssStrPtr("Parent one")
			s.Modified = time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		})
		parentTwo := tssMakeSession("parent-two", func(s *core.SessionInfo) {
			s.Name = tssStrPtr("Parent two")
			s.Modified = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		})
		childTwo := tssMakeSession("child-two", func(s *core.SessionInfo) {
			s.Name = tssStrPtr("Child two")
			s.ParentSessionPath = tssStrPtr(parentTwo.Path)
			s.Modified = time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
		})

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) {
				return []core.SessionInfo{parentOne, parentTwo, childTwo}, nil
			},
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		output := utils.StripAnsi(strings.Join(sel.Render(120), "\n"))
		parentTwoIdx := strings.Index(output, "Parent two")
		childTwoIdx := strings.Index(output, "└─ Child two")
		parentOneIdx := strings.Index(output, "Parent one")
		if parentTwoIdx < 0 || childTwoIdx <= parentTwoIdx || parentOneIdx <= childTwoIdx {
			t.Fatalf("unexpected ordering:\n%s", output)
		}
	})

	t.Run("treats the current session as active across symlink aliases", func(t *testing.T) {
		parentAliasA, parentAliasB, _ := tssCreateSymlinkedSessionPaths(t)
		sessions := []core.SessionInfo{
			tssMakeSession("parent", func(s *core.SessionInfo) {
				s.Path = parentAliasB
				s.Name = tssStrPtr("Parent")
			}),
		}

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
			parentAliasA,
		)

		list := sel.GetSessionList()
		var confirmationChanges []string
		var errorMessage string
		list.OnDeleteConfirmationChange = func(p *string) {
			if p != nil {
				confirmationChanges = append(confirmationChanges, *p)
			}
		}
		list.OnError = func(msg string) {
			errorMessage = msg
		}

		list.HandleInput(tssCtrlD)
		if len(confirmationChanges) != 0 {
			t.Fatalf("confirmationChanges = %v, want empty", confirmationChanges)
		}
		if errorMessage != "Cannot delete the currently active session" {
			t.Fatalf("errorMessage = %q", errorMessage)
		}
	})

	t.Run("keeps the next session selected when a progressive reload after delete starts with another session", func(t *testing.T) {
		dir := t.TempDir()
		sessions := make([]core.SessionInfo, 5)
		for i := range 5 {
			p := filepath.Join(dir, "s"+string(rune('1'+i))+".jsonl")
			if err := os.WriteFile(p, []byte("session\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			sessions[i] = tssMakeSession("s"+string(rune('1'+i)), func(s *core.SessionInfo) {
				s.Path = p
				s.Modified = time.UnixMilli(int64(10_000 - (i+1)*1000))
			})
		}

		sel := tssNewSelectorAndWait(
			t,
			func(onProgress core.SessionListProgress, _ context.Context) ([]core.SessionInfo, error) {
				var remaining []core.SessionInfo
				for _, s := range sessions {
					if _, err := os.Stat(s.Path); err == nil {
						remaining = append(remaining, s)
					}
				}
				if onProgress != nil && len(remaining) > 0 {
					onProgress(1, len(remaining), []core.SessionInfo{remaining[len(remaining)-1]})
					onProgress(len(remaining), len(remaining), remaining)
				}
				return remaining, nil
			},
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{Keybindings: kb},
		)

		list := sel.GetSessionList()
		list.HandleInput("\x1b[B")
		list.HandleInput("\x1b[A")
		if got := list.GetSelectedSessionPath(); got == nil || *got != sessions[0].Path {
			t.Fatalf("selected = %v, want %q", got, sessions[0].Path)
		}

		list.HandleInput(tssCtrlD)
		list.HandleInput("\r")

		tssWaitUntil(t, "deleted file removed and reloaded", func() bool {
			if _, err := os.Stat(sessions[0].Path); err == nil {
				return false
			}
			selPath := list.GetSelectedSessionPath()
			return selPath != nil && *selPath == sessions[1].Path && !sel.headerLoading()
		})
	})
}

func TestSessionSelectorRename(t *testing.T) {
	tssInitEnv(t)
	kb := core.NewKeybindingsManager(nil)

	t.Run("shows rename hint in interactive /resume picker configuration", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a")}
		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{ShowRenameHint: tssBoolPtr(true), Keybindings: kb},
		)

		output := strings.Join(sel.Render(120), "\n")
		if !strings.Contains(output, "ctrl+r") || !strings.Contains(output, "rename") {
			t.Fatalf("expected rename hint in output:\n%s", output)
		}
	})

	t.Run("does not show rename hint in --resume picker configuration", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a")}
		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{ShowRenameHint: tssBoolPtr(false), Keybindings: kb},
		)

		output := strings.Join(sel.Render(120), "\n")
		if strings.Contains(output, "ctrl+r") || strings.Contains(output, "rename") {
			t.Fatalf("did not expect rename hint in output:\n%s", output)
		}
	})

	t.Run("enters rename mode on Ctrl+R and submits with Enter", func(t *testing.T) {
		sessions := []core.SessionInfo{tssMakeSession("a", func(s *core.SessionInfo) { s.Name = tssStrPtr("Old") })}
		var renamedPath, renamedNext string
		var renameCalls atomic.Int32

		sel := tssNewSelectorAndWait(
			t,
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return sessions, nil },
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{
				RenameSession: func(_ context.Context, path string, next *string) error {
					renamedPath = path
					if next != nil {
						renamedNext = *next
					}
					renameCalls.Add(1)
					return nil
				},
				ShowRenameHint: tssBoolPtr(true),
				Keybindings:    kb,
			},
		)

		sel.GetSessionList().HandleInput(tssCtrlR)
		output := strings.Join(sel.Render(120), "\n")
		if !strings.Contains(output, "Rename Session") || strings.Contains(output, "Resume Session") {
			t.Fatalf("expected rename mode layout:\n%s", output)
		}

		sel.HandleInput("X")
		sel.HandleInput("\r")
		tssWaitUntil(t, "rename completion and exit rename mode", func() bool {
			return renameCalls.Load() == 1 && sel.CapturesViewportInput()
		})

		if renamedPath != sessions[0].Path || renamedNext != "XOld" {
			t.Fatalf("rename call = (%q, %q), want (%q, %q)", renamedPath, renamedNext, sessions[0].Path, "XOld")
		}
	})

	t.Run("keeps the renamed session selected when the reload starts with another session", func(t *testing.T) {
		sessions := make([]core.SessionInfo, 5)
		for i := range 5 {
			sessions[i] = tssMakeSession("s"+string(rune('1'+i)), func(s *core.SessionInfo) {
				s.Modified = time.UnixMilli(int64(10_000 - (i+1)*1000))
			})
		}
		var loads atomic.Int32
		sel := tssNewSelectorAndWait(
			t,
			func(onProgress core.SessionListProgress, _ context.Context) ([]core.SessionInfo, error) {
				n := loads.Add(1)
				if n > 1 && onProgress != nil {
					onProgress(1, len(sessions), []core.SessionInfo{sessions[4]})
				}
				return sessions, nil
			},
			func(core.SessionListProgress, context.Context) ([]core.SessionInfo, error) { return nil, nil },
			&SessionSelectorOptions{
				RenameSession:  func(context.Context, string, *string) error { return nil },
				ShowRenameHint: tssBoolPtr(true),
				Keybindings:    kb,
			},
		)

		list := sel.GetSessionList()
		list.HandleInput("\x1b[B")
		list.HandleInput("\x1b[B")
		if got := list.GetSelectedSessionPath(); got == nil || *got != sessions[2].Path {
			t.Fatalf("selected = %v, want %q", got, sessions[2].Path)
		}

		list.HandleInput(tssCtrlR)
		sel.HandleInput("X")
		sel.HandleInput("\r")
		tssWaitUntil(t, "reload after rename completed", func() bool {
			return loads.Load() == 2 && sel.CapturesViewportInput() && !sel.headerLoading()
		})

		if got := sel.GetSessionList().GetSelectedSessionPath(); got == nil || *got != sessions[2].Path {
			t.Fatalf("selected after reload = %v, want %q", got, sessions[2].Path)
		}
	})
}
