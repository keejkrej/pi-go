package gui

import (
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// MarkdownConfig configures optional MarkdownView behavior.
type MarkdownConfig struct {
	Done           bool
	Theme          *GuiTheme
	WrapCode       bool
	MaxWidth       float32
	ShareSelection bool
	Reveal         *markdown.Reveal
	OnLink         func(url string)
	OnCopyCode     func(language, code string)
	LoadImage      func(url string) (ui.ImageSource, error)
}

// MarkdownView renders Markdown content using github.com/egoist/mygo/ui/markdown,
// seamlessly using markdown.Stream while streaming and markdown.View once done.
type MarkdownView struct {
	mu             sync.Mutex
	text           string
	done           bool
	theme          *GuiTheme
	wrapCode       bool
	maxWidth       float32
	shareSelection bool
	reveal         *markdown.Reveal
	onLink         func(url string)
	onCopyCode     func(language, code string)
	loadImage      func(url string) (ui.ImageSource, error)
	opts           markdown.Options
}

// NewMarkdownView constructs a MarkdownView with initial text and options.
func NewMarkdownView(text string, cfg ...MarkdownConfig) *MarkdownView {
	var c MarkdownConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return &MarkdownView{
		text:           text,
		done:           c.Done,
		theme:          c.Theme,
		wrapCode:       c.WrapCode,
		maxWidth:       c.MaxWidth,
		shareSelection: c.ShareSelection,
		reveal:         c.Reveal,
		onLink:         c.OnLink,
		onCopyCode:     c.OnCopyCode,
		loadImage:      c.LoadImage,
	}
}

// SetText replaces the rendered Markdown source.
func (m *MarkdownView) SetText(text string) {
	m.mu.Lock()
	m.text = text
	m.mu.Unlock()
}

// AppendText appends a streaming token/chunk to the Markdown source.
func (m *MarkdownView) AppendText(chunk string) {
	m.mu.Lock()
	m.text += chunk
	m.mu.Unlock()
}

// Text returns the current Markdown source string.
func (m *MarkdownView) Text() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.text
}

// SetDone switches between streaming reveal mode (false -> markdown.Stream)
// and settled static mode (true -> markdown.View).
func (m *MarkdownView) SetDone(done bool) {
	m.mu.Lock()
	m.done = done
	m.mu.Unlock()
}

// Done reports whether the Markdown stream has finalized.
func (m *MarkdownView) Done() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.done
}

// SetTheme sets an optional GuiTheme override.
func (m *MarkdownView) SetTheme(theme *GuiTheme) {
	m.mu.Lock()
	m.theme = theme
	m.mu.Unlock()
}

// SetOnLink sets a handler invoked when the user clicks a Markdown link.
func (m *MarkdownView) SetOnLink(fn func(url string)) {
	m.mu.Lock()
	m.onLink = fn
	m.mu.Unlock()
}

// SetOnCopyCode sets a callback notified whenever a fenced code block's copy
// button is clicked. When set, MarkdownView wraps fenced code blocks with a
// copyable header that writes to the clipboard and invokes fn(language, code).
func (m *MarkdownView) SetOnCopyCode(fn func(language, code string)) {
	m.mu.Lock()
	m.onCopyCode = fn
	m.mu.Unlock()
}

// SetWrapCode toggles soft-wrapping inside fenced code blocks.
func (m *MarkdownView) SetWrapCode(wrap bool) {
	m.mu.Lock()
	m.wrapCode = wrap
	m.mu.Unlock()
}

// SetReveal configures streaming word/sentence/line reveal cadence.
func (m *MarkdownView) SetReveal(r *markdown.Reveal) {
	m.mu.Lock()
	m.reveal = r
	m.mu.Unlock()
}

// SetMaxWidth sets an optional maximum width in DIPs for the Markdown column.
func (m *MarkdownView) SetMaxWidth(maxWidth float32) {
	m.mu.Lock()
	m.maxWidth = maxWidth
	m.mu.Unlock()
}

// Options returns a pointer to the underlying markdown.Options (safe to customize
// on the UI thread before building).
func (m *MarkdownView) Options() *markdown.Options {
	return &m.opts
}

// Revealed reports whether a streamed Markdown element has finished revealing.
func Revealed(el ui.Element) bool {
	return markdown.Revealed(el)
}

// View builds the immediate-mode Markdown UI element inside c.
func (m *MarkdownView) View(c *ui.Context) ui.Element {
	m.mu.Lock()
	src := m.text
	done := m.done
	theme := ResolveGuiTheme(c, m.theme)
	wrapCode := m.wrapCode
	maxWidth := m.maxWidth
	shareSelection := m.shareSelection
	reveal := m.reveal
	onLink := m.onLink
	onCopyCode := m.onCopyCode
	loadImg := m.loadImage
	m.mu.Unlock()

	m.opts.Theme = theme.MarkdownTheme()
	m.opts.WrapCode = wrapCode
	m.opts.ShareSelection = shareSelection
	m.opts.Reveal = reveal
	m.opts.OnLink = onLink
	m.opts.LoadImage = loadImg

	if onCopyCode != nil {
		m.opts.CodeBlock = func(bc *ui.Context, b *markdown.CodeBlockView) {
			renderCopyableCodeBlock(bc, b, theme, onCopyCode)
		}
	} else {
		m.opts.CodeBlock = nil
	}

	wrap := ui.Column(c).FillWidth()
	if maxWidth > 0 {
		wrap.MaxWidth(maxWidth)
	}
	var mdEl ui.Element
	wrap.Children(func() {
		if done {
			mdEl = markdown.View(c, src, &m.opts)
		} else {
			mdEl = markdown.Stream(c, src, &m.opts)
		}
	})
	_ = mdEl
	return wrap
}

func renderCopyableCodeBlock(
	c *ui.Context,
	b *markdown.CodeBlockView,
	theme *GuiTheme,
	onCopy func(language, code string),
) {
	radius := theme.Radius
	if radius <= 0 {
		radius = 6
	}
	box := ui.Column(c).
		FillWidth().
		Background(theme.MdCodeBlockBg).
		Border(1, theme.MdCodeBlockBorder).
		Radius(radius).
		Padding(8, 12).
		Gap(6)

	box.Children(func() {
		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			lang := b.Language
			if lang == "" {
				lang = "code"
			}
			ui.Text(c, lang).
				TextColor(theme.Muted).
				FontSize(11).
				FontWeight(600)

			copyBtn := ui.Button(c, "Copy").Cursor(ui.CursorPointer)
			if copyBtn.Clicked() {
				code := b.Code()
				c.WriteClipboard(code)
				if onCopy != nil {
					onCopy(b.Language, code)
				}
			}
		})
		b.Body(c)
	})
}

// RenderMarkdown is a stateless convenience helper for rendering Markdown
// directly with optional streaming support.
func RenderMarkdown(c *ui.Context, src string, done bool, opts *markdown.Options) ui.Element {
	if done {
		return markdown.View(c, src, opts)
	}
	return markdown.Stream(c, src, opts)
}
