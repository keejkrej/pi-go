package core

import (
	"strings"
	"testing"
)

func TestAnsiToHtmlWhitespaceColorsAndOsc8(t *testing.T) {
	if got := EscapeHtml(`<a href="x">&'test'`); got != `&lt;a href=&quot;x&quot;&gt;&amp;&#039;test&#039;` {
		t.Fatalf("unexpected EscapeHtml output: %q", got)
	}

	// Leading/consecutive spaces, SGR styles, truecolor, 256-color, and OSC 8 hyperlinks.
	line := "  \x1b[1;38;2;255;0;128mred\x1b[0m   \x1b]8;;https://example.com\x07click\x1b]8;;\x07"
	html := AnsiLinesToHtml([]string{line, "\x1b[38;5;196mhi\x1b[0m"})
	if !strings.Contains(html, "  ") {
		t.Fatalf("expected leading indentation preserved, got %q", html)
	}
	if !strings.Contains(html, "color:rgb(255,0,128)") || !strings.Contains(html, "font-weight:bold") {
		t.Fatalf("expected truecolor and bold span in %q", html)
	}
	if !strings.Contains(html, `<a href="https://example.com"`) || !strings.Contains(html, ">click</a>") {
		t.Fatalf("expected sanitized OSC 8 anchor in %q", html)
	}
	// Unsafe URL scheme (javascript:) must be stripped rather than emitted as href.
	badLink := AnsiToHtml("\x1b]8;;javascript:alert(1)\x07xss\x1b]8;;\x07")
	if strings.Contains(badLink, "javascript:") || !strings.Contains(badLink, "xss") {
		t.Fatalf("unsafe OSC 8 URL was not sanitized: %q", badLink)
	}
	if stripped := StripAnsiEscapes("\x1b[31mred\x1b[0m"); stripped != "red" {
		t.Fatalf("expected StripAnsiEscapes=red, got %q", stripped)
	}
}
