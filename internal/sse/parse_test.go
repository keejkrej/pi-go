package sse

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func i64Ptr(n int64) *int64 { return &n }

func eqEvent(a, b Event) bool {
	if a.Event != b.Event || a.Data != b.Data {
		return false
	}
	if (a.ID == nil) != (b.ID == nil) || (a.ID != nil && *a.ID != *b.ID) {
		return false
	}
	if (a.Retry == nil) != (b.Retry == nil) || (a.Retry != nil && *a.Retry != *b.Retry) {
		return false
	}
	return true
}

func TestParserEvents(t *testing.T) {
	var events []Event
	var comments []string
	var retries []int64
	var errs []string
	p := CreateParser(ParserConfig{
		OnEvent:   func(ev Event) { events = append(events, ev) },
		OnComment: func(c string) { comments = append(comments, c) },
		OnRetry:   func(n int64) { retries = append(retries, n) },
		OnError: func(err *ParseError) {
			errs = append(errs, err.Error())
		},
	})
	chunk := strings.Join([]string{
		": hi",
		":hi",
		"event: ping",
		"data: a",
		"data: b",
		"id: 1",
		"retry: 15",
		"",
		"id: a\x00b",
		"data: y",
		"",
		"id:",
		"event:",
		"data: z",
		"retry: 007",
		"retry: 20",
		"",
		"retry: 5",
		"",
		"event: only",
		"",
		"foo: bar",
		"retry: 12a",
		"data: after",
		"",
	}, "\n") + "\n"
	if err := p.Feed(chunk); err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{ID: strPtr("1"), Event: "ping", Data: "a\nb", Retry: i64Ptr(15)},
		{Event: "", Data: "y"},
		{ID: strPtr(""), Event: "", Data: "z", Retry: i64Ptr(20)},
		{Data: "after"},
	}
	if len(events) != len(want) {
		t.Fatalf("events %#v", events)
	}
	for i := range want {
		if !eqEvent(events[i], want[i]) {
			t.Errorf("event %d = %#v, want %#v", i, events[i], want[i])
		}
	}
	if strings.Join(comments, ",") != "hi,hi" {
		t.Errorf("comments %q", comments)
	}
	if joinInts(retries) != "15,7,20,5" {
		t.Errorf("retries %s", joinInts(retries))
	}
	if len(errs) != 2 || !strings.Contains(errs[0], `Unknown field "foo"`) || !strings.Contains(errs[1], "Invalid `retry` value: \"12a\"") {
		t.Errorf("errors %q", errs)
	}
	if events[1].ID != nil {
		t.Error("id leaked into the next event")
	}
}

func joinInts(ns []int64) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

