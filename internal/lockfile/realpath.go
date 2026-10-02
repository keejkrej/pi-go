// Ported from Node.js v24 lib/fs.js realpath/realpathSync and lib/path.js resolve, as used by proper-lockfile@4.1.2
// resolveCanonicalPath (graceful-fs passes fs.realpath through unchanged).

package lockfile

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// lfSplitRootWindowsRe is Node's splitRootRe: the device root on Windows (e.g. 'c:\'), including trailing slash.
var lfSplitRootWindowsRe = regexp.MustCompile(`^(?:[a-zA-Z]:|[\\/]{2}[^\\/]+[\\/][^\\/]+)?[\\/]*`)

// lfPathResolve is path.resolve(p): an absolute, normalized path. On Windows this is path.win32.resolve,
// which is purely lexical. filepath.Abs calls GetFullPathName and would strip trailing dots and spaces
// and expand 8.3 names; Node does neither, so a lock path would not match proper-lockfile.
func lfPathResolve(p string) (string, error) {
	if runtime.GOOS == "windows" {
		return lfWinResolve(p)
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Abs(p)
}

// lfPathResolveFrom is path.resolve(from, p).
func lfPathResolveFrom(from, p string) (string, error) {
	if runtime.GOOS == "windows" {
		return lfWinResolve(from, p)
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Clean(filepath.Join(from, p)), nil
}

func lfSplitRoot(s string) string {
	if runtime.GOOS == "windows" {
		return lfSplitRootWindowsRe.FindString(s)
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '/' {
			return s[:i]
		}
	}
	return s
}

func lfNextPart(p string, i int) int {
	var idx int
	if runtime.GOOS == "windows" {
		idx = strings.IndexAny(p[i:], `\/`)
	} else {
		idx = strings.IndexByte(p[i:], '/')
	}
	if idx < 0 {
		return -1
	}
	return i + idx
}

// lfRealpath is fs.realpath (the JS implementation, not realpath.native): it resolves symlinks component by
// component with lstat, stats a link before reading it, and keeps the case of components that are not links.
// Errors are Node fs errors (syscall lstat, stat or readlink).
func lfRealpath(file string) (string, error) {
	p, err := lfPathResolve(file)
	if err != nil {
		return "", toNodeError(err, "realpath", file)
	}

	seenLinks := map[string]string{}
	knownHard := map[string]bool{}

	// Skip over roots
	current := lfSplitRoot(p)
	base := current
	pos := len(current)

	// On windows, check that the root exists. On unix there is no need.
	checkRoot := func() error {
		if runtime.GOOS == "windows" && !knownHard[base] {
			if _, err := os.Lstat(base); err != nil {
				return toNodeError(err, "lstat", base)
			}
			knownHard[base] = true
		}
		return nil
	}
	if err := checkRoot(); err != nil {
		return "", err
	}

	// Walk down the path, swapping out linked path parts for their real values
	for pos < len(p) {
		// find the next part
		result := lfNextPart(p, pos)
		previous := current
		if result == -1 {
			last := p[pos:]
			current += last
			base = previous + last
			pos = len(p)
		} else {
			current += p[pos : result+1]
			base = previous + p[pos:result]
			pos = result + 1
		}

		// Continue if not a symlink
		if knownHard[base] {
			continue
		}

		stat, err := os.Lstat(base)
		if err != nil {
			return "", toNodeError(err, "lstat", base)
		}

		// If not a symlink, skip to the next path part
		if !lfIsSymlink(base, stat) {
			knownHard[base] = true
			continue
		}

		// Stat & read the link if not read before.
		// dev/ino always return 0 on windows, so skip the check.
		id := lfFileID(stat)
		target, seen := "", false
		if id != "" {
			target, seen = seenLinks[id]
		}
		if !seen {
			if _, err := os.Stat(base); err != nil {
				return "", toNodeError(err, "stat", base)
			}
			target, err = os.Readlink(base)
			if err != nil {
				return "", toNodeError(err, "readlink", base)
			}
			if id != "" {
				seenLinks[id] = target
			}
		}
		resolvedLink, err := lfPathResolveFrom(previous, target)
		if err != nil {
			return "", toNodeError(err, "readlink", base)
		}

		// Resolve the link, then start over
		p, err = lfPathResolveFrom(resolvedLink, p[pos:])
		if err != nil {
			return "", toNodeError(err, "readlink", base)
		}
		current = lfSplitRoot(p)
		base = current
		pos = len(current)
		if err := checkRoot(); err != nil {
			return "", err
		}
	}

	return p, nil
}

// lfWinResolve is Node's path.win32.resolve. Paths are indexed by byte, which matches Node's UTF-16
// indices for the ASCII separators the algorithm splits on, and keeps non-ASCII segments intact.
func lfWinResolve(args ...string) (string, error) {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false

	for i := len(args) - 1; i >= -1; i-- {
		var path string
		if i >= 0 {
			path = args[i]
			if path == "" {
				continue
			}
		} else if resolvedDevice == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			path = cwd
			// Fast path for the current directory when cwd is a UNC path (starts with a separator).
			if (len(args) == 0 || (len(args) == 1 && (args[0] == "" || args[0] == "."))) &&
				len(path) > 0 && lfWinSep(path[0]) {
				return path, nil
			}
		} else {
			// Drive-specific cwd lives in the "=C:" environment variable cmd.exe sets. An empty or
			// foreign value falls through to the process cwd, then to the drive root.
			path = os.Getenv("=" + resolvedDevice)
			if path == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return "", err
				}
				path = cwd
			}
			if len(path) > 2 && !strings.EqualFold(path[:2], resolvedDevice) && path[2] == '\\' {
				path = resolvedDevice + `\`
			}
		}
		if path == "" {
			continue
		}

		n := len(path)
		rootEnd := 0
		device := ""
		isAbsolute := false
		code := path[0]

		if n == 1 {
			if lfWinSep(code) {
				rootEnd = 1
				isAbsolute = true
			}
		} else if lfWinSep(code) {
			isAbsolute = true
			if lfWinSep(path[1]) {
				j := 2
				last := j
				for j < n && !lfWinSep(path[j]) {
					j++
				}
				if j < n && j != last {
					firstPart := path[last:j]
					last = j
					for j < n && lfWinSep(path[j]) {
						j++
					}
					if j < n && j != last {
						last = j
						for j < n && !lfWinSep(path[j]) {
							j++
						}
						if j == n || j != last {
							if firstPart != "." && firstPart != "?" {
								device = `\\` + firstPart + `\` + path[last:j]
								rootEnd = j
							} else {
								device = `\\` + firstPart
								rootEnd = 4
							}
						}
					}
				}
			} else {
				rootEnd = 1
			}
		} else if lfWinDevice(code) && n > 1 && path[1] == ':' {
			device = path[:2]
			rootEnd = 2
			if n > 2 && lfWinSep(path[2]) {
				isAbsolute = true
				rootEnd = 3
			}
		}

		if device != "" {
			if resolvedDevice != "" {
				if !strings.EqualFold(device, resolvedDevice) {
					continue
				}
			} else {
				resolvedDevice = device
			}
		}

		if resolvedAbsolute {
			if resolvedDevice != "" {
				break
			}
		} else {
			resolvedTail = path[rootEnd:] + `\` + resolvedTail
			resolvedAbsolute = isAbsolute
			if isAbsolute && resolvedDevice != "" {
				break
			}
		}
	}

	resolvedTail = lfWinNormalize(resolvedTail, !resolvedAbsolute)
	if resolvedAbsolute {
		return resolvedDevice + `\` + resolvedTail, nil
	}
	if resolvedDevice+resolvedTail == "" {
		return ".", nil
	}
	return resolvedDevice + resolvedTail, nil
}

func lfWinSep(c byte) bool { return c == '/' || c == '\\' }

func lfWinDevice(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// lfWinNormalize is Node's normalizeString with separator '\\' and both slashes as separators.
func lfWinNormalize(path string, allowAboveRoot bool) string {
	res := ""
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code byte
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if lfWinSep(code) {
			break
		} else {
			code = '/'
		}
		if lfWinSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// noop
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = ""
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							lastSegmentLength = len(res) - 1 - strings.LastIndexByte(res, '\\')
						}
						lastSlash = i
						dots = 0
						continue
					} else if res != "" {
						res = ""
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if res != "" {
						res += `\..`
					} else {
						res = ".."
					}
					lastSegmentLength = 2
				}
			} else {
				seg := path[lastSlash+1 : i]
				if res != "" {
					res += `\` + seg
				} else {
					res = seg
				}
				lastSegmentLength = i - lastSlash - 1
			}
			lastSlash = i
			dots = 0
		} else if code == '.' && dots != -1 {
			dots++
		} else {
			dots = -1
		}
	}
	return res
}
