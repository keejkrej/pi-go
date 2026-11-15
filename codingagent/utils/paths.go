// Ported from packages/coding-agent/src/utils/paths.ts (pi v1.5.2).

package utils

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
)

var (
	pathsUnicodeSpacesRe     = regexp.MustCompile("[\u00A0\u2000-\u200A\u202F\u205F\u3000]")
	pathsWindowsShellDriveRe = regexp.MustCompile(`(?i)^/(?:mnt/|cygdrive/)?([a-z])(?:/(.*))?$`)
	pathsPlatform            = js.Platform
)

// PathInputOptions configures path input normalization in NormalizePath and ResolvePath.
type PathInputOptions struct {
	Trim                   bool
	ExpandTilde            *bool
	HomeDir                *string
	StripAtPrefix          bool
	NormalizeUnicodeSpaces bool
}

// CanonicalizePath resolves a path to its real form by following symlinks,
// falling back to the raw path if resolution fails.
func CanonicalizePath(p string) string {
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return realPath
}

func pathsIsWin32() bool {
	return pathsPlatform() == "win32"
}

func pathsSep() string {
	if pathsIsWin32() {
		return `\`
	}
	return `/`
}

func pathsIsWinSep(b byte) bool {
	return b == '/' || b == '\\'
}

func pathsIsWinDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func pathsWin32IsAbsolute(p string) bool {
	n := len(p)
	if n == 0 {
		return false
	}
	if pathsIsWinSep(p[0]) {
		return true
	}
	return n > 2 && pathsIsWinDriveLetter(p[0]) && p[1] == ':' && pathsIsWinSep(p[2])
}

func pathsPosixIsAbsolute(p string) bool {
	return strings.HasPrefix(p, "/")
}

func pathsAPIisAbsolute(p string) bool {
	if pathsIsWin32() {
		return pathsWin32IsAbsolute(p)
	}
	return pathsPosixIsAbsolute(p)
}

func pathsNormalizeDots(parts []string, allowAboveRoot bool) []string {
	var res []string
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(res) > 0 && res[len(res)-1] != ".." {
				res = res[:len(res)-1]
			} else if allowAboveRoot {
				res = append(res, "..")
			}
		} else {
			res = append(res, part)
		}
	}
	return res
}

func pathsWin32SplitRoot(p string) (device string, isUNC bool, rest string) {
	n := len(p)
	if n == 0 {
		return "", false, ""
	}
	if pathsIsWinSep(p[0]) {
		if n > 1 && pathsIsWinSep(p[1]) && (n == 2 || !pathsIsWinSep(p[2])) {
			// UNC \\server\share\...
			i := 2
			for i < n && !pathsIsWinSep(p[i]) {
				i++
			}
			server := p[2:i]
			for i < n && pathsIsWinSep(p[i]) {
				i++
			}
			j := i
			for j < n && !pathsIsWinSep(p[j]) {
				j++
			}
			share := p[i:j]
			if server != "" && share != "" {
				dev := `\\` + server + `\` + share
				if j < n {
					return dev + `\`, true, p[j+1:]
				}
				return dev + `\`, true, ""
			}
		}
		return `\`, false, p[1:]
	}
	if n >= 2 && pathsIsWinDriveLetter(p[0]) && p[1] == ':' {
		if n > 2 && pathsIsWinSep(p[2]) {
			return p[:2] + `\`, false, p[3:]
		}
		return p[:2], false, p[2:]
	}
	return "", false, p
}

func pathsWin32Normalize(p string) string {
	if p == "" {
		return "."
	}
	dev, _, rest := pathsWin32SplitRoot(p)
	isAbs := dev != "" && (strings.HasSuffix(dev, `\`) || strings.HasPrefix(dev, `\`))
	rawParts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	norm := pathsNormalizeDots(rawParts, !isAbs)
	tail := strings.Join(norm, `\`)
	if dev == "" {
		if tail == "" {
			return "."
		}
		return tail
	}
	if tail == "" {
		return dev
	}
	return dev + tail
}

func pathsWin32Resolve(segments ...string) string {
	var resolvedDevice string
	var resolvedTail string
	resolvedAbs := false

	for i := len(segments) - 1; i >= -1; i-- {
		var seg string
		if i >= 0 {
			seg = segments[i]
		} else if resolvedDevice == "" {
			wd, err := os.Getwd()
			if err == nil {
				seg = wd
			}
		} else {
			if cwdDrive := os.Getenv("=" + resolvedDevice); cwdDrive != "" {
				seg = cwdDrive
			} else {
				seg = resolvedDevice + `\`
			}
		}
		if seg == "" {
			continue
		}
		dev, _, rest := pathsWin32SplitRoot(seg)
		isUncOrAbs := strings.HasSuffix(dev, `\`) || strings.HasPrefix(dev, `\`)
		if resolvedDevice != "" && dev != "" && !strings.EqualFold(dev, resolvedDevice) {
			continue
		}
		if resolvedDevice == "" && dev != "" {
			resolvedDevice = dev
		}
		if !resolvedAbs {
			resolvedTail = rest + `\` + resolvedTail
			resolvedAbs = isUncOrAbs
		}
		if resolvedDevice != "" && resolvedAbs {
			break
		}
	}

	rawParts := strings.FieldsFunc(resolvedTail, func(r rune) bool { return r == '/' || r == '\\' })
	norm := pathsNormalizeDots(rawParts, !resolvedAbs)
	tail := strings.Join(norm, `\`)
	if resolvedDevice == "" {
		if resolvedAbs {
			return `\` + tail
		}
		if tail == "" {
			return "."
		}
		return tail
	}
	if resolvedAbs && !strings.HasSuffix(resolvedDevice, `\`) {
		resolvedDevice += `\`
	}
	if tail == "" {
		return resolvedDevice
	}
	return resolvedDevice + tail
}

func pathsWin32Relative(from, to string) string {
	from = pathsWin32Resolve(from)
	to = pathsWin32Resolve(to)
	lowerFrom := strings.ToLower(from)
	lowerTo := strings.ToLower(to)
	if lowerFrom == lowerTo {
		return ""
	}

	fromTrimmed := strings.TrimSuffix(lowerFrom, `\`)
	toTrimmed := strings.TrimSuffix(lowerTo, `\`)
	fromOrigTrimmed := strings.TrimSuffix(from, `\`)
	toOrigTrimmed := strings.TrimSuffix(to, `\`)
	_ = fromOrigTrimmed

	fromParts := strings.Split(fromTrimmed, `\`)
	toParts := strings.Split(toTrimmed, `\`)
	toOrigParts := strings.Split(toOrigTrimmed, `\`)

	if len(fromParts) > 0 && len(toParts) > 0 && fromParts[0] != toParts[0] {
		return to
	}

	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	if common == 0 {
		return to
	}
	upCount := len(fromParts) - common
	out := make([]string, 0, upCount+(len(toParts)-common))
	for i := 0; i < upCount; i++ {
		out = append(out, "..")
	}
	out = append(out, toOrigParts[common:]...)
	return strings.Join(out, `\`)
}

func pathsWin32Join(elem ...string) string {
	var nonEmpty []string
	for _, e := range elem {
		if e != "" {
			nonEmpty = append(nonEmpty, e)
		}
	}
	if len(nonEmpty) == 0 {
		return "."
	}
	joined := strings.Join(nonEmpty, `\`)
	return pathsWin32Normalize(joined)
}

func pathsPosixResolve(segments ...string) string {
	var resolved string
	resolvedAbs := false
	for i := len(segments) - 1; i >= -1 && !resolvedAbs; i-- {
		var seg string
		if i >= 0 {
			seg = segments[i]
		} else {
			wd, err := os.Getwd()
			if err == nil {
				seg = wd
			}
		}
		if seg == "" {
			continue
		}
		resolved = seg + "/" + resolved
		resolvedAbs = strings.HasPrefix(seg, "/")
	}
	parts := strings.Split(resolved, "/")
	norm := pathsNormalizeDots(parts, !resolvedAbs)
	tail := strings.Join(norm, "/")
	if resolvedAbs {
		return "/" + tail
	}
	if tail == "" {
		return "."
	}
	return tail
}

func pathsPosixRelative(from, to string) string {
	from = pathsPosixResolve(from)
	to = pathsPosixResolve(to)
	if from == to {
		return ""
	}
	fromParts := strings.FieldsFunc(from, func(r rune) bool { return r == '/' })
	toParts := strings.FieldsFunc(to, func(r rune) bool { return r == '/' })

	common := 0
	for common < len(fromParts) && common < len(toParts) && fromParts[common] == toParts[common] {
		common++
	}
	upCount := len(fromParts) - common
	out := make([]string, 0, upCount+(len(toParts)-common))
	for i := 0; i < upCount; i++ {
		out = append(out, "..")
	}
	out = append(out, toParts[common:]...)
	return strings.Join(out, "/")
}

func pathsAPIResolve(segments ...string) string {
	if pathsIsWin32() {
		return pathsWin32Resolve(segments...)
	}
	return pathsPosixResolve(segments...)
}

func pathsAPIRelative(from, to string) string {
	if pathsIsWin32() {
		return pathsWin32Relative(from, to)
	}
	return pathsPosixRelative(from, to)
}

func pathsAPIJoin(elem ...string) string {
	if pathsIsWin32() {
		return pathsWin32Join(elem...)
	}
	return path.Join(elem...)
}

// GetPathComparisonKey returns a normalized comparison key for p:
// resolved absolute path, lowercased on Windows.
func GetPathComparisonKey(p string) string {
	resolved := pathsAPIResolve(p)
	if pathsIsWin32() {
		return strings.ToLower(resolved)
	}
	return resolved
}

// PathsEqual reports whether left and right name the same filesystem location.
func PathsEqual(left, right string) bool {
	return GetPathComparisonKey(left) == GetPathComparisonKey(right)
}

// IsPathWithin reports whether child is equal to or inside parent.
func IsPathWithin(parent, child string) bool {
	parentKey := GetPathComparisonKey(parent)
	childKey := GetPathComparisonKey(child)
	if childKey == parentKey {
		return true
	}
	sep := pathsSep()
	if strings.HasSuffix(parentKey, sep) {
		return strings.HasPrefix(childKey, parentKey)
	}
	return strings.HasPrefix(childKey, parentKey+sep)
}

// ShortenHomePath replaces a leading home directory with ~ for display.
// When homeDir is omitted, os.UserHomeDir() is used.
func ShortenHomePath(p string, homeDir ...string) string {
	var home string
	if len(homeDir) > 0 {
		home = homeDir[0]
	} else {
		h, _ := os.UserHomeDir()
		home = h
	}
	if home == "" || !pathsAPIisAbsolute(p) || !IsPathWithin(home, p) {
		return p
	}
	rel := pathsAPIRelative(home, p)
	if rel == "" {
		return "~"
	}
	return "~" + pathsSep() + rel
}

// GetFileRevision returns a revision string combining dev:ino:size:mtimeNs:ctimeNs or nil on error.
func GetFileRevision(p string) *string {
	return pathsGetFileRevision(p)
}

// IsLocalPath reports whether value represents a local path rather than a package source
// or remote URL protocol.
func IsLocalPath(value string) bool {
	trimmed := js.Trim(value)
	if strings.HasPrefix(trimmed, "npm:") ||
		strings.HasPrefix(trimmed, "git:") ||
		strings.HasPrefix(trimmed, "github:") ||
		strings.HasPrefix(trimmed, "http:") ||
		strings.HasPrefix(trimmed, "https:") ||
		strings.HasPrefix(trimmed, "ssh:") ||
		strings.HasPrefix(trimmed, "builtin:") {
		return false
	}
	return true
}

// NormalizeWindowsShellPath converts Git Bash, MSYS, Cygwin, and WSL drive paths
// to a Windows drive path form.
func NormalizeWindowsShellPath(filePath string) string {
	if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "//") || strings.Contains(filePath, `\`) {
		return filePath
	}
	m := pathsWindowsShellDriveRe.FindStringSubmatch(filePath)
	if m == nil {
		return filePath
	}
	suffix := strings.ReplaceAll(m[2], "/", `\`)
	return strings.ToUpper(m[1]) + `:\` + suffix
}

func pathsFileURLToPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid file URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "file") {
		return "", errors.New("The URL must be of scheme file")
	}
	rawPath := u.EscapedPath()
	lowerRaw := strings.ToLower(rawPath)
	if strings.Contains(lowerRaw, "%2f") || (pathsIsWin32() && strings.Contains(lowerRaw, "%5c")) {
		return "", errors.New("File URL path must not include encoded / characters")
	}
	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", fmt.Errorf("invalid file URL encoding: %w", err)
	}
	if !utf8.ValidString(decoded) {
		return "", errors.New("invalid UTF-8 in file URL path")
	}

	if pathsIsWin32() {
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return `\\` + u.Host + strings.ReplaceAll(decoded, "/", `\`), nil
		}
		if len(decoded) < 3 || decoded[0] != '/' || !pathsIsWinDriveLetter(decoded[1]) || decoded[2] != ':' {
			return "", errors.New("File URL path must be absolute")
		}
		return strings.ToUpper(string(decoded[1])) + strings.ReplaceAll(decoded[2:], "/", `\`), nil
	}

	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("File URL host must be \"localhost\" or empty on %s", pathsPlatform())
	}
	return decoded, nil
}

// NormalizePath normalizes user-supplied path text according to options.
func NormalizePath(input string, options *PathInputOptions) (string, error) {
	normalized := input
	if options != nil && options.Trim {
		normalized = js.Trim(normalized)
	}
	if options != nil && options.NormalizeUnicodeSpaces {
		normalized = pathsUnicodeSpacesRe.ReplaceAllString(normalized, " ")
	}
	if options != nil && options.StripAtPrefix && strings.HasPrefix(normalized, "@") {
		normalized = normalized[1:]
	}
	if pathsIsWin32() {
		normalized = NormalizeWindowsShellPath(normalized)
	}

	expandTilde := true
	if options != nil && options.ExpandTilde != nil {
		expandTilde = *options.ExpandTilde
	}
	if expandTilde {
		var home string
		if options != nil && options.HomeDir != nil {
			home = *options.HomeDir
		} else {
			home, _ = os.UserHomeDir()
		}
		if normalized == "~" {
			return home, nil
		}
		if strings.HasPrefix(normalized, "~/") || (pathsIsWin32() && strings.HasPrefix(normalized, `~\`)) {
			return pathsAPIJoin(home, normalized[2:]), nil
		}
	}

	if strings.HasPrefix(normalized, "file://") {
		return pathsFileURLToPath(normalized)
	}

	return normalized, nil
}

// ResolvePath normalizes input and resolves it against baseDir (or cwd when empty).
func ResolvePath(input string, baseDir string, options *PathInputOptions) (string, error) {
	if baseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		baseDir = wd
	}
	normalized, err := NormalizePath(input, options)
	if err != nil {
		return "", err
	}
	normalizedBase, err := NormalizePath(baseDir, nil)
	if err != nil {
		return "", err
	}
	if pathsAPIisAbsolute(normalized) {
		return pathsAPIResolve(normalized), nil
	}
	return pathsAPIResolve(normalizedBase, normalized), nil
}

// GetCwdRelativePath returns the path relative to cwd if filePath is inside cwd, or nil otherwise.
func GetCwdRelativePath(filePath string, cwd string) *string {
	resolvedCwd, err := ResolvePath(cwd, "", nil)
	if err != nil {
		return nil
	}
	resolvedPath, err := ResolvePath(filePath, resolvedCwd, nil)
	if err != nil {
		return nil
	}
	rel := pathsAPIRelative(resolvedCwd, resolvedPath)
	sep := pathsSep()
	isInside := rel == "" || (rel != ".." && !strings.HasPrefix(rel, ".."+sep) && !pathsAPIisAbsolute(rel))
	if !isInside {
		return nil
	}
	if rel == "" {
		dot := "."
		return &dot
	}
	return &rel
}

// FormatPathRelativeToCwdOrAbsolute resolves filePath relative to cwd, returning the
// slash-separated relative path if inside cwd or the slash-separated absolute path otherwise.
func FormatPathRelativeToCwdOrAbsolute(filePath string, cwd string) string {
	absPath, err := ResolvePath(filePath, cwd, nil)
	if err != nil {
		absPath = filePath
	}
	res := absPath
	if rel := GetCwdRelativePath(absPath, cwd); rel != nil {
		res = *rel
	}
	return strings.ReplaceAll(res, pathsSep(), "/")
}

// MarkPathIgnoredByCloudSync sets platform cloud-sync ignore xattrs on path (best-effort).
func MarkPathIgnoredByCloudSync(p string) {
	switch pathsPlatform() {
	case "darwin":
		for _, attr := range []string{"com.dropbox.ignored", "com.apple.fileprovider.ignore#P"} {
			_ = SpawnProcessSync("xattr", []string{"-w", attr, "1", p}, nil)
		}
	case "linux":
		_ = SpawnProcessSync("setfattr", []string{"-n", "user.com.dropbox.ignored", "-v", "1", p}, nil)
	}
}