func TestParserLineEndingsAndChunks(t *testing.T) {
	var events []Event
	p := CreateParser(ParserConfig{OnEvent: func(ev Event) { events = append(events, ev) }})
	if err := p.Feed("data: x\r"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("held CR dispatched %#v", events)
	}
	if err := p.Feed("\ndata: y\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "x\ny" {
		t.Fatalf("got %#v", events)
	}
	events = nil
	// A single CR separates lines. Two CRs are a line break and a blank line.
	if err := p.Feed("data: cr\rdata: lf\n\n"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "cr\nlf" {
		t.Fatalf("got %#v", events)
	}
}

func TestParserFinishFlushesDataNotEventOnly(t *testing.T) {
	var events []Event
	p := CreateParser(ParserConfig{OnEvent: func(ev Event) { events = append(events, ev) }})
	if err := p.Feed("event: ping\ndata: hi"); err != nil {
		t.Fatal(err)
	}
	p.Finish()
	if len(events) != 1 || events[0].Event != "ping" || events[0].Data != "hi" {
		t.Fatalf("finish %#v", events)
	}
	events = nil
	p = CreateParser(ParserConfig{OnEvent: func(ev Event) { events = append(events, ev) }})
	if err := p.Feed("event: ping\n"); err != nil {
		t.Fatal(err)
	}
	p.Finish()
	if len(events) != 0 {
		t.Fatalf("event-only dispatched %#v", events)
	}
	events = nil
	p = CreateParser(ParserConfig{OnEvent: func(ev Event) { events = append(events, ev) }})
	if err := p.Feed("data: hi\r"); err != nil {
		t.Fatal(err)
	}
	p.Finish()
	if len(events) != 1 || events[0].Data != "hi" {
		t.Fatalf("CR at EOF %#v", events)
	}
}

func TestParserBOMAndBuffer(t *testing.T) {
	var events []Event
	p := CreateParser(ParserConfig{OnEvent: func(ev Event) { events = append(events, ev) }})
	if err := p.Feed(latin1BOM + "data: a\n\n"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "a" {
		t.Fatalf("bom %#v", events)
	}

	limit := 5
	var messages []string
	p = CreateParser(ParserConfig{
		MaxBufferSize: &limit,
		OnEvent:       func(ev Event) { events = append(events, ev) },
		OnError: func(err *ParseError) {
			messages = append(messages, err.Error())
			if err.Type != ErrorTypeMaxBufferSizeExceeded {
				t.Errorf("type %s", err.Type)
			}
		},
	})
	events = nil
	if err := p.Feed("data: abcdefghij"); err != nil {
		t.Fatal(err)
	}
	const wantTerm = "Cannot feed parser: it was terminated after exceeding the configured max buffer size. Call `reset()` to resume parsing."
	if err := p.Feed("more"); err == nil || err.Error() != wantTerm {
		t.Fatalf("second feed %v", err)
	}
	if len(messages) != 1 || !strings.Contains(messages[0], "max buffer size of 5") {
		t.Errorf("overflow messages %q", messages)
	}
	if len(events) != 0 {
		t.Errorf("overflow dispatched %#v", events)
	}
	p.Reset(nil)
	if err := p.Feed("data: z\n\n"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Data != "z" {
		t.Fatalf("after reset %#v", events)
	}
}

func TestParserRetrySaturates(t *testing.T) {
	var got int64
	p := CreateParser(ParserConfig{OnRetry: func(n int64) { got = n }})
	if err := p.Feed("retry: 999999999999999999999\n\n"); err != nil {
		t.Fatal(err)
	}
	if got != 9223372036854775807 {
		t.Fatalf("retry %d", got)
	}
}

type chunkReader struct {
	parts []string
	i     int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.i >= len(c.parts) {
		return 0, io.EOF
	}
	s := c.parts[c.i]
	c.i++
	n := copy(p, s)
	if n < len(s) {
		c.parts[c.i-1] = s[n:]
		c.i--
	}
	return n, nil
}

func collect(t *testing.T, r *Reader) []Event {
	t.Helper()
	var out []Event
	for {
		ev, err := r.Next()
		if err == io.EOF {
			_, err2 := r.Next()
			if err2 != io.EOF {
				t.Fatalf("EOF not sticky: %v", err2)
			}
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
}

func TestReaderNext(t *testing.T) {
	r := NewReader(strings.NewReader("data: a\ndata: b\n\nevent: ping\ndata: c\n\n"))
	got := collect(t, r)
	if len(got) != 2 || got[0].Data != "a\nb" || got[1].Event != "ping" || got[1].Data != "c" {
		t.Fatalf("%#v", got)
	}

	r = NewReader(&chunkReader{parts: []string{"data: hel", "lo\n\n"}})
	got = collect(t, r)
	if len(got) != 1 || got[0].Data != "hello" {
		t.Fatalf("chunked %#v", got)
	}

	// A trailing data event with no blank line is dispatched at EOF.
	r = NewReader(strings.NewReader("data: tail"))
	got = collect(t, r)
	if len(got) != 1 || got[0].Data != "tail" {
		t.Fatalf("tail %#v", got)
	}

	// An event type with no data is not an event.
	r = NewReader(strings.NewReader("event: ping\n\n"))
	got = collect(t, r)
	if len(got) != 0 {
		t.Fatalf("event-only %#v", got)
	}

	var retries []int64
	r = NewReaderWithOptions(strings.NewReader("retry: 12\ndata: q\n\n"), &StreamOptions{
		OnRetry: func(n int64) { retries = append(retries, n) },
	})
	got = collect(t, r)
	if len(got) != 1 || got[0].Retry == nil || *got[0].Retry != 12 || len(retries) != 1 || retries[0] != 12 {
		t.Fatalf("retry event %#v callbacks %v", got, retries)
	}

	retries = nil
	r = NewReaderWithOptions(strings.NewReader("retry: 9\n\n"), &StreamOptions{
		OnRetry: func(n int64) { retries = append(retries, n) },
	})
	got = collect(t, r)
	if len(got) != 0 || len(retries) != 1 || retries[0] != 9 {
		t.Fatalf("retry-only events %#v callbacks %v", got, retries)
	}

	// UTF-8 BOM is dropped by the decoder. The Latin-1 BOM characters are
	// stripped by the parser when they are the first decoded characters.
	r = NewReader(strings.NewReader("\uFEFFdata: bom\n\n"))
	got = collect(t, r)
	if len(got) != 1 || got[0].Data != "bom" {
		t.Fatalf("utf8 bom %#v", got)
	}

	id := "abc"
	r = NewReader(strings.NewReader("id: " + id + "\ndata: x\n\ndata: y\n\n"))
	got = collect(t, r)
	if len(got) != 2 || got[0].ID == nil || *got[0].ID != "abc" || got[1].ID != nil {
		t.Fatalf("id %#v", got)
	}
}

func TestReaderUnknownFieldContinues(t *testing.T) {
	var nerr int
	r := NewReaderWithOptions(strings.NewReader("nope: x\ndata: ok\n\n"), &StreamOptions{
		OnError: func(err error) {
			nerr++
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Type != ErrorTypeUnknownField {
				t.Errorf("err %v", err)
			}
		},
	})
	got := collect(t, r)
	if nerr != 1 || len(got) != 1 || got[0].Data != "ok" {
		t.Fatalf("nerr %d events %#v", nerr, got)
	}
}
