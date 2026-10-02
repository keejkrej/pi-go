package jsregex

// Regexp is a compiled JavaScript regular expression. MatchString follows
// RegExp.prototype.test: an unanchored search, UTF-16 code units, and the
// u/i/m flags. The g and y flags are accepted and ignored (y is not sticky
// across calls; each MatchString starts at the beginning).
type Regexp struct {
	root       node
	ncap       int
	unicode    bool
	ignoreCase bool
	multiline  bool
	source     string
}

type syntaxErr string

func (e syntaxErr) Error() string { return "SyntaxError: " + string(e) }

func errorf(msg string) error { return syntaxErr(msg) }

// Compile parses pattern with the given JS flags (a subset of "gimsuy").
func Compile(pattern, flags string) (*Regexp, error) {
	var u, icase, multiline bool
	for _, f := range flags {
		switch f {
		case 'g', 'y', 'd', 's':
			// s (dotAll) is accepted but `.` stays "not a line terminator",
			// which is what the patterns these ports emit rely on.
		case 'i':
			icase = true
		case 'm':
			multiline = true
		case 'u':
			u = true
		default:
			return nil, errorf("Invalid flags")
		}
	}
	src := ToUTF16(pattern)
	root, ncap, err := parse(src, u, icase)
	if err != nil {
		return nil, err
	}
	if ncap < 0 {
		ncap = 0
	}
	return &Regexp{
		root:       root,
		ncap:       ncap,
		unicode:    u,
		ignoreCase: icase,
		multiline:  multiline,
		source:     pattern,
	}, nil
}

// MatchString reports whether re matches anywhere in s.
func (re *Regexp) MatchString(s string) bool {
	in := ToUTF16(s)
	m := &matcher{re: re, in: in, caps: make([]int, 2*(re.ncap+1))}
	reset := func() {
		for i := range m.caps {
			m.caps[i] = -1
		}
	}
	last := len(in)
	for i := 0; i <= last; i++ {
		reset()
		if m.match(re.root, i, func(int) bool { return true }) {
			return true
		}
		if re.unicode && i < last {
			c := in[i]
			if c >= 0xD800 && c <= 0xDBFF && i+1 < last && in[i+1] >= 0xDC00 && in[i+1] <= 0xDFFF {
				i++
			}
		}
	}
	return false
}

// MustCompile is Compile, panicking on error.
func MustCompile(pattern, flags string) *Regexp {
	re, err := Compile(pattern, flags)
	if err != nil {
		panic(err)
	}
	return re
}
