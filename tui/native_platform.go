// Ported from packages/tui/src/native-platform.ts (pi v1.0.0).

package tui

// ModifierKey is a native modifier polled outside the terminal stream.
type ModifierKey string

const (
	ModifierKeyShift   ModifierKey = "shift"
	ModifierKeyCommand ModifierKey = "command"
	ModifierKeyControl ModifierKey = "control"
	ModifierKeyOption  ModifierKey = "option"
)

// NativeClipboard reads, and on some platforms writes, the system clipboard.
// Optional TS methods are separate interfaces: NativeClipboardSetText and NativeClipboardFilePaths.
//
// Read results are tri-state. ok false is TS undefined (unavailable). A nil payload
// with ok true is TS null (nothing of that kind on the clipboard). A non-nil payload
// is the value and may be empty. A non-nil error is a rejected transfer.
type NativeClipboard interface {
	GetText() (text *string, ok bool, err error)
	GetImage() (image []byte, ok bool, err error)
}

// NativeClipboardSetText is implemented when the helper exports setText.
// Linux keeps clipboard ownership with command-line tools and does not implement this.
type NativeClipboardSetText interface {
	SetText(text string) error
}

// NativeClipboardFilePaths is implemented when the helper exports getFilePaths.
// Paths are POSIX paths of file URLs. ok false means unsupported. A nil slice with ok true means no files.
type NativeClipboardFilePaths interface {
	GetFilePaths() (paths []string, ok bool, err error)
}

// npNativePlatformHelper is the darwin/windows native module surface.
// A nil interface is TS undefined. Linux clipboard helpers do not implement it.
type npNativePlatformHelper interface {
	NativeClipboard
	NativeClipboardSetText
	IsModifierPressed(name ModifierKey) (pressed bool, err error)
}

// npVirtualTerminalInput is implemented when the helper exports enableVirtualTerminalInput.
// Only Windows does. Darwin file URLs are NativeClipboardFilePaths.
type npVirtualTerminalInput interface {
	EnableVirtualTerminalInput() (enabled bool, err error)
}

// GetNativePlatformHelper returns the darwin or Windows helper.
// It is nil on linux and on every other GOOS, and nil when the arch is not amd64 or arm64.
// The Go port does not load a .node prebuild; the helper calls the GOOS np* functions.
func GetNativePlatformHelper() npNativePlatformHelper {
	panic("unported: GetNativePlatformHelper")
}

// GetNativeClipboard returns the clipboard helper.
// Module loading is cached; display availability is not, so a disconnected display can recover.
// Darwin and Windows return GetNativePlatformHelper. Linux returns the X11 helper when DISPLAY
// is set, and nil otherwise (Wayland paste uses wl-paste). Other GOOS values are nil.
func GetNativeClipboard() NativeClipboard {
	panic("unported: GetNativeClipboard")
}

// npLoadNativePlatformHelper returns the cached helper for platform and suffix.
// suffix "" selects the "<platform>-platform" helper; linux passes "-x11".
// A cached miss is unavailable and must stay distinct from a key that has not been loaded.
// Only amd64 and arm64 are supported. This does not load a .node binary.
// Darwin and Windows values also implement npNativePlatformHelper. Windows values implement
// npVirtualTerminalInput. Darwin values implement NativeClipboardFilePaths. Linux values do not.
func npLoadNativePlatformHelper(platform string, suffix string) NativeClipboard {
	panic("unported: npLoadNativePlatformHelper")
}
