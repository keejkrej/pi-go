// Ported from packages/tui/src/editor-component.ts (pi v1.0.0).

package tui

// EditorComponent is a custom editor.
// Extensions can replace the editor (vim, emacs, custom keybindings) and stay
// compatible with the core application.
//
// TypeScript optional methods are required here. An implementation that does not
// support one returns the zero value or does nothing: AddToHistory, InsertTextAtCursor,
// SetAutocompleteProvider, SetPaddingX, and SetAutocompleteMaxVisible are no-ops,
// and GetExpandedText returns GetText().
// OnSubmit, OnChange, and BorderColor nil means the callback is unset.
type EditorComponent interface {
	Component

	// GetText returns the current text.
	GetText() string
	// SetText replaces the text.
	SetText(text string)
	// HandleInput applies raw terminal input.
	HandleInput(data string)

	// OnSubmit is called when the user submits. Nil means unset.
	OnSubmit() func(text string)
	SetOnSubmit(fn func(text string))

	// OnChange is called when the text changes. Nil means unset.
	OnChange() func(text string)
	SetOnChange(fn func(text string))

	// AddToHistory stores text for up/down navigation.
	AddToHistory(text string)

	// InsertTextAtCursor inserts text at the cursor.
	InsertTextAtCursor(text string)
	// GetExpandedText returns text with markers expanded, such as paste markers.
	GetExpandedText() string

	// SetAutocompleteProvider sets the autocomplete provider.
	SetAutocompleteProvider(provider AutocompleteProvider)

	// BorderColor styles the border. Nil means unset.
	BorderColor() func(str string) string
	SetBorderColor(fn func(str string) string)

	// SetPaddingX sets horizontal padding.
	SetPaddingX(padding int)
	// SetAutocompleteMaxVisible sets how many autocomplete rows are shown.
	SetAutocompleteMaxVisible(maxVisible int)
}
