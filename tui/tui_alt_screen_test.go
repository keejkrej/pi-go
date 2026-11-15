package tui

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
)

func tasTestUnsetEnvs(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		prev, ok := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("Unsetenv(%q): %v", k, err)
		}
		key, hadPrev, oldVal := k, ok, prev
		t.Cleanup(func() {
			if hadPrev {
				_ = os.Setenv(key, oldVal)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

type tasRestoreCall struct {
	before string
	after  string
}

type tasTestTerminal struct {
	mu           sync.Mutex
	cols         int
	rows         int
	writes       []string
	restoreCalls []tasRestoreCall
	onInput      func(string)
}

func newTasTestTerminal(cols, rows int) *tasTestTerminal {
	return &tasTestTerminal{cols: cols, rows: rows}
}

func (m *tasTestTerminal) Start(onInput func(data string), _ func()) {
	m.mu.Lock()
	m.onInput = onInput
	m.mu.Unlock()
}

func (m *tasTestTerminal) Stop() {}

func (m *tasTestTerminal) DrainInput(_, _ *int64) error { return nil }

func (m *tasTestTerminal) Write(data string) {
	m.mu.Lock()
	m.writes = append(m.writes, data)
	m.mu.Unlock()
}

func (m *tasTestTerminal) Columns() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cols
}

func (m *tasTestTerminal) Rows() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rows
}

func (m *tasTestTerminal) KittyProtocolActive() bool        { return false }
func (m *tasTestTerminal) MoveBy(_ int)                     {}
func (m *tasTestTerminal) HideCursor()                      {}
func (m *tasTestTerminal) ShowCursor()                      {}
func (m *tasTestTerminal) ClearLine()                       {}
func (m *tasTestTerminal) ClearFromCursor()                 {}
func (m *tasTestTerminal) ClearScreen()                     {}
func (m *tasTestTerminal) SetTitle(_ string)                {}
func (m *tasTestTerminal) SetProgress(_ bool)               {}
func (m *tasTestTerminal) SetProgramStatus(_ ProgramStatus) {}

func (m *tasTestTerminal) RestoreOnExit(before, after string) {
	m.mu.Lock()
	m.restoreCalls = append(m.restoreCalls, tasRestoreCall{before: before, after: after})
	m.mu.Unlock()
}

func (m *tasTestTerminal) allWrites() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.writes, "")
}

type tasTestComponent struct {
	mu          sync.Mutex
	lines       []string
	focused     bool
	events      []TuiMouseEvent
	handleMouse func(event TuiMouseEvent) *TuiMouseEventResult
}

func (c *tasTestComponent) Render(_ int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.lines)
}

func (c *tasTestComponent) Invalidate() {}

func (c *tasTestComponent) Focused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.focused
}

func (c *tasTestComponent) SetFocused(focused bool) {
	c.mu.Lock()
	c.focused = focused
	c.mu.Unlock()
}

func (c *tasTestComponent) HandleMouse(event TuiMouseEvent) *TuiMouseEventResult {
	c.mu.Lock()
	c.events = append(c.events, event)
	fn := c.handleMouse
	c.mu.Unlock()
	if fn != nil {
		return fn(event)
	}
	return nil
}

// newTestAltScreenDirect constructs a TuiAltScreen without calling unported component constructors
// (ScrollView / AltScreenFlashContainer methods that remain skeletons prior to integration).
func newTestAltScreenDirect(term Terminal) *TuiAltScreen {
	base := NewTuiBase(term, nil, nil)
	lines := WheelScrollLines{Lines: 1}
	t := &TuiAltScreen{
		TuiBase:             base,
		uploadedKittyImages: omap.NewMap[int, tasCachedKittyImage](),
		wheelScroll:         NewWheelScrollAccelerator(&lines, nil),
		mouseEnabled:        true,
		copyOnSelect:        false,
		searchMatchStyle: func(text string) string {
			return "\x1b[4m" + text + "\x1b[24m"
		},
		searchCurrentMatchStyle: func(text string) string {
			return "\x1b[1;7m" + text + "\x1b[27;22m"
		},
	}
	base.override = t
	return t
}

