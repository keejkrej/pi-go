package agentloop

import (
	"strings"
	"testing"
)

func TestTruncateHead_Lines(t *testing.T) {
	content := strings.Repeat("line\n", 9) + "line"
	res := TruncateHead(content, TruncateOptions{MaxLines: 3, MaxBytes: -1})
	if !res.Truncated || res.TruncatedBy != "lines" {
		t.Fatalf("res = %+v", res)
	}
	if res.Content != "line\nline\nline" {
		t.Fatalf("content = %q", res.Content)
	}
	if res.TotalLines != 10 || res.OutputLines != 3 {
		t.Fatalf("lines = %d/%d", res.OutputLines, res.TotalLines)
	}
}

func TestTruncateHead_Bytes(t *testing.T) {
	content := strings.Repeat("a", 100)
	res := TruncateHead(content, TruncateOptions{MaxLines: -1, MaxBytes: 10})
	if !res.Truncated || res.TruncatedBy != "bytes" || len(res.Content) != 10 {
		t.Fatalf("res = %+v", res)
	}
	if !res.LastLinePartial {
		t.Fatal("expected LastLinePartial")
	}
}

func TestTruncateHead_RuneBoundary(t *testing.T) {
	content := strings.Repeat("é", 10) // 2 bytes each
	res := TruncateHead(content, TruncateOptions{MaxLines: -1, MaxBytes: 5})
	if res.Content != "éé" {
		t.Fatalf("content = %q (len %d)", res.Content, len(res.Content))
	}
}

func TestTruncateTail_Lines(t *testing.T) {
	content := "first\nsecond\nthird\nfourth"
	res := TruncateTail(content, TruncateOptions{MaxLines: 2, MaxBytes: -1})
	if res.Content != "third\nfourth" {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestTruncateTail_Bytes(t *testing.T) {
	content := "aaaa" + strings.Repeat("é", 3)
	res := TruncateTail(content, TruncateOptions{MaxLines: -1, MaxBytes: 5})
	// 5 bytes from the end lands mid-rune; the partial rune is dropped.
	if res.Content != "éé" {
		t.Fatalf("content = %q", res.Content)
	}
}

func TestTruncate_NoOp(t *testing.T) {
	res := TruncateHead("short", TruncateOptions{})
	if res.Truncated || res.Content != "short" {
		t.Fatalf("res = %+v", res)
	}
}

func TestTruncateLine(t *testing.T) {
	if got := TruncateLine("abcdef", 3); got != "abc..." {
		t.Fatalf("got %q", got)
	}
	if got := TruncateLine("ab", 3); got != "ab" {
		t.Fatalf("got %q", got)
	}
}
