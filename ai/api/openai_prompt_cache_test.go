// Unit tests for openai_prompt_cache.go (pi v1.5.2).

package api

import (
	"strings"
	"testing"
)

func topcStrPtr(s string) *string { return &s }

func TestClampOpenAIPromptCacheKey(t *testing.T) {
	if got := ClampOpenAIPromptCacheKey(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %v", *got)
	}

	short := "session-123"
	got := ClampOpenAIPromptCacheKey(&short)
	if got == nil || *got != short {
		t.Fatalf("got %v, want %q", got, short)
	}
	if got == &short {
		t.Fatalf("expected distinct string copy")
	}

	asciiLong := strings.Repeat("x", 67)
	gotASCII := ClampOpenAIPromptCacheKey(&asciiLong)
	if gotASCII == nil || *gotASCII != strings.Repeat("x", OpenaiPromptCacheKeyMaxLength) {
		t.Fatalf("expected 64 ASCII chars, got %v", gotASCII)
	}

	// Surrogate/astral runes count by Unicode scalar value (rune) cleanly without splitting UTF-8 sequences.
	astral := strings.Repeat("𐐀", 67)
	gotAstral := ClampOpenAIPromptCacheKey(&astral)
	wantAstral := strings.Repeat("𐐀", 64)
	if gotAstral == nil || *gotAstral != wantAstral {
		t.Fatalf("expected 64 astral runes, got len=%d", len([]rune(*gotAstral)))
	}
}
