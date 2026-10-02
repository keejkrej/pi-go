//go:build darwin

// Ported from packages/tui/native/darwin/src/darwin-platform.m (pi v1.0.0).

package tui

// npIsModifierPressed is the isModifierPressed export.
// Names are shift, command, control, and option. An unknown name is false.
func npIsModifierPressed(name string) (pressed bool, err error) {
	panic("unported: npIsModifierPressed")
}

// npGetText is the getText export.
// ok false is unavailable. A nil text with ok true is an empty clipboard. A non-nil error rejects.
func npGetText() (text *string, ok bool, err error) {
	panic("unported: npGetText")
}

// npSetText is the setText export.
func npSetText(text string) error {
	panic("unported: npSetText")
}

// npGetImage is the getImage export.
// ok false is unavailable. A nil image with ok true means no image. Bytes may be PNG converted from TIFF.
func npGetImage() (image []byte, ok bool, err error) {
	panic("unported: npGetImage")
}

// npGetFilePaths is the getFilePaths export.
// ok false is unavailable. A nil slice with ok true means no file URLs.
func npGetFilePaths() (paths []string, ok bool, err error) {
	panic("unported: npGetFilePaths")
}
