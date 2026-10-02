// Ported from packages/tui/src/native-module-path.ts (pi v1.0.0).

package tui

const nmpTuiPackageName = "@earendil-works/pi-tui"

// NativeModuleCandidateOptions overrides how native module paths are resolved.
// A nil options pointer uses the defaults. Nil ModuleUrl uses this module's URL.
// Nil ExecPath uses the executable path. Nil ResolvePackage resolves nmpTuiPackageName
// and returns an error when the package is not installed.
type NativeModuleCandidateOptions struct {
	ModuleUrl      *string
	ExecPath       *string
	ResolvePackage func(specifier string) (string, error)
}

// GetNativeModuleCandidates returns deduplicated paths that may contain nativePath.
// Order is the installed-package path, then the module directory parents, then the executable directory.
func GetNativeModuleCandidates(nativePath string, options *NativeModuleCandidateOptions) []string {
	panic("unported: GetNativeModuleCandidates")
}
