// Ported from chalk@6.0.0 source/index.js and source/utilities.js.

package ansi

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Options are the Chalk constructor options.
type Options struct {
	// Level is the color support level (0-3). Nil detects it from stdout.
	Level *int
}

// Chalk is a chalk instance (TS `new Chalk(options)` / the default `chalk` export). It is the root
// generator of style chains: every Builder created from it reads the instance's current level.
//
// A Chalk called as a function (`chalk("a", "b")`) is Join.
type Chalk struct {
	level atomic.Int32
}

// Builder is a style chain such as `chalk.bold.cyan`. Apply styles a string; further style methods extend
// the chain. Builders are immutable and safe for concurrent use.
type Builder struct {
	generator *Chalk
	styler    *styler
	isEmpty   bool
}

type styler struct {
	open     string
	close    string
	openAll  string
	closeAll string
	parent   *styler
}

const invalidLevelMessage = "The `level` should be an integer from 0 to 3"

func assertValidLevel(level int) {
	if level < 0 || level > 3 {
		panic(invalidLevelMessage)
	}
}

// NewChalk creates a chalk instance. A nil Level detects the level from stdout color support.
// It panics with "The `level` should be an integer from 0 to 3" for an invalid level, like TS throws.
func NewChalk(options *Options) *Chalk {
	c := &Chalk{}
	if options != nil && options.Level != nil {
		assertValidLevel(*options.Level)
		c.level.Store(int32(*options.Level))
		return c
	}
	// Detect level if not set manually.
	colorLevel := 0
	if stdoutColor := SupportsColor(); stdoutColor != nil {
		colorLevel = stdoutColor.Level
	}
	c.level.Store(int32(colorLevel))
	return c
}

var defaultChalks struct {
	once   sync.Once
	stdout *Chalk
	stderr *Chalk
}

func initDefaultChalks() {
	defaultChalks.once.Do(func() {
		defaultChalks.stdout = NewChalk(nil)
		stderrLevel := 0
		if stderrColor := SupportsColorStderr(); stderrColor != nil {
			stderrLevel = stderrColor.Level
		}
		defaultChalks.stderr = NewChalk(&Options{Level: &stderrLevel})
	})
}

// Default is the default chalk instance (`import chalk from "chalk"`), with the level detected from stdout
// on first use.
func Default() *Chalk {
	initDefaultChalks()
	return defaultChalks.stdout
}

// ChalkStderr is the chalk instance for stderr (`chalkStderr`).
func ChalkStderr() *Chalk {
	initDefaultChalks()
	return defaultChalks.stderr
}

// Level returns the color support level (0-3).
func (c *Chalk) Level() int {
	return int(c.level.Load())
}

// SetLevel sets the color support level. It panics for anything outside 0-3, like the TS setter throws.
func (c *Chalk) SetLevel(level int) {
	assertValidLevel(level)
	c.level.Store(int32(level))
}

// Join is calling the chalk instance itself: the arguments joined with a space, unstyled.
func (c *Chalk) Join(texts ...string) string {
	return strings.Join(texts, " ")
}

// Level returns the level of the chain's root chalk instance.
func (b *Builder) Level() int {
	return b.generator.Level()
}

// SetLevel sets the level of the chain's root chalk instance.
func (b *Builder) SetLevel(level int) {
	b.generator.SetLevel(level)
}

// Apply styles a single string (`chalk.bold(text)`).
func (b *Builder) Apply(text string) string {
	return applyStyle(b, text)
}

// Join styles the arguments joined with a space (`chalk.bold(a, b, c)`).
func (b *Builder) Join(texts ...string) string {
	return applyStyle(b, strings.Join(texts, " "))
}

func createStyler(open, close string, parent *styler) *styler {
	if parent == nil {
		return &styler{open: open, close: close, openAll: open, closeAll: close}
	}
	return &styler{
		open:     open,
		close:    close,
		openAll:  parent.openAll + open,
		closeAll: close + parent.closeAll,
		parent:   parent,
	}
}

func createBuilder(generator *Chalk, s *styler, isEmpty bool) *Builder {
	return &Builder{generator: generator, styler: s, isEmpty: isEmpty}
}

// chainStyle extends the chain rooted at c (b nil) or b with a fixed open/close pair.
func (c *Chalk) chainStyle(open, close string) *Builder {
	return createBuilder(c, createStyler(open, close, nil), false)
}

func (b *Builder) chainStyle(open, close string) *Builder {
	return createBuilder(b.generator, createStyler(open, close, b.styler), b.isEmpty)
}

// Visible prints the text only when the level is above zero.
func (c *Chalk) Visible() *Builder {
	return createBuilder(c, nil, true)
}

// Visible prints the text only when the level is above zero.
func (b *Builder) Visible() *Builder {
	return createBuilder(b.generator, b.styler, true)
}

