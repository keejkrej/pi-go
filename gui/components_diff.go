package gui

import (
	"fmt"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// DiffViewer renders a side-by-side or unified code diff using
// github.com/egoist/mygo/ui/editor.DiffView, with optional toolbar controls for
// toggling split/unified layout, word wrap, and navigating diff hunks.
type DiffViewer struct {
	mu          sync.Mutex
	state       editor.DiffState
	theme       *GuiTheme
	filename    string
	showToolbar bool
	height      float32
	patches     []editor.FilePatch
	activePatch int
	baseTexts   map[string]string
}

// NewDiffViewer constructs a DiffViewer comparing oldText and newText.
func NewDiffViewer(oldText, newText string) *DiffViewer {
	dv := &DiffViewer{
		showToolbar: true,
		state: editor.DiffState{
			Old:         editor.NewBuffer(oldText),
			New:         editor.NewBuffer(newText),
			Layout:      editor.DiffSplit,
			Inline:      editor.DiffInlineWordAlt,
			Indicators:  editor.DiffBars,
			HunkActions: false,
		},
	}
	return dv
}

// NewDiffViewerFromPatch parses a unified diff / git patch string and prepares
// a DiffViewer displaying the parsed file changes.
func NewDiffViewerFromPatch(patchText string) (*DiffViewer, error) {
	dv := NewDiffViewer("", "")
	if err := dv.ParseAndSetPatch(patchText); err != nil {
		return nil, err
	}
	return dv, nil
}

// SetTexts replaces the Old and New diff buffers and auto-detects language if
// a filename was set.
func (d *DiffViewer) SetTexts(oldText, newText string) {
	d.mu.Lock()
	oldBuf := editor.NewBuffer(oldText)
	newBuf := editor.NewBuffer(newText)
	if d.filename != "" {
		if lang := editor.LanguageForText(d.filename, newText); lang != nil {
			oldBuf.SetLanguage(lang)
			newBuf.SetLanguage(lang)
		}
	}
	d.state.Old = oldBuf
	d.state.New = newBuf
	d.patches = nil
	d.mu.Unlock()
}

// SetBuffers assigns existing editor.Buffer instances to Old and New.
func (d *DiffViewer) SetBuffers(oldBuf, newBuf *editor.Buffer) {
	d.mu.Lock()
	d.state.Old = oldBuf
	d.state.New = newBuf
	d.mu.Unlock()
}

// SetLanguage applies a syntax language explicitly to both Old and New buffers.
func (d *DiffViewer) SetLanguage(lang *editor.Language) {
	d.mu.Lock()
	if d.state.Old != nil {
		d.state.Old.SetLanguage(lang)
	}
	if d.state.New != nil {
		d.state.New.SetLanguage(lang)
	}
	d.mu.Unlock()
}

// SetLanguageForFile detects the language for filename and applies it to both buffers.
func (d *DiffViewer) SetLanguageForFile(filename string) {
	d.mu.Lock()
	d.filename = filename
	sample := ""
	if d.state.New != nil && d.state.New.Len() > 0 {
		sample = d.state.New.String()
	} else if d.state.Old != nil {
		sample = d.state.Old.String()
	}
	lang := editor.LanguageForText(filename, sample)
	if lang == nil {
		lang = editor.LanguageFor(filename)
	}
	if d.state.Old != nil {
		d.state.Old.SetLanguage(lang)
	}
	if d.state.New != nil {
		d.state.New.SetLanguage(lang)
	}
	d.mu.Unlock()
}

// Filename returns the displayed filename.
func (d *DiffViewer) Filename() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.filename
}

// SetPatch displays a single parsed editor.FilePatch without requiring full file contents.
func (d *DiffViewer) SetPatch(p editor.FilePatch) {
	d.mu.Lock()
	d.patches = []editor.FilePatch{p}
	d.activePatch = 0
	name := p.NewName
	if name == "" {
		name = p.OldName
	}
	d.filename = name
	d.state.SetPatch(p)
	if lang := editor.LanguageFor(name); lang != nil {
		if d.state.Old != nil {
			d.state.Old.SetLanguage(lang)
		}
		if d.state.New != nil {
			d.state.New.SetLanguage(lang)
		}
	}
	d.mu.Unlock()
}

