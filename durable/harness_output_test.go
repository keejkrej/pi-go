// Ported from packages/durable/test/harness-output.test.ts (pi v1.0.0).

package durable

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func thotHead(maxLines int, maxBytes ...int) OutputLimits {
	mb := 1000
	if len(maxBytes) > 0 {
		mb = maxBytes[0]
	}
	return OutputLimits{MaxBytes: mb, MaxLines: maxLines, Retain: ToolOutputRetainHead}
}

func thotTail(maxLines int, maxBytes ...int) OutputLimits {
	mb := 1000
	if len(maxBytes) > 0 {
		mb = maxBytes[0]
	}
	return OutputLimits{MaxBytes: mb, MaxLines: maxLines, Retain: ToolOutputRetainTail}
}

func thotBufferTail(content string, maxBytes int) string {
	raw := []byte(content)
	if len(raw) <= maxBytes {
		return content
	}
	start := len(raw) - maxBytes
	for start < len(raw) && (raw[start]&0xc0) == 0x80 {
		start++
	}
	return string(raw[start:])
}

func thotAssertMatchesBufferTail(t *testing.T, input string, maxByteValues ...int) {
	t.Helper()
	totalBytes := len(input)
	values := maxByteValues
	if values == nil {
		values = make([]int, totalBytes+5)
		for i := range values {
			values[i] = i
		}
	}
	for _, maxBytes := range values {
		kept := BoundOutput(input, OutputLimits{
			MaxBytes: maxBytes,
			MaxLines: 10,
			Retain:   ToolOutputRetainTail,
		}).Text
		expected := thotBufferTail(input, maxBytes)
		if kept != expected {
			t.Fatalf("tail mismatch input=%q maxBytes=%d expected=%q actual=%q", input, maxBytes, expected, kept)
		}
		if len(kept) > maxBytes {
			t.Fatalf("tail output %q exceeded %d bytes", kept, maxBytes)
		}
	}
}

