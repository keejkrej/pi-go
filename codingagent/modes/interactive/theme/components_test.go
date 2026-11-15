package theme

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestRenderDiff(t *testing.T) {
	t.Cleanup(func() { _, _ = InitTheme("dark") })
	_, _ = InitTheme("dark")

	cases := []struct {
		name     string
		diff     string
		expected []string
	}{
		{
			name:     "text inserted at start of line",
			diff:     "-83   await commitRecordingChunk({\n+83   const result = await commitRecordingChunk({",
			expected: []string{"-83   await commitRecordingChunk({", "+83   const result = await commitRecordingChunk({"},
		},
		{
			name:     "indented text replaced",
			diff:     "-12     const value = oldValue\n+12     const value = newValue",
			expected: []string{"-12     const value = oldValue", "+12     const value = newValue"},
		},
	}

	for _, tc := range cases {
		rendered := RenderDiff(tc.diff)
		lines := strings.Split(utils.StripAnsi(rendered), "\n")
		if !slices.Equal(lines, tc.expected) {
			t.Fatalf("%s: StripAnsi mismatch: got=%q want=%q", tc.name, lines, tc.expected)
		}
	}

	diffText := "-83   await commitRecordingChunk({\n+83   const result = await commitRecordingChunk({"
	rendered := RenderDiff(diffText)
	invInsert := CurrentTheme().Inverse("const result = ")
	prefixIdx := strings.Index(rendered, "+83   ")
	invIdx := strings.Index(rendered, invInsert)
	if prefixIdx < 0 || invIdx < 0 || prefixIdx >= invIdx {
		t.Fatalf("expected shared indentation '+83   ' before inverse insertion %q in %q", invInsert, rendered)
	}
}

func TestKeybindingHintsAndVisualTruncate(t *testing.T) {
	_, _ = InitTheme("dark")
	if got := FormatKeys([]tui.KeyId{"escape", "ctrl+c"}); got != "escape/ctrl+c" {
		t.Fatalf("FormatKeys = %q", got)
	}
	hint := KeyHint("enter", "confirm")
	if !strings.Contains(utils.StripAnsi(hint), "enter confirm") {
		t.Fatalf("unexpected KeyHint: %q", hint)
	}

	res := TruncateToVisualLines("line one\nline two\nline three\nline four", 2, 40, 0)
	if len(res.VisualLines) != 2 || res.SkippedCount < 2 {
		t.Fatalf("TruncateToVisualLines = %+v", res)
	}
}
