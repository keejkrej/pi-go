package gui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// AttachmentKind distinguishes image vs file attachments in the prompt editor.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
	AttachmentFile  AttachmentKind = "file"
)

// EditorAttachment represents an attached file or pasted PNG image chip.
type EditorAttachment struct {
	ID        string
	Kind      AttachmentKind
	Name      string
	Path      string
	MIMEType  string
	Data      []byte
	SizeBytes int64
	Bitmap    *ui.Bitmap
}

// EditorSubmitEvent carries submitted prompt text, attachments, and bash-mode flags.
type EditorSubmitEvent struct {
	Text               string
	Attachments        []EditorAttachment
	BashMode           bool
	ExcludeFromContext bool
}

type editorSnapshot struct {
	Text     string
	SelStart int
	SelEnd   int
}

type undoStack struct {
	items []editorSnapshot
}

func (u *undoStack) push(s editorSnapshot) {
	if len(u.items) > 0 && u.items[len(u.items)-1].Text == s.Text {
		return
	}
	u.items = append(u.items, s)
	if len(u.items) > 200 {
		u.items = u.items[len(u.items)-200:]
	}
}

func (u *undoStack) pop() (editorSnapshot, bool) {
	if len(u.items) == 0 {
		return editorSnapshot{}, false
	}
	top := u.items[len(u.items)-1]
	u.items = u.items[:len(u.items)-1]
	return top, true
}

func (u *undoStack) clear() {
	u.items = nil
}

type killRing struct {
	entries []string
}

func (k *killRing) push(text string, prepend, accumulate bool) {
	if text == "" {
		return
	}
	if accumulate && len(k.entries) > 0 {
		last := k.entries[len(k.entries)-1]
		if prepend {
			k.entries[len(k.entries)-1] = text + last
		} else {
			k.entries[len(k.entries)-1] = last + text
		}
		return
	}
	k.entries = append(k.entries, text)
	if len(k.entries) > 64 {
		k.entries = k.entries[len(k.entries)-64:]
	}
}

func (k *killRing) peek() (string, bool) {
	if len(k.entries) == 0 {
		return "", false
	}
	return k.entries[len(k.entries)-1], true
}

func (k *killRing) rotate() (string, bool) {
	if len(k.entries) == 0 {
		return "", false
	}
	if len(k.entries) > 1 {
		last := k.entries[len(k.entries)-1]
		copy(k.entries[1:], k.entries[:len(k.entries)-1])
		k.entries[0] = last
	}
	return k.entries[len(k.entries)-1], true
}

// EditorOptions configures a new multiline prompt Editor.
type EditorOptions struct {
	Placeholder   string
	MinLines      int
	MaxLines      int
	Theme         *GuiTheme
	Keybindings   *KeybindingsManager
	DisableSubmit bool
	AutoFocus     bool
	Label         string
}

// Editor is a multiline GPU prompt composer supporting history navigation,
// undo/redo, Emacs kill-ring, `!` / `!!` shell indicator, dropped/pasted
// image and file attachment chips, fuzzy slash/`@file` autocomplete popup, and
// Enter submit / Shift+Enter newline / Alt+Enter follow-up / Escape cancel.
type Editor struct {
	mu          sync.Mutex
	buf         *ui.TextBuffer
	inputHandle ui.Handle

	placeholder   string
	label         string
	minLines      int
	maxLines      int
	theme         *GuiTheme
	keybindings   *KeybindingsManager
	disableSubmit bool
	disabled      bool
	autoFocus     bool
	chromeless    bool

	// Selection & pending mutations synced across build passes.
	lastText       string
	selStart       int
	selEnd         int
	pendingSel     *[2]int
	pendingReplace *struct {
		start int
		end   int
		text  string
	}

	// Undo / Redo / Kill-ring
	undo       undoStack
	redo       undoStack
	kill       killRing
	lastAction string // "kill", "yank", ""
	yankStart  int
	yankEnd    int

	// Prompt history (newest at index 0)
	history      []string
	historyIndex int
	historyDraft *editorSnapshot

	// Attachments
	nextAttachID uint64
	attachments  []EditorAttachment

	// Autocomplete
	acProvider AutocompleteProvider
	acPopup    *AutocompletePopup
	acVisible  bool
	acPrefix   string

	// Callbacks
	onSubmit            func(EditorSubmitEvent)
	onFollowUp          func(EditorSubmitEvent)
	onCancel            func()
	onChange            func(text string)
	onAttachmentsChange func([]EditorAttachment)
	readClipboardPNG    func() ([]byte, error)
}

// NewEditor creates a multiline prompt Editor.
func NewEditor(opts ...EditorOptions) *Editor {
	var o EditorOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	minL := o.MinLines
	if minL <= 0 {
		minL = 2
	}
	maxL := o.MaxLines
	if maxL < minL {
		maxL = 8
	}
	label := o.Label
	if label == "" {
		label = "Prompt editor"
	}
	placeholder := o.Placeholder
	if placeholder == "" {
		placeholder = "Ask anything... (Shift+Enter for newline, @ for files, / for commands, ! for bash)"
	}
	km := o.Keybindings
	if km == nil {
		km = NewKeybindingsManager()
	}

	e := &Editor{
		buf:           ui.NewTextBuffer(""),
		placeholder:   placeholder,
		label:         label,
		minLines:      minL,
		maxLines:      maxL,
		theme:         o.Theme,
		keybindings:   km,
		disableSubmit: o.DisableSubmit,
		autoFocus:     o.AutoFocus,
		historyIndex:  -1,
		acPopup:       NewAutocompletePopup(),
	}
	e.acPopup.SetOnSelect(func(item AutocompleteItem) {
		e.AcceptAutocompleteItem(item)
	})
	return e
}