// ParseAndSetPatch parses unified patch text via editor.ParsePatch and displays the first patch.
func (d *DiffViewer) ParseAndSetPatch(patchText string) error {
	patches, err := editor.ParsePatch(patchText)
	if err != nil {
		return err
	}
	if len(patches) == 0 {
		return fmt.Errorf("gui: patch contains no file hunks")
	}
	d.mu.Lock()
	d.patches = patches
	d.activePatch = 0
	p := patches[0]
	name := p.NewName
	if name == "" {
		name = p.OldName
	}
	d.filename = name
	d.state.SetPatch(p)
	d.mu.Unlock()
	return nil
}

// Patches returns all parsed patches when constructed via ParseAndSetPatch/SetPatch.
func (d *DiffViewer) Patches() []editor.FilePatch {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]editor.FilePatch, len(d.patches))
	copy(out, d.patches)
	return out
}

// SetPatchFiles displays p applied onto old/new file texts.
func (d *DiffViewer) SetPatchFiles(p editor.FilePatch, oldText, newText string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.SetPatchFiles(p, oldText, newText)
}

// Layout returns the active diff layout (DiffSplit or DiffUnified).
func (d *DiffViewer) Layout() editor.DiffLayout {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Layout
}

// SetLayout sets DiffSplit or DiffUnified layout.
func (d *DiffViewer) SetLayout(layout editor.DiffLayout) {
	d.mu.Lock()
	d.state.Layout = layout
	d.mu.Unlock()
}

// ToggleLayout switches between DiffSplit and DiffUnified.
func (d *DiffViewer) ToggleLayout() editor.DiffLayout {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Layout == editor.DiffSplit {
		d.state.Layout = editor.DiffUnified
	} else {
		d.state.Layout = editor.DiffSplit
	}
	return d.state.Layout
}

// SetInlineMode configures intra-line change highlighting (word, char, etc.).
func (d *DiffViewer) SetInlineMode(mode editor.DiffInline) {
	d.mu.Lock()
	d.state.Inline = mode
	d.mu.Unlock()
}

// SetWrap enables or disables line wrapping in both diff panes.
func (d *DiffViewer) SetWrap(wrap bool) {
	d.mu.Lock()
	d.state.Wrap = wrap
	d.mu.Unlock()
}

// Wrap reports whether line wrapping is enabled.
func (d *DiffViewer) Wrap() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Wrap
}

// SetFitContent makes the diff viewer size itself to its lines instead of scrolling internally.
func (d *DiffViewer) SetFitContent(fit bool) {
	d.mu.Lock()
	d.state.FitContent = fit
	d.mu.Unlock()
}

// SetEditable allows editing the New buffer inside the diff view.
func (d *DiffViewer) SetEditable(editable bool) {
	d.mu.Lock()
	d.state.Editable = editable
	d.mu.Unlock()
}

// SetHunkActions enables or disables per-hunk Revert/Keep actions.
func (d *DiffViewer) SetHunkActions(enabled bool) {
	d.mu.Lock()
	d.state.HunkActions = enabled
	d.mu.Unlock()
}

// SetShowToolbar shows or hides the DiffViewer header toolbar.
func (d *DiffViewer) SetShowToolbar(show bool) {
	d.mu.Lock()
	d.showToolbar = show
	d.mu.Unlock()
}

// SetHeight sets an explicit DIP height for the diff viewer container (0 fills available space).
func (d *DiffViewer) SetHeight(height float32) {
	d.mu.Lock()
	d.height = height
	d.mu.Unlock()
}

// SetExpandUnchanged toggles expanding all unchanged context lines.
func (d *DiffViewer) SetExpandUnchanged(expand bool) {
	d.mu.Lock()
	d.state.ExpandUnchanged = expand
	d.mu.Unlock()
}

// ExpandAll expands all collapsed context separators.
func (d *DiffViewer) ExpandAll() {
	d.mu.Lock()
	d.state.ExpandAll()
	d.mu.Unlock()
}

// CollapseAll collapses unchanged lines to ContextLines.
func (d *DiffViewer) CollapseAll() {
	d.mu.Lock()
	d.state.CollapseAll()
	d.mu.Unlock()
}

// GoToHunk scrolls to hunk index i.
func (d *DiffViewer) GoToHunk(i int) {
	d.mu.Lock()
	d.state.GoToHunk(i)
	d.mu.Unlock()
}

