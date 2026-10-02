package minimatch

import (
	"runtime"
	"strings"

	"github.com/keejkrej/pi-go/internal/minimatch/jsregex"
)

// Options are the minimatch flags pi passes. The zero value matches npm
// defaults: braces and extglobs on, dotfiles not matched by *, case
// sensitive, matchBase off. Platform "" follows GOOS (windows → win32).
//
// Several npm options are fixed at their defaults and are not fields:
// comments and negation are on, globstar is on, optimizationLevel is 1,
// partial and flipNegate are off, preserveMultipleSlashes is off,
// windowsPathsNoEscape is off, magicalBraces is on, nocaseMagicOnly is off,
// braceExpandMax is 100000, max globstar recursion is 200, and max extglob
// recursion is 2. A pattern longer than 65536 UTF-16 code units makes Match
// return false; npm throws TypeError.
type Options struct {
	NoCase    bool
	Dot       bool
	MatchBase bool
	NoBrace   bool
	NoExt     bool
	Platform  string
}

const (
	kindLit = iota
	kindRE
	kindStar
)

type piece struct {
	kind  int
	s     string
	src   string
	flags string
	re    *jsregex.Regexp
	bad   bool
}

type matcher struct {
	opts               Options
	isWindows          bool
	windowsNoMagicRoot bool
	negate             bool
	comment            bool
	empty              bool
	set                [][]piece
	maxGS              int
}

// Match reports whether path matches pattern using npm minimatch 10 semantics.
func Match(path, pattern string, opts Options) bool {
	if jsLen(pattern) > 1024*64 {
		return false
	}
	return compile(pattern, opts).match(path)
}

func (o Options) platform() string {
	if o.Platform != "" {
		return o.Platform
	}
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return "posix"
}

func compile(pattern string, opts Options) *matcher {
	m := &matcher{
		opts:  opts,
		maxGS: 200,
	}
	m.isWindows = opts.platform() == "win32"
	m.windowsNoMagicRoot = m.isWindows && opts.NoCase
	if pattern != "" && pattern[0] == '#' {
		m.comment = true
		return m
	}
	if pattern == "" {
		m.empty = true
		return m
	}
	pattern, m.negate = parseNegate(pattern)
	globs := braceExpand(pattern, opts.NoBrace)
	raw := make([][]string, len(globs))
	for i, g := range globs {
		raw[i] = m.slashSplit(g)
	}
	parts := levelOneOptimize(raw)
	set := make([][]piece, 0, len(parts))
	for _, segs := range parts {
		row := m.parseParts(segs)
		if hasBad(row) {
			continue
		}
		if m.isWindows && len(row) > 3 &&
			row[0].kind == kindLit && row[0].s == "" &&
			row[1].kind == kindLit && row[1].s == "" &&
			len(segs) > 2 && segs[2] == "?" &&
			row[3].kind == kindLit && isDrive(row[3].s) {
			row[2] = piece{kind: kindLit, s: "?"}
		}
		set = append(set, row)
	}
	m.set = set
	return m
}

func parseNegate(pattern string) (string, bool) {
	negate := false
	i := 0
	for i < len(pattern) && pattern[i] == '!' {
		negate = !negate
		i++
	}
	if i > 0 {
		return pattern[i:], negate
	}
	return pattern, false
}

func (m *matcher) slashSplit(p string) []string {
	if m.isWindows && len(p) >= 3 && p[0] == '/' && p[1] == '/' && p[2] != '/' {
		return append([]string{""}, splitSlashes(p)...)
	}
	return splitSlashes(p)
}

func splitSlashes(p string) []string {
	out := make([]string, 0, 4)
	start := 0
	i := 0
	for i < len(p) {
		if p[i] != '/' {
			i++
			continue
		}
		out = append(out, p[start:i])
		for i < len(p) && p[i] == '/' {
			i++
		}
		start = i
	}
	out = append(out, p[start:])
	return out
}

func levelOneOptimize(globParts [][]string) [][]string {
	out := make([][]string, len(globParts))
	for i, parts := range globParts {
		set := make([]string, 0, len(parts))
		for _, part := range parts {
			var prev string
			has := len(set) > 0
			if has {
				prev = set[len(set)-1]
			}
			if part == "**" && has && prev == "**" {
				continue
			}
			if part == ".." && has && prev != "" && prev != ".." && prev != "." && prev != "**" {
				set = set[:len(set)-1]
				continue
			}
			set = append(set, part)
		}
		if len(set) == 0 {
			set = []string{""}
		}
		out[i] = set
	}
	return out
}

