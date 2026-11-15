// Ported from packages/tui/src/native-module-path.ts (pi v1.0.0).

package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

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
	var moduleDir string
	if options != nil && options.ModuleUrl != nil && *options.ModuleUrl != "" {
		moduleDir = filepath.Dir(nmpFileURLToPath(*options.ModuleUrl))
	} else if _, file, _, ok := runtime.Caller(0); ok && file != "" {
		moduleDir = filepath.Dir(file)
	} else if wd, err := os.Getwd(); err == nil {
		moduleDir = wd
	} else {
		moduleDir = "."
	}

	var execPath string
	if options != nil && options.ExecPath != nil {
		execPath = *options.ExecPath
	} else if exe, err := os.Executable(); err == nil && exe != "" {
		execPath = exe
	} else if len(os.Args) > 0 {
		execPath = os.Args[0]
	}
	execDir := filepath.Dir(execPath)

	var candidates []string
	if options != nil && options.ResolvePackage != nil {
		if pkgJSONPath, err := options.ResolvePackage(nmpTuiPackageName + "/package.json"); err == nil && pkgJSONPath != "" {
			packageRoot := filepath.Dir(pkgJSONPath)
			candidates = append(candidates, filepath.Join(packageRoot, nativePath))
		}
	}

	candidates = append(candidates, nmpResolve(moduleDir, "..", nativePath))
	if filepath.Dir(moduleDir) != moduleDir {
		candidates = append(candidates, nmpResolve(moduleDir, "..", "..", nativePath))
	}

	baseName := filepath.Base(nativePath)
	candidates = append(candidates,
		filepath.Join(execDir, baseName),
		filepath.Join(execDir, "native", baseName),
	)

	seen := make(map[string]struct{}, len(candidates))
	deduped := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		deduped = append(deduped, candidate)
	}
	return deduped
}

func nmpFileURLToPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return raw
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' &&
		((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) && p[2] == ':' {
		p = p[1:]
	} else if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
		p = "//" + u.Host + p
	}
	return filepath.FromSlash(p)
}

func nmpResolve(parts ...string) string {
	joined := filepath.Join(parts...)
	if abs, err := filepath.Abs(joined); err == nil {
		return abs
	}
	return filepath.Clean(joined)
}
