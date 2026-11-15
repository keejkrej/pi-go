package tui_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestWrapTextWithAnsi(t *testing.T) {
	t.Run("empty and simple wrapping", func(t *testing.T) {
		if got := tui.WrapTextWithAnsi("", 10); !reflect.DeepEqual(got, []string{""}) {
			t.Fatalf("empty = %v", got)
		}
		if got := tui.WrapTextWithAnsi("hello world", 5); !reflect.DeepEqual(got, []string{"hello", "world"}) {
			t.Fatalf("hello world @5 = %v", got)
		}
		if got := tui.WrapTextWithAnsi("  hello world", 8); !reflect.DeepEqual(got, []string{"  hello", "world"}) {
			t.Fatalf("leading spaces = %v", got)
		}
	})

	t.Run("preserves SGR styles and OSC 8 hyperlinks across wrapped and explicit lines", func(t *testing.T) {
		lines := tui.WrapTextWithAnsi("\x1b[31mhello world\x1b[0m", 5)
		if len(lines) != 2 {
			t.Fatalf("got %d lines: %v", len(lines), lines)
		}
		if lines[0] != "\x1b[31mhello" || lines[1] != "\x1b[31mworld\x1b[0m" {
			t.Fatalf("styled wrap = %q", lines)
		}

		multiline := tui.WrapTextWithAnsi("\x1b[34mline1\nline2\x1b[0m", 20)
		if !reflect.DeepEqual(multiline, []string{"\x1b[34mline1", "\x1b[34mline2\x1b[0m"}) {
			t.Fatalf("multiline color carry = %q", multiline)
		}

		emptyStyled := tui.WrapTextWithAnsi("\x1b[31ma\n\nb\x1b[0m", 20)
		if !reflect.DeepEqual(emptyStyled, []string{"\x1b[31ma", "\x1b[31m", "\x1b[31mb\x1b[0m"}) {
			t.Fatalf("empty styled line carry = %q", emptyStyled)
		}

		crlf := tui.WrapTextWithAnsi("first\nsecond\r\nthird\rfourth", 80)
		if !reflect.DeepEqual(crlf, []string{"first", "second", "third", "fourth"}) {
			t.Fatalf("crlf/cr split = %q", crlf)
		}

		crlfStyled := tui.WrapTextWithAnsi("\x1b[31mfirst\r\nsecond\rthird\x1b[0m", 80)
		if !reflect.DeepEqual(crlfStyled, []string{"\x1b[31mfirst", "\x1b[31msecond", "\x1b[31mthird\x1b[0m"}) {
			t.Fatalf("crlf/cr styled = %q", crlfStyled)
		}

		strikeBg := tui.WrapTextWithAnsi("\x1b[41;9mstrike me\x1b[0m", 6)
		if !reflect.DeepEqual(strikeBg, []string{"\x1b[41;9mstrike\x1b[29m", "\x1b[9;41mme\x1b[0m"}) {
			t.Fatalf("strike+bg wrap = %q", strikeBg)
		}

		boldClose := tui.WrapTextWithAnsi("aaaa \x1b[1mbold\x1b[22m next word", 9)
		if !reflect.DeepEqual(boldClose, []string{"aaaa \x1b[1mbold\x1b[22m", "next word"}) {
			t.Fatalf("boldClose = %q", boldClose)
		}

		redClose := tui.WrapTextWithAnsi("aaaa \x1b[31mred\x1b[39m  next", 8)
		if !reflect.DeepEqual(redClose, []string{"aaaa \x1b[31mred\x1b[39m", "next"}) {
			t.Fatalf("redClose = %q", redClose)
		}

		strikeSpaces := tui.WrapTextWithAnsi("aaa\x1b[9m    abcdefghij\x1b[29m", 9)
		if !reflect.DeepEqual(strikeSpaces, []string{"aaa\x1b[9m\x1b[29m", "\x1b[9mabcdefghi\x1b[29m", "\x1b[9mj\x1b[29m"}) {
			t.Fatalf("strikeSpaces = %q", strikeSpaces)
		}

		strikeWords := tui.WrapTextWithAnsi("aaaa \x1b[9mstrike me\x1b[29m next", 12)
		if !reflect.DeepEqual(strikeWords, []string{"aaaa \x1b[9mstrike\x1b[29m", "\x1b[9mme\x1b[29m next"}) {
			t.Fatalf("strikeWords = %q", strikeWords)
		}

		wideSpaces := []string{
			"\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[0m",
			"ab\x1b[41m" + strings.Repeat(" ", 20) + "\x1b[49mcd",
			"\x1b[33m" + strings.Repeat(" ", 20) + "x\x1b[39m",
		}
		for _, ws := range wideSpaces {
			for _, l := range tui.WrapTextWithAnsi(ws, 10) {
				if tui.VisibleWidth(l) > 10 {
					t.Fatalf("styled whitespace exceeded width 10: %q", l)
				}
			}
		}

		bgWrapped := tui.WrapTextWithAnsi("\x1b[44mhello world this is blue background text\x1b[0m", 15)
		for i := 0; i < len(bgWrapped)-1; i++ {
			if strings.HasSuffix(bgWrapped[i], "\x1b[0m") {
				t.Fatalf("bg line %d should not end with full reset: %q", i, bgWrapped[i])
			}
		}

		ulWrapped := tui.WrapTextWithAnsi("\x1b[41mprefix \x1b[4mUNDERLINED_CONTENT_THAT_WRAPS\x1b[24m suffix\x1b[0m", 20)
		for i, l := range ulWrapped {
			if !strings.Contains(l, "[41m") && !strings.Contains(l, ";41m") && !strings.Contains(l, "[41;") {
				t.Fatalf("underline+bg line %d missing bg 41: %q", i, l)
			}
			if i < len(ulWrapped)-1 && (strings.Contains(l, "[4m") || strings.Contains(l, "[4;") || strings.Contains(l, ";4m")) && !strings.Contains(l, "\x1b[24m") {
				if !strings.HasSuffix(l, "\x1b[24m") || strings.HasSuffix(l, "\x1b[0m") {
					t.Fatalf("underline+bg line %d invalid end reset: %q", i, l)
				}
			}
		}

		osc := "\x1b]8;;https://example.com\x07click here now\x1b]8;;\x07"
		wrappedOsc := tui.WrapTextWithAnsi(osc, 10)
		if len(wrappedOsc) != 2 {
			t.Fatalf("osc lines = %q", wrappedOsc)
		}
		if !strings.HasPrefix(wrappedOsc[0], "\x1b]8;;https://example.com\x07") || !strings.HasSuffix(wrappedOsc[0], "\x1b]8;;\x07") {
			t.Fatalf("osc line 0 = %q", wrappedOsc[0])
		}
		if !strings.HasPrefix(wrappedOsc[1], "\x1b]8;;https://example.com\x07") || !strings.HasSuffix(wrappedOsc[1], "\x1b]8;;\x07") {
			t.Fatalf("osc line 1 = %q", wrappedOsc[1])
		}
		if tui.StripTerminalSequences(wrappedOsc[0]) != "click here" || tui.StripTerminalSequences(wrappedOsc[1]) != "now" {
			t.Fatalf("osc plain lines = %q %q", tui.StripTerminalSequences(wrappedOsc[0]), tui.StripTerminalSequences(wrappedOsc[1]))
		}

		oauthUrl := "https://example.com/oauth/" + strings.Repeat("a", 32)
		belWrapped := tui.WrapTextWithAnsi("\x1b]8;;"+oauthUrl+"\x07"+oauthUrl+"\x1b]8;;\x07", 20)
		if len(belWrapped) <= 1 {
			t.Fatalf("expected BEL OAuth URL to wrap, got %q", belWrapped)
		}
		for _, l := range belWrapped {
			if !strings.Contains(l, "\x1b]8;;"+oauthUrl+"\x07") || strings.Contains(l, "\x1b]8;;"+oauthUrl+"\x1b\\") {
				t.Fatalf("BEL line %q did not preserve BEL open", l)
			}
		}
		for _, l := range belWrapped[:len(belWrapped)-1] {
			if !strings.HasSuffix(l, "\x1b]8;;\x07") {
				t.Fatalf("BEL non-final line %q missing BEL close", l)
			}
		}
	})

	t.Run("breaks long words and wide characters safely", func(t *testing.T) {
		lines := tui.WrapTextWithAnsi("hi abcdefghij", 6)
		for _, l := range lines {
			if tui.VisibleWidth(l) > 6 {
				t.Fatalf("line %q width %d > 6", l, tui.VisibleWidth(l))
			}
		}
		cjk := tui.WrapTextWithAnsi("你好世界你好", 5)
		for _, l := range cjk {
			if tui.VisibleWidth(l) > 5 {
				t.Fatalf("CJK line %q width %d > 5", l, tui.VisibleWidth(l))
			}
		}

		cjkLatin := tui.WrapTextWithAnsi("This is an example 中文汉字测试段落内容中文汉字测试段落内容.", 40)
		wantCjkLatin := []string{"This is an example 中文汉字测试段落内容", "中文汉字测试段落内容."}
		if !reflect.DeepEqual(cjkLatin, wantCjkLatin) {
			t.Fatalf("CJK after Latin = %q, want %q", cjkLatin, wantCjkLatin)
		}

		cjkColored := tui.WrapTextWithAnsi("\x1b[31mThis is an example 中文汉字测试段落内容中文汉字测试段落内容.\x1b[0m", 40)
		wantCjkColored := []string{"\x1b[31mThis is an example 中文汉字测试段落内容", "\x1b[31m中文汉字测试段落内容.\x1b[0m"}
		if !reflect.DeepEqual(cjkColored, wantCjkColored) {
			t.Fatalf("colored CJK after Latin = %q, want %q", cjkColored, wantCjkColored)
		}
	})

	t.Run("CJK segment and utility helpers", func(t *testing.T) {
		seg := tui.ExtractSegments("abcd让EFGH", 5, 9, 11, true)
		if seg.Before != "abcd" || seg.BeforeWidth != 4 || seg.After != "H" || seg.AfterWidth != 1 {
			t.Fatalf("ExtractSegments = %+v", seg)
		}
		styledSeg := tui.ExtractSegments("\x1b[32mhello world\x1b[0m", 5, 6, 5, true)
		if styledSeg.Before != "\x1b[32mhello" || styledSeg.After != "\x1b[32mworld" {
			t.Fatalf("styled ExtractSegments = %+v", styledSeg)
		}

		if r := tui.GetGraphemeCellRange("ab你d", 3); r == nil || r.Start != 2 || r.End != 4 {
			t.Fatalf("GetGraphemeCellRange = %+v", r)
		}
		oscLine := "go \x1b]8;id=1;https://pi.dev\x07site\x1b]8;;\x07 end"
		if link := tui.GetOsc8LinkAtColumn(oscLine, 4); link == nil || *link != "https://pi.dev" {
			t.Fatalf("GetOsc8LinkAtColumn@4 = %v", link)
		}
		if link := tui.GetOsc8LinkAtColumn(oscLine, 1); link != nil {
			t.Fatalf("GetOsc8LinkAtColumn@1 = %v, want nil", *link)
		}
		if bg := tui.GetActiveBackgroundAnsi("hi \x1b[48;5;24mblue\x1b[31m"); bg != "\x1b[48;5;24m" {
			t.Fatalf("GetActiveBackgroundAnsi = %q", bg)
		}
		if ext := tui.ExtractAnsiCode("😀\x1b[31mhi", 2); ext == nil || ext.Code != "\x1b[31m" || ext.Length != 5 {
			t.Fatalf("ExtractAnsiCode UTF-16 = %+v", ext)
		}
		if norm := tui.NormalizeTerminalOutput("a\r\nb\rc\td"); norm != "a\r\nb\rc   d" {
			t.Fatalf("NormalizeTerminalOutput = %q", norm)
		}
		if repl := tui.ReplaceControlCharacters("a\x00\x1b\nb\u009fc"); repl != "a b c" {
			t.Fatalf("ReplaceControlCharacters = %q", repl)
		}
	})
}