func (m *matcher) parseParts(segs []string) []piece {
	if m.isWindows && m.windowsNoMagicRoot {
		isUNC := len(segs) >= 2 && segs[0] == "" && segs[1] == "" &&
			(at(segs, 2) == "?" || !hasGlobMagic(magicAt(segs, 2))) &&
			!hasGlobMagic(magicAt(segs, 3))
		if isUNC {
			n := 4
			if n > len(segs) {
				n = len(segs)
			}
			out := make([]piece, 0, len(segs))
			for _, s := range segs[:n] {
				out = append(out, piece{kind: kindLit, s: s})
			}
			for _, s := range segs[n:] {
				p, ok := m.parseSegment(s)
				if !ok {
					return []piece{{bad: true}}
				}
				out = append(out, p)
			}
			return out
		}
		if len(segs) > 0 && isDrivePrefix(segs[0]) {
			out := []piece{{kind: kindLit, s: segs[0]}}
			for _, s := range segs[1:] {
				p, ok := m.parseSegment(s)
				if !ok {
					return []piece{{bad: true}}
				}
				out = append(out, p)
			}
			return out
		}
	}
	out := make([]piece, 0, len(segs))
	for _, s := range segs {
		p, ok := m.parseSegment(s)
		if !ok {
			return []piece{{bad: true}}
		}
		out = append(out, p)
	}
	return out
}

func at(segs []string, i int) string {
	if i < 0 || i >= len(segs) {
		return ""
	}
	return segs[i]
}

func magicAt(segs []string, i int) string {
	if i < 0 || i >= len(segs) {
		return "undefined"
	}
	return segs[i]
}

func hasGlobMagic(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '?', '*', '[', ']':
			return true
		case '+', '@', '!':
			if i+1 < len(s) && s[i+1] == '(' && strings.Contains(s[i+2:], ")") {
				return true
			}
		}
	}
	return false
}

func (m *matcher) parseSegment(s string) (piece, bool) {
	if jsLen(s) > 1024*64 {
		return piece{}, false
	}
	if s == "**" {
		return piece{kind: kindStar}, true
	}
	if s == "" {
		return piece{kind: kindLit, s: ""}, true
	}
	a := fromGlob(s, globOpts{dot: m.opts.Dot, nocase: m.opts.NoCase, noext: m.opts.NoExt})
	p := a.toMMPattern()
	if p.bad {
		return piece{}, false
	}
	return p, true
}

func hasBad(ps []piece) bool {
	for _, p := range ps {
		if p.bad {
			return true
		}
	}
	return false
}