// Text returns the editor's current plain text.
func (e *Editor) Text() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buf.String()
}

// SetText replaces the editor text and moves the caret to the end.
func (e *Editor) SetText(text string) {
	e.mu.Lock()
	prev := e.buf.String()
	e.undo.push(editorSnapshot{Text: prev, SelStart: e.selStart, SelEnd: e.selEnd})
	e.redo.clear()
	e.buf.Set(text)
	e.lastText = text
	n := utf8.RuneCountInString(text)
	e.selStart, e.selEnd = n, n
	e.pendingSel = &[2]int{n, n}
	e.lastAction = ""
	e.historyIndex = -1
	e.historyDraft = nil
	cb := e.onChange
	e.mu.Unlock()

	e.refreshAutocompleteFor(text, n)
	if cb != nil {
		cb(text)
	}
}

// Clear clears text and attachments while preserving prompt history.
func (e *Editor) Clear() {
	e.mu.Lock()
	e.buf.Set("")
	e.lastText = ""
	e.selStart, e.selEnd = 0, 0
	e.pendingSel = &[2]int{0, 0}
	e.attachments = nil
	e.acVisible = false
	e.acPopup.Clear()
	e.historyIndex = -1
	e.historyDraft = nil
	e.lastAction = ""
	onCh := e.onChange
	onAtt := e.onAttachmentsChange
	e.mu.Unlock()

	if onCh != nil {
		onCh("")
	}
	if onAtt != nil {
		onAtt(nil)
	}
}

// Focus requests keyboard focus for the underlying text area on next frame.
func (e *Editor) Focus(c *ui.Context) {
	if c != nil {
		e.inputHandle.Focus(c)
	}
}

// BashMode reports whether trimmed text starts with `!` (shell command mode).
func (e *Editor) BashMode() bool {
	return strings.HasPrefix(strings.TrimSpace(e.Text()), "!")
}

// BashExcludedFromContext reports whether trimmed text starts with `!!`.
func (e *Editor) BashExcludedFromContext() bool {
	return strings.HasPrefix(strings.TrimSpace(e.Text()), "!!")
}

// SetDisableSubmit prevents Enter / Alt+Enter submission while keeping editing enabled.
func (e *Editor) SetDisableSubmit(disable bool) {
	e.mu.Lock()
	e.disableSubmit = disable
	e.mu.Unlock()
}

// SetDisabled disables editing and controls.
func (e *Editor) SetDisabled(disabled bool) {
	e.mu.Lock()
	e.disabled = disabled
	e.mu.Unlock()
}

// Placeholder returns the empty prompt placeholder text.
func (e *Editor) Placeholder() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.placeholder
}

// SetPlaceholder changes the empty prompt placeholder text.
func (e *Editor) SetPlaceholder(placeholder string) {
	e.mu.Lock()
	e.placeholder = placeholder
	e.mu.Unlock()
}

// SetChromeless toggles borderless/fill-less embedded mode used inside the Waku
// Composer Card where the parent container owns card framing and bash badges.
func (e *Editor) SetChromeless(chromeless bool) {
	e.mu.Lock()
	e.chromeless = chromeless
	e.mu.Unlock()
}

// Chromeless reports whether the editor renders without its own outer card frame.
func (e *Editor) Chromeless() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chromeless
}

// SetTheme sets an optional GuiTheme override.
func (e *Editor) SetTheme(theme *GuiTheme) {
	e.mu.Lock()
	e.theme = theme
	e.acPopup.SetTheme(theme)
	e.mu.Unlock()
}

// SetKeybindings replaces the editor's KeybindingsManager.
func (e *Editor) SetKeybindings(km *KeybindingsManager) {
	if km == nil {
		km = NewKeybindingsManager()
	}
	e.mu.Lock()
	e.keybindings = km
	e.mu.Unlock()
}

// SetAutocompleteProvider configures the completion engine used for `/`, `@`, and Tab.
func (e *Editor) SetAutocompleteProvider(provider AutocompleteProvider) {
	e.mu.Lock()
	e.acProvider = provider
	e.acVisible = false
	e.acPrefix = ""
	e.acPopup.Clear()
	e.mu.Unlock()
}

// AutocompleteVisible reports whether the autocomplete popup is open.
func (e *Editor) AutocompleteVisible() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acVisible && !e.acPopup.Empty()
}

// AutocompletePopup returns the popup widget for inspection in tests or embedders.
func (e *Editor) AutocompletePopup() *AutocompletePopup {
	return e.acPopup
}

// SetOnSubmit sets the Enter submit handler.
func (e *Editor) SetOnSubmit(fn func(EditorSubmitEvent)) {
	e.mu.Lock()
	e.onSubmit = fn
	e.mu.Unlock()
}

// SetOnFollowUp sets the Alt+Enter follow-up queue handler.
func (e *Editor) SetOnFollowUp(fn func(EditorSubmitEvent)) {
	e.mu.Lock()
	e.onFollowUp = fn
	e.mu.Unlock()
}

// SetOnCancel sets the Escape cancel handler (when autocomplete is closed).
func (e *Editor) SetOnCancel(fn func()) {
	e.mu.Lock()
	e.onCancel = fn
	e.mu.Unlock()
}

// SetOnChange sets the text change listener.
func (e *Editor) SetOnChange(fn func(string)) {
	e.mu.Lock()
	e.onChange = fn
	e.mu.Unlock()
}

// SetOnAttachmentsChange sets a listener fired when attachments change.
func (e *Editor) SetOnAttachmentsChange(fn func([]EditorAttachment)) {
	e.mu.Lock()
	e.onAttachmentsChange = fn
	e.mu.Unlock()
}

// AddToHistory records a submitted prompt for Up/Down history navigation.
func (e *Editor) AddToHistory(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.history) > 0 && e.history[0] == trimmed {
		return
	}
	e.history = append([]string{trimmed}, e.history...)
	if len(e.history) > 100 {
		e.history = e.history[:100]
	}
}

