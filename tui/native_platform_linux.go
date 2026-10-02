//go:build linux

// Ported from packages/tui/native/linux/src/linux-platform-x11.c and clipboard-worker.h (pi v1.0.0).

package tui

// Linux N-API exports are getText and getImage. clipboard-worker.h runs each read on a
// private thread; a busy or unready worker resolves unavailable. There is no setText export.

// npGetText is the getText export.
// ok false is unavailable. A nil text with ok true is an empty clipboard. A non-nil error rejects.
func npGetText() (text *string, ok bool, err error) {
	panic("unported: npGetText")
}

// npGetImage is the getImage export.
// ok false is unavailable. A nil image with ok true means no image.
func npGetImage() (image []byte, ok bool, err error) {
	panic("unported: npGetImage")
}
