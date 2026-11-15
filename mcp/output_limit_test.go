// Ported from packages/mcp/test/output-limit.test.ts (pi v1.5.2).

package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

func toutlTextOf(t *testing.T, content []protocol.LlmContent) string {
	t.Helper()
	if len(content) == 0 {
		t.Fatal("expected at least one content block")
	}
	tc, ok := content[0].(*protocol.LlmTextContent)
	if !ok {
		t.Fatalf("expected text block first, got %T", content[0])
	}
	return tc.Text
}

func TestOutputLimit_WithinLimitPassesThroughWithoutSaving(t *testing.T) {
	image := &protocol.LlmImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}
	content := []protocol.LlmContent{
		&protocol.LlmTextContent{Type: "text", Text: strings.Repeat("x", McpOutputMaxBytes)},
		image,
	}
	saves := 0
	limited := LimitMcpContent(t.Context(), content, func(context.Context, []byte, string) (string, error) {
		saves++
		return "/tmp/full.txt", nil
	})
	if saves != 0 {
		t.Fatalf("saves = %d, want 0", saves)
	}
	if limited.FullOutputPath != nil {
		t.Fatalf("expected nil FullOutputPath, got %q", *limited.FullOutputPath)
	}
	if !jsonx.DeepEqual(limited.Content, content) {
		t.Fatalf("content mutated: %#v", limited.Content)
	}
}

func TestOutputLimit_CutsMiddleSavesFullAndKeepsImages(t *testing.T) {
	image := &protocol.LlmImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"}
	lines := make([]string, 3000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	full := strings.Join(lines, "\n")
	split := strings.Index(full[100:], "\n") + 100

	type savedEntry struct {
		data string
		ext  string
	}
	var saved []savedEntry
	limited := LimitMcpContent(
		t.Context(),
		[]protocol.LlmContent{
			&protocol.LlmTextContent{Type: "text", Text: full[:split]},
			image,
			&protocol.LlmTextContent{Type: "text", Text: full[split+1:]},
		},
		func(_ context.Context, data []byte, extension string) (string, error) {
			saved = append(saved, savedEntry{data: string(data), ext: extension})
			return "/tmp/full.txt", nil
		},
	)
	if len(saved) != 1 || saved[0].data != full || saved[0].ext != ".txt" {
		t.Fatalf("unexpected saved calls: %d", len(saved))
	}
	if limited.FullOutputPath == nil || *limited.FullOutputPath != "/tmp/full.txt" {
		t.Fatalf("FullOutputPath = %v", limited.FullOutputPath)
	}
	if len(limited.Content) != 2 || !jsonx.DeepEqual(limited.Content[1], image) {
		t.Fatalf("unexpected limited.Content = %#v", limited.Content)
	}

	text := toutlTextOf(t, limited.Content)
	wantPrefix := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: 3000\n\nline 1\nline 2\n", (len(full)+3)/4)
	if !strings.HasPrefix(text, wantPrefix) {
		t.Fatalf("unexpected prefix: %q", text[:min(len(text), 160)])
	}
	if !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
		t.Fatalf("missing truncation marker in %q", text)
	}
	if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") {
		t.Fatalf("unexpected suffix: %q", text[max(0, len(text)-100):])
	}
	if len(text) >= McpOutputMaxBytes+1024 {
		t.Fatalf("text length %d unexpectedly large", len(text))
	}
}

func TestOutputLimit_ReportsSaveFailureAndOmitsPathWithoutSaver(t *testing.T) {
	content := []protocol.LlmContent{
		&protocol.LlmTextContent{Type: "text", Text: strings.Repeat("y", McpOutputMaxBytes+1)},
	}
	failed := LimitMcpContent(t.Context(), content, func(context.Context, []byte, string) (string, error) {
		return "", errors.New("disk full")
	})
	if failed.FullOutputPath != nil {
		t.Fatalf("expected nil FullOutputPath")
	}
	if !strings.HasSuffix(toutlTextOf(t, failed.Content), "\n\n[Could not save the full output: disk full]") {
		t.Fatalf("unexpected failed suffix: %q", toutlTextOf(t, failed.Content))
	}

	unsaved := LimitMcpContent(t.Context(), content, nil)
	if unsaved.FullOutputPath != nil {
		t.Fatalf("expected nil FullOutputPath")
	}
	unsavedText := toutlTextOf(t, unsaved.Content)
	if !regexp.MustCompile(`(?s)^Warning: truncated output .*…\d+ chars truncated…y+$`).MatchString(unsavedText) {
		t.Fatalf("unexpected unsaved text: %q", unsavedText)
	}
}

func TestOutputLimit_CutsMultiByteOnlyAtRuneBoundaries(t *testing.T) {
	text := "a" + strings.Repeat("é", 20_000) + "end"
	limited := LimitMcpContent(t.Context(), []protocol.LlmContent{&protocol.LlmTextContent{Type: "text", Text: text}}, nil)
	out := toutlTextOf(t, limited.Content)
	if !utf8.ValidString(out) || strings.ContainsRune(out, utf8.RuneError) {
		t.Fatal("output contains invalid UTF-8 / replacement rune")
	}
	if !strings.HasSuffix(out, "end") {
		t.Fatalf("expected suffix 'end', got %q", out[max(0, len(out)-20):])
	}
	m := regexp.MustCompile(`…(\d+) chars truncated…`).FindStringSubmatch(out)
	if len(m) != 2 {
		t.Fatalf("expected truncation marker in %q", out)
	}
	removed, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	body := out[strings.Index(out, "\n\n")+2:]
	parts := strings.Split(body, m[0])
	if len(parts) != 2 {
		t.Fatalf("expected 2 halves around marker, got %d", len(parts))
	}
	head, tail := parts[0], parts[1]
	if len(head) > McpOutputMaxBytes/2 || len(tail) > McpOutputMaxBytes/2 {
		t.Fatalf("halves exceed budget: head=%d tail=%d", len(head), len(tail))
	}
	if utf8.RuneCountInString(head)+utf8.RuneCountInString(tail)+removed != utf8.RuneCountInString(text) {
		t.Fatalf("rune count mismatch")
	}
}
