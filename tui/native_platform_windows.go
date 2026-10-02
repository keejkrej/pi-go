//go:build windows

// Ported from packages/tui/native/win32/src/win32-platform.c (pi v1.0.0).

package tui

// npEnableVirtualTerminalInput is the enableVirtualTerminalInput export.
// It sets ENABLE_VIRTUAL_TERMINAL_INPUT on stdin. A non-nil error rejects.
func npEnableVirtualTerminalInput() (enabled bool, err error) {
	panic("unported: npEnableVirtualTerminalInput")
}

// npIsModifierPressed is the isModifierPressed export.
// shift, control, option/alt, and command/super/win are recognized. An unknown name is false.
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
// ok false is unavailable. A nil image with ok true means no image.
func npGetImage() (image []byte, ok bool, err error) {
	panic("unported: npGetImage")
}
