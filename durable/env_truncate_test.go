// Ported from packages/durable/test/env-truncate.test.ts (pi v1.0.0).

package durable_test

import (
	"testing"

	"github.com/keejkrej/pi-go/durable"
)

func tetPtr(v int) *int { return &v }

func TestTruncateUtilities(t *testing.T) {
	t.Run("reports UTF-8 byte counts in truncation results", func(t *testing.T) {
		content := "aé🙂\nb"
		result := durable.TruncateHead(content, &durable.TruncationOptions{
			MaxBytes: tetPtr(100),
			MaxLines: tetPtr(10),
		})

		if result.Truncated {
			t.Fatalf("expected Truncated=false, got true")
		}
		if result.TotalBytes != len(content) || result.TotalBytes != 9 {
			t.Fatalf("expected TotalBytes=9 (%d), got %d", len(content), result.TotalBytes)
		}
		if result.OutputBytes != len(content) {
			t.Fatalf("expected OutputBytes=%d, got %d", len(content), result.OutputBytes)
		}
	})

	t.Run("counts UTF-8 bytes and truncates correctly", func(t *testing.T) {
		content := "aé🙂\nb"
		head := durable.TruncateHead(content, &durable.TruncationOptions{
			MaxBytes: tetPtr(7),
			MaxLines: tetPtr(10),
		})
		if head.Content != "aé🙂" || head.OutputBytes != 7 || head.TruncatedBy.OrElse("") != "bytes" {
			t.Fatalf("unexpected head result: %+v", head)
		}
	})

	t.Run("does not count a trailing newline as an extra line", func(t *testing.T) {
		content := "line\nline\nline\n"
		head := durable.TruncateHead(content, &durable.TruncationOptions{
			MaxBytes: tetPtr(100),
			MaxLines: tetPtr(3),
		})
		if head.Truncated || head.TotalLines != 3 || head.OutputLines != 3 {
			t.Fatalf("unexpected head result: %+v", head)
		}
	})

	t.Run("truncates head by line limits", func(t *testing.T) {
		content := "one\ntwo\nthree\nfour"
		head := durable.TruncateHead(content, &durable.TruncationOptions{
			MaxBytes: tetPtr(100),
			MaxLines: tetPtr(2),
		})
		if head.Content != "one\ntwo" || !head.Truncated || head.TruncatedBy.OrElse("") != "lines" || head.TotalLines != 4 || head.OutputLines != 2 {
			t.Fatalf("unexpected head result: %+v", head)
		}
	})

	t.Run("reports bytes when only a trailing newline exceeds limits at the line cap", func(t *testing.T) {
		head := durable.TruncateHead("hello\nworld\n", &durable.TruncationOptions{
			MaxBytes: tetPtr(11),
			MaxLines: tetPtr(2),
		})
		if head.Content != "hello\nworld" || !head.Truncated || head.TruncatedBy.OrElse("") != "bytes" || head.TotalLines != 2 || head.OutputLines != 2 {
			t.Fatalf("unexpected head result: %+v", head)
		}
	})

	t.Run("truncates head on UTF-8 byte limits without partial lines", func(t *testing.T) {
		content := "éé\nabc"
		result := durable.TruncateHead(content, &durable.TruncationOptions{
			MaxBytes: tetPtr(4),
			MaxLines: tetPtr(10),
		})
		if result.Content != "éé" || !result.Truncated || result.TruncatedBy.OrElse("") != "bytes" || result.OutputBytes != 4 || result.FirstLineExceedsLimit {
			t.Fatalf("unexpected head result: %+v", result)
		}
	})

	t.Run("reports head truncation when the first line exceeds the byte limit", func(t *testing.T) {
		result := durable.TruncateHead("éé\nabc", &durable.TruncationOptions{
			MaxBytes: tetPtr(3),
			MaxLines: tetPtr(10),
		})
		if result.Content != "" || !result.Truncated || result.TruncatedBy.OrElse("") != "bytes" || !result.FirstLineExceedsLimit {
			t.Fatalf("unexpected head result: %+v", result)
		}
	})

	t.Run("formats sizes", func(t *testing.T) {
		if got := durable.FormatSize(1023); got != "1023B" {
			t.Fatalf("got %q, want 1023B", got)
		}
		if got := durable.FormatSize(1536); got != "1.5KB" {
			t.Fatalf("got %q, want 1.5KB", got)
		}
		if got := durable.FormatSize(3 * 1024 * 1024); got != "3.0MB" {
			t.Fatalf("got %q, want 3.0MB", got)
		}
	})
}
