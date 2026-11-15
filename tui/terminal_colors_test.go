package tui_test

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/keejkrej/pi-go/tui"
)

func TestParseTerminalColorSchemeReport(t *testing.T) {
	cases := []struct {
		in   string
		want *tui.TerminalColorScheme
	}{
		{"\x1b[?997;1n", schemePtr(tui.TerminalColorSchemeDark)},
		{"\x1b[?997;2n", schemePtr(tui.TerminalColorSchemeLight)},
		{"\x1b[?997;2n\x1b[?997;1n\x1b[?997;1n", schemePtr(tui.TerminalColorSchemeDark)},
		{"\x1b[?997;1n\x1b[?997;2n\x1b[?997;2n", schemePtr(tui.TerminalColorSchemeLight)},
		{"\x1b[?997;3n", nil},
		{"\x1b[?996n", nil},
		{"x\x1b[?997;1n", nil},
	}
	for _, tc := range cases {
		got := tui.ParseTerminalColorSchemeReport(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseTerminalColorSchemeReport(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseOscColorResponse(t *testing.T) {
	got := tui.ParseOscColorResponse("\x1b]10;rgb:ffff/ffff/ffff\x07")
	want := &tui.OscColorResponse{
		Target: &tui.OscColorTargetForeground{},
		Rgb:    &tui.RgbColor{R: 255, G: 255, B: 255},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OSC 10 = %#v, want %#v", got, want)
	}

	got = tui.ParseOscColorResponse("\x1b]4;13;#ff0080\x1b\\")
	want = &tui.OscColorResponse{
		Target: &tui.OscColorTargetIndex{Index: 13},
		Rgb:    &tui.RgbColor{R: 255, G: 0, B: 128},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OSC 4;13 = %#v, want %#v", got, want)
	}

	got = tui.ParseOscColorResponse("\x1b]4;1;bogus\x07")
	want = &tui.OscColorResponse{
		Target: &tui.OscColorTargetIndex{Index: 1},
		Rgb:    nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OSC 4;1;bogus = %#v, want %#v", got, want)
	}

	if got := tui.ParseOscColorResponse("\x1b]12;#ffffff\x07"); got != nil {
		t.Errorf("OSC 12 = %#v, want nil", got)
	}
}

func schemePtr(s tui.TerminalColorScheme) *tui.TerminalColorScheme {
	return &s
}

type colorQueryMockTerm struct {
	mu      sync.Mutex
	onInput func(data string)
	writes  []string
}

func (m *colorQueryMockTerm) Start(onInput func(data string), onResize func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onInput = onInput
}

func (m *colorQueryMockTerm) Stop()                                        {}
func (m *colorQueryMockTerm) DrainInput(maxMs *int64, idleMs *int64) error { return nil }
func (m *colorQueryMockTerm) Write(data string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, data)
}
func (m *colorQueryMockTerm) Columns() int                              { return 80 }
func (m *colorQueryMockTerm) Rows() int                                 { return 24 }
func (m *colorQueryMockTerm) KittyProtocolActive() bool                 { return false }
func (m *colorQueryMockTerm) MoveBy(lines int)                          {}
func (m *colorQueryMockTerm) HideCursor()                               {}
func (m *colorQueryMockTerm) ShowCursor()                               {}
func (m *colorQueryMockTerm) ClearLine()                                {}
func (m *colorQueryMockTerm) ClearFromCursor()                          {}
func (m *colorQueryMockTerm) ClearScreen()                              {}
func (m *colorQueryMockTerm) SetTitle(title string)                     {}
func (m *colorQueryMockTerm) SetProgress(active bool)                   {}
func (m *colorQueryMockTerm) SetProgramStatus(status tui.ProgramStatus) {}

func (m *colorQueryMockTerm) emitInput(data string) {
	m.mu.Lock()
	h := m.onInput
	m.mu.Unlock()
	if h != nil {
		h(data)
	}
}

func (m *colorQueryMockTerm) allWrites() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.writes, "")
}

type colorQueryFocusComp struct {
	mu     sync.Mutex
	inputs []string
}

func (c *colorQueryFocusComp) Render(width int) []string { return []string{"comp"} }
func (c *colorQueryFocusComp) Invalidate()               {}
func (c *colorQueryFocusComp) HandleInput(data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs = append(c.inputs, data)
}

func (c *colorQueryFocusComp) snapshotInputs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.inputs))
	copy(out, c.inputs)
	return out
}

