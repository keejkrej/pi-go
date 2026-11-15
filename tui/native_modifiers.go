// Ported from packages/tui/src/native-modifiers.ts (pi v1.0.0).

package tui

// IsNativeModifierPressed reports whether the native helper currently sees key held.
// A missing helper, a missing poll, or a poll error is false.
func IsNativeModifierPressed(key ModifierKey) bool {
	helper := GetNativePlatformHelper()
	if helper == nil {
		return false
	}
	pressed, err := helper.IsModifierPressed(key)
	if err != nil {
		return false
	}
	return pressed
}
