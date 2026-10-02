package jsdiff

import "strings"

// LineDiff is the jsdiff lineDiff instance (class LineDiff).
var LineDiff = &Diff{
	Tokenize: tokenizeLines,
	Equals:   lineEquals,
}

func lineEquals(left, right string, options *Options) bool {
	// If we're ignoring whitespace, we need to normalise lines by stripping whitespace
	// before checking equality. (This has an annoying interaction with newlineIsToken
	// that requires special handling: if newlines get their own token, then we DON'T
	// want to trim the *newline* tokens down to empty strings, since this would cause us
	// to treat whitespace-only line content as equal to a separator between lines, which
	// would be weird and inconsistent with the documented behavior of the options.)
	if options.IgnoreWhitespace != nil && *options.IgnoreWhitespace {
		if !options.NewlineIsToken || !strings.Contains(left, "\n") {
			left = jsTrim(left)
		}
		if !options.NewlineIsToken || !strings.Contains(right, "\n") {
			right = jsTrim(right)
		}
	} else if options.IgnoreNewlineAtEof && !options.NewlineIsToken {
		left = strings.TrimSuffix(left, "\n")
		right = strings.TrimSuffix(right, "\n")
	}
	return BaseEquals(left, right, options)
}

// DiffLines diffs two blocks of text, treating each line as a token. It returns nil if
// MaxEditLength or Timeout was exceeded.
func DiffLines(oldStr, newStr string, options *Options) []Change {
	return LineDiff.Diff(oldStr, newStr, options)
}

// DiffTrimmedLines is DiffLines with IgnoreWhitespace defaulting to true (an explicit
// value in options wins, as with generateOptions in TS).
func DiffTrimmedLines(oldStr, newStr string, options *Options) []Change {
	opts := Options{}
	if options != nil {
		opts = *options
	}
	if opts.IgnoreWhitespace == nil {
		ignoreWhitespace := true
		opts.IgnoreWhitespace = &ignoreWhitespace
	}
	return LineDiff.Diff(oldStr, newStr, &opts)
}

// tokenizeLines is line.js tokenize, shared with the JSON diff.
func tokenizeLines(value string, options *Options) []string {
	if options.StripTrailingCr {
		// remove one \r before \n to match GNU diff's --strip-trailing-cr behavior
		value = strings.ReplaceAll(value, "\r\n", "\n")
	}
	linesAndNewlines := splitLinesAndNewlines(value)
	// Ignore the final empty token that occurs if the string ends with a new line
	if linesAndNewlines[len(linesAndNewlines)-1] == "" {
		linesAndNewlines = linesAndNewlines[:len(linesAndNewlines)-1]
	}
	// Merge the content and line separators into single tokens
	retLines := make([]string, 0, len(linesAndNewlines))
	for i, line := range linesAndNewlines {
		if i%2 == 1 && !options.NewlineIsToken {
			retLines[len(retLines)-1] += line
		} else {
			retLines = append(retLines, line)
		}
	}
	return retLines
}

// splitLinesAndNewlines is value.split(/(\n|\r\n)/): content and separators alternate,
// and the result always has an odd length.
func splitLinesAndNewlines(value string) []string {
	parts := make([]string, 0, strings.Count(value, "\n")*2+1)
	start := 0
	for i := 0; i < len(value); i++ {
		switch {
		case value[i] == '\n':
			parts = append(parts, value[start:i], "\n")
			start = i + 1
		case value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n':
			parts = append(parts, value[start:i], "\r\n")
			start = i + 2
			i++
		}
	}
	return append(parts, value[start:])
}
