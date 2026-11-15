package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestResolveEscapeTimeoutMs(t *testing.T) {
	t.Run("explicit PI_TUI_ESC_TIMEOUT wins over SSH and default", func(t *testing.T) {
		got := ResolveEscapeTimeoutMs(map[string]string{
			"PI_TUI_ESC_TIMEOUT": "250",
			"SSH_CONNECTION":     "1.2.3.4 22",
		})
		if got != 250 {
			t.Fatalf("got %d, want 250", got)
		}
	})

	t.Run("invalid or non-positive PI_TUI_ESC_TIMEOUT falls through", func(t *testing.T) {
		for _, bad := range []string{"0", "-10", "abc", "Infinity", "NaN", "", "   "} {
			got := ResolveEscapeTimeoutMs(map[string]string{"PI_TUI_ESC_TIMEOUT": bad})
			if got != 10 {
				t.Fatalf("bad=%q: got %d, want 10", bad, got)
			}
		}
	})

	t.Run("SSH_CONNECTION or SSH_TTY selects 100ms", func(t *testing.T) {
		if got := ResolveEscapeTimeoutMs(map[string]string{"SSH_CONNECTION": "1.2.3.4"}); got != 100 {
			t.Fatalf("SSH_CONNECTION: got %d, want 100", got)
		}
		if got := ResolveEscapeTimeoutMs(map[string]string{"SSH_TTY": "/dev/pts/1"}); got != 100 {
			t.Fatalf("SSH_TTY: got %d, want 100", got)
		}
	})

	t.Run("nil env reads process environment; empty map does not", func(t *testing.T) {
		t.Setenv("PI_TUI_ESC_TIMEOUT", "77")
		if got := ResolveEscapeTimeoutMs(nil); got != 77 {
			t.Fatalf("nil env: got %d, want 77", got)
		}
		if got := ResolveEscapeTimeoutMs(map[string]string{}); got != 10 {
			t.Fatalf("empty map: got %d, want 10", got)
		}
	})
}

func TestKeyboardProtocolNegotiationParsing(t *testing.T) {
	if p := ParseKeyboardProtocolNegotiationSequence("\x1b[?7u"); p == nil {
		t.Fatal("expected kitty-flags parse")
	} else if kf, ok := p.(*KeyboardProtocolNegotiationSequenceKittyFlags); !ok || kf.Flags != 7 {
		t.Fatalf("got %#v", p)
	}

	if p := ParseKeyboardProtocolNegotiationSequence("\x1b[?0u"); p == nil {
		t.Fatal("expected kitty-flags 0 parse")
	} else if kf, ok := p.(*KeyboardProtocolNegotiationSequenceKittyFlags); !ok || kf.Flags != 0 {
		t.Fatalf("got %#v", p)
	}

	if p := ParseKeyboardProtocolNegotiationSequence("\x1b[?64;1;2;6;9;15c"); p == nil {
		t.Fatal("expected device-attributes parse")
	} else if _, ok := p.(*KeyboardProtocolNegotiationSequenceDeviceAttributes); !ok {
		t.Fatalf("got %#v", p)
	}

	if p := ParseKeyboardProtocolNegotiationSequence("\x1b[A"); p != nil {
		t.Fatalf("expected nil for arrow key, got %#v", p)
	}

	for _, pref := range []string{"\x1b[", "\x1b[?", "\x1b[?7", "\x1b[?64;1"} {
		if !termIsKeyboardProtocolNegotiationSequencePrefix(pref) {
			t.Fatalf("expected prefix true for %q", pref)
		}
	}
	for _, nonPref := range []string{"\x1b", "\x1b[A", "\x1b[?7u", "\x1b[?1c"} {
		if termIsKeyboardProtocolNegotiationSequencePrefix(nonPref) {
			t.Fatalf("expected prefix false for %q", nonPref)
		}
	}
}