// History returns a copy of stored prompt history entries (newest first).
func (e *Editor) History() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.history))
	copy(out, e.history)
	return out
}

// Attachments returns a copy of current file/image attachments.
func (e *Editor) Attachments() []EditorAttachment {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]EditorAttachment, len(e.attachments))
	copy(out, e.attachments)
	return out
}

// AddFileAttachment attaches a dropped or selected local file path.
func (e *Editor) AddFileAttachment(path string) EditorAttachment {
	name := filepath.Base(path)
	if name == "" || name == "." {
		name = path
	}
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}

	kind := AttachmentFile
	mime := ""
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		kind = AttachmentImage
		mime = "image/" + strings.TrimPrefix(ext, ".")
	}

	e.mu.Lock()
	e.nextAttachID++
	att := EditorAttachment{
		ID:        fmt.Sprintf("att-%d", e.nextAttachID),
		Kind:      kind,
		Name:      name,
		Path:      path,
		MIMEType:  mime,
		SizeBytes: size,
	}
	e.attachments = append(e.attachments, att)
	cp := append([]EditorAttachment(nil), e.attachments...)
	cb := e.onAttachmentsChange
	e.mu.Unlock()

	if cb != nil {
		cb(cp)
	}
	return att
}

// AddImageAttachment attaches raw PNG bytes (e.g. from clipboard paste).
func (e *Editor) AddImageAttachment(name string, pngBytes []byte) EditorAttachment {
	var bmp *ui.Bitmap
	if decoded, err := ui.DecodeBitmap(pngBytes); err == nil {
		bmp = decoded
	}

	e.mu.Lock()
	e.nextAttachID++
	if name == "" {
		name = fmt.Sprintf("pasted-image-%d.png", e.nextAttachID)
	}
	dataCopy := append([]byte(nil), pngBytes...)
	att := EditorAttachment{
		ID:        fmt.Sprintf("img-%d", e.nextAttachID),
		Kind:      AttachmentImage,
		Name:      name,
		MIMEType:  "image/png",
		Data:      dataCopy,
		SizeBytes: int64(len(dataCopy)),
		Bitmap:    bmp,
	}
	e.attachments = append(e.attachments, att)
	cp := append([]EditorAttachment(nil), e.attachments...)
	cb := e.onAttachmentsChange
	e.mu.Unlock()

	if cb != nil {
		cb(cp)
	}
	return att
}

// RemoveAttachment removes the attachment with id.
func (e *Editor) RemoveAttachment(id string) bool {
	e.mu.Lock()
	removed := false
	for i, a := range e.attachments {
		if a.ID == id {
			e.attachments = append(e.attachments[:i], e.attachments[i+1:]...)
			removed = true
			break
		}
	}
	cp := append([]EditorAttachment(nil), e.attachments...)
	cb := e.onAttachmentsChange
	e.mu.Unlock()

	if removed && cb != nil {
		cb(cp)
	}
	return removed
}

// Undo reverts the editor text and selection to the previous undo snapshot.
func (e *Editor) Undo() bool {
	e.mu.Lock()
	snap, ok := e.undo.pop()
	if !ok {
		e.mu.Unlock()
		return false
	}
	cur := e.buf.String()
	e.redo.push(editorSnapshot{Text: cur, SelStart: e.selStart, SelEnd: e.selEnd})
	e.buf.Set(snap.Text)
	e.lastText = snap.Text
	e.selStart, e.selEnd = snap.SelStart, snap.SelEnd
	e.pendingSel = &[2]int{snap.SelStart, snap.SelEnd}
	e.lastAction = ""
	cb := e.onChange
	e.mu.Unlock()

	if cb != nil {
		cb(snap.Text)
	}
	return true
}

// Redo reapplies a previously undone text snapshot.
func (e *Editor) Redo() bool {
	e.mu.Lock()
	snap, ok := e.redo.pop()
	if !ok {
		e.mu.Unlock()
		return false
	}
	cur := e.buf.String()
	e.undo.push(editorSnapshot{Text: cur, SelStart: e.selStart, SelEnd: e.selEnd})
	e.buf.Set(snap.Text)
	e.lastText = snap.Text
	e.selStart, e.selEnd = snap.SelStart, snap.SelEnd
	e.pendingSel = &[2]int{snap.SelStart, snap.SelEnd}
	e.lastAction = ""
	cb := e.onChange
	e.mu.Unlock()

	if cb != nil {
		cb(snap.Text)
	}
	return true
}

// AcceptAutocompleteItem inserts item at the current cursor position, replacing
// the active completion prefix.
func (e *Editor) AcceptAutocompleteItem(item AutocompleteItem) bool {
	e.mu.Lock()
	prov := e.acProvider
	if prov == nil {
		e.mu.Unlock()
		return false
	}
	text := e.buf.String()
	caret := e.selStart
	prefix := e.acPrefix
	e.undo.push(editorSnapshot{Text: text, SelStart: e.selStart, SelEnd: e.selEnd})
	e.redo.clear()
	e.mu.Unlock()

	lines, curLine, curCol := runeOffsetToLineCol(text, caret)
	res := prov.ApplyCompletion(lines, curLine, curCol, item, prefix)
	newText := strings.Join(res.Lines, "\n")
	newCaret := lineColToRuneOffset(res.Lines, res.CursorLine, res.CursorCol)

	e.mu.Lock()
	e.buf.Set(newText)
	e.lastText = newText
	e.selStart, e.selEnd = newCaret, newCaret
	e.pendingSel = &[2]int{newCaret, newCaret}
	e.acVisible = false
	e.acPrefix = ""
	e.acPopup.Clear()
	cb := e.onChange
	e.mu.Unlock()

	if cb != nil {
		cb(newText)
	}
	return true
}

