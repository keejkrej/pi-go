package xspawn

import (
	"os"
	"strings"
)

// Windows path functions below follow Node's path.win32 (the algorithms in
// lib/path.js). filepath.Clean is not a substitute: Node keeps a trailing
// separator ("foo/" is "foo\") and treats UNC and reserved device names.

var windowsReserved = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {},
	"COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {},
	"LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
	"COM\u00b9": {}, "COM\u00b2": {}, "COM\u00b3": {},
	"LPT\u00b9": {}, "LPT\u00b2": {}, "LPT\u00b3": {},
}

func isPathSep(c rune) bool { return c == '/' || c == '\\' }

func isDeviceRoot(c rune) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func indexRune(rs []rune, r rune) int {
	for i, c := range rs {
		if c == r {
			return i
		}
	}
	return -1
}

func indexRuneFrom(rs []rune, r rune, start int) int {
	if start < 0 {
		start = 0
	}
	for i := start; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

// isWindowsReservedName matches Node's helper. A negative colonIndex uses
// the same end-relative slice as String.prototype.slice.
func isWindowsReservedName(path string, colonIndex int) bool {
	rs := []rune(path)
	var device []rune
	switch {
	case colonIndex < 0:
		if len(rs) == 0 {
			return false
		}
		device = rs[:len(rs)-1]
	case colonIndex > len(rs):
		device = rs
	default:
		device = rs[:colonIndex]
	}
	_, ok := windowsReserved[strings.ToUpper(string(device))]
	return ok
}

// normalizeString resolves "." and ".." . separator is backslash.
func normalizeString(path []rune, allowAboveRoot bool) []rune {
	var res []rune
	lastSegmentLength := 0
	lastSlash := -1
	dots := 0
	var code rune
	const sep = '\\'
	for i := 0; i <= len(path); i++ {
		if i < len(path) {
			code = path[i]
		} else if isPathSep(code) {
			break
		} else {
			code = '/'
		}
		if isPathSep(code) {
			if lastSlash == i-1 || dots == 1 {
				// noop
			} else if dots == 2 {
				if len(res) < 2 || lastSegmentLength != 2 || res[len(res)-1] != '.' || res[len(res)-2] != '.' {
					if len(res) > 2 {
						lastSlashIndex := len(res) - lastSegmentLength - 1
						if lastSlashIndex == -1 {
							res = nil
							lastSegmentLength = 0
						} else {
							res = res[:lastSlashIndex]
							last := -1
							for j, c := range res {
								if c == sep {
									last = j
								}
							}
							lastSegmentLength = len(res) - 1 - last
						}
						lastSlash = i
						dots = 0
						continue
					} else if len(res) != 0 {
						res = nil
						lastSegmentLength = 0
						lastSlash = i
						dots = 0
						continue
					}
				}
				if allowAboveRoot {
					if len(res) > 0 {
						res = append(res, sep, '.', '.')
					} else {
						res = append(res, '.', '.')
					}
					lastSegmentLength = 2
				}
			} else {
				seg := path[lastSlash+1 : i]
				if len(res) > 0 {
					res = append(res, sep)
					res = append(res, seg...)
				} else {
					res = append([]rune(nil), seg...)
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

// winNormalize is path.win32.normalize.
func winNormalize(path string) string {
	rs := []rune(path)
	n := len(rs)
	if n == 0 {
		return "."
	}
	if n == 1 {
		if rs[0] == '/' {
			return `\`
		}
		return path
	}

	rootEnd := 0
	device := ""
	hasDevice := false
	isAbsolute := false
	code := rs[0]

	if isPathSep(code) {
		isAbsolute = true
		if isPathSep(rs[1]) {
			j := 2
			last := j
			for j < n && !isPathSep(rs[j]) {
				j++
			}
			if j < n && j != last {
				firstPart := string(rs[last:j])
				last = j
				for j < n && isPathSep(rs[j]) {
					j++
				}
				if j < n && j != last {
					last = j
					for j < n && !isPathSep(rs[j]) {
						j++
					}
					if j == n || j != last {
						if firstPart == "." || firstPart == "?" {
							device = `\\` + firstPart
							hasDevice = true
							rootEnd = 4
							colonIndex := indexRune(rs, ':')
							end := colonIndex + 1
							if colonIndex >= 0 && end > 4 {
								if end > n {
									end = n
								}
								if 4 < end {
									possible := string(rs[4:end])
									if isWindowsReservedName(possible, len([]rune(possible))-1) {
										device = `\\?\` + possible
										rootEnd = 4 + len([]rune(possible))
									}
								}
							}
						} else if j == n {
							return `\\` + firstPart + `\` + string(rs[last:]) + `\`
						} else {
							device = `\\` + firstPart + `\` + string(rs[last:j])
							hasDevice = true
							rootEnd = j
						}
					}
				}
			}
		} else {
			rootEnd = 1
		}
	} else {
		colonIndex := indexRune(rs, ':')
		if colonIndex > 0 {
			if isDeviceRoot(code) && colonIndex == 1 {
				device = string(rs[:2])
				hasDevice = true
				rootEnd = 2
				if n > 2 && isPathSep(rs[2]) {
					isAbsolute = true
					rootEnd = 3
				}
			} else if isWindowsReservedName(path, colonIndex) {
				device = string(rs[:colonIndex+1])
				hasDevice = true
				rootEnd = colonIndex + 1
			}
		}
	}

	var tail []rune
	if rootEnd < n {
		tail = normalizeString(rs[rootEnd:], !isAbsolute)
	}
	if len(tail) == 0 && !isAbsolute {
		tail = []rune{'.'}
	}
	if len(tail) > 0 && isPathSep(rs[n-1]) {
		tail = append(tail, '\\')
	}
	// CVE-2024-36139: a relative tail must not look like an absolute device path.
	if !isAbsolute && !hasDevice && strings.ContainsRune(path, ':') {
		if len(tail) >= 2 && isDeviceRoot(tail[0]) && tail[1] == ':' {
			return `.\` + string(tail)
		}
		index := indexRune(rs, ':')
		for index != -1 {
			if index == n-1 || (index+1 < n && isPathSep(rs[index+1])) {
				return `.\` + string(tail)
			}
			index = indexRuneFrom(rs, ':', index+1)
		}
	}
	colonIndex := indexRune(rs, ':')
	if isWindowsReservedName(path, colonIndex) {
		dev := ""
		if hasDevice {
			dev = device
		}
		return `.\` + dev + string(tail)
	}
	if !hasDevice {
		if isAbsolute {
			return `\` + string(tail)
		}
		return string(tail)
	}
	if isAbsolute {
		return device + `\` + string(tail)
	}
	return device + string(tail)
}

// winIsAbsolute is path.win32.isAbsolute.
func winIsAbsolute(path string) bool {
	rs := []rune(path)
	if len(rs) == 0 {
		return false
	}
	if isPathSep(rs[0]) {
		return true
	}
	return len(rs) > 2 && isDeviceRoot(rs[0]) && rs[1] == ':' && isPathSep(rs[2])
}

// winJoin is path.win32.join.
func winJoin(elem ...string) string {
	if len(elem) == 0 {
		return "."
	}
	parts := make([]string, 0, len(elem))
	for _, arg := range elem {
		if arg != "" {
			parts = append(parts, arg)
		}
	}
	if len(parts) == 0 {
		return "."
	}
	first := []rune(parts[0])
	joined := strings.Join(parts, `\`)
	needsReplace := true
	slashCount := 0
	if len(first) > 0 && isPathSep(first[0]) {
		slashCount++
		if len(first) > 1 && isPathSep(first[1]) {
			slashCount++
			if len(first) > 2 {
				if isPathSep(first[2]) {
					slashCount++
				} else {
					needsReplace = false
				}
			}
		}
	}
	rs := []rune(joined)
	if needsReplace {
		for slashCount < len(rs) && isPathSep(rs[slashCount]) {
			slashCount++
		}
		if slashCount >= 2 {
			joined = `\` + string(rs[slashCount:])
			rs = []rune(joined)
		}
	}

	var segs []string
	var cur []rune
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' {
			if len(cur) > 0 {
				segs = append(segs, string(cur))
				cur = cur[:0]
			}
			for i+1 < len(rs) && rs[i+1] == '\\' {
				i++
			}
		} else {
			cur = append(cur, rs[i])
		}
	}
	if len(cur) > 0 {
		segs = append(segs, string(cur))
	}
	for _, p := range segs {
		ci := indexRune([]rune(p), ':')
		if ci != -1 && isWindowsReservedName(p, ci) {
			return strings.ReplaceAll(joined, "/", `\`)
		}
	}
	return winNormalize(joined)
}

// resolveRoot is the root scan inside path.win32.resolve (not normalize).
func resolveRoot(rs []rune) (device string, has bool, rootEnd int, isAbs bool) {
	n := len(rs)
	if n == 0 {
		return "", false, 0, false
	}
	code := rs[0]
	if n == 1 {
		if isPathSep(code) {
			return "", false, 1, true
		}
		return "", false, 0, false
	}
	if isPathSep(code) {
		isAbs = true
		if isPathSep(rs[1]) {
			j := 2
			last := j
			for j < n && !isPathSep(rs[j]) {
				j++
			}
			if j < n && j != last {
				firstPart := string(rs[last:j])
				last = j
				for j < n && isPathSep(rs[j]) {
					j++
				}
				if j < n && j != last {
					last = j
					for j < n && !isPathSep(rs[j]) {
						j++
					}
					if j == n || j != last {
						if firstPart != "." && firstPart != "?" {
							device = `\\` + firstPart + `\` + string(rs[last:j])
							has = true
							rootEnd = j
						} else {
							device = `\\` + firstPart
							has = true
							rootEnd = 4
						}
					}
				}
			}
		} else {
			rootEnd = 1
		}
		return device, has, rootEnd, isAbs
	}
	if isDeviceRoot(code) && rs[1] == ':' {
		device = string(rs[:2])
		has = true
		rootEnd = 2
		if n > 2 && isPathSep(rs[2]) {
			isAbs = true
			rootEnd = 3
		}
	}
	return device, has, rootEnd, isAbs
}

// winResolve is path.win32.resolve with cwd standing in for process.cwd().
// Drive-specific working directories (the "=C:" environment variables) are
// read from the process environment, as Node does.
func winResolve(cwd string, elem ...string) string {
	resolvedDevice := ""
	resolvedTail := ""
	resolvedAbsolute := false

	for i := len(elem) - 1; i >= -1; i-- {
		var path string
		if i >= 0 {
			path = elem[i]
			if path == "" {
				continue
			}
		} else if resolvedDevice == "" {
			path = cwd
			prs := []rune(path)
			sep0 := len(prs) > 0 && isPathSep(prs[0])
			if len(elem) == 0 || (len(elem) == 1 && (elem[0] == "" || elem[0] == ".") && sep0) {
				return path
			}
		} else {
			path = os.Getenv("=" + resolvedDevice)
			if path == "" {
				path = cwd
			}
			prs := []rune(path)
			bad := len(prs) >= 2 && !strings.EqualFold(string(prs[:2]), resolvedDevice) && len(prs) > 2 && prs[2] == '\\'
			if path == "" || bad {
				path = resolvedDevice + `\`
			}
		}

		rs := []rune(path)
		device, has, rootEnd, isAbs := resolveRoot(rs)
		if has {
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
			resolvedTail = string(rs[rootEnd:]) + `\` + resolvedTail
			resolvedAbsolute = isAbs
			if isAbs && resolvedDevice != "" {
				break
			}
		}
	}

	tail := string(normalizeString([]rune(resolvedTail), !resolvedAbsolute))
	if resolvedAbsolute {
		return resolvedDevice + `\` + tail
	}
	out := resolvedDevice + tail
	if out == "" {
		return "."
	}
	return out
}