func applyStyle(b *Builder, s string) string {
	if b.generator.Level() <= 0 || s == "" {
		if b.isEmpty {
			return ""
		}
		return s
	}

	st := b.styler
	if st == nil {
		return s
	}

	openAll, closeAll := st.openAll, st.closeAll
	if strings.Contains(s, "\x1b") {
		for st != nil {
			// Replace any instances already present with a re-opening code
			// otherwise only the part of the string until said closing code
			// will be colored, and the rest will simply be 'plain'.
			s = stringReplaceAll(s, st.close, st.open)
			st = st.parent
		}
	}

	// We can move both next actions out of loop, because remaining actions in loop won't have
	// any/visible effect on parts we add here. Close the styling before a linebreak and reopen
	// after next line to fix a bleed issue on macOS: https://github.com/chalk/chalk/pull/92
	if lfIndex := strings.IndexByte(s, '\n'); lfIndex != -1 {
		s = stringEncaseCRLFWithFirstIndex(s, closeAll, openAll, lfIndex)
	}

	return openAll + s + closeAll
}

// Model converters: one per level, as in createModelConverters.
type colorTarget int

const (
	targetColor colorTarget = iota
	targetBgColor
	targetUnderlineColor
)

func (t colorTarget) close() string {
	switch t {
	case targetBgColor:
		return BgColorClose
	case targetUnderlineColor:
		return UnderlineColorClose
	}
	return ColorClose
}

func (t colorTarget) ansi(code int) string {
	switch t {
	case targetBgColor:
		return BgColorAnsi(code)
	case targetUnderlineColor:
		return UnderlineColorAnsi(code)
	}
	return ColorAnsi(code)
}

func (t colorTarget) ansi256(code int) string {
	switch t {
	case targetBgColor:
		return BgColorAnsi256(code)
	case targetUnderlineColor:
		return UnderlineColorAnsi256(code)
	}
	return ColorAnsi256(code)
}

func (t colorTarget) ansi16m(red, green, blue int) string {
	switch t {
	case targetBgColor:
		return BgColorAnsi16m(red, green, blue)
	case targetUnderlineColor:
		return UnderlineColorAnsi16m(red, green, blue)
	}
	return ColorAnsi16m(red, green, blue)
}

func (t colorTarget) rgbOpen(level, red, green, blue int) string {
	switch level {
	case 0, 1:
		return t.ansi(RgbToAnsi(red, green, blue))
	case 2:
		return t.ansi256(RgbToAnsi256(red, green, blue))
	}
	return t.ansi16m(red, green, blue)
}

func (t colorTarget) hexOpen(level int, hex string) string {
	switch level {
	case 0, 1:
		return t.ansi(HexToAnsi(hex))
	case 2:
		return t.ansi256(HexToAnsi256(hex))
	}
	rgb := HexToRgb(hex)
	return t.ansi16m(rgb[0], rgb[1], rgb[2])
}

// `ansi256` is already the native form, so only the 16-color levels need converting.
func (t colorTarget) ansi256Open(level, code int) string {
	if level <= 1 {
		return t.ansi(Ansi256ToAnsi(code))
	}
	return t.ansi256(code)
}

func (c *Chalk) rgbStyle(t colorTarget, red, green, blue int) *Builder {
	return c.chainStyle(t.rgbOpen(c.Level(), red, green, blue), t.close())
}

func (b *Builder) rgbStyle(t colorTarget, red, green, blue int) *Builder {
	return b.chainStyle(t.rgbOpen(b.Level(), red, green, blue), t.close())
}

func (c *Chalk) hexStyle(t colorTarget, hex string) *Builder {
	return c.chainStyle(t.hexOpen(c.Level(), hex), t.close())
}

func (b *Builder) hexStyle(t colorTarget, hex string) *Builder {
	return b.chainStyle(t.hexOpen(b.Level(), hex), t.close())
}

func (c *Chalk) ansi256Style(t colorTarget, code int) *Builder {
	return c.chainStyle(t.ansi256Open(c.Level(), code), t.close())
}

func (b *Builder) ansi256Style(t colorTarget, code int) *Builder {
	return b.chainStyle(t.ansi256Open(b.Level(), code), t.close())
}

// Rgb sets the foreground to an RGB color, downsampled to the instance level.
func (c *Chalk) Rgb(red, green, blue int) *Builder { return c.rgbStyle(targetColor, red, green, blue) }

// BgRgb sets the background to an RGB color, downsampled to the instance level.
func (c *Chalk) BgRgb(red, green, blue int) *Builder {
	return c.rgbStyle(targetBgColor, red, green, blue)
}

// UnderlineRgb sets the underline color to an RGB color, downsampled to the instance level.
func (c *Chalk) UnderlineRgb(red, green, blue int) *Builder {
	return c.rgbStyle(targetUnderlineColor, red, green, blue)
}

// Hex sets the foreground to a hex color ("#DEADED"), downsampled to the instance level.
func (c *Chalk) Hex(hex string) *Builder { return c.hexStyle(targetColor, hex) }

// BgHex sets the background to a hex color, downsampled to the instance level.
func (c *Chalk) BgHex(hex string) *Builder { return c.hexStyle(targetBgColor, hex) }