func (e *Editor) refreshAutocompleteFor(text string, caretRune int) {
	e.mu.Lock()
	prov := e.acProvider
	e.mu.Unlock()
	if prov == nil {
		return
	}

	lines, curLine, curCol := runeOffsetToLineCol(text, caretRune)
	before := textBeforeCursor(lines, curLine, curCol)

	shouldCheck := false
	if curLine == 0 && strings.HasPrefix(before, "/") {
		shouldCheck = true
	} else {
		for _, trig := range prov.TriggerCharacters() {
			if strings.Contains(before, trig) {
				shouldCheck = true
				break
			}
		}
	}
	if !shouldCheck {
		e.mu.Lock()
		e.acVisible = false
		e.acPrefix = ""
		e.acPopup.Clear()
		e.mu.Unlock()
		return
	}

	sugg, err := prov.GetSuggestions(context.Background(), lines, curLine, curCol, false)
	e.mu.Lock()
	if err != nil || sugg == nil || len(sugg.Items) == 0 {
		e.acVisible = false
		e.acPrefix = ""
		e.acPopup.Clear()
	} else {
		e.acVisible = true
		e.acPrefix = sugg.Prefix
		e.acPopup.SetItems(sugg.Prefix, sugg.Items)
	}
	e.mu.Unlock()
}

func (e *Editor) triggerAutocompleteForced(text string, caretRune int) bool {
	e.mu.Lock()
	prov := e.acProvider
	e.mu.Unlock()
	if prov == nil {
		return false
	}
	lines, curLine, curCol := runeOffsetToLineCol(text, caretRune)
	if !prov.ShouldTriggerFileCompletion(lines, curLine, curCol) && !(curLine == 0 && strings.HasPrefix(text, "/")) {
		// Allow slash command tab completion too.
		sugg, err := prov.GetSuggestions(context.Background(), lines, curLine, curCol, false)
		if err != nil || sugg == nil || len(sugg.Items) == 0 {
			return false
		}
		if len(sugg.Items) == 1 {
			return e.AcceptAutocompleteItem(sugg.Items[0])
		}
		e.mu.Lock()
		e.acVisible = true
		e.acPrefix = sugg.Prefix
		e.acPopup.SetItems(sugg.Prefix, sugg.Items)
		e.mu.Unlock()
		return true
	}

	sugg, err := prov.GetSuggestions(context.Background(), lines, curLine, curCol, true)
	if err != nil || sugg == nil || len(sugg.Items) == 0 {
		return false
	}
	if len(sugg.Items) == 1 {
		e.mu.Lock()
		e.acPrefix = sugg.Prefix
		e.buf.Set(text)
		e.selStart, e.selEnd = caretRune, caretRune
		e.mu.Unlock()
		return e.AcceptAutocompleteItem(sugg.Items[0])
	}
	e.mu.Lock()
	e.acVisible = true
	e.acPrefix = sugg.Prefix
	e.acPopup.SetItems(sugg.Prefix, sugg.Items)
	e.mu.Unlock()
	return true
}

