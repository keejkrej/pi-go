// Ported from packages/tui/test/stdin-buffer.test.ts and packages/tui/test/terminal-colors.test.ts (pi v1.5.2).

package tui_test

import (
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/tui"
)

type sbTestRecorder struct {
	mu      sync.Mutex
	emitted []string
	pasted  []string
}

func (r *sbTestRecorder) getEmitted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.emitted)
}

func (r *sbTestRecorder) takeEmitted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.emitted)
	r.emitted = nil
	return out
}

func (r *sbTestRecorder) clear() {
	r.mu.Lock()
	r.emitted = nil
	r.pasted = nil
	r.mu.Unlock()
}

func (r *sbTestRecorder) takePasted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.pasted)
	r.pasted = nil
	return out
}

func sbTestCollect(buf *tui.StdinBuffer) *sbTestRecorder {
	r := &sbTestRecorder{}
	buf.OnData(func(seq string) {
		r.mu.Lock()
		r.emitted = append(r.emitted, seq)
		r.mu.Unlock()
	})
	buf.OnPaste(func(content string) {
		r.mu.Lock()
		r.pasted = append(r.pasted, content)
		r.mu.Unlock()
	})
	return r
}

func TestStdinBufferRegularCharacters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buf := tui.NewStdinBuffer(nil)
		defer buf.Destroy()
		rec := sbTestCollect(buf)

		buf.Process("a")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"a"}) {
			t.Fatalf("got %v", got)
		}

		buf.Process("abc")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Fatalf("got %v", got)
		}

		buf.Process("hello 世界")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"h", "e", "l", "l", "o", " ", "世", "界"}) {
			t.Fatalf("got %v", got)
		}

		buf.Process("\r\n\t ")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\r", "\n", "\t", " "}) {
			t.Fatalf("got %v", got)
		}

		buf.Process("a\x1b[A\x1b[Bb")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"a", "\x1b[A", "\x1b[B", "b"}) {
			t.Fatalf("got %v", got)
		}
	})
}

func TestStdinBufferCompleteSequences(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buf := tui.NewStdinBuffer(nil)
		defer buf.Destroy()
		rec := sbTestCollect(buf)

		cases := []struct {
			input string
			want  []string
		}{
			{"\x1b[<35;20;5m", []string{"\x1b[<35;20;5m"}},
			{"\x1b[Mabc", []string{"\x1b[Mabc"}},
			{"\x1b[M abc", []string{"\x1b[M ab", "c"}},
			{"\x1b[A\x1b[B\x1b[C\x1b[D", []string{"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D"}},
			{"\x1bOA", []string{"\x1bOA"}},
			{"\x1ba", []string{"\x1ba"}},
			{"\x1b]0;Title\x07", []string{"\x1b]0;Title\x07"}},
			{"\x1b]0;Title\x1b\\", []string{"\x1b]0;Title\x1b\\"}},
			{"\x1b[11~", []string{"\x1b[11~"}},
			{"\x1b[1;5A", []string{"\x1b[1;5A"}},
			{"\x1b[15~", []string{"\x1b[15~"}},
			{"\x1b[?2004h", []string{"\x1b[?2004h"}},
			{"\x1b[97;5u", []string{"\x1b[97;5u"}},
		}

		for _, tc := range cases {
			rec.clear()
			buf.Process(tc.input)
			if got := rec.takeEmitted(); !slices.Equal(got, tc.want) {
				t.Fatalf("input %q: got %v, want %v", tc.input, got, tc.want)
			}
			if buf.GetBuffer() != "" {
				t.Fatalf("remainder not empty for %q: %q", tc.input, buf.GetBuffer())
			}
		}
	})
}

func TestStdinBufferPartialAndTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		timeout := int64(50)
		escTimeout := int64(10)
		buf := tui.NewStdinBuffer(&tui.StdinBufferOptions{
			Timeout:       &timeout,
			EscapeTimeout: &escTimeout,
		})
		defer buf.Destroy()
		rec := sbTestCollect(buf)

		// Split SGR mouse event across chunks
		buf.Process("\x1b[<35")
		if len(rec.getEmitted()) != 0 || buf.GetBuffer() != "\x1b[<35" {
			t.Fatalf("expected buffered SGR mouse prefix, got emitted=%v buf=%q", rec.getEmitted(), buf.GetBuffer())
		}
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()
		buf.Process(";20;5m")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b[<35;20;5m"}) {
			t.Fatalf("got %v", got)
		}

		// Lone ESC flushes after escapeTimeout (10ms)
		buf.Process("\x1b")
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("lone ESC should wait")
		}
		time.Sleep(11 * time.Millisecond)
		synctest.Wait()
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b"}) || buf.GetBuffer() != "" {
			t.Fatalf("lone ESC did not flush on timeout: emitted=%v buf=%q", got, buf.GetBuffer())
		}

		// Split meta key arriving before escapeTimeout
		buf.Process("\x1b")
		time.Sleep(5 * time.Millisecond)
		synctest.Wait()
		buf.Process("x")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1bx"}) {
			t.Fatalf("got %v", got)
		}

		// Unknown incomplete CSI sequence flushes after timeout (50ms)
		buf.Process("\x1b[1")
		time.Sleep(40 * time.Millisecond)
		synctest.Wait()
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("should still be waiting at 40ms")
		}
		time.Sleep(15 * time.Millisecond)
		synctest.Wait()
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b[1"}) {
			t.Fatalf("did not flush incomplete CSI at 50ms: %v", got)
		}
	})
}