// UnderlineHex sets the underline color to a hex color, downsampled to the instance level.
func (c *Chalk) UnderlineHex(hex string) *Builder { return c.hexStyle(targetUnderlineColor, hex) }

// Ansi256 sets the foreground to a 256-color palette index, downsampled to the instance level.
func (c *Chalk) Ansi256(code int) *Builder { return c.ansi256Style(targetColor, code) }

// BgAnsi256 sets the background to a 256-color palette index, downsampled to the instance level.
func (c *Chalk) BgAnsi256(code int) *Builder { return c.ansi256Style(targetBgColor, code) }

// UnderlineAnsi256 sets the underline color to a 256-color palette index.
func (c *Chalk) UnderlineAnsi256(code int) *Builder {
	return c.ansi256Style(targetUnderlineColor, code)
}

// Rgb extends the chain with an RGB foreground color.
func (b *Builder) Rgb(red, green, blue int) *Builder {
	return b.rgbStyle(targetColor, red, green, blue)
}

// BgRgb extends the chain with an RGB background color.
func (b *Builder) BgRgb(red, green, blue int) *Builder {
	return b.rgbStyle(targetBgColor, red, green, blue)
}

// UnderlineRgb extends the chain with an RGB underline color.
func (b *Builder) UnderlineRgb(red, green, blue int) *Builder {
	return b.rgbStyle(targetUnderlineColor, red, green, blue)
}

// Hex extends the chain with a hex foreground color.
func (b *Builder) Hex(hex string) *Builder { return b.hexStyle(targetColor, hex) }

// BgHex extends the chain with a hex background color.
func (b *Builder) BgHex(hex string) *Builder { return b.hexStyle(targetBgColor, hex) }

// UnderlineHex extends the chain with a hex underline color.
func (b *Builder) UnderlineHex(hex string) *Builder { return b.hexStyle(targetUnderlineColor, hex) }

// Ansi256 extends the chain with a 256-color palette foreground color.
func (b *Builder) Ansi256(code int) *Builder { return b.ansi256Style(targetColor, code) }

// BgAnsi256 extends the chain with a 256-color palette background color.
func (b *Builder) BgAnsi256(code int) *Builder { return b.ansi256Style(targetBgColor, code) }

// UnderlineAnsi256 extends the chain with a 256-color palette underline color.
func (b *Builder) UnderlineAnsi256(code int) *Builder {
	return b.ansi256Style(targetUnderlineColor, code)
}

// Rgb is Default().Rgb.
func Rgb(red, green, blue int) *Builder { return Default().Rgb(red, green, blue) }

// BgRgb is Default().BgRgb.
func BgRgb(red, green, blue int) *Builder { return Default().BgRgb(red, green, blue) }

// UnderlineRgb is Default().UnderlineRgb.
func UnderlineRgb(red, green, blue int) *Builder { return Default().UnderlineRgb(red, green, blue) }

// Hex is Default().Hex.
func Hex(hex string) *Builder { return Default().Hex(hex) }

// BgHex is Default().BgHex.
func BgHex(hex string) *Builder { return Default().BgHex(hex) }

// UnderlineHex is Default().UnderlineHex.
func UnderlineHex(hex string) *Builder { return Default().UnderlineHex(hex) }

// Ansi256 is Default().Ansi256.
func Ansi256(code int) *Builder { return Default().Ansi256(code) }

// BgAnsi256 is Default().BgAnsi256.
func BgAnsi256(code int) *Builder { return Default().BgAnsi256(code) }

// UnderlineAnsi256 is Default().UnderlineAnsi256.
func UnderlineAnsi256(code int) *Builder { return Default().UnderlineAnsi256(code) }

// Visible styles text with the default instance only when its level is above zero.
func Visible(text string) string { return Default().Visible().Apply(text) }

// stringReplaceAll keeps each match of substring and inserts postfix after it.
func stringReplaceAll(s, substring, postfix string) string {
	index := strings.Index(s, substring)
	if index == -1 {
		return s
	}

	substringLength := len(substring)
	endIndex := 0
	var b strings.Builder
	for {
		b.WriteString(s[endIndex:index])
		b.WriteString(substring)
		b.WriteString(postfix)
		endIndex = index + substringLength
		next := strings.Index(s[endIndex:], substring)
		if next == -1 {
			break
		}
		index = endIndex + next
	}

	b.WriteString(s[endIndex:])
	return b.String()
}

// stringEncaseCRLFWithFirstIndex closes the styling before every line break and reopens it after.
func stringEncaseCRLFWithFirstIndex(s, prefix, postfix string, index int) string {
	endIndex := 0
	var b strings.Builder
	for {
		isGotCR := index > 0 && s[index-1] == '\r'
		end := index
		if isGotCR {
			end = index - 1
		}
		b.WriteString(s[endIndex:end])
		b.WriteString(prefix)
		if isGotCR {
			b.WriteString("\r\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString(postfix)
		endIndex = index + 1
		next := strings.IndexByte(s[endIndex:], '\n')
		if next == -1 {
			break
		}
		index = endIndex + next
	}

	b.WriteString(s[endIndex:])
	return b.String()
}