func TestNormalizeNativeShiftEnterInput(t *testing.T) {
	if got := NormalizeNativeShiftEnterInput("\r", true, true); got != "\x1b[13;2u" {
		t.Fatalf("CR with shift: got %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\x1b\r", true, true); got != "\x1b[13;2u" {
		t.Fatalf("ESC+CR with shift: got %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\r", true, false); got != "\r" {
		t.Fatalf("CR without shift: got %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("\r", false, true); got != "\r" {
		t.Fatalf("CR detection off: got %q", got)
	}
	if got := NormalizeNativeShiftEnterInput("a", true, true); got != "a" {
		t.Fatalf("plain key: got %q", got)
	}
	if got := NormalizeAppleTerminalInput("\r", true, true); got != "\x1b[13;2u" {
		t.Fatalf("AppleTerminal: got %q", got)
	}
}

type testTermHarness struct {
	term     *ProcessTerminal
	mu       sync.Mutex
	writes   []string
	syncOuts []string
	inputs   []string
	env      map[string]string
}

func newTestTermHarness(env map[string]string) *testTermHarness {
	h := &testTermHarness{env: env}
	if h.env == nil {
		h.env = map[string]string{}
	}
	pt := &ProcessTerminal{
		skipOSIO: true,
		getenvFn: func(k string) string {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.env[k]
		},
		writeOutFn: func(s string) {
			h.mu.Lock()
			h.writes = append(h.writes, s)
			h.mu.Unlock()
		},
		writeSyncFn: func(s string) {
			h.mu.Lock()
			h.syncOuts = append(h.syncOuts, s)
			h.mu.Unlock()
		},
	}
	h.term = pt
	return h
}

func (h *testTermHarness) allWrites() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.writes, "")
}

func (h *testTermHarness) takeWrites() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Clone(h.writes)
	h.writes = nil
	return out
}

func (h *testTermHarness) takeInputs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Clone(h.inputs)
	h.inputs = nil
	return out
}