// View builds the immediate-mode multiline prompt Editor card, attachment chips,
// shell mode indicator, and floating AutocompletePopup overlay.
func (e *Editor) View(c *ui.Context) ui.Element {
	e.mu.Lock()
	theme := ResolveGuiTheme(c, e.theme)
	placeholder := e.placeholder
	label := e.label
	minL := e.minLines
	maxL := e.maxLines
	km := e.keybindings
	disabled := e.disabled
	disableSubmit := e.disableSubmit
	autoFocus := e.autoFocus
	chromeless := e.chromeless
	attachments := append([]EditorAttachment(nil), e.attachments...)
	pendingSel := e.pendingSel
	e.pendingSel = nil
	pendingRep := e.pendingReplace
	e.pendingReplace = nil
	e.mu.Unlock()

	curText := e.buf.String()
	trimmed := strings.TrimSpace(curText)
	isBash := strings.HasPrefix(trimmed, "!")
	isBashExcluded := strings.HasPrefix(trimmed, "!!")

	borderColor := theme.Border
	if isBash {
		if isBashExcluded {
			borderColor = theme.Dim
		} else {
			borderColor = theme.BashMode
		}
	} else if e.inputHandle.Focused(c) {
		borderColor = theme.BorderAccent
	}

	svc := c.Services()
	var card ui.Element
	var inputEl ui.Element
	var removeID string

	if chromeless {
		card = ui.Column(c).FillWidth().Gap(6)
	} else {
		card = ui.Column(c).
			FillWidth().
			Background(theme.ComposerBg).
			Border(1.5, borderColor).
			Radius(theme.Radius).
			Padding(8, 12).
			Gap(6)

		if card.FileDragOver() {
			card.Border(2, theme.Accent)
		}
	}

	card.Children(func() {
		if isBash && !chromeless {
			ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
				badgeText := "BASH"
				badgeColor := theme.BashMode
				if isBashExcluded {
					badgeText = "BASH (excluded from context)"
					badgeColor = theme.Dim
				}
				ui.Text(c, badgeText).
					TextColor(badgeColor).
					FontSize(11).
					FontWeight(600)
			})
		}

		if len(attachments) > 0 {
			ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
				for _, att := range attachments {
					a := att
					chip := ui.Row(c).
						Key(a.ID).
						Background(theme.Raised).
						Border(1, theme.Border).
						Radius(7).
						Padding(3, 8).
						Gap(6).
						AlignItems(ui.Center)
					chip.Children(func() {
						prefix := "File: "
						if a.Kind == AttachmentImage {
							prefix = "Image: "
						}
						ui.Text(c, prefix+a.Name).
							TextColor(theme.Text).
							FontSize(12).
							SingleLine()
						if a.SizeBytes > 0 {
							ui.Text(c, formatByteSize(a.SizeBytes)).
								TextColor(theme.TextTertiary).
								FontSize(11)
						}
						rem := ui.Button(c, "×").
							Label("Remove " + a.Name).
							Cursor(ui.CursorPointer)
						if rem.Clicked() {
							removeID = a.ID
						}
					})
				}
			})
		}

		inputEl = ui.TextAreaBufferBase(c, e.buf).
			Bind(&e.inputHandle).
			FillWidth().
			Lines(minL, maxL).
			FontSize(13.5).
			TextColor(theme.Text).
			Placeholder(placeholder).
			Label(label).
			Disabled(disabled)
		if autoFocus {
			inputEl.AutoFocus()
		}

		if pendingRep != nil {
			inputEl.ReplaceText(pendingRep.start, pendingRep.end, pendingRep.text)
		}
		if pendingSel != nil {
			inputEl.SetTextSelection(pendingSel[0], pendingSel[1])
		}

		selStart, selEnd := inputEl.TextSelection()
		e.mu.Lock()
		prevSeen := e.lastText
		e.selStart = selStart
		e.selEnd = selEnd
		if curText != prevSeen {
			if prevSeen != "" || curText != "" {
				e.undo.push(editorSnapshot{Text: prevSeen, SelStart: selStart, SelEnd: selEnd})
				e.redo.clear()
			}
			e.lastText = curText
			e.lastAction = ""
		}
		e.mu.Unlock()

		inputEl.OnChange(func() {
			nextText := e.buf.String()
			e.mu.Lock()
			caret := e.selStart
			if nextText != e.lastText {
				prevRunes := utf8.RuneCountInString(e.lastText)
				nextRunes := utf8.RuneCountInString(nextText)
				e.undo.push(editorSnapshot{Text: e.lastText, SelStart: e.selStart, SelEnd: e.selEnd})
				e.redo.clear()
				caret = e.selStart + (nextRunes - prevRunes)
				if caret < 0 {
					caret = 0
				}
				if caret > nextRunes {
					caret = nextRunes
				}
				e.selStart = caret
				e.selEnd = caret
				e.lastText = nextText
				e.historyIndex = -1
				e.historyDraft = nil
				e.lastAction = ""
			}
			onCh := e.onChange
			e.mu.Unlock()

			e.refreshAutocompleteFor(nextText, caret)
			if onCh != nil {
				onCh(nextText)
			}
		})

		inputEl.OnPaste(func(pastedText string) bool {
			var pngBytes []byte
			e.mu.Lock()
			pngHook := e.readClipboardPNG
			e.mu.Unlock()
			if pngHook != nil {
				if b, err := pngHook(); err == nil && len(b) > 0 {
					pngBytes = b
				}
			} else {
				imgData := svc.ReadClipboardData(transfer.PNG)
				if b, err := imgData.Read(transfer.PNG); err == nil && len(b) > 0 {
					pngBytes = b
				}
			}
			if len(pngBytes) > 0 {
				e.AddImageAttachment("", pngBytes)
				return true
			}
			return false
		})

		inputEl.OnTextKey(func(k ui.TextKey) bool {
			return e.handleTextKey(k, km, disableSubmit)
		})
	})

	if dropped := append(inputEl.DroppedFiles(), card.DroppedFiles()...); len(dropped) > 0 {
		seen := make(map[string]bool, len(dropped))
		for _, path := range dropped {
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			e.AddFileAttachment(path)
		}
	}

	if removeID != "" {
		e.RemoveAttachment(removeID)
		e.inputHandle.Focus(c)
	}

	// Render AutocompletePopup overlay anchored to the editor card.
	e.mu.Lock()
	acVis := e.acVisible && !e.acPopup.Empty()
	e.mu.Unlock()
	if acVis {
		ui.Overlay(c, func() {
			pop := e.acPopup.View(c).
				AttachTo(card, ui.AnchorTopStart, ui.AnchorBottomStart).
				Margin(4, 0, 4, 0)
			if pop.PressedOutside() {
				e.mu.Lock()
				e.acVisible = false
				e.acPopup.Clear()
				e.mu.Unlock()
			}
		})
	}

	return card
}

