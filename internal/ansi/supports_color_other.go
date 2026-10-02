// Ported from chalk@6.0.0 source/vendor/supports-color/index.js (os.release() is only read on win32).

//go:build !windows

package ansi

// supportsColorOSRelease is only consulted on Windows; other platforms never read it.
func supportsColorOSRelease() string {
	return ""
}