func TestTuiAltScreen(t *testing.T) {
	t.Run("mode_and_viewport_brand", func(t *testing.T) {
		term := newTasTestTerminal(80, 24)
		tui := newTestAltScreenDirect(term)

		if tui.Mode() != TuiModeFullscreen {
			t.Fatalf("Mode() = %q, want %q", tui.Mode(), TuiModeFullscreen)
		}
		if !tui.ViewportTui() || !IsViewportTui(tui) {
			t.Fatalf("expected ViewportTui brand true")
		}
		if IsViewportTui(nil) {
			t.Fatalf("expected IsViewportTui(nil) == false")
		}
		tui.SetCopyOnSelect(true)
		if !tui.GetCopyOnSelect() {
			t.Fatalf("expected GetCopyOnSelect() == true after SetCopyOnSelect(true)")
		}
		tui.SetCopyOnSelect(false)
		if tui.GetCopyOnSelect() {
			t.Fatalf("expected GetCopyOnSelect() == false")
		}

		tui.previousScreen = []string{"alpha", "beta"}
		gotLines := tui.GetScreenLines()
		gotLines[0] = "mutated"
		if tui.previousScreen[0] != "alpha" {
			t.Fatalf("GetScreenLines() should clone backing slice")
		}

		tui.selectionAnchor = &tasSelectionPoint{row: 0, col: 0}
		tui.selectionFocus = &tasSelectionPoint{row: 0, col: 2}
		if !tui.HasActiveSelection() {
			t.Fatalf("expected HasActiveSelection() == true")
		}
		tui.ResetTextSelection()
		if tui.HasActiveSelection() {
			t.Fatalf("expected HasActiveSelection() == false after ResetTextSelection")
		}
	})

	t.Run("multiplexer_mouse_tracking_and_exit_restore", func(t *testing.T) {
		t.Cleanup(ResetCapabilitiesCache)
		SetCapabilities(TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true})

		tasTestUnsetEnvs(t, "TMUX", "ZELLIJ", "STY")
		t.Setenv("TERM", "xterm-256color")

		directSeq := tasMouseTrackingSequence()
		if !strings.Contains(directSeq, "\x1b[?1003h") {
			t.Fatalf("expected all-motion mouse tracking outside multiplexer, got %q", directSeq)
		}

		directTerm := newTasTestTerminal(80, 24)
		directTui := newTestAltScreenDirect(directTerm)
		directTui.altScreenActive = true

		prefix := directTui.getExitRestorePrefix()
		suffix := directTui.getExitRestoreSuffix()
		if !strings.Contains(prefix, tasDisableMouse) || !strings.Contains(prefix, tasEnableAutowrap) || !strings.Contains(prefix, tasEndSynchronizedOutput) {
			t.Fatalf("unexpected exit restore prefix: %q", prefix)
		}
		if !strings.Contains(suffix, tasExitAltScreen) || !strings.Contains(suffix, "\x1b[?25h") {
			t.Fatalf("unexpected exit restore suffix: %q", suffix)
		}

		directTui.TuiBase.mu.Lock()
		directTui.TuiBase.started = true
		directTui.TuiBase.mu.Unlock()
		directTui.RestoreOnExit()
		directTerm.mu.Lock()
		callsAfterFirst := slices.Clone(directTerm.restoreCalls)
		directTerm.mu.Unlock()
		if len(callsAfterFirst) != 1 {
			t.Fatalf("expected 1 RestoreOnExit call, got %d", len(callsAfterFirst))
		}
		if !strings.Contains(callsAfterFirst[0].before, tasDisableMouse) || !strings.Contains(callsAfterFirst[0].after, tasExitAltScreen) {
			t.Fatalf("expected RestoreOnExit before/after to disable mouse and exit alt-screen, got %+v", callsAfterFirst[0])
		}
		// RestoreOnExit must be idempotent.
		directTui.RestoreOnExit()
		directTerm.mu.Lock()
		callCountAfterSecond := len(directTerm.restoreCalls)
		directTerm.mu.Unlock()
		if callCountAfterSecond != 1 {
			t.Fatalf("expected second RestoreOnExit to be a no-op, got %d calls", callCountAfterSecond)
		}

		multiplexers := []struct {
			name string
			key  string
			val  string
		}{
			{name: "TMUX env", key: "TMUX", val: "/tmp/tmux/default,1,0"},
			{name: "ZELLIJ env", key: "ZELLIJ", val: "0"},
			{name: "STY env", key: "STY", val: "123.session"},
			{name: "TERM tmux", key: "TERM", val: "tmux-256color"},
			{name: "TERM screen", key: "TERM", val: "screen-256color"},
		}
		for _, mux := range multiplexers {
			t.Run(mux.name, func(t *testing.T) {
				tasTestUnsetEnvs(t, "TMUX", "ZELLIJ", "STY")
				t.Setenv("TERM", "xterm-256color")
				t.Setenv(mux.key, mux.val)

				seq := tasMouseTrackingSequence()
				if !strings.Contains(seq, "\x1b[?1002h") {
					t.Fatalf("%s: expected button-motion mouse tracking (1002h), got %q", mux.name, seq)
				}
				if strings.Contains(seq, "\x1b[?1003h") {
					t.Fatalf("%s: unexpected all-motion tracking (1003h), got %q", mux.name, seq)
				}
				if !strings.Contains(seq, "\x1b[?1006h") {
					t.Fatalf("%s: expected SGR mouse mode (1006h), got %q", mux.name, seq)
				}
			})
		}
	})

	t.Run("iterm2_restore_on_stop", func(t *testing.T) {
		t.Cleanup(ResetCapabilitiesCache)
		SetCapabilities(TerminalCapabilities{Images: "", TrueColor: true, Hyperlinks: true})

		term := newTasTestTerminal(80, 24)
		tui := newTestAltScreenDirect(term)
		saved := TerminalCapabilities{Images: ImageProtocolIterm2, TrueColor: true, Hyperlinks: true}
		tui.altScreenActive = true
		tui.savedCapabilities = &saved

		preserve := true
		tui.afterTerminalStop(&TuiStopOptions{PreserveScreen: &preserve})
		if got := GetCapabilities().Images; got != ImageProtocolIterm2 {
			t.Fatalf("expected Images capability restored to %q after stop, got %q", ImageProtocolIterm2, got)
		}
		if tui.altScreenActive {
			t.Fatalf("expected altScreenActive == false after stop")
		}
		writes := term.allWrites()
		if !strings.Contains(writes, tasExitAltScreen) || !strings.Contains(writes, "\x1b[?25h") {
			t.Fatalf("expected preserve-screen stop to exit alt screen and show cursor, got %q", writes)
		}
	})

	t.Run("wheel_and_sgr_mouse_parsing", func(t *testing.T) {
		term := newTasTestTerminal(80, 24)
		tui := newTestAltScreenDirect(term)

		upSGR := tui.parseWheelEvent("\x1b[<64;10;5M")
		if upSGR == nil || upSGR.direction != -1 || upSGR.x != 9 || upSGR.y != 4 {
			t.Fatalf("unexpected SGR wheel up parse: %+v", upSGR)
		}
		downSGR := tui.parseWheelEvent("\x1b[<65;1;2M")
		if downSGR == nil || downSGR.direction != 1 || downSGR.x != 0 || downSGR.y != 1 {
			t.Fatalf("unexpected SGR wheel down parse: %+v", downSGR)
		}
		if tui.parseWheelEvent("\x1b[<66;1;1M") != nil || tui.parseWheelEvent("\x1b[<67;1;1M") != nil {
			t.Fatalf("expected horizontal SGR wheel events to be ignored")
		}

		// X10 wheel: button 64 -> byte 96 ('`'), button 65 -> byte 97 ('a'), button 66 -> byte 98 ('b')
		upX10 := tui.parseWheelEvent("\x1b[M`%#") // x=37-33=4, y=35-33=2
		if upX10 == nil || upX10.direction != -1 || upX10.x != 4 || upX10.y != 2 {
			t.Fatalf("unexpected X10 wheel up parse: %+v", upX10)
		}
		downX10 := tui.parseWheelEvent("\x1b[Ma!!")
		if downX10 == nil || downX10.direction != 1 || downX10.x != 0 || downX10.y != 0 {
			t.Fatalf("unexpected X10 wheel down parse: %+v", downX10)
		}
		if tui.parseWheelEvent("\x1b[Mb!!") != nil {
			t.Fatalf("expected horizontal X10 wheel event to be ignored")
		}

		press := tui.parseSgrMouseEvent("\x1b[<0;12;7M")
		if press == nil || press.button != 0 || press.x != 11 || press.y != 6 || press.release {
			t.Fatalf("unexpected SGR press: %+v", press)
		}
		rel := tui.parseSgrMouseEvent("\x1b[<0;12;7m")
		if rel == nil || !rel.release || rel.x != 11 || rel.y != 6 {
			t.Fatalf("unexpected SGR release: %+v", rel)
		}

		if !tui.isMouseSequence("\x1b[<0;1;1M") || !tui.isMouseSequence("\x1b[M !!") || tui.isMouseSequence("abc") {
			t.Fatalf("unexpected isMouseSequence results")
		}

		called := false
		tui.onRightClickPaste = func() { called = true }
		if tui.handleRightClickPaste(tasSgrMouseEvent{button: 0, release: false}) {
			t.Fatalf("left click must not trigger right-click paste")
		}
		if tui.handleRightClickPaste(tasSgrMouseEvent{button: 2, release: true}) {
			t.Fatalf("right release must not trigger right-click paste")
		}
		if called {
			t.Fatalf("unexpected right-click paste callback invocation")
		}
	})

	t.Run("kitty_prepare_screen_cache_reuse_and_eviction", func(t *testing.T) {
		term := newTasTestTerminal(80, 24)
		tui := newTestAltScreenDirect(term)
		tui.imageProtocol = ImageProtocolKitty
		if got := tui.deleteKittyImages(); got != DeleteAllKittyImages() {
			t.Fatalf("deleteKittyImages() = %q, want %q", got, DeleteAllKittyImages())
		}

		moveCursorFalse := false
		const firstID = 910101
		RegisterKittyImageMetadata(KittyImageMetadata{
			ImageId:  firstID,
			Columns:  2,
			Rows:     1,
			WidthPx:  100,
			HeightPx: 50,
		})
		line1 := EncodeKitty("AAAA", &EncodeKittyOptions{
			Columns:    2,
			Rows:       1,
			ImageId:    firstID,
			MoveCursor: &moveCursorFalse,
		})

		prep1 := tui.prepareKittyScreen([]string{line1})
		if prep1.lines[0] != line1 || prep1.evictedImageDeletion != "" {
			t.Fatalf("first upload should transmit full payload with no eviction")
		}

		prep2 := tui.prepareKittyScreen([]string{line1})
		if !strings.Contains(prep2.lines[0], "\x1b_Ga=p,q=2") || strings.Contains(prep2.lines[0], "\x1b_Ga=T") {
			t.Fatalf("second frame should reuse placement-only command, got %q", prep2.lines[0])
		}

		// Re-registering bumps transmission generation -> forces re-transmission.
		RegisterKittyImageMetadata(KittyImageMetadata{
			ImageId:  firstID,
			Columns:  2,
			Rows:     1,
			WidthPx:  100,
			HeightPx: 50,
		})
		prepBumped := tui.prepareKittyScreen([]string{line1})
		if prepBumped.lines[0] != line1 {
			t.Fatalf("expected bumped generation to re-transmit original line, got %q", prepBumped.lines[0])
		}

		// Offscreen within count quota (16) stays reusable.
		prepOff := tui.prepareKittyScreen([]string{"plain"})
		if prepOff.evictedImageDeletion != "" {
			t.Fatalf("single offscreen image should not be evicted, got %q", prepOff.evictedImageDeletion)
		}
		prepReenter := tui.prepareKittyScreen([]string{line1})
		if !strings.Contains(prepReenter.lines[0], "\x1b_Ga=p,q=2") {
			t.Fatalf("offscreen retained image should reuse placement on reentry, got %q", prepReenter.lines[0])
		}

		// Push 17 additional images through to trigger LRU count eviction (>16 offscreen).
		tui.uploadedKittyImages.Clear()
		const baseID = 910200
		imageLines := make([]string, 18)
		for i := range imageLines {
			id := baseID + i
			RegisterKittyImageMetadata(KittyImageMetadata{
				ImageId:  id,
				Columns:  2,
				Rows:     1,
				WidthPx:  100,
				HeightPx: 50,
			})
			imageLines[i] = EncodeKitty("AAAA", &EncodeKittyOptions{
				Columns:    2,
				Rows:       1,
				ImageId:    id,
				MoveCursor: &moveCursorFalse,
			})
		}
		var sawFirstEviction bool
		for i := range imageLines {
			prep := tui.prepareKittyScreen([]string{imageLines[i]})
			if strings.Contains(prep.evictedImageDeletion, DeleteKittyImage(baseID)) {
				sawFirstEviction = true
			}
		}
		if !sawFirstEviction {
			t.Fatalf("expected oldest offscreen Kitty image %d to be evicted once >%d offscreen", baseID, tasMaxCachedOffscreenKittyImages)
		}
		prepAfterEvict := tui.prepareKittyScreen([]string{imageLines[0]})
		if prepAfterEvict.lines[0] != imageLines[0] {
			t.Fatalf("evicted Kitty image must re-transmit full payload on reentry")
		}

		// Decoded raster memory quota eviction (>64MB decoded bytes).
		tui.uploadedKittyImages.Clear()
		const bigA, bigB, bigC = 910301, 910302, 910303
		for _, id := range []int{bigA, bigB, bigC} {
			RegisterKittyImageMetadata(KittyImageMetadata{
				ImageId:  id,
				Columns:  2,
				Rows:     1,
				WidthPx:  4096, // 4096*2500*4 = ~40MB each; 2 offscreen exceed 64MB
				HeightPx: 2500,
			})
		}
		lineBigA := EncodeKitty("AAAA", &EncodeKittyOptions{Columns: 2, Rows: 1, ImageId: bigA, MoveCursor: &moveCursorFalse})
		lineBigB := EncodeKitty("AAAA", &EncodeKittyOptions{Columns: 2, Rows: 1, ImageId: bigB, MoveCursor: &moveCursorFalse})
		lineBigC := EncodeKitty("AAAA", &EncodeKittyOptions{Columns: 2, Rows: 1, ImageId: bigC, MoveCursor: &moveCursorFalse})
		tui.prepareKittyScreen([]string{lineBigA})
		tui.prepareKittyScreen([]string{lineBigB})
		prepBigC := tui.prepareKittyScreen([]string{lineBigC})
		if !strings.Contains(prepBigC.evictedImageDeletion, DeleteKittyImage(bigA)) {
			t.Fatalf("expected huge decoded offscreen image %d to be evicted, got %q", bigA, prepBigC.evictedImageDeletion)
		}
		if strings.Contains(prepBigC.evictedImageDeletion, DeleteKittyImage(bigC)) {
			t.Fatalf("visible image %d must never be evicted", bigC)
		}
	})

	t.Run("mouse_dispatch_capture_focus_and_clicks", func(t *testing.T) {
		term := newTasTestTerminal(20, 10)
		tui := newTestAltScreenDirect(term)

		boolTrue := true
		var captureOn bool
		comp := &tasTestComponent{
			lines: []string{"box"},
			handleMouse: func(ev TuiMouseEvent) *TuiMouseEventResult {
				if ev.Type == TuiMouseEventTypePress {
					return &TuiMouseEventResult{
						Handled: &boolTrue,
						Capture: &captureOn,
						Focus:   &boolTrue,
					}
				}
				return &TuiMouseEventResult{Handled: &boolTrue}
			},
		}
		tui.currentLayout = &LayoutFrame{
			Width:  20,
			Height: 10,
			Root: &LayoutBox{
				Component: comp,
				Rect:      LayoutRect{X: 2, Y: 1, Width: 10, Height: 3},
				Clip:      LayoutRect{X: 0, Y: 0, Width: 20, Height: 10},
			},
		}

		// Press inside box -> local coords (2, 1), focused.
		captureOn = true
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 2, release: false})
		if tui.GetFocusedComponent() != comp {
			t.Fatalf("expected comp to gain focus on press")
		}
		// Release at same point -> triggers Release + Click (count 1).
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 2, release: true})
		// Second press/release at same point -> Click (count 2).
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 2, release: false})
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 2, release: true})
		// Drag move before release -> suppresses click on release.
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 2, release: false})
		tui.handleMouseEvent(tasSgrMouseEvent{button: 32, x: 15, y: 8, release: false})
		tui.handleMouseEvent(tasSgrMouseEvent{button: 0, x: 15, y: 8, release: true})

		comp.mu.Lock()
		evs := slices.Clone(comp.events)
		comp.mu.Unlock()

		var clicks []int
		var sawDragOutside bool
		for _, ev := range evs {
			if ev.Type == TuiMouseEventTypePress && ev.X == 2 && ev.Y == 1 && ev.Width != 10 {
				t.Fatalf("unexpected press box dimensions: %+v", ev)
			}
			if ev.Type == TuiMouseEventTypeDrag && ev.ScreenX == 15 && ev.ScreenY == 8 {
				sawDragOutside = true
			}
			if ev.Type == TuiMouseEventTypeClick && ev.ClickCount != nil {
				clicks = append(clicks, *ev.ClickCount)
			}
		}
		if !sawDragOutside {
			t.Fatalf("expected captured component to receive drag outside its rect, got %+v", evs)
		}
		if len(clicks) != 2 || clicks[0] != 1 || clicks[1] != 2 {
			t.Fatalf("clicks = %v, want [1 2]", clicks)
		}
	})

	t.Run("selection_word_joiners_line_graphemes_highlights_and_osc8", func(t *testing.T) {
		t.Run("word_selection_and_joiners", func(t *testing.T) {
			term := newTasTestTerminal(80, 2)
			tui := newTestAltScreenDirect(term)

			// Double click should not include trailing whitespace.
			tui.previousScreen = []string{"foo  bar"}
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 0, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 0, y: 0, release: true})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 2, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 2, y: 0, release: true})
			got := tui.getActiveSelectionText()
			if got == nil || *got != "foo" {
				t.Fatalf("double-click foo = %v, want \"foo\"", got)
			}

			// Word joiners: '/', '-', '.', '\\', '+'.
			cases := []struct {
				line   string
				needle string
			}{
				{line: "extensions/starline/fixed-editor/compositor.ts", needle: "starline"},
				{line: "earendil-works/pi-tui", needle: "works"},
				{line: "~/.pi/agent/settings.json", needle: "agent"},
				{line: `~\Documents\pi`, needle: "Documents"},
				{line: "foo+bar", needle: "bar"},
			}
			for _, tc := range cases {
				tui.ResetTextSelection()
				tui.lastClick = nil
				tui.previousScreen = []string{tc.line}
				col := strings.Index(tc.line, tc.needle)
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: col, y: 0, release: false})
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: col, y: 0, release: true})
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: col, y: 0, release: false})
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: col, y: 0, release: true})
				got := tui.getActiveSelectionText()
				if got == nil || *got != tc.line {
					t.Fatalf("joiner double-click on %q (%q): got %v, want %q", tc.line, tc.needle, got, tc.line)
				}
			}

			// Triple-click selects whole line.
			tui.ResetTextSelection()
			tui.lastClick = nil
			tui.previousScreen = []string{"zero alpha beta", "gamma delta"}
			for click := 0; click < 3; click++ {
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 6, y: 1, release: false})
				tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 6, y: 1, release: true})
			}
			gotLine := tui.getActiveSelectionText()
			if gotLine == nil || *gotLine != "gamma delta" {
				t.Fatalf("triple-click = %v, want \"gamma delta\"", gotLine)
			}

			// Word drag extends over whole words across rows.
			tui.ResetTextSelection()
			tui.lastClick = nil
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 11, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 11, y: 0, release: true})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 13, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 32, x: 2, y: 1, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 2, y: 1, release: true})
			gotDrag := tui.getActiveSelectionText()
			if gotDrag == nil || *gotDrag != "beta\ngamma" {
				t.Fatalf("word drag = %v, want \"beta\\ngamma\"", gotDrag)
			}
		})

		t.Run("grapheme_snapping_and_highlights", func(t *testing.T) {
			term := newTasTestTerminal(20, 2)
			tui := newTestAltScreenDirect(term)
			tui.previousScreen = []string{"A界🙂e\u0301Z"}

			// Wide CJK + emoji columns: A(0), 界(1..2), 🙂(3..4), é(5), Z(6).
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 2, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 32, x: 3, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 3, y: 0, release: true})
			gotForward := tui.getActiveSelectionText()
			if gotForward == nil || *gotForward != "界🙂" {
				t.Fatalf("wide forward drag = %v, want \"界🙂\"", gotForward)
			}

			// Reverse drag from col 4 back to col 1 snaps cleanly too.
			tui.ResetTextSelection()
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 32, x: 1, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 1, y: 0, release: true})
			gotReverse := tui.getActiveSelectionText()
			if gotReverse == nil || *gotReverse != "界🙂" {
				t.Fatalf("wide reverse drag = %v, want \"界🙂\"", gotReverse)
			}

			// Combining mark grapheme é + Z.
			tui.ResetTextSelection()
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 5, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 32, x: 6, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 6, y: 0, release: true})
			gotComb := tui.getActiveSelectionText()
			if gotComb == nil || *gotComb != "e\u0301Z" {
				t.Fatalf("combining drag = %v, want \"e\\u0301Z\"", gotComb)
			}

			// applySelection inverse-wraps selected columns and preserves ANSI resets inside selection.
			styled := "hi \x1b[31mred\x1b[39m end"
			tui.previousScreen = []string{styled}
			tui.selectionAnchor = &tasSelectionPoint{row: 0, col: 3}
			tui.selectionFocus = &tasSelectionPoint{row: 0, col: 5}
			highlighted := tui.applySelection([]string{styled}, nil)
			if !strings.Contains(highlighted[0], "\x1b[7m") || !strings.Contains(highlighted[0], "\x1b[27m") {
				t.Fatalf("expected reverse video around selection, got %q", highlighted[0])
			}
			// applySearchTextHighlight skips ANSI codes and wraps plain runs.
			searchOut := tui.applySearchTextHighlight("\x1b[32mhit\x1b[39m", true)
			if searchOut != "\x1b[32m\x1b[1;7mhit\x1b[27;22m\x1b[39m" {
				t.Fatalf("applySearchTextHighlight = %q", searchOut)
			}
		})

		t.Run("osc8_link_click", func(t *testing.T) {
			term := newTasTestTerminal(40, 1)
			tui := newTestAltScreenDirect(term)
			var opened []string
			tui.openUrl = func(url string) {
				opened = append(opened, url)
			}
			line := "go \x1b]8;;https://example.com\x07link\x1b]8;;\x07"
			tui.previousScreen = []string{line}

			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 0, release: false})
			tui.handleSelectionMouseEvent(tasSgrMouseEvent{button: 0, x: 4, y: 0, release: true})
			if len(opened) != 1 || opened[0] != "https://example.com" {
				t.Fatalf("opened = %v, want [https://example.com]", opened)
			}
			if tui.HasActiveSelection() {
				t.Fatalf("OSC 8 activation should clear selection")
			}
		})
	})

	t.Run("viewport_input_deferral_and_empty_clipboard_noop", func(t *testing.T) {
		term := newTasTestTerminal(40, 10)
		tui := newTestAltScreenDirect(term)

		comp := &tasTestComponent{lines: []string{"base"}}
		tui.SetFocus(comp)
		if tui.shouldDeferViewportInputToOverlay() {
			t.Fatalf("plain focused component should not defer viewport input")
		}

		overlay := &tasTestComponent{lines: []string{"modal"}}
		tui.ShowOverlay(overlay, nil)
		if !tui.shouldDeferViewportInputToOverlay() {
			t.Fatalf("focused overlay should defer viewport input")
		}
		tui.HideOverlay()

		ok, err := tui.CopyActiveSelectionToClipboard()
		if err != nil || ok {
			t.Fatalf("empty selection copy = (%v, %v), want (false, nil)", ok, err)
		}
	})
}