func (e *Editor) handleTextKey(
	k ui.TextKey,
	km *KeybindingsManager,
	disableSubmit bool,
) bool {
	liveText := k.Text()
	caret := e.estimateLiveCaret(liveText)

	// 1. Undo / Redo
	if km.MatchesTextKey(k, string(ActionUndo)) {
		e.mu.Lock()
		snap, ok := e.undo.pop()
		if ok {
			e.redo.push(editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret})
			e.buf.Set(snap.Text)
			e.lastText = snap.Text
			e.selStart, e.selEnd = snap.SelStart, snap.SelEnd
			e.pendingSel = &[2]int{snap.SelStart, snap.SelEnd}
			e.lastAction = ""
			cb := e.onChange
			e.mu.Unlock()
			k.SetText(snap.Text)
			if cb != nil {
				cb(snap.Text)
			}
			return true
		}
		e.mu.Unlock()
		return true
	}
	if km.MatchesTextKey(k, string(ActionRedo)) {
		e.mu.Lock()
		snap, ok := e.redo.pop()
		if ok {
			e.undo.push(editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret})
			e.buf.Set(snap.Text)
			e.lastText = snap.Text
			e.selStart, e.selEnd = snap.SelStart, snap.SelEnd
			e.pendingSel = &[2]int{snap.SelStart, snap.SelEnd}
			e.lastAction = ""
			cb := e.onChange
			e.mu.Unlock()
			k.SetText(snap.Text)
			if cb != nil {
				cb(snap.Text)
			}
			return true
		}
		e.mu.Unlock()
		return true
	}

	// 2. Active autocomplete popup navigation / accept / dismiss
	e.mu.Lock()
	acOpen := e.acVisible && !e.acPopup.Empty()
	e.mu.Unlock()
	if acOpen {
		if km.MatchesTextKey(k, string(ActionSelectCancel)) {
			e.mu.Lock()
			e.acVisible = false
			e.acPopup.Clear()
			e.mu.Unlock()
			return true
		}
		if km.MatchesTextKey(k, string(ActionSelectUp)) {
			e.acPopup.MoveUp()
			return true
		}
		if km.MatchesTextKey(k, string(ActionSelectDown)) {
			e.acPopup.MoveDown()
			return true
		}
		if km.MatchesTextKey(k, string(ActionTab)) {
			if item, ok := e.acPopup.SelectedItem(); ok {
				e.applyCompletionThroughTextKey(k, liveText, caret, item)
				return true
			}
		}
		if km.MatchesTextKey(k, string(ActionSelectConfirm)) {
			if item, ok := e.acPopup.SelectedItem(); ok {
				e.mu.Lock()
				prefix := e.acPrefix
				e.mu.Unlock()
				updated := e.applyCompletionThroughTextKey(k, liveText, caret, item)
				if !strings.HasPrefix(prefix, "/") {
					return true
				}
				// For leading slash commands, fall through to submit on Enter.
				liveText = updated
				caret = utf8.RuneCountInString(liveText)
			}
		}
	}

	// 3. Tab completion trigger when popup is closed
	if km.MatchesTextKey(k, string(ActionTab)) {
		e.mu.Lock()
		prov := e.acProvider
		e.mu.Unlock()
		if prov != nil {
			lines, curLine, curCol := runeOffsetToLineCol(liveText, caret)
			sugg, err := prov.GetSuggestions(context.Background(), lines, curLine, curCol, true)
			if err == nil && sugg != nil && len(sugg.Items) > 0 {
				e.mu.Lock()
				e.acPrefix = sugg.Prefix
				e.mu.Unlock()
				if len(sugg.Items) == 1 {
					e.applyCompletionThroughTextKey(k, liveText, caret, sugg.Items[0])
				} else {
					e.mu.Lock()
					e.acVisible = true
					e.acPopup.SetItems(sugg.Prefix, sugg.Items)
					e.mu.Unlock()
				}
				return true
			}
		}
		return true
	}

	// 4. Follow-up queue (Alt+Enter / configured AppMessageFollowUp)
	if km.MatchesTextKey(k, string(AppMessageFollowUp)) {
		if disableSubmit {
			return true
		}
		e.dispatchSubmit(k, liveText, true)
		return true
	}

	// 5. Explicit newline (Shift+Enter) passes through to TextArea
	if km.MatchesTextKey(k, string(ActionNewLine)) {
		e.mu.Lock()
		e.historyIndex = -1
		e.historyDraft = nil
		e.lastAction = ""
		e.mu.Unlock()
		return false
	}

	// 6. Submit (Enter)
	if km.MatchesTextKey(k, string(ActionSubmit)) {
		if disableSubmit {
			return true
		}
		e.dispatchSubmit(k, liveText, false)
		return true
	}

	// 7. Escape cancel (when autocomplete closed)
	if km.MatchesTextKey(k, string(AppInterrupt)) || km.MatchesTextKey(k, string(ActionSelectCancel)) {
		e.mu.Lock()
		cb := e.onCancel
		e.mu.Unlock()
		if cb != nil {
			cb()
		}
		return true
	}

	// 8. Emacs kill-ring shortcuts
	if km.MatchesTextKey(k, string(ActionDeleteWordBackward)) {
		return e.killRange(k, liveText, wordBackwardStart(liveText, caret), caret, true)
	}
	if km.MatchesTextKey(k, string(ActionDeleteToLineStart)) {
		return e.killRange(k, liveText, lineStartRune(liveText, caret), caret, true)
	}
	if km.MatchesTextKey(k, string(ActionDeleteToLineEnd)) {
		return e.killRange(k, liveText, caret, lineEndRune(liveText, caret), false)
	}
	if km.MatchesTextKey(k, string(ActionYank)) {
		return e.doYank(k, liveText, caret)
	}
	if km.MatchesTextKey(k, string(ActionYankPop)) {
		return e.doYankPop(k, liveText)
	}

	// 9. History navigation
	if km.MatchesTextKey(k, string(ActionHistoryPrevious)) {
		return e.navigateHistoryThroughKey(k, liveText, caret, -1)
	}
	if km.MatchesTextKey(k, string(ActionHistoryNext)) {
		return e.navigateHistoryThroughKey(k, liveText, caret, 1)
	}
	if km.MatchesTextKey(k, string(ActionCursorUp)) {
		e.mu.Lock()
		inHist := e.historyIndex > -1
		hasHist := len(e.history) > 0
		e.mu.Unlock()
		if hasHist && isOnFirstLineRune(liveText, caret) && (liveText == "" || inHist || caret == 0) {
			return e.navigateHistoryThroughKey(k, liveText, caret, -1)
		}
		return false
	}
	if km.MatchesTextKey(k, string(ActionCursorDown)) {
		e.mu.Lock()
		inHist := e.historyIndex > -1
		e.mu.Unlock()
		if inHist && isOnLastLineRune(liveText, caret) {
			return e.navigateHistoryThroughKey(k, liveText, caret, 1)
		}
		return false
	}

	return false
}