func TestStdinBufferAltRecoveryAndReplyDeferral(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		timeout := int64(20)
		buf := tui.NewStdinBuffer(&tui.StdinBufferOptions{Timeout: &timeout})
		defer buf.Destroy()
		rec := sbTestCollect(buf)

		// Unrecognized Alt+] followed by text recovers Alt+] and reparses tail on timeout
		buf.Process("\x1b]hello\r")
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("should defer ambiguous control-string start before timeout")
		}
		time.Sleep(25 * time.Millisecond)
		synctest.Wait()
		want := []string{"\x1b]", "h", "e", "l", "l", "o", "\r"}
		if got := rec.takeEmitted(); !slices.Equal(got, want) {
			t.Fatalf("alt+] recovery mismatch: got %v, want %v", got, want)
		}

		// Recognized terminal reply headers wait beyond timeout without flushing
		buf.Process("\x1b]11;rgb:1111/2222")
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("recognized OSC 11 reply header should not flush on timeout, got %v", rec.getEmitted())
		}
		buf.Process("/3333\x07")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b]11;rgb:1111/2222/3333\x07"}) {
			t.Fatalf("got %v", got)
		}

		// Aborted terminal reply recovers following escape sequence
		buf.Process("\x1b]11;rgb:1111")
		buf.Process("\x1b[A")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b[A"}) || buf.GetBuffer() != "" {
			t.Fatalf("aborted reply mismatch: emitted=%v buf=%q", got, buf.GetBuffer())
		}

		// CAN (\x18) cancels a terminal reply and keeps subsequent input
		buf.Process("\x1b]10;rgb:0000\x18abc")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Fatalf("CAN cancel mismatch: %v", got)
		}

		// Ghostty fragmented APC / DCS replies across chunks
		buf.Process("\x1bP>|ghostty ")
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("DCS XTVersion header must wait beyond timeout")
		}
		buf.Process("1.1.0\x1b\\")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1bP>|ghostty 1.1.0\x1b\\"}) {
			t.Fatalf("got %v", got)
		}

		buf.Process("\x1b_Gi=31337;")
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if len(rec.getEmitted()) != 0 {
			t.Fatalf("APC Kitty reply header must wait beyond timeout")
		}
		buf.Process("OK\x1b\\")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b_Gi=31337;OK\x1b\\"}) {
			t.Fatalf("got %v", got)
		}
	})
}

func TestStdinBufferSpecialCasesAndPaste(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		buf := tui.NewStdinBuffer(nil)
		defer buf.Destroy()
		rec := sbTestCollect(buf)

		// WezTerm concatenated ESC + Kitty CSI-u release
		buf.Process("\x1b\x1b[27;1;3u")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b", "\x1b[27;1;3u"}) {
			t.Fatalf("WezTerm split mismatch: %v", got)
		}

		// Unmodified Kitty printable duplicate suppression
		buf.Process("\x1b[97ua")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b[97u"}) {
			t.Fatalf("expected raw 'a' suppressed, got %v", got)
		}
		buf.Process("\x1b[97ub")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1b[97u", "b"}) {
			t.Fatalf("different char should not be suppressed, got %v", got)
		}

		// Bracketed paste complete + split across chunks + trailing input
		buf.Process("a\x1b[200~hello ")
		buf.Process("world\x1b[201~b")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"a", "b"}) {
			t.Fatalf("emitted mismatch: %v", got)
		}
		if got := rec.takePasted(); !slices.Equal(got, []string{"hello world"}) {
			t.Fatalf("pasted mismatch: %v", got)
		}

		// ProcessBytes single byte > 127 -> ESC + (byte - 128)
		buf.ProcessBytes([]byte{0x80 + 'x'})
		if got := rec.takeEmitted(); !slices.Equal(got, []string{"\x1bx"}) {
			t.Fatalf("high byte conversion mismatch: %v", got)
		}

		// Empty input on empty buffer emits ""
		buf.Process("")
		if got := rec.takeEmitted(); !slices.Equal(got, []string{""}) {
			t.Fatalf("empty input mismatch: %v", got)
		}

		// Flush and Clear
		buf.Process("\x1b[")
		flushed := buf.Flush()
		if !slices.Equal(flushed, []string{"\x1b["}) || buf.GetBuffer() != "" {
			t.Fatalf("flush mismatch: %v", flushed)
		}
		buf.Process("\x1b[")
		buf.Clear()
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		if buf.GetBuffer() != "" || len(buf.Flush()) != 0 {
			t.Fatalf("clear should drop incomplete sequence")
		}
	})
}