func isDrive(s string) bool {
	if len(s) != 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDrivePrefix(s string) bool {
	if len(s) < 2 || s[1] != ':' {
		return false
	}
	c := s[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func (m *matcher) match(f string) bool {
	if m.comment {
		return false
	}
	if m.empty {
		return f == ""
	}
	if m.isWindows {
		f = strings.ReplaceAll(f, `\`, "/")
	}
	ff := m.slashSplit(f)
	filename := ""
	if len(ff) > 0 {
		filename = ff[len(ff)-1]
	}
	if filename == "" {
		for i := len(ff) - 2; filename == "" && i >= 0; i-- {
			filename = ff[i]
		}
	}
	for _, pattern := range m.set {
		file := ff
		if m.opts.MatchBase && len(pattern) == 1 {
			file = []string{filename}
		}
		if m.matchOne(file, pattern) {
			return !m.negate
		}
	}
	return m.negate
}

func (m *matcher) matchOne(file []string, pattern []piece) bool {
	pat := append([]piece(nil), pattern...)
	fileStart, patStart := 0, 0
	if m.isWindows {
		fileStart, patStart = alignDrives(file, pat)
	}
	if indexKind(pat, kindStar, 0) >= 0 {
		return m.matchGlobstar(file, pat, fileStart, patStart)
	}
	return m.matchParts(file, pat, fileStart, patStart)
}

func alignDrives(file []string, pat []piece) (int, int) {
	fileDrive := len(file) > 0 && isDrive(file[0])
	fileUNC := !fileDrive && len(file) > 3 && file[0] == "" && file[1] == "" && file[2] == "?" && isDrive(file[3])
	patDrive := len(pat) > 0 && pat[0].kind == kindLit && isDrive(pat[0].s)
	patUNC := !patDrive && len(pat) > 3 &&
		pat[0].kind == kindLit && pat[0].s == "" &&
		pat[1].kind == kindLit && pat[1].s == "" &&
		pat[2].kind == kindLit && pat[2].s == "?" &&
		pat[3].kind == kindLit && isDrive(pat[3].s)
	fdi, pdi := -1, -1
	if fileUNC {
		fdi = 3
	} else if fileDrive {
		fdi = 0
	}
	if patUNC {
		pdi = 3
	} else if patDrive {
		pdi = 0
	}
	if fdi >= 0 && pdi >= 0 && strings.EqualFold(file[fdi], pat[pdi].s) {
		pat[pdi].s = file[fdi]
		return fdi, pdi
	}
	return 0, 0
}

func indexKind(p []piece, kind, from int) int {
	if from < 0 {
		from = 0
	}
	for i := from; i < len(p); i++ {
		if p[i].kind == kind {
			return i
		}
	}
	return -1
}

func lastKind(p []piece, kind int) int {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i].kind == kind {
			return i
		}
	}
	return -1
}

func (m *matcher) matchParts(file []string, pat []piece, fi, pi int) bool {
	fl, pl := len(file), len(pat)
	for fi < fl && pi < pl {
		p := pat[pi]
		f := file[fi]
		if p.kind == kindStar {
			return false
		}
		hit := false
		switch p.kind {
		case kindLit:
			hit = f == p.s
		case kindRE:
			if p.re != nil {
				hit = p.re.MatchString(f)
			}
		}
		if !hit {
			return false
		}
		fi++
		pi++
	}
	if fi == fl && pi == pl {
		return true
	}
	if fi == fl {
		return false
	}
	if pi == pl {
		return fi == fl-1 && file[fi] == ""
	}
	return false
}

func (m *matcher) matchGlobstar(file []string, pat []piece, fileIndex, patternIndex int) bool {
	firstgs := indexKind(pat, kindStar, patternIndex)
	lastgs := lastKind(pat, kindStar)
	head := slicePieces(pat, patternIndex, firstgs)
	body := slicePieces(pat, firstgs+1, lastgs)
	tail := slicePieces(pat, lastgs+1, len(pat))
	if len(head) > 0 {
		end := fileIndex + len(head)
		if end > len(file) {
			end = len(file)
		}
		if !m.matchParts(file[fileIndex:end], head, 0, 0) {
			return false
		}
		fileIndex += len(head)
		patternIndex += len(head)
	}
	fileTailMatch := 0
	if len(tail) > 0 {
		if len(tail)+fileIndex > len(file) {
			return false
		}
		tailStart := len(file) - len(tail)
		if m.matchParts(file, tail, tailStart, 0) {
			fileTailMatch = len(tail)
		} else {
			if file[len(file)-1] != "" || fileIndex+len(tail) == len(file) {
				return false
			}
			tailStart--
			if tailStart < 0 || !m.matchParts(file, tail, tailStart, 0) {
				return false
			}
			fileTailMatch = len(tail) + 1
		}
	}
	if len(body) == 0 {
		sawSome := fileTailMatch > 0
		for i := fileIndex; i < len(file)-fileTailMatch; i++ {
			sawSome = true
			if badDot(file[i], m.opts.Dot) {
				return false
			}
		}
		return sawSome
	}
	segs := []bodySeg{{}}
	nonGs := 0
	sums := []int{0}
	for _, b := range body {
		if b.kind == kindStar {
			sums = append(sums, nonGs)
			segs = append(segs, bodySeg{})
		} else {
			segs[len(segs)-1].parts = append(segs[len(segs)-1].parts, b)
			nonGs++
		}
	}
	fileLength := len(file) - fileTailMatch
	idx := len(segs) - 1
	for i := range segs {
		segs[i].after = fileLength - (sums[idx] + len(segs[i].parts))
		idx--
	}
	sub := m.matchGSBody(file, segs, fileIndex, 0, 0, fileTailMatch > 0)
	return sub == 1
}

type bodySeg struct {
	parts []piece
	after int
}

func (m *matcher) matchGSBody(file []string, segs []bodySeg, fileIndex, bodyIndex, depth int, sawTail bool) int {
	if bodyIndex >= len(segs) {
		for i := fileIndex; i < len(file); i++ {
			sawTail = true
			if badDot(file[i], m.opts.Dot) {
				return 0
			}
		}
		if sawTail {
			return 1
		}
		return 0
	}
	body := segs[bodyIndex].parts
	after := segs[bodyIndex].after
	for fileIndex <= after {
		end := fileIndex + len(body)
		if end > len(file) {
			end = len(file)
		}
		hit := false
		if fileIndex <= end {
			hit = m.matchParts(file[:end], body, fileIndex, 0)
		}
		if hit && depth < m.maxGS {
			sub := m.matchGSBody(file, segs, fileIndex+len(body), bodyIndex+1, depth+1, sawTail)
			if sub != 0 {
				return sub
			}
		}
		if fileIndex >= len(file) {
			return 0
		}
		if badDot(file[fileIndex], m.opts.Dot) {
			return 0
		}
		fileIndex++
	}
	return -1
}

func badDot(f string, dot bool) bool {
	if f == "." || f == ".." {
		return true
	}
	return !dot && strings.HasPrefix(f, ".")
}

func slicePieces(p []piece, start, end int) []piece {
	n := len(p)
	if start < 0 {
		start += n
	}
	if end < 0 {
		end += n
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > n {
		start = n
	}
	if end > n {
		end = n
	}
	if end < start {
		return nil
	}
	out := make([]piece, end-start)
	copy(out, p[start:end])
	return out
}

// partsDebug exposes the compiled set for tests. Each cell is "**",
// "s:"+literal, or "r:"+flags+":"+source.
func partsDebug(pattern string, opts Options) (comment, empty, negate bool, rows [][]string) {
	if jsLen(pattern) > 1024*64 {
		return false, false, false, nil
	}
	m := compile(pattern, opts)
	rows = make([][]string, len(m.set))
	for i, row := range m.set {
		cells := make([]string, len(row))
		for j, p := range row {
			switch p.kind {
			case kindStar:
				cells[j] = "**"
			case kindLit:
				cells[j] = "s:" + p.s
			default:
				cells[j] = "r:" + p.flags + ":" + p.src
			}
		}
		rows[i] = cells
	}
	return m.comment, m.empty, m.negate, rows
}