func TestProcessTerminalNegotiationAndProgramStatus(t *testing.T) {
	defer SetKittyProtocolActive(false)

	t.Run("Kitty flags enable Kitty protocol and ignore trailing DA1", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			SetKittyProtocolActive(false)
			h := newTestTermHarness(nil)
			h.term.Start(func(data string) {
				h.mu.Lock()
				h.inputs = append(h.inputs, data)
				h.mu.Unlock()
			}, nil)
			defer h.term.Stop()

			writes := h.takeWrites()
			if joined := strings.Join(writes, ""); joined != "\x1b[?2004h\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c" {
				t.Fatalf("unexpected startup writes: %q", joined)
			}

			// Simulate OSC 7501 reply, Kitty flags=7 reply, and DA1 reply.
			h.term.feedStdinChunk("\x1b]7501;?state=idle\x07\x1b[?7u\x1b[?64;1;2c")
			synctest.Wait()

			if !h.term.KittyProtocolActive() || !IsKittyProtocolActive() {
				t.Fatal("expected Kitty protocol to become active")
			}
			if h.term.ModifyOtherKeysActive() {
				t.Fatal("modifyOtherKeys must not be active when Kitty is supported")
			}
			if got := h.takeInputs(); len(got) != 0 {
				t.Fatalf("negotiation replies must not leak to onInput, got %q", got)
			}

			// SetProgramStatus works immediately once OSC 7501 was confirmed.
			h.term.SetProgramStatus(ProgramStatus{State: ProgramStatusStateWorking, App: "pi"})
			if got := strings.Join(h.takeWrites(), ""); got != "\x1b]7501;state=working:app=pi\x1b\\" {
				t.Fatalf("unexpected SetProgramStatus write: %q", got)
			}

			// Unexpected second DA1 after negotiation settled IS forwarded to onInput.
			h.term.feedStdinChunk("\x1b[?64;1c")
			synctest.Wait()
			if got := h.takeInputs(); !slices.Equal(got, []string{"\x1b[?64;1c"}) {
				t.Fatalf("expected extra DA1 forwarded, got %q", got)
			}
		})
	})

	t.Run("DA1 without Kitty enables modifyOtherKeys and clears pending status query", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			SetKittyProtocolActive(false)
			h := newTestTermHarness(nil)
			h.term.Start(func(data string) {
				h.mu.Lock()
				h.inputs = append(h.inputs, data)
				h.mu.Unlock()
			}, nil)
			defer h.term.Stop()
			h.takeWrites()

			h.term.feedStdinChunk("\x1b[?1;2c")
			synctest.Wait()

			if h.term.KittyProtocolActive() {
				t.Fatal("Kitty protocol must stay inactive")
			}
			if !h.term.ModifyOtherKeysActive() {
				t.Fatal("expected modifyOtherKeys fallback active")
			}
			if got := strings.Join(h.takeWrites(), ""); got != "\x1b[>4;2m" {
				t.Fatalf("expected modifyOtherKeys enable sequence, got %q", got)
			}

			// Status stays suppressed when no OSC 7501 reply preceded DA1.
			h.term.SetProgramStatus(ProgramStatus{State: ProgramStatusStateWorking})
			if got := h.takeWrites(); len(got) != 0 {
				t.Fatalf("expected no OSC 7501 write when unsupported, got %q", got)
			}
		})
	})

	t.Run("Kitty flags 0 falls back to modifyOtherKeys", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			SetKittyProtocolActive(false)
			h := newTestTermHarness(nil)
			h.term.Start(nil, nil)
			defer h.term.Stop()
			h.takeWrites()

			h.term.feedStdinChunk("\x1b[?0u\x1b[?1;2c")
			synctest.Wait()

			if h.term.KittyProtocolActive() {
				t.Fatal("expected Kitty inactive for flags=0")
			}
			if !h.term.ModifyOtherKeysActive() {
				t.Fatal("expected modifyOtherKeys active for flags=0")
			}
		})
	})

	t.Run("split Kitty reply reassembles and stale fragment flushes after 150ms", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			SetKittyProtocolActive(false)
			h := newTestTermHarness(nil)
			h.term.Start(func(data string) {
				h.mu.Lock()
				h.inputs = append(h.inputs, data)
				h.mu.Unlock()
			}, nil)
			defer h.term.Stop()

			// Split Kitty reply across chunks: StdinBuffer flushes "\x1b[?" after 50ms,
			// then ProcessTerminal holds it up to 150ms and reassembles with "7u".
			h.term.feedStdinChunk("\x1b[?")
			time.Sleep(60 * time.Millisecond)
			synctest.Wait()
			h.term.feedStdinChunk("7u")
			synctest.Wait()

			if !h.term.KittyProtocolActive() {
				t.Fatal("expected fragmented Kitty reply to reassemble")
			}
			if got := h.takeInputs(); len(got) != 0 {
				t.Fatalf("unexpected inputs after reassembly: %q", got)
			}

			// Now send an incomplete fragment "\x1b[?" and wait > 50ms (StdinBuffer) + 150ms (negotiation).
			h.term.feedStdinChunk("\x1b[?")
			time.Sleep(220 * time.Millisecond)
			synctest.Wait()

			if got := h.takeInputs(); !slices.Equal(got, []string{"\x1b[?"}) {
				t.Fatalf("expected stale fragment flushed as input, got %q", got)
			}

			// Incomplete fragment emitted by StdinBuffer (at 50ms) followed by unrelated input flushes in order.
			h.term.feedStdinChunk("\x1b[?")
			time.Sleep(60 * time.Millisecond)
			synctest.Wait()
			h.term.feedStdinChunk("x")
			synctest.Wait()
			if got := h.takeInputs(); !slices.Equal(got, []string{"\x1b[?", "x"}) {
				t.Fatalf("expected buffered prefix then 'x', got %q", got)
			}
		})
	})

	t.Run("PI_PROGRAM_STATUS=1 and 0 overrides and Stop/Start replay", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTestTermHarness(map[string]string{"PI_PROGRAM_STATUS": "1"})
			h.term.SetProgramStatus(ProgramStatus{State: ProgramStatusStateWorking, App: "pi"})
			if got := h.takeWrites(); len(got) != 0 {
				t.Fatalf("before Start(), should buffer status silently, got %q", got)
			}

			h.term.Start(nil, nil)
			gotStart := strings.Join(h.takeWrites(), "")
			wantStart := "\x1b[?2004h\x1b[>7u\x1b[?u\x1b[c\x1b]7501;state=working:app=pi\x1b\\"
			if gotStart != wantStart {
				t.Fatalf("Start with PI_PROGRAM_STATUS=1:\ngot:  %q\nwant: %q", gotStart, wantStart)
			}

			h.term.Stop()
			gotStop := strings.Join(h.takeWrites(), "")
			if !strings.Contains(gotStop, "\x1b]7501;state=clear\x1b\\") {
				t.Fatalf("expected Stop() to clear OSC 7501 status, got %q", gotStop)
			}

			// Starting again replays the stored status.
			h.term.Start(nil, nil)
			if got := strings.Join(h.takeWrites(), ""); !strings.Contains(got, "\x1b]7501;state=working:app=pi\x1b\\") {
				t.Fatalf("expected second Start() to replay status, got %q", got)
			}
			h.term.Stop()

			// PI_PROGRAM_STATUS=0 skips query and ignores SetProgramStatus.
			h0 := newTestTermHarness(map[string]string{"PI_PROGRAM_STATUS": "0"})
			h0.term.Start(nil, nil)
			defer h0.term.Stop()
			if got := strings.Join(h0.takeWrites(), ""); got != "\x1b[?2004h\x1b[>7u\x1b[?u\x1b[c" {
				t.Fatalf("PI_PROGRAM_STATUS=0 start writes: %q", got)
			}
			h0.term.SetProgramStatus(ProgramStatus{State: ProgramStatusStateIdle})
			if got := h0.takeWrites(); len(got) != 0 {
				t.Fatalf("PI_PROGRAM_STATUS=0 must not emit OSC 7501, got %q", got)
			}
		})
	})
}