func TestTuiQueryTerminalColors(t *testing.T) {
	t.Run("resolves OSC 10, OSC 11, and all 16 palette entries without leaking to listeners", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			term := &colorQueryMockTerm{}
			ui := tui.NewTuiMainScreen(term, nil, nil)
			focus := &colorQueryFocusComp{}
			ui.AddChild(focus)
			ui.SetFocus(focus)

			var listened []string
			ui.AddInputListener(func(data string) *tui.TuiInputListenerResult {
				listened = append(listened, data)
				return nil
			})

			ui.Start()

			var got tui.TerminalColors
			var queryErr error
			done := make(chan struct{})
			defer func() {
				ui.Stop(nil)
				<-done
			}()
			go func() {
				got, queryErr = ui.QueryTerminalColors(tui.TuiQueryTerminalColorsOptions{TimeoutMs: 500})
				close(done)
			}()
			synctest.Wait()

			written := term.allWrites()
			if !strings.Contains(written, "\x1b]10;?\x07") || !strings.Contains(written, "\x1b]11;?\x07") || !strings.Contains(written, "\x1b]4;15;?\x07") || !strings.Contains(written, "\x1b[c") {
				t.Fatalf("missing expected OSC color queries in %q", written)
			}

			term.emitInput("\x1b]10;rgb:1111/2222/3333\x07")
			term.emitInput("x")
			term.emitInput("\x1b]11;rgb:aaaa/bbbb/cccc\x1b\\")
			for i := 0; i < 16; i++ {
				term.emitInput(fmt.Sprintf("\x1b]4;%d;rgb:%02x%02x/%02x%02x/%02x%02x\x07", i, i, i, i*2, i*2, i*3, i*3))
			}

			<-done
			if queryErr != nil {
				t.Fatalf("QueryTerminalColors error = %v", queryErr)
			}
			wantFg := &tui.RgbColor{R: 0x11, G: 0x22, B: 0x33}
			wantBg := &tui.RgbColor{R: 0xaa, G: 0xbb, B: 0xcc}
			if !reflect.DeepEqual(got.Foreground, wantFg) || !reflect.DeepEqual(got.Background, wantBg) {
				t.Fatalf("fg=%+v bg=%+v, want fg=%+v bg=%+v", got.Foreground, got.Background, wantFg, wantBg)
			}
			if len(got.Palette) != 16 || got.Palette[15] != (tui.RgbColor{R: 15, G: 30, B: 45}) {
				t.Fatalf("palette = %#v", got.Palette)
			}
			if !reflect.DeepEqual(listened, []string{"x"}) || !reflect.DeepEqual(focus.snapshotInputs(), []string{"x"}) {
				t.Fatalf("unexpected non-color forwarding: listened=%v focus=%v", listened, focus.snapshotInputs())
			}
		})
	})

	t.Run("timeout returns partial foreground/background and invokes OnLateReply when palette finishes later", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			term := &colorQueryMockTerm{}
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.Start()
			defer ui.Stop(nil)

			lateCh := make(chan tui.TerminalColors, 1)
			var initial tui.TerminalColors
			var queryErr error
			done := make(chan struct{})
			go func() {
				initial, queryErr = ui.QueryTerminalColors(tui.TuiQueryTerminalColorsOptions{
					TimeoutMs: 50,
					OnLateReply: func(colors tui.TerminalColors) {
						lateCh <- colors
					},
				})
				close(done)
			}()
			synctest.Wait()

			term.emitInput("\x1b]10;#123456\x07")
			term.emitInput("\x1b]11;#654321\x1b\\")

			<-done
			if queryErr != nil {
				t.Fatalf("QueryTerminalColors err = %v", queryErr)
			}
			if initial.Foreground == nil || *initial.Foreground != (tui.RgbColor{R: 0x12, G: 0x34, B: 0x56}) {
				t.Fatalf("initial Foreground = %+v", initial.Foreground)
			}
			if initial.Background == nil || *initial.Background != (tui.RgbColor{R: 0x65, G: 0x43, B: 0x21}) {
				t.Fatalf("initial Background = %+v", initial.Background)
			}
			if initial.Palette != nil {
				t.Fatalf("expected nil Palette when incomplete before timeout, got %#v", initial.Palette)
			}

			for i := 0; i < 16; i++ {
				term.emitInput(fmt.Sprintf("\x1b]4;%d;#%02x%02x%02x\x07", i, i, 16+i, 32+i))
			}

			late := <-lateCh
			if len(late.Palette) != 16 || late.Palette[5] != (tui.RgbColor{R: 5, G: 21, B: 37}) {
				t.Fatalf("late Palette = %#v", late.Palette)
			}
		})
	})

	t.Run("Stop cancels pending color query cleanly", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			term := &colorQueryMockTerm{}
			ui := tui.NewTuiMainScreen(term, nil, nil)
			ui.Start()

			var got tui.TerminalColors
			done := make(chan struct{})
			go func() {
				got, _ = ui.QueryTerminalColors(tui.TuiQueryTerminalColorsOptions{TimeoutMs: 5000})
				close(done)
			}()
			synctest.Wait()

			ui.Stop(nil)
			<-done
			if got.Foreground != nil || got.Background != nil || got.Palette != nil {
				t.Fatalf("expected empty TerminalColors on Stop, got %+v", got)
			}
		})
	})
}
