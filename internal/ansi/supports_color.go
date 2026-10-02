// Ported from chalk@6.0.0 source/vendor/supports-color/index.js.

package ansi

import (
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

// ColorSupport is the supports-color result object. A nil *ColorSupport is the TS `false` (level 0).
type ColorSupport struct {
	Level    int
	HasBasic bool
	Has256   bool
	Has16m   bool
}

// ColorStream is the stream argument of CreateSupportsColor (only `isTTY` is read).
type ColorStream struct {
	IsTTY bool
}

// SupportsColorOptions are the options of CreateSupportsColor.
type SupportsColorOptions struct {
	// StreamIsTTY overrides the stream's isTTY when set.
	StreamIsTTY *bool
	// SniffFlags controls whether process arguments (--color, --no-color, ...) are honored. Default true.
	SniffFlags *bool
}

// Test hooks: process.argv, the environment, platform, os.release(), and tty.isatty.
var (
	scArgs      = func() []string { return os.Args }
	scLookupEnv = os.LookupEnv
	scPlatform  = func() string { return runtime.GOOS }
	scOSRelease = supportsColorOSRelease
	scIsatty    = func(fd int) bool {
		switch fd {
		case 1:
			return term.IsTerminal(int(os.Stdout.Fd()))
		case 2:
			return term.IsTerminal(int(os.Stderr.Fd()))
		}
		return false
	}
)

var (
	scMu sync.Mutex
	// scFlagForceColor is the module-level `flagForceColor` (nil = undefined). It is derived from argv once
	// (module load in TS) and then overwritten by every _supportsColor call that sees FORCE_COLOR.
	scFlagForceColor     *int
	scFlagForceColorInit bool

	scNumericExpr    = regexp.MustCompile(`^\d+$`)
	scTeamCityExpr   = regexp.MustCompile(`^(?:9\.0*[1-9]\d*\.|\d{2,}\.)`)
	scTerm256Expr    = regexp.MustCompile(`(?i)-256(?:color)?$`)
	scTermBasicExpr  = regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`)
	scDefaultStreams struct {
		once   sync.Once
		stdout *ColorSupport
		stderr *ColorSupport
	}
)

// From: https://github.com/sindresorhus/has-flag/blob/main/index.js
func hasFlag(flag string) bool {
	argv := scArgs()
	prefix := "--"
	if strings.HasPrefix(flag, "-") {
		prefix = ""
	} else if len(flag) == 1 {
		prefix = "-"
	}
	position := indexOfString(argv, prefix+flag)
	terminatorPosition := indexOfString(argv, "--")
	return position != -1 && (terminatorPosition == -1 || position < terminatorPosition)
}

func indexOfString(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func intPtr(v int) *int { return &v }

// scInitFlagForceColor evaluates the module-load `flagForceColor` from argv. Caller holds scMu.
func scInitFlagForceColor() {
	if scFlagForceColorInit {
		return
	}
	scFlagForceColorInit = true
	if hasFlag("no-color") || hasFlag("no-colors") || hasFlag("color=false") || hasFlag("color=never") {
		scFlagForceColor = intPtr(0)
	} else if hasFlag("color") || hasFlag("colors") || hasFlag("color=true") || hasFlag("color=always") {
		scFlagForceColor = intPtr(1)
	}
}

// envString returns env[name] the way JS coerces a possibly-undefined env value into a string ("undefined").
func envString(name string) string {
	if v, ok := scLookupEnv(name); ok {
		return v
	}
	return "undefined"
}

func envHas(name string) bool {
	_, ok := scLookupEnv(name)
	return ok
}

// Whether `FORCE_COLOR` names a level. Shared with the exact-level check below so that the two cannot disagree on what counts as numeric.
func hasNumericForceColor() bool {
	return scNumericExpr.MatchString(envString("FORCE_COLOR"))
}

func envForceColor() *int {
	value, ok := scLookupEnv("FORCE_COLOR")
	if !ok {
		return nil
	}
	if value == "false" {
		return intPtr(0)
	}
	if value == "true" || len(value) == 0 {
		return intPtr(1)
	}
	if !hasNumericForceColor() {
		return nil
	}
	// Math.min(Number.parseInt(env.FORCE_COLOR, 10), 3); the value is all ASCII digits here.
	n, err := strconv.Atoi(value)
	if err != nil || n > 3 {
		n = 3
	}
	return intPtr(n)
}

func translateLevel(level int) *ColorSupport {
	if level == 0 {
		return nil
	}
	return &ColorSupport{
		Level:    level,
		HasBasic: true,
		Has256:   level >= 2,
		Has16m:   level >= 3,
	}
}

// jsParseInt10 is Number.parseInt(s, 10); ok is false for NaN.
func jsParseInt10(s string) (int, bool) {
	s = strings.TrimLeftFunc(s, isJSWhitespace)
	sign := 1
	if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	n, digits := 0, 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		if n < 1<<40 {
			n = n*10 + int(s[digits]-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	return sign * n, true
}

// isJSWhitespace reports whether r is in the ECMAScript WhiteSpace or LineTerminator sets.
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsNumber is Number(s) for the os.release() parts ("10", "19045"); ok is false for NaN.
func jsNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func supportsColorLevel(haveStream bool, streamIsTTY bool, sniffFlags bool) int {
	scMu.Lock()
	scInitFlagForceColor()
	noFlagForceColor := envForceColor()
	if noFlagForceColor != nil {
		scFlagForceColor = intPtr(*noFlagForceColor)
	}

	forceColor := noFlagForceColor
	if sniffFlags {
		forceColor = scFlagForceColor
	}
	scMu.Unlock()

	if forceColor != nil && *forceColor == 0 {
		return 0
	}

	if sniffFlags {
		if hasFlag("color=16m") || hasFlag("color=full") || hasFlag("color=truecolor") {
			return 3
		}
		if hasFlag("color=256") {
			return 2
		}
	}

	// A numeric `FORCE_COLOR` requests an exact level, while `FORCE_COLOR=true` and `FORCE_COLOR=` only enable color and let the level be detected.
	if forceColor != nil && hasNumericForceColor() {
		return *forceColor
	}

	// Check for Azure DevOps pipelines.
	// Has to be above the `!streamIsTTY` check.
	if envHas("TF_BUILD") && envHas("AGENT_NAME") {
		return 1
	}

	if haveStream && !streamIsTTY && forceColor == nil {
		return 0
	}

	minLevel := 0
	if forceColor != nil {
		minLevel = *forceColor
	}

	if term, ok := scLookupEnv("TERM"); ok && term == "dumb" {
		return minLevel
	}

	if scPlatform() == "windows" {
		// Windows 10 build 10586 is the first Windows release that supports 256 colors.
		// Windows 10 build 14931 is the first release that supports 16m/TrueColor.
		osRelease := strings.Split(scOSRelease(), ".")
		major, majorOK := jsNumber(osRelease[0])
		build, buildOK := 0.0, false
		if len(osRelease) > 2 {
			build, buildOK = jsNumber(osRelease[2])
		}
		if majorOK && buildOK && major >= 10 && build >= 10_586 {
			if build >= 14_931 {
				return 3
			}
			return 2
		}
		return 1
	}

	if envHas("CI") {
		for _, key := range []string{"GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI"} {
			if envHas(key) {
				return 3
			}
		}
		for _, sign := range []string{"TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE"} {
			if envHas(sign) {
				return 1
			}
		}
		if v, ok := scLookupEnv("CI_NAME"); ok && v == "codeship" {
			return 1
		}
		return minLevel
	}

	if v, ok := scLookupEnv("TEAMCITY_VERSION"); ok {
		if scTeamCityExpr.MatchString(v) {
			return 1
		}
		return 0
	}

	if v, ok := scLookupEnv("COLORTERM"); ok && v == "truecolor" {
		return 3
	}

	termValue, termSet := scLookupEnv("TERM")
	if termSet && termValue == "xterm-kitty" {
		return 3
	}
	if termSet && termValue == "xterm-ghostty" {
		return 3
	}
	if termSet && termValue == "wezterm" {
		return 3
	}

	if program, ok := scLookupEnv("TERM_PROGRAM"); ok {
		versionString, _ := scLookupEnv("TERM_PROGRAM_VERSION")
		version, versionOK := jsParseInt10(strings.SplitN(versionString, ".", 2)[0])

		switch program {
		case "iTerm.app":
			if versionOK && version >= 3 {
				return 3
			}
			return 2
		case "Apple_Terminal":
			return 2
			// No default
		}
	}

	termString := envString("TERM")
	if scTerm256Expr.MatchString(termString) {
		return 2
	}

	if scTermBasicExpr.MatchString(termString) {
		return 1
	}

	if envHas("COLORTERM") {
		return 1
	}

	return minLevel
}

// CreateSupportsColor reports the color support of a stream (supports-color `createSupportsColor`).
// A nil stream is "no stream"; nil options use the defaults.
func CreateSupportsColor(stream *ColorStream, options *SupportsColorOptions) *ColorSupport {
	streamIsTTY := stream != nil && stream.IsTTY
	sniffFlags := true
	if options != nil {
		if options.StreamIsTTY != nil {
			streamIsTTY = *options.StreamIsTTY
		}
		if options.SniffFlags != nil {
			sniffFlags = *options.SniffFlags
		}
	}
	return translateLevel(supportsColorLevel(stream != nil, streamIsTTY, sniffFlags))
}

func scDetectDefaultStreams() {
	scDefaultStreams.once.Do(func() {
		scDefaultStreams.stdout = CreateSupportsColor(&ColorStream{IsTTY: scIsatty(1)}, nil)
		scDefaultStreams.stderr = CreateSupportsColor(&ColorStream{IsTTY: scIsatty(2)}, nil)
	})
}

// SupportsColor is the color support of stdout (chalk `supportsColor`). It is detected once, on first use
// (module load in TS). Nil means no color.
func SupportsColor() *ColorSupport {
	scDetectDefaultStreams()
	return scDefaultStreams.stdout
}

// SupportsColorStderr is the color support of stderr (chalk `supportsColorStderr`). Nil means no color.
func SupportsColorStderr() *ColorSupport {
	scDetectDefaultStreams()
	return scDefaultStreams.stderr
}