func (e *Editor) estimateLiveCaret(liveText string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if liveText == e.lastText {
		n := utf8.RuneCountInString(liveText)
		if e.selStart > n {
			return n
		}
		return e.selStart
	}
	// Adjust for un-framed typing before TextKey ran.
	prevRunes := []rune(e.lastText)
	curRunes := []rune(liveText)
	diff := len(curRunes) - len(prevRunes)
	caret := e.selStart + diff
	if caret < 0 {
		caret = 0
	}
	if caret > len(curRunes) {
		caret = len(curRunes)
	}
	return caret
}

func (e *Editor) dispatchSubmit(k ui.TextKey, liveText string, followUp bool) {
	trimmed := strings.TrimSpace(liveText)
	e.mu.Lock()
	atts := append([]EditorAttachment(nil), e.attachments...)
	e.attachments = nil
	e.buf.Set("")
	e.lastText = ""
	e.selStart, e.selEnd = 0, 0
	e.pendingSel = &[2]int{0, 0}
	e.acVisible = false
	e.acPrefix = ""
	e.acPopup.Clear()
	e.historyIndex = -1
	e.historyDraft = nil
	e.lastAction = ""

	ev := EditorSubmitEvent{
		Text:               liveText,
		Attachments:        atts,
		BashMode:           strings.HasPrefix(trimmed, "!"),
		ExcludeFromContext: strings.HasPrefix(trimmed, "!!"),
	}
	cb := e.onSubmit
	if followUp && e.onFollowUp != nil {
		cb = e.onFollowUp
	}
	onCh := e.onChange
	onAtt := e.onAttachmentsChange
	e.mu.Unlock()

	k.SetText("")
	if len(atts) > 0 && onAtt != nil {
		onAtt(nil)
	}
	if onCh != nil {
		onCh("")
	}
	if cb != nil {
		cb(ev)
	}
}

func (e *Editor) applyCompletionThroughTextKey(
	k ui.TextKey,
	liveText string,
	caret int,
	item AutocompleteItem,
) string {
	e.mu.Lock()
	prov := e.acProvider
	prefix := e.acPrefix
	e.mu.Unlock()
	if prov == nil {
		return liveText
	}
	lines, curLine, curCol := runeOffsetToLineCol(liveText, caret)
	res := prov.ApplyCompletion(lines, curLine, curCol, item, prefix)
	newText := strings.Join(res.Lines, "\n")
	newCaret := lineColToRuneOffset(res.Lines, res.CursorLine, res.CursorCol)

	e.mu.Lock()
	e.undo.push(editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret})
	e.redo.clear()
	e.buf.Set(newText)
	e.lastText = newText
	e.selStart, e.selEnd = newCaret, newCaret
	e.pendingSel = &[2]int{newCaret, newCaret}
	e.acVisible = false
	e.acPrefix = ""
	e.acPopup.Clear()
	e.lastAction = ""
	onCh := e.onChange
	e.mu.Unlock()

	k.SetText(newText)
	if onCh != nil {
		onCh(newText)
	}
	return newText
}

func (e *Editor) killRange(
	k ui.TextKey,
	liveText string,
	start, end int,
	prepend bool,
) bool {
	runes := []rune(liveText)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start >= end {
		return true
	}
	killed := string(runes[start:end])
	newRunes := make([]rune, 0, len(runes)-(end-start))
	newRunes = append(newRunes, runes[:start]...)
	newRunes = append(newRunes, runes[end:]...)
	newText := string(newRunes)

	e.mu.Lock()
	e.undo.push(editorSnapshot{Text: liveText, SelStart: start, SelEnd: end})
	e.redo.clear()
	e.kill.push(killed, prepend, e.lastAction == "kill")
	e.lastAction = "kill"
	e.buf.Set(newText)
	e.lastText = newText
	e.selStart, e.selEnd = start, start
	e.pendingSel = &[2]int{start, start}
	onCh := e.onChange
	e.mu.Unlock()

	k.SetText(newText)
	if onCh != nil {
		onCh(newText)
	}
	return true
}

func (e *Editor) doYank(k ui.TextKey, liveText string, caret int) bool {
	e.mu.Lock()
	yanked, ok := e.kill.peek()
	if !ok || yanked == "" {
		e.mu.Unlock()
		return true
	}
	runes := []rune(liveText)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	insRunes := []rune(yanked)
	newRunes := make([]rune, 0, len(runes)+len(insRunes))
	newRunes = append(newRunes, runes[:caret]...)
	newRunes = append(newRunes, insRunes...)
	newRunes = append(newRunes, runes[caret:]...)
	newText := string(newRunes)
	newCaret := caret + len(insRunes)

	e.undo.push(editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret})
	e.redo.clear()
	e.lastAction = "yank"
	e.yankStart = caret
	e.yankEnd = newCaret
	e.buf.Set(newText)
	e.lastText = newText
	e.selStart, e.selEnd = newCaret, newCaret
	e.pendingSel = &[2]int{newCaret, newCaret}
	onCh := e.onChange
	e.mu.Unlock()

	k.SetText(newText)
	if onCh != nil {
		onCh(newText)
	}
	return true
}

func (e *Editor) doYankPop(k ui.TextKey, liveText string) bool {
	e.mu.Lock()
	if e.lastAction != "yank" {
		e.mu.Unlock()
		return true
	}
	yanked, ok := e.kill.rotate()
	if !ok {
		e.mu.Unlock()
		return true
	}
	runes := []rune(liveText)
	if e.yankStart < 0 || e.yankEnd > len(runes) || e.yankStart > e.yankEnd {
		e.mu.Unlock()
		return true
	}
	insRunes := []rune(yanked)
	newRunes := make([]rune, 0, len(runes)-(e.yankEnd-e.yankStart)+len(insRunes))
	newRunes = append(newRunes, runes[:e.yankStart]...)
	newRunes = append(newRunes, insRunes...)
	newRunes = append(newRunes, runes[e.yankEnd:]...)
	newText := string(newRunes)
	newCaret := e.yankStart + len(insRunes)

	e.yankEnd = newCaret
	e.buf.Set(newText)
	e.lastText = newText
	e.selStart, e.selEnd = newCaret, newCaret
	e.pendingSel = &[2]int{newCaret, newCaret}
	onCh := e.onChange
	e.mu.Unlock()

	k.SetText(newText)
	if onCh != nil {
		onCh(newText)
	}
	return true
}