func TestProcessTerminalProgressRestoreDrainAndHelpers(t *testing.T) {
	defer SetKittyProtocolActive(false)

	t.Run("SetProgress keepalive and Stop cleanup", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			h := newTestTermHarness(nil)
			h.term.Start(nil, nil)
			h.takeWrites()

			h.term.SetProgress(true)
			time.Sleep(2100 * time.Millisecond)
			synctest.Wait()

			writes := strings.Join(h.takeWrites(), "")
			if count := strings.Count(writes, termTerminalProgressActiveSequence); count != 3 {
				t.Fatalf("expected 3 progress active emissions (initial + 2 ticks), got %d (%q)", count, writes)
			}

			// Stop clears active progress automatically.
			h.term.Stop()
			if stopWrites := strings.Join(h.takeWrites(), ""); !strings.Contains(stopWrites, termTerminalProgressClearSequence) {
				t.Fatalf("expected Stop() to clear active progress, got %q", stopWrites)
			}

			// Starting and stopping without progress active does not emit progress clear.
			h.term.Start(nil, nil)
			h.takeWrites()
			h.term.Stop()
			if stopWrites := strings.Join(h.takeWrites(), ""); strings.Contains(stopWrites, termTerminalProgressClearSequence) {
				t.Fatalf("Stop() must not emit progress clear when inactive, got %q", stopWrites)
			}

			// SetProgress(false) stops ticker and writes clear sequence.
			h.term.SetProgress(true)
			h.takeWrites()
			h.term.SetProgress(false)
			if got := strings.Join(h.takeWrites(), ""); got != termTerminalProgressClearSequence {
				t.Fatalf("SetProgress(false) = %q", got)
			}
		})
	})

	t.Run("RestoreOnExit emits exact mode resets synchronously", func(t *testing.T) {
		h := newTestTermHarness(nil)
		h.term.Start(nil, nil)
		h.term.feedStdinChunk("\x1b[?7u")
		h.term.SetProgress(true)
		h.term.enableModifyOtherKeysLocked() // impossible during active Kitty unless forced; test forced modifyOtherKeys too:
		h.term.mu.Lock()
		h.term.modifyOtherKeysActive = true
		h.term.mu.Unlock()

		h.term.RestoreOnExit("[BEFORE]", "[AFTER]")
		want := "[BEFORE]" + termTerminalProgressClearSequence + "\x1b[?2004l\x1b[<u\x1b[>4;0m[AFTER]"
		if got := strings.Join(h.syncOuts, ""); got != want {
			t.Fatalf("RestoreOnExit:\ngot:  %q\nwant: %q", got, want)
		}
		h.term.Stop()
	})

	t.Run("DrainInput suppresses inputHandler, extends on chunks, caps at maxMs", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			SetKittyProtocolActive(false)
			h := newTestTermHarness(nil)
			h.term.Start(func(data string) {
				h.mu.Lock()
				h.inputs = append(h.inputs, data)
				h.mu.Unlock()
			}, nil)
			defer h.term.Stop()

			h.term.feedStdinChunk("\x1b[?7u")
			synctest.Wait()
			h.takeWrites()

			maxMs := int64(200)
			idleMs := int64(50)
			done := make(chan error, 1)
			go func() {
				done <- h.term.DrainInput(&maxMs, &idleMs)
			}()
			synctest.Wait()

			// Feeding late key release during drain must not reach onInput.
			time.Sleep(25 * time.Millisecond)
			h.term.feedStdinChunk("\x1b[13;3u")
			synctest.Wait()

			if err := <-done; err != nil {
				t.Fatalf("DrainInput err: %v", err)
			}
			if got := h.takeInputs(); len(got) != 0 {
				t.Fatalf("drained input leaked to onInput: %q", got)
			}
			if h.term.KittyProtocolActive() {
				t.Fatal("DrainInput must deactivate Kitty protocol")
			}

			// Normal input handler is restored after DrainInput completes.
			h.term.feedStdinChunk("a")
			synctest.Wait()
			if got := h.takeInputs(); !slices.Equal(got, []string{"a"}) {
				t.Fatalf("inputHandler not restored after DrainInput: %q", got)
			}
		})
	})

	t.Run("Native Shift+Enter rewriting on Apple Terminal", func(t *testing.T) {
		shiftHeld := true
		h := newTestTermHarness(map[string]string{"TERM_PROGRAM": "Apple_Terminal"})
		h.term.platformOS = "darwin"
		h.term.isNativeShiftFn = func() bool { return shiftHeld }
		h.term.Start(func(data string) {
			h.mu.Lock()
			h.inputs = append(h.inputs, data)
			h.mu.Unlock()
		}, nil)
		defer h.term.Stop()

		h.term.feedStdinChunk("\r")
		time.Sleep(30 * time.Millisecond) // allow any esc timer if needed
		shiftHeld = false
		h.term.feedStdinChunk("\r")

		if got := h.takeInputs(); !slices.Equal(got, []string{"\x1b[13;2u", "\r"}) {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("SetTitle sanitizes controls and Columns/Rows/WriteLog/MoveBy", func(t *testing.T) {
		logDir := t.TempDir()
		t.Setenv("PI_TUI_WRITE_LOG", logDir)
		pt := NewProcessTerminal()
		if !strings.HasPrefix(pt.writeLogPath, logDir+string(filepath.Separator)) {
			t.Fatalf("expected timestamped file inside %s, got %q", logDir, pt.writeLogPath)
		}
		var out strings.Builder
		pt.writeOutFn = func(s string) { out.WriteString(s) }
		pt.getSizeFn = func() (int, int, bool) { return 0, 0, false }
		pt.getenvFn = func(k string) string {
			switch k {
			case "COLUMNS":
				return "120"
			case "LINES":
				return "42"
			default:
				return ""
			}
		}

		if pt.Columns() != 120 || pt.Rows() != 42 {
			t.Fatalf("size=%dx%d, want 120x42", pt.Columns(), pt.Rows())
		}
		pt.SetTitle("hi\x07\x1bthere")
		pt.MoveBy(3)
		pt.MoveBy(-2)
		pt.MoveBy(0)
		pt.HideCursor()
		pt.ShowCursor()
		pt.ClearLine()
		pt.ClearFromCursor()
		pt.ClearScreen()
		pt.Write("FRAME")

		want := "\x1b]0;hi there\x07\x1b[3B\x1b[2A\x1b[?25l\x1b[?25h\x1b[K\x1b[J\x1b[2J\x1b[HFRAME"
		if out.String() != want {
			t.Fatalf("writes mismatch:\ngot:  %q\nwant: %q", out.String(), want)
		}
		logBytes, err := os.ReadFile(pt.writeLogPath)
		if err != nil || string(logBytes) != "FRAME" {
			t.Fatalf("writeLog=%q err=%v", string(logBytes), err)
		}
	})

	t.Run("termDecodeUTF8Chunk buffers split multi-byte runes cleanly", func(t *testing.T) {
		var pending []byte
		emoji := []byte("😀")
		if got := termDecodeUTF8Chunk(emoji[:2], &pending); got != "" {
			t.Fatalf("first half emitted %q", got)
		}
		if got := termDecodeUTF8Chunk(emoji[2:], &pending); got != "😀" {
			t.Fatalf("second half emitted %q", got)
		}
	})
}
