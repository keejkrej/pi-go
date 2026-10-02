package marked

import (
	"sync"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// jsRE is a JavaScript RegExp executed by regexp2 in ECMAScript mode.
// Indexes on caps are rune indexes, which match UTF-16 for BMP text.
type jsRE struct {
	re     *regexp2.Regexp
	global bool
	noop   bool
}

type caps struct {
	full   string
	index  int
	groups []string
	has    []bool
}

func (c *caps) group(i int) string {
	if c == nil || i < 0 || i >= len(c.groups) || !c.has[i] {
		return ""
	}
	return c.groups[i]
}

func (c *caps) ok(i int) bool {
	return c != nil && i >= 0 && i < len(c.has) && c.has[i]
}

func compileRE(source, flags string) (*jsRE, error) {
	if source == "" {
		return &jsRE{noop: true}, nil
	}
	opt := regexp2.ECMAScript
	global := false
	for _, f := range flags {
		switch f {
		case 'i':
			opt |= regexp2.IgnoreCase
		case 'm':
			opt |= regexp2.Multiline
		case 'u':
			opt |= regexp2.Unicode
		case 's':
			opt |= regexp2.Singleline
		case 'g':
			global = true
		}
	}
	re, err := regexp2.Compile(source, opt)
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = 10 * time.Second
	return &jsRE{re: re, global: global}, nil
}

func mustCompile(source, flags string) *jsRE {
	re, err := compileRE(source, flags)
	if err != nil {
		panic(err)
	}
	return re
}

func (r *jsRE) exec(s string) *caps {
	if r == nil || r.noop || r.re == nil {
		return nil
	}
	m, err := r.re.FindStringMatch(s)
	if err != nil {
		panic(err)
	}
	if m == nil {
		return nil
	}
	return capsFrom(m)
}

func (r *jsRE) execAll(s string) []*caps {
	if r == nil || r.noop || r.re == nil {
		return nil
	}
	m, err := r.re.FindStringMatch(s)
	if err != nil {
		panic(err)
	}
	var out []*caps
	for m != nil {
		out = append(out, capsFrom(m))
		m, err = r.re.FindNextMatch(m)
		if err != nil {
			panic(err)
		}
	}
	return out
}

func capsFrom(m *regexp2.Match) *caps {
	n := m.GroupCount()
	c := &caps{
		full:   m.String(),
		index:  m.RuneIndex,
		groups: make([]string, n),
		has:    make([]bool, n),
	}
	for i := 0; i < n; i++ {
		g := m.GroupByNumber(i)
		if g == nil || len(g.Captures) == 0 {
			continue
		}
		c.groups[i] = g.String()
		c.has[i] = true
	}
	if n > 0 && !c.has[0] {
		c.groups[0] = c.full
		c.has[0] = c.full != "" || m.RuneLength == 0
	}
	return c
}

func (r *jsRE) test(s string) bool {
	if r == nil || r.noop || r.re == nil {
		return false
	}
	ok, err := r.re.MatchString(s)
	if err != nil {
		panic(err)
	}
	return ok
}

func (r *jsRE) search(s string) int {
	c := r.exec(s)
	if c == nil {
		return -1
	}
	return c.index
}

func (r *jsRE) replace(s, repl string) string {
	if r == nil || r.noop || r.re == nil {
		return s
	}
	count := 1
	if r.global {
		count = -1
	}
	out, err := r.re.Replace(s, repl, -1, count)
	if err != nil {
		panic(err)
	}
	return out
}

func (r *jsRE) replaceFunc(s string, fn func(*caps) string) string {
	if r == nil || r.noop || r.re == nil {
		return s
	}
	count := 1
	if r.global {
		count = -1
	}
	out, err := r.re.ReplaceFunc(s, func(m regexp2.Match) string {
		return fn(capsFrom(&m))
	}, -1, count)
	if err != nil {
		panic(err)
	}
	return out
}

var (
	dynMu    sync.Mutex
	dynCache = map[string]*jsRE{}
)

func compileCached(source, flags string) *jsRE {
	key := flags + "\x00" + source
	dynMu.Lock()
	defer dynMu.Unlock()
	if re, ok := dynCache[key]; ok {
		return re
	}
	re := mustCompile(source, flags)
	dynCache[key] = re
	return re
}