// Hunks returns the computed change hunks.
func (d *DiffViewer) Hunks() []editor.DiffHunk {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Hunks()
}

// Stats returns the count of added and deleted lines.
func (d *DiffViewer) Stats() (added, deleted int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Stats()
}

// AcceptHunk accepts change hunk i.
func (d *DiffViewer) AcceptHunk(i int) {
	d.mu.Lock()
	d.state.Accept(i)
	d.mu.Unlock()
}

// RevertHunk reverts change hunk i.
func (d *DiffViewer) RevertHunk(i int) {
	d.mu.Lock()
	d.state.Revert(i)
	d.mu.Unlock()
}

// Find opens/updates diff text search for query.
func (d *DiffViewer) Find(query string) {
	d.mu.Lock()
	d.state.Find(query)
	d.mu.Unlock()
}

// SetTheme sets an optional GuiTheme override.
func (d *DiffViewer) SetTheme(theme *GuiTheme) {
	d.mu.Lock()
	d.theme = theme
	d.mu.Unlock()
}

// DiffState returns the underlying editor.DiffState for advanced customization.
func (d *DiffViewer) DiffState() *editor.DiffState {
	return &d.state
}

// View builds the immediate-mode DiffViewer UI element.
func (d *DiffViewer) View(c *ui.Context) ui.Element {
	d.mu.Lock()
	theme := ResolveGuiTheme(c, d.theme)
	showToolbar := d.showToolbar
	filename := d.filename
	height := d.height
	d.state.Theme = theme.EditorTheme()
	d.mu.Unlock()

	root := ui.Column(c).
		FillWidth().
		Background(theme.Surface).
		Border(1, theme.Border).
		Radius(theme.Radius)
	if height > 0 {
		root.Height(height)
	} else if !d.state.FitContent {
		root.Grow(1)
	}

	root.Children(func() {
		if showToolbar {
			ui.Row(c).
				FillWidth().
				Padding(6, 10).
				Gap(8).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				BorderWidth(0, 0, 1, 0).
				BorderColor(theme.BorderMuted).
				Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						label := filename
						if label == "" {
							label = "Diff"
						}
						ui.Text(c, label).
							TextColor(theme.Text).
							FontWeight(600).
							SingleLine()

						added, deleted := d.state.Stats()
						ui.Textf(c, "+%d", added).
							TextColor(theme.DiffAdded).
							Font("monospace").
							FontSize(12)
						ui.Textf(c, "-%d", deleted).
							TextColor(theme.DiffRemoved).
							Font("monospace").
							FontSize(12)
					})

					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						toggleLabel := "Unified"
						if d.state.Layout == editor.DiffUnified {
							toggleLabel = "Split"
						}
						if ui.Button(c, toggleLabel).Cursor(ui.CursorPointer).Clicked() {
							d.ToggleLayout()
						}
						wrapLabel := "Wrap"
						if d.state.Wrap {
							wrapLabel = "No Wrap"
						}
						if ui.Button(c, wrapLabel).Cursor(ui.CursorPointer).Clicked() {
							d.SetWrap(!d.Wrap())
						}
						hunks := d.state.Hunks()
						if len(hunks) > 0 {
							if ui.Button(c, "Prev Hunk").Cursor(ui.CursorPointer).Clicked() {
								cur, _ := d.state.CurrentHunk()
								next := cur - 1
								if next < 0 {
									next = len(hunks) - 1
								}
								d.state.GoToHunk(next)
							}
							if ui.Button(c, "Next Hunk").Cursor(ui.CursorPointer).Clicked() {
								cur, inside := d.state.CurrentHunk()
								next := cur + 1
								if !inside && cur == 0 {
									next = 0
								}
								if next >= len(hunks) {
									next = 0
								}
								d.state.GoToHunk(next)
							}
						}
					})
				})
		}

		diffEl := editor.DiffView(c, &d.state).FillWidth()
		if !d.state.FitContent {
			diffEl.Grow(1)
		}
	})
	return root
}

// CodeViewer wraps github.com/egoist/mygo/ui/editor.View for syntax-highlighted
// read-only (or editable) source display using editor.Buffer and LanguageForText.
type CodeViewer struct {
	mu         sync.Mutex
	state      editor.State
	theme      *GuiTheme
	filename   string
	showHeader bool
	height     float32
}

