// Ported from packages/coding-agent/test/rpc-jsonl.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func trjCollectLines(t *testing.T, input string) []string {
	t.Helper()
	var (
		mu    sync.Mutex
		lines []string
		done  = make(chan struct{})
	)
	stop := AttachJsonlLineReader(context.Background(), strings.NewReader(input), func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}, func() {
		close(done)
	})
	defer stop()
	<-done

	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), lines...)
}

func TestJsonlSerializeStrictWithoutEscapingUnicodeSeparators(t *testing.T) {
	payload := map[string]string{"text": "a\u2028b\u2029c"}
	line, err := SerializeJsonLine(payload)
	if err != nil {
		t.Fatalf("SerializeJsonLine failed: %v", err)
	}
	if !strings.Contains(line, "a\u2028b\u2029c") {
		t.Fatalf("expected raw U+2028/U+2029 in line, got %q", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("expected trailing LF, got %q", line)
	}
	var decoded map[string]string
	if err := jsonx.Unmarshal([]byte(strings.TrimSpace(line)), &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded["text"] != "a\u2028b\u2029c" {
		t.Fatalf("round-trip mismatch: got %q", decoded["text"])
	}
}

func TestJsonlSplitsOnLFOnlyPreservingUnicodeSeparators(t *testing.T) {
	serialized, err := SerializeJsonLine(map[string]string{"text": "a\u2028b\u2029c"})
	if err != nil {
		t.Fatalf("SerializeJsonLine failed: %v", err)
	}
	lines := trjCollectLines(t, serialized)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d (%#v)", len(lines), lines)
	}
	var decoded map[string]string
	if err := jsonx.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("Unmarshal line failed: %v", err)
	}
	if decoded["text"] != "a\u2028b\u2029c" {
		t.Fatalf("expected preserved U+2028/U+2029, got %q", decoded["text"])
	}
}

func TestJsonlHandlesCRLFInput(t *testing.T) {
	lines := trjCollectLines(t, "{\"a\":1}\r\n{\"b\":2}\r\n")
	want := []string{`{"a":1}`, `{"b":2}`}
	if len(lines) != len(want) || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("got %#v, want %#v", lines, want)
	}
}

func TestJsonlEmitsTrailingLineWithoutLF(t *testing.T) {
	lines := trjCollectLines(t, `{"a":1}`)
	if len(lines) != 1 || lines[0] != `{"a":1}` {
		t.Fatalf("got %#v, want [{\"a\":1}]", lines)
	}
}

func TestJsonlHandlesLongLinesAbove64KB(t *testing.T) {
	big := strings.Repeat("x", 150*1024)
	serialized, err := SerializeJsonLine(map[string]string{"payload": big})
	if err != nil {
		t.Fatalf("SerializeJsonLine failed: %v", err)
	}
	lines := trjCollectLines(t, serialized)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var decoded map[string]string
	if err := jsonx.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(decoded["payload"]) != len(big) {
		t.Fatalf("payload len mismatch: got %d, want %d", len(decoded["payload"]), len(big))
	}
}