func thotSampledByteLimits(input string) []int {
	totalBytes := len(input)
	candidates := []int{
		0, 1, 2, 3, 4, 5, 8,
		totalBytes / 2,
		totalBytes - 4,
		totalBytes - 1,
		totalBytes,
		totalBytes + 1,
	}
	seen := make(map[int]struct{})
	var out []int
	for _, v := range candidates {
		if v >= 0 {
			if _, ok := seen[v]; !ok {
				seen[v] = struct{}{}
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return out
}

func thotBound(text string, limits OutputLimits) BoundedOutput {
	s := BoundOutput(text, limits)
	return BoundedOutput{
		Text:         s.Text,
		DroppedBytes: s.DroppedBytes,
		DroppedLines: s.DroppedLines,
	}
}

func TestHarnessOutput_ToolOutputBounds(t *testing.T) {
	t.Run("removes control characters but keeps tabs newlines and other text", func(t *testing.T) {
		got := SanitizeOutput("a\x00b\tc\nd\re\x07f\ufff9g\ufffbh😀")
		want := "ab\tc\ndefgh😀"
		if got != want {
			t.Fatalf("SanitizeOutput got %q, want %q", got, want)
		}
	})

	t.Run("keeps output within limits unchanged", func(t *testing.T) {
		if got := thotBound("a\nb\n", thotHead(2)); got != (BoundedOutput{Text: "a\nb\n", DroppedBytes: 0, DroppedLines: 0}) {
			t.Fatalf("unexpected head result: %+v", got)
		}
		if got := thotBound("a\nb", thotTail(2)); got != (BoundedOutput{Text: "a\nb", DroppedBytes: 0, DroppedLines: 0}) {
			t.Fatalf("unexpected tail result: %+v", got)
		}
		if got := thotBound("", thotTail(2)); got != (BoundedOutput{Text: "", DroppedBytes: 0, DroppedLines: 0}) {
			t.Fatalf("unexpected empty result: %+v", got)
		}
	})

	t.Run("keeps nothing with a zero limit", func(t *testing.T) {
		if got := thotBound("ab\ncd\n", thotHead(10, 0)); got != (BoundedOutput{Text: "", DroppedBytes: 6, DroppedLines: 2}) {
			t.Fatalf("unexpected zero byte limit: %+v", got)
		}
		if got := thotBound("ab\ncd\n", thotTail(0)); got != (BoundedOutput{Text: "", DroppedBytes: 6, DroppedLines: 2}) {
			t.Fatalf("unexpected zero line limit: %+v", got)
		}
	})

	t.Run("keeps exact slices of whole lines trailing newline included", func(t *testing.T) {
		if got := thotBound("a\nb\nc\n", thotHead(2)); got != (BoundedOutput{Text: "a\nb\n", DroppedBytes: 2, DroppedLines: 1}) {
			t.Fatalf("head 2: %+v", got)
		}
		if got := thotBound("a\nb\nc\n", thotTail(2)); got != (BoundedOutput{Text: "b\nc\n", DroppedBytes: 2, DroppedLines: 1}) {
			t.Fatalf("tail 2 trailing newline: %+v", got)
		}
		if got := thotBound("a\nb\nc", thotTail(2)); got != (BoundedOutput{Text: "b\nc", DroppedBytes: 2, DroppedLines: 1}) {
			t.Fatalf("tail 2 no trailing newline: %+v", got)
		}
		if got := thotBound("a\nb\nc\n\n", thotTail(3)); got != (BoundedOutput{Text: "b\nc\n\n", DroppedBytes: 2, DroppedLines: 1}) {
			t.Fatalf("tail blank line: %+v", got)
		}
	})

	t.Run("cuts at the byte limit on whole lines when possible", func(t *testing.T) {
		if got := thotBound("aa\nbb\ncc\n", thotHead(10, 7)); got != (BoundedOutput{Text: "aa\nbb\n", DroppedBytes: 3, DroppedLines: 1}) {
			t.Fatalf("head byte limit: %+v", got)
		}
		if got := thotBound("aa\nbb\ncc\n", thotTail(10, 7)); got != (BoundedOutput{Text: "bb\ncc\n", DroppedBytes: 3, DroppedLines: 1}) {
			t.Fatalf("tail byte limit: %+v", got)
		}
	})

	t.Run("cuts a single line longer than the byte limit on a character boundary", func(t *testing.T) {
		if got := thotBound("ééé\n", thotHead(10, 5)); got != (BoundedOutput{Text: "éé", DroppedBytes: 3, DroppedLines: 0}) {
			t.Fatalf("head long char boundary: %+v", got)
		}
		if got := thotBound("x\néééé", thotTail(10, 5)); got != (BoundedOutput{Text: "éé", DroppedBytes: 6, DroppedLines: 1}) {
			t.Fatalf("tail long char boundary: %+v", got)
		}
		if end := CharacterEnd([]byte("abc"), 10); end != 3 {
			t.Fatalf("CharacterEnd past end = %d, want 3", end)
		}
	})

	t.Run("cuts tails of multi-byte and raw byte edge cases on UTF-8 character boundaries", func(t *testing.T) {
		inputs := []string{
			"a\xef\xbf\xbd",
			"\xef\xbf\xbdb",
			"a\xef\xbf\xbdb",
			"\xef\xbf\xbd\xef\xbf\xbd\xef\xbf\xbd",
			"👩‍💻",
			string([]byte{'a', 0xed, 0xa0, 0xbd}),
			string([]byte{0x80, 0x80, 'b'}),
		}
		for _, input := range inputs {
			thotAssertMatchesBufferTail(t, input)
		}
	})

	t.Run("cuts tails exactly like UTF-8 tail boundary across deterministic fuzz cases", func(t *testing.T) {
		alphabet := []string{
			"a",
			"\u007f",
			"\u0080",
			"é",
			"\u07ff",
			"\u0800",
			"中",
			"\ud7ff",
			string([]byte{0xed, 0xa0, 0x80}),
			string([]byte{0xed, 0xa0, 0xbd}),
			string([]byte{0xed, 0xb0, 0x80}),
			string([]byte{0xed, 0xb9, 0x82}),
			"🙂",
			"\ue000",
			"\uffff",
		}
		var checkExhaustive func(prefix string, depth int)
		checkExhaustive = func(prefix string, depth int) {
			thotAssertMatchesBufferTail(t, prefix, thotSampledByteLimits(prefix)...)
			if depth == 0 {
				return
			}
			for _, ch := range alphabet {
				checkExhaustive(prefix+ch, depth-1)
			}
		}
		checkExhaustive("", 3)

		var seed uint32 = 0x12345678
		nextRand := func() float64 {
			seed = seed*1664525 + 1013904223
			return float64(seed) / 4294967296.0
		}
		for i := 0; i < 1000; i++ {
			length := int(nextRand() * 80)
			var input string
			for j := 0; j < length; j++ {
				idx := int(nextRand() * float64(len(alphabet)))
				input += alphabet[idx]
			}
			thotAssertMatchesBufferTail(t, input, thotSampledByteLimits(input)...)
		}
	})
}

func TestHarnessOutput_OutputBuffer(t *testing.T) {
	t.Run("keeps exact totals across chunks and decodes UTF-8 split across byte chunks", func(t *testing.T) {
		buf := NewOutputBuffer(thotTail(2))
		raw := []byte("😀\n")
		buf.Push("a\nb\n")
		buf.PushBytes(raw[:2])
		buf.PushBytes(raw[2:])
		got := buf.Snapshot()
		if got != (BoundedOutput{Text: "b\n😀\n", DroppedBytes: 2, DroppedLines: 1}) {
			t.Fatalf("unexpected snapshot: %+v", got)
		}
	})

	t.Run("sanitizes the retained text but counts the raw stream", func(t *testing.T) {
		buf := NewOutputBuffer(thotTail(1))
		buf.Push("a\x07\n")
		if got := buf.Snapshot(); got != (BoundedOutput{Text: "a\n", DroppedBytes: 0, DroppedLines: 0}) {
			t.Fatalf("first snapshot: %+v", got)
		}
		buf.Push("b\x1b\n")
		if got := buf.Snapshot(); got != (BoundedOutput{Text: "b\n", DroppedBytes: 3, DroppedLines: 1}) {
			t.Fatalf("second snapshot: %+v", got)
		}
	})

	t.Run("flushes an incomplete character before a string chunk and at the end", func(t *testing.T) {
		buf := NewOutputBuffer(thotTail(10))
		euro := []byte("€")
		buf.PushBytes(euro[:1])
		buf.Push("x")
		buf.PushBytes(euro[:2])
		buf.End()
		want := string(utf8.RuneError) + "x" + string(utf8.RuneError)
		if got := buf.Snapshot().Text; got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("matches bounding the whole stream when several chunks arrive between snapshots", func(t *testing.T) {
		limitsList := []OutputLimits{
			thotHead(3, 40),
			thotTail(3, 40),
			thotHead(50, 25),
			thotTail(50, 25),
		}
		for _, limits := range limitsList {
			buf := NewOutputBuffer(limits)
			var stream string
			for index := 0; index < 300; index++ {
				var chunk string
				if index%7 == 0 {
					for k := 0; k < index%30; k++ {
						chunk += "é"
					}
					chunk += "\n"
				} else {
					chunk = fmt.Sprintf("line %d\n", index)
				}
				stream += chunk
				buf.Push(chunk)
				if index%5 != 4 {
					continue
				}
				expected := BoundOutput(stream, limits)
				got := buf.Snapshot()
				if got != (BoundedOutput{
					Text:         expected.Text,
					DroppedBytes: expected.DroppedBytes,
					DroppedLines: expected.DroppedLines,
				}) {
					t.Fatalf("limits=%+v index=%d got=%+v want=%+v", limits, index, got, expected)
				}
			}
		}
	})

	t.Run("stops storing head output once the window is full", func(t *testing.T) {
		buf := NewOutputBuffer(thotHead(2))
		for index := 0; index < 1000; index++ {
			buf.Push(fmt.Sprintf("line %d\n", index))
		}
		if buf.StoredBytes() >= 20 {
			t.Fatalf("StoredBytes = %d, expected < 20", buf.StoredBytes())
		}
		want := BoundedOutput{Text: "line 0\nline 1\n", DroppedBytes: 8876, DroppedLines: 998}
		if got := buf.Snapshot(); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("stores only the tail window after each snapshot", func(t *testing.T) {
		limits := thotTail(3, 100)
		buf := NewOutputBuffer(limits)
		var stream string
		for index := 0; index < 2000; index++ {
			chunk := fmt.Sprintf("line %d\n\n", index)
			stream += chunk
			buf.Push(chunk)
			snapshot := buf.Snapshot()
			if buf.StoredBytes() > 100 {
				t.Fatalf("StoredBytes = %d, exceeded 100 at index %d", buf.StoredBytes(), index)
			}
			expected := BoundOutput(stream, limits)
			if snapshot.Text != expected.Text {
				t.Fatalf("snapshot text %q != expected %q", snapshot.Text, expected.Text)
			}
		}
	})
}

type thotFakeTimer struct {
	clock     *thotFakeClock
	id        int
	at        int64
	fn        func()
	cancelled bool
}

func (tm *thotFakeTimer) Stop() bool {
	tm.clock.mu.Lock()
	defer tm.clock.mu.Unlock()
	wasPending := !tm.cancelled
	tm.cancelled = true
	return wasPending
}

type thotFakeClock struct {
	mu     sync.Mutex
	now    int64
	nextId int
	timers []*thotFakeTimer
}

func (c *thotFakeClock) nowMs() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *thotFakeClock) afterFunc(d time.Duration, fn func()) hoTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextId++
	tm := &thotFakeTimer{
		clock: c,
		id:    c.nextId,
		at:    c.now + d.Milliseconds(),
		fn:    fn,
	}
	c.timers = append(c.timers, tm)
	return tm
}

func (c *thotFakeClock) pendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, tm := range c.timers {
		if !tm.cancelled {
			n++
		}
	}
	return n
}

func (c *thotFakeClock) advance(ms int64) {
	c.mu.Lock()
	target := c.now + ms
	c.mu.Unlock()

	for {
		var due *thotFakeTimer
		c.mu.Lock()
		bestIdx := -1
		for i, tm := range c.timers {
			if tm.cancelled || tm.at > target {
				continue
			}
			if bestIdx == -1 || tm.at < c.timers[bestIdx].at {
				bestIdx = i
			}
		}
		if bestIdx == -1 {
			c.now = target
			c.mu.Unlock()
			return
		}
		due = c.timers[bestIdx]
		due.cancelled = true
		c.now = due.at
		c.mu.Unlock()

		due.fn()
	}
}

func thotWaitProgressSettled(t *testing.T, p *hoProgress, wantTimer bool, fc *thotFakeClock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		inFlight := p.inFlight
		hasTimer := p.timer != nil
		p.mu.Unlock()
		if !inFlight && (!wantTimer || (hasTimer && fc.pendingCount() > 0)) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for hoProgress flush to settle")
}

func TestHarnessOutput_Progress(t *testing.T) {
	t.Run("commits first change at once then waits at least 100ms and written size at 100 KiB/s", func(t *testing.T) {
		fc := &thotFakeClock{}
		var commitMu sync.Mutex
		var commits []int64
		var size atomic.Int64
		size.Store(50 * 1024)
		commitSignal := make(chan struct{}, 10)

		var p *hoProgress
		p = hoNewProgressWithClock(
			func() (int, error) {
				commitMu.Lock()
				commits = append(commits, fc.nowMs())
				commitMu.Unlock()
				commitSignal <- struct{}{}
				return int(size.Load()), nil
			},
			func(error) {},
			fc.nowMs,
			fc.afterFunc,
		)

		p.Mark()
		select {
		case <-commitSignal:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for initial progress commit")
		}
		thotWaitProgressSettled(t, p, false, fc)

		commitMu.Lock()
		if !slices.Equal(commits, []int64{0}) {
			commitMu.Unlock()
			t.Fatalf("initial commits = %v, want [0]", commits)
		}
		commitMu.Unlock()

		// 50 KiB buys 500 ms; changes meanwhile coalesce into one commit.
		size.Store(10)
		p.Mark()
		p.Mark()
		thotWaitProgressSettled(t, p, true, fc)

		fc.advance(499)
		commitMu.Lock()
		if !slices.Equal(commits, []int64{0}) {
			commitMu.Unlock()
			t.Fatalf("commits at 499ms = %v, want [0]", commits)
		}
		commitMu.Unlock()

		fc.advance(1)
		select {
		case <-commitSignal:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for second progress commit")
		}
		thotWaitProgressSettled(t, p, false, fc)

		commitMu.Lock()
		if !slices.Equal(commits, []int64{0, 500}) {
			commitMu.Unlock()
			t.Fatalf("commits at 500ms = %v, want [0 500]", commits)
		}
		commitMu.Unlock()

		// A small commit still waits the minimum 100 ms.
		p.Mark()
		thotWaitProgressSettled(t, p, true, fc)
		fc.advance(99)
		commitMu.Lock()
		if !slices.Equal(commits, []int64{0, 500}) {
			commitMu.Unlock()
			t.Fatalf("commits at 599ms = %v, want [0 500]", commits)
		}
		commitMu.Unlock()

		fc.advance(1)
		select {
		case <-commitSignal:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for third progress commit")
		}
		thotWaitProgressSettled(t, p, false, fc)

		commitMu.Lock()
		got := slices.Clone(commits)
		commitMu.Unlock()
		if !slices.Equal(got, []int64{0, 500, 600}) {
			t.Fatalf("commits at 600ms = %v, want [0 500 600]", got)
		}
		_ = p.Stop()
	})

	t.Run("rejects waiters of a failed commit and reports its error", func(t *testing.T) {
		var errMu sync.Mutex
		var reported []error
		failure := errors.New("commit failed")
		p := hoNewProgress(
			func() (int, error) {
				return 0, failure
			},
			func(err error) {
				errMu.Lock()
				reported = append(reported, err)
				errMu.Unlock()
			},
		)
		err := p.MarkAndWait(nil)
		if !errors.Is(err, failure) {
			t.Fatalf("MarkAndWait err = %v, want %v", err, failure)
		}
		thotWaitProgressSettled(t, p, false, nil)
		errMu.Lock()
		if len(reported) != 1 || !errors.Is(reported[0], failure) {
			errMu.Unlock()
			t.Fatalf("reported errors = %v, want [%v]", reported, failure)
		}
		errMu.Unlock()
		_ = p.Stop()
	})

	t.Run("stops: waits for commit in flight and hands back waiters no commit covered yet", func(t *testing.T) {
		fc := &thotFakeClock{}
		inFlight := make(chan struct{})
		entered := make(chan struct{})
		p := hoNewProgressWithClock(
			func() (int, error) {
				close(entered)
				<-inFlight
				return 0, nil
			},
			func(error) {},
			fc.nowMs,
			fc.afterFunc,
		)

		firstDone := make(chan error, 1)
		go func() {
			firstDone <- p.MarkAndWait(nil)
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for first write entry")
		}

		secondDone := make(chan error, 1)
		go func() {
			secondDone <- p.MarkAndWait(nil)
		}()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			p.mu.Lock()
			n := len(p.waiters)
			p.mu.Unlock()
			if n == 1 {
				break
			}
			time.Sleep(time.Millisecond)
		}

		pendingCh := make(chan []chan error, 1)
		go func() {
			pendingCh <- p.Stop()
		}()
		// Ensure Stop() is waiting on inFlight before releasing.
		time.Sleep(10 * time.Millisecond)
		close(inFlight)

		pending := <-pendingCh
		if err := <-firstDone; err != nil {
			t.Fatalf("first error = %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("pending waiters = %d, want 1", len(pending))
		}
		pending[0] <- nil
		if err := <-secondDone; err != nil {
			t.Fatalf("second error = %v", err)
		}
	})
}