// NewCodeViewer constructs a syntax-highlighted CodeViewer for filename and content.
func NewCodeViewer(filename, content string) *CodeViewer {
	buf := editor.NewBuffer(content)
	if lang := editor.LanguageForText(filename, content); lang != nil {
		buf.SetLanguage(lang)
	}
	return &CodeViewer{
		filename:   filename,
		showHeader: true,
		state: editor.State{
			Buffer:          buf,
			ReadOnly:        true,
			NoScrollPastEnd: true,
		},
	}
}

// SetContent replaces the displayed buffer text and re-detects language.
func (cv *CodeViewer) SetContent(filename, content string) {
	cv.mu.Lock()
	cv.filename = filename
	if cv.state.Buffer == nil {
		cv.state.Buffer = editor.NewBuffer(content)
	} else {
		cv.state.Buffer.Set(content)
	}
	cv.state.Buffer.SetLanguage(editor.LanguageForText(filename, content))
	cv.mu.Unlock()
}

// Buffer returns the underlying editor.Buffer.
func (cv *CodeViewer) Buffer() *editor.Buffer {
	cv.mu.Lock()
	defer cv.mu.Unlock()
	return cv.state.Buffer
}

// State returns a pointer to the underlying editor.State.
func (cv *CodeViewer) State() *editor.State {
	return &cv.state
}

// SetReadOnly toggles read-only mode (default true).
func (cv *CodeViewer) SetReadOnly(ro bool) {
	cv.mu.Lock()
	cv.state.ReadOnly = ro
	cv.mu.Unlock()
}

// SetSoftWrap toggles soft wrapping.
func (cv *CodeViewer) SetSoftWrap(wrap bool) {
	cv.mu.Lock()
	cv.state.SoftWrap = wrap
	cv.mu.Unlock()
}

// SetFitContent toggles sizing the viewer to its content height.
func (cv *CodeViewer) SetFitContent(fit bool) {
	cv.mu.Lock()
	cv.state.FitContent = fit
	cv.mu.Unlock()
}

// SetShowHeader toggles the header strip showing filename, language, and Copy button.
func (cv *CodeViewer) SetShowHeader(show bool) {
	cv.mu.Lock()
	cv.showHeader = show
	cv.mu.Unlock()
}

// SetHeight sets an explicit DIP height (0 expands to fill parent).
func (cv *CodeViewer) SetHeight(height float32) {
	cv.mu.Lock()
	cv.height = height
	cv.mu.Unlock()
}

// SetTheme overrides the GuiTheme used to color the syntax tokens.
func (cv *CodeViewer) SetTheme(theme *GuiTheme) {
	cv.mu.Lock()
	cv.theme = theme
	cv.mu.Unlock()
}

// View builds the immediate-mode CodeViewer UI element.
func (cv *CodeViewer) View(c *ui.Context) ui.Element {
	cv.mu.Lock()
	theme := ResolveGuiTheme(c, cv.theme)
	showHeader := cv.showHeader
	filename := cv.filename
	height := cv.height
	cv.state.Theme = theme.EditorTheme()
	buf := cv.state.Buffer
	cv.mu.Unlock()

	root := ui.Column(c).
		FillWidth().
		Background(theme.Surface).
		Border(1, theme.Border).
		Radius(theme.Radius)
	if height > 0 {
		root.Height(height)
	} else if !cv.state.FitContent {
		root.Grow(1)
	}

	root.Children(func() {
		if showHeader {
			ui.Row(c).
				FillWidth().
				Padding(6, 10).
				Justify(ui.SpaceBetween).
				AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).
				BorderColor(theme.BorderMuted).
				Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						name := filename
						if name == "" {
							name = "Code"
						}
						ui.Text(c, name).TextColor(theme.Text).FontWeight(600).SingleLine()
						if buf != nil && buf.Language() != nil {
							ui.Text(c, buf.Language().Name).TextColor(theme.Muted).FontSize(12)
						}
					})
					if ui.Button(c, "Copy Code").Cursor(ui.CursorPointer).Clicked() && buf != nil {
						c.WriteClipboard(buf.String())
					}
				})
		}

		ed := editor.View(c, &cv.state).FillWidth()
		if !cv.state.FitContent {
			ed.Grow(1)
		}
	})
	return root
}