func (e *Editor) navigateHistoryThroughKey(
	k ui.TextKey,
	liveText string,
	caret int,
	direction int,
) bool {
	e.mu.Lock()
	if len(e.history) == 0 {
		e.mu.Unlock()
		return false
	}
	newIdx := e.historyIndex - direction // Up(-1) increases index, Down(+1) decreases index
	if newIdx < -1 || newIdx >= len(e.history) {
		e.mu.Unlock()
		return true
	}
	if e.historyIndex == -1 && newIdx >= 0 {
		e.undo.push(editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret})
		draft := editorSnapshot{Text: liveText, SelStart: caret, SelEnd: caret}
		e.historyDraft = &draft
	}
	e.historyIndex = newIdx
	e.lastAction = ""

	var nextText string
	var nextCaret int
	if newIdx == -1 {
		if e.historyDraft != nil {
			nextText = e.historyDraft.Text
			nextCaret = e.historyDraft.SelStart
			e.historyDraft = nil
		}
	} else {
		nextText = e.history[newIdx]
		if direction == -1 {
			nextCaret = 0
		} else {
			nextCaret = utf8.RuneCountInString(nextText)
		}
	}
	e.buf.Set(nextText)
	e.lastText = nextText
	e.selStart, e.selEnd = nextCaret, nextCaret
	e.pendingSel = &[2]int{nextCaret, nextCaret}
	e.acVisible = false
	e.acPopup.Clear()
	onCh := e.onChange
	e.mu.Unlock()

	k.SetText(nextText)
	if onCh != nil {
		onCh(nextText)
	}
	return true
}

// NavigateHistory exposes Up (-1) / Down (+1) prompt history browsing programmatically.
func (e *Editor) NavigateHistory(direction int) bool {
	e.mu.Lock()
	if len(e.history) == 0 {
		e.mu.Unlock()
		return false
	}
	newIdx := e.historyIndex - direction
	if newIdx < -1 || newIdx >= len(e.history) {
		e.mu.Unlock()
		return false
	}
	cur := e.buf.String()
	if e.historyIndex == -1 && newIdx >= 0 {
		draft := editorSnapshot{Text: cur, SelStart: e.selStart, SelEnd: e.selEnd}
		e.historyDraft = &draft
	}
	e.historyIndex = newIdx
	var nextText string
	var nextCaret int
	if newIdx == -1 {
		if e.historyDraft != nil {
			nextText = e.historyDraft.Text
			nextCaret = e.historyDraft.SelStart
			e.historyDraft = nil
		}
	} else {
		nextText = e.history[newIdx]
		if direction < 0 {
			nextCaret = 0
		} else {
			nextCaret = utf8.RuneCountInString(nextText)
		}
	}
	e.buf.Set(nextText)
	e.lastText = nextText
	e.selStart, e.selEnd = nextCaret, nextCaret
	e.pendingSel = &[2]int{nextCaret, nextCaret}
	onCh := e.onChange
	e.mu.Unlock()

	if onCh != nil {
		onCh(nextText)
	}
	return true
}

func runeOffsetToLineCol(text string, offset int) (lines []string, line, col int) {
	lines = strings.Split(text, "\n")
	if len(lines) == 0 {
		return []string{""}, 0, 0
	}
	rem := offset
	if rem < 0 {
		rem = 0
	}
	for i, l := range lines {
		n := utf8.RuneCountInString(l)
		if rem <= n || i == len(lines)-1 {
			if rem > n {
				rem = n
			}
			return lines, i, rem
		}
		rem -= n + 1 // +1 for '\n'
	}
	return lines, 0, 0
}

func lineColToRuneOffset(lines []string, line, col int) int {
	offset := 0
	for i := 0; i < line && i < len(lines); i++ {
		offset += utf8.RuneCountInString(lines[i]) + 1
	}
	if line >= 0 && line < len(lines) {
		n := utf8.RuneCountInString(lines[line])
		if col > n {
			col = n
		}
		if col < 0 {
			col = 0
		}
		offset += col
	}
	return offset
}

func isOnFirstLineRune(text string, caret int) bool {
	runes := []rune(text)
	if caret > len(runes) {
		caret = len(runes)
	}
	for i := 0; i < caret; i++ {
		if runes[i] == '\n' {
			return false
		}
	}
	return true
}

func isOnLastLineRune(text string, caret int) bool {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	for i := caret; i < len(runes); i++ {
		if runes[i] == '\n' {
			return false
		}
	}
	return true
}

func lineStartRune(text string, caret int) int {
	runes := []rune(text)
	if caret > len(runes) {
		caret = len(runes)
	}
	for i := caret - 1; i >= 0; i-- {
		if runes[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

func lineEndRune(text string, caret int) int {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	for i := caret; i < len(runes); i++ {
		if runes[i] == '\n' {
			return i
		}
	}
	return len(runes)
}

func wordBackwardStart(text string, caret int) int {
	runes := []rune(text)
	if caret > len(runes) {
		caret = len(runes)
	}
	i := caret
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	isPunct := unicode.IsPunct(runes[i-1]) || unicode.IsSymbol(runes[i-1])
	for i > 0 && !unicode.IsSpace(runes[i-1]) {
		curPunct := unicode.IsPunct(runes[i-1]) || unicode.IsSymbol(runes[i-1])
		if curPunct != isPunct {
			break
		}
		i--
	}
	return i
}

func formatByteSize(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
