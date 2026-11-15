// Ported from packages/tui/test/bug-regression-isimageline-startswith-bug.test.ts (pi v1.5.2).

package tui_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestBugRegressionIsImageLine(t *testing.T) {
	t.Run("detects image sequences in tool output and Kitty lines", func(t *testing.T) {
		toolOutputLine := "Read image file [image/jpeg]\x1b]1337;File=size=800,600;inline=1:base64image...\x07"
		if !tui.IsImageLine(toolOutputLine) {
			t.Fatalf("should detect image sequence in tool output line")
		}

		kittyLine := "\x1b_Ga=T,f=100,t=f,d=base64data...\x1b\\\x1b_Gm=i=1;\x1b\\"
		if !tui.IsImageLine(kittyLine) {
			t.Fatalf("should detect Kitty image component output")
		}
	})

	t.Run("handles ANSI codes before image sequences", func(t *testing.T) {
		lines := []string{
			"\x1b[31mError\x1b[0m: \x1b]1337;File=inline=1:base64==\x07",
			"\x1b[33mWarning\x1b[0m: \x1b_Ga=T,data...\x1b\\",
			"\x1b[1mBold\x1b[0m \x1b]1337;File=:base64==\x07\x1b[0m",
		}
		for _, line := range lines {
			if !tui.IsImageLine(line) {
				t.Fatalf("should detect image sequence after ANSI codes: %q", line)
			}
		}
	})

	t.Run("handles very long lines with image sequences", func(t *testing.T) {
		crashLine := "Output: \x1b]1337;File=size=800,600;inline=1:" + strings.Repeat("A", 304000) + " end of output"
		if !tui.IsImageLine(crashLine) {
			t.Fatalf("should detect image sequence in 300KB+ line")
		}

		prefix := "Text"
		seq := "\x1b_Ga=T,f=100"
		suffix := "End"
		line58649 := prefix + seq + strings.Repeat("A", 58649-len(prefix)-len(seq)-len(suffix)) + suffix
		if len(line58649) != 58649 || !tui.IsImageLine(line58649) {
			t.Fatalf("should detect image sequence in 58649-char line")
		}
	})

	t.Run("does not false positive on plain text or file paths", func(t *testing.T) {
		if tui.IsImageLine(strings.Repeat("A", 100000)) {
			t.Fatalf("should not detect images in plain long text")
		}
		paths := []string{
			"/path/to/1337/image.jpg",
			"/usr/local/bin/File_converter",
			"~/Documents/1337File_backup.png",
			"./_G_test_file.txt",
		}
		for _, p := range paths {
			if tui.IsImageLine(p) {
				t.Fatalf("should not falsely detect image sequence in path: %q", p)
			}
		}
	})
}
