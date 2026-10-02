// Package ignore is a port of the default-options surface of npm ignore 7.0.8.
//
// Patterns are translated with node-ignore's gitignore regex chain and matched
// with a JavaScript regular expression (ignorecase on). Paths must be relative;
// an empty path or an absolute / ./ / ../ path panics with node's message.
// On Windows, backslashes become slashes and a drive prefix is absolute, matching
// process.platform === "win32".
package ignore

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/keejkrej/pi-go/internal/minimatch/jsregex"
)

// Rule is the pattern that produced a positive match.
// Negative matches leave TestResult.Rule nil, as node-ignore does.
type Rule struct {
	Pattern  string
	Mark     string
	Negative bool
}

// TestResult is the outcome of Test.
type TestResult struct {
	Ignored   bool
	Unignored bool
	Rule      *Rule
}

type rule struct {
	pattern  string
	mark     string
	negative bool
	src      string
	re       *jsregex.Regexp
}

// Ignore is a set of gitignore patterns.
type Ignore struct {
	rules   []*rule
	windows bool
}

// New returns an ignore set with npm's defaults (ignorecase on).
func New() *Ignore {
	return &Ignore{windows: runtime.GOOS == "windows"}
}

// Add splits each argument on newlines and appends the patterns.
// Blank lines, comments, and a pattern that ends in a single unescaped
// backslash are skipped. Both Add("*.o\n*.a") and Add("*.o", "*.a") work.
func (g *Ignore) Add(patterns ...string) *Ignore {
	for _, p := range patterns {
		for _, line := range splitLines(p) {
			if !checkPattern(line) {
				continue
			}
			g.rules = append(g.rules, newRule(line))
		}
	}
	return g
}

func newRule(pattern string) *rule {
	negative := false
	body := pattern
	if strings.HasPrefix(body, "!") {
		negative = true
		body = body[1:]
	}
	if strings.HasPrefix(body, `\!`) {
		body = "!" + body[2:]
	}
	if strings.HasPrefix(body, `\#`) {
		body = "#" + body[2:]
	}
	src, re := compileBody(body, true)
	return &rule{pattern: pattern, negative: negative, src: src, re: re}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		line := s[start:i]
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		if line != "" {
			out = append(out, line)
		}
		start = i + 1
	}
	if start <= len(s) {
		line := s[start:]
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func checkPattern(pattern string) bool {
	if pattern == "" || strings.HasPrefix(pattern, "#") {
		return false
	}
	for _, r := range pattern {
		if r != ' ' {
			return !invalidTrailingBS(pattern)
		}
	}
	return false
}

func invalidTrailingBS(s string) bool {
	rs := []rune(s)
	if len(rs) == 0 || rs[len(rs)-1] != '\\' {
		return false
	}
	return len(rs) == 1 || rs[len(rs)-2] != '\\'
}

// Ignores reports whether path is ignored. It panics on an invalid path.
func (g *Ignore) Ignores(path string) bool {
	return g.eval(path, false).Ignored
}

// Test reports the match, including a negative-pattern hit that ignores() skips
// until something has been ignored.
func (g *Ignore) Test(path string) TestResult {
	return g.eval(path, true)
}

// Filter returns the paths that are not ignored, in order.
func (g *Ignore) Filter(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !g.Ignores(p) {
			out = append(out, p)
		}
	}
	return out
}

func (g *Ignore) eval(original string, checkUnignored bool) TestResult {
	path := original
	if original != "" {
		path = g.convert(original)
	}
	g.check(path, original)
	return g.walk(path, checkUnignored)
}

func (g *Ignore) check(path, original string) {
	if path == "" {
		panic("path must not be empty")
	}
	if g.notRelative(path) {
		panic(fmt.Sprintf("path should be a `path.relative()`d string, but got \"%s\"", original))
	}
}

func (g *Ignore) convert(p string) string {
	if !g.windows {
		return p
	}
	if strings.HasPrefix(p, `\\?\`) {
		return p
	}
	for _, r := range p {
		if r == '"' || r == '<' || r == '>' || r == '|' || r < 0x20 {
			return p
		}
	}
	return strings.ReplaceAll(p, `\`, "/")
}

func (g *Ignore) notRelative(path string) bool {
	if g.windows && isWinDrive(path) {
		return true
	}
	if path == "" {
		return true
	}
	if path[0] == '/' {
		return true
	}
	if path[0] != '.' {
		return false
	}
	if len(path) == 1 {
		return true
	}
	if path[1] == '/' {
		return true
	}
	if path[1] != '.' {
		return false
	}
	return len(path) == 2 || path[2] == '/'
}

func isWinDrive(path string) bool {
	if len(path) < 3 {
		return false
	}
	c := path[0]
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	return c >= 'a' && c <= 'z' && path[1] == ':' && path[2] == '/'
}

func parentOf(path string) string {
	if path == "" {
		return ""
	}
	if path[0] == '/' || strings.Contains(path, "//") {
		parts := strings.Split(path, "/")
		var slices []string
		for _, p := range parts {
			if p != "" {
				slices = append(slices, p)
			}
		}
		if len(slices) == 0 {
			return ""
		}
		slices = slices[:len(slices)-1]
		if len(slices) == 0 {
			return ""
		}
		return strings.Join(slices, "/") + "/"
	}
	end := len(path) - 1
	from := end
	if path[end] == '/' {
		from = end - 1
	}
	if from < 0 {
		return ""
	}
	cut := strings.LastIndex(path[:from+1], "/")
	if cut < 0 {
		return ""
	}
	return path[:cut+1]
}

func (g *Ignore) walk(path string, checkUnignored bool) TestResult {
	if parent := parentOf(path); parent != "" {
		pr := g.walk(parent, checkUnignored)
		if pr.Ignored {
			return pr
		}
	}
	return g.matchRules(path, checkUnignored)
}

func (g *Ignore) matchRules(path string, checkUnignored bool) TestResult {
	var ignored, unignored bool
	var matched *rule
	for _, rl := range g.rules {
		neg := rl.negative
		skip := (unignored == neg && ignored != unignored) || (neg && !ignored && !unignored && !checkUnignored)
		if skip || !rl.re.MatchString(path) {
			continue
		}
		ignored = !neg
		unignored = neg
		if neg {
			matched = nil
		} else {
			matched = rl
		}
	}
	res := TestResult{Ignored: ignored, Unignored: unignored}
	if matched != nil {
		res.Rule = &Rule{Pattern: matched.pattern, Mark: matched.mark, Negative: matched.negative}
	}
	return res
}
