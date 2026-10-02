package jsdiff

// CharacterDiff is the jsdiff characterDiff instance (class CharacterDiff): the base
// Diff, whose tokens are code points.
var CharacterDiff = &Diff{}

// DiffChars diffs two blocks of text, treating each character as a token. It returns nil
// if MaxEditLength or Timeout was exceeded.
func DiffChars(oldStr, newStr string, options *Options) []Change {
	return CharacterDiff.Diff(oldStr, newStr, options)
}

func isSentenceEndPunct(r rune) bool {
	return r == '.' || r == '!' || r == '?'
}

// sentenceTokenize is equivalent to value.split(/(?<=[.!?])(\s+|$)/), written out by hand
// in TS for environments without lookbehind support.
func sentenceTokenize(value string, _ *Options) []string {
	result := []string{}
	tokenStart := 0
	for i := 0; i < len(value); {
		r, size := decodeChar(value, i)
		if i+size >= len(value) {
			result = append(result, value[tokenStart:])
			break
		}
		next, nextSize := decodeChar(value, i+size)
		if isSentenceEndPunct(r) && isJSSpace(next) {
			// We've hit a sentence break - i.e. a punctuation mark followed by
			// whitespace. We now want to push TWO tokens to the result:
			// 1. the sentence
			result = append(result, value[tokenStart:i+size])
			// 2. the whitespace
			tokenStart = i + size
			end := tokenStart + nextSize
			for end < len(value) {
				r2, s2 := decodeChar(value, end)
				if !isJSSpace(r2) {
					break
				}
				end += s2
			}
			result = append(result, value[tokenStart:end])
			// Then the next token (a sentence) starts on the character after the
			// whitespace. (It's okay if this is off the end of the string - then the
			// outer loop will terminate here anyway.)
			tokenStart = end
			i = end
			continue
		}
		i += size
	}
	return result
}

// SentenceDiff is the jsdiff sentenceDiff instance (class SentenceDiff).
var SentenceDiff = &Diff{
	Tokenize: sentenceTokenize,
}

// DiffSentences diffs two blocks of text, treating each sentence, and the whitespace
// between each pair of sentences, as a token. It returns nil if MaxEditLength or Timeout
// was exceeded.
func DiffSentences(oldStr, newStr string, options *Options) []Change {
	return SentenceDiff.Diff(oldStr, newStr, options)
}

// cssTokenize is value.split(/([{}:;,]|\s+)/).
func cssTokenize(value string, _ *Options) []string {
	parts := []string{}
	start := 0
	for i := 0; i < len(value); {
		r, size := decodeChar(value, i)
		switch {
		case r == '{' || r == '}' || r == ':' || r == ';' || r == ',':
			parts = append(parts, value[start:i], value[i:i+size])
			i += size
			start = i
		case isJSSpace(r):
			end := scanRun(value, i, isJSSpace)
			parts = append(parts, value[start:i], value[i:end])
			i = end
			start = i
		default:
			i += size
		}
	}
	return append(parts, value[start:])
}

// CssDiff is the jsdiff cssDiff instance (class CssDiff).
var CssDiff = &Diff{
	Tokenize: cssTokenize,
}

// DiffCss diffs two blocks of text, comparing CSS tokens. It returns nil if
// MaxEditLength or Timeout was exceeded.
func DiffCss(oldStr, newStr string, options *Options) []Change {
	return CssDiff.Diff(oldStr, newStr, options)
}

// ArrayOptions are the options of DiffArrays.
type ArrayOptions[T any] struct {
	// OneChangePerToken: see Options.OneChangePerToken.
	OneChangePerToken bool
	// Comparator replaces === equality of elements.
	Comparator func(left, right T) bool
	// MaxEditLength: see Options.MaxEditLength.
	MaxEditLength *int
	// Timeout: see Options.Timeout.
	Timeout *int64
}

// DiffArrays diffs two arrays of tokens, comparing each item for strict equality (===)
// unless a Comparator is given. It returns nil if MaxEditLength or Timeout was exceeded.
func DiffArrays[T comparable](oldArr, newArr []T, options *ArrayOptions[T]) []ArrayChange[T] {
	if options == nil {
		options = &ArrayOptions[T]{}
	}
	equals := func(left, right T) bool { return left == right }
	if options.Comparator != nil {
		equals = options.Comparator
	}
	// ArrayDiff: tokenize copies the array, removeEmpty is a no-op and join returns the
	// tokens unchanged.
	oldTokens := append([]T(nil), oldArr...)
	newTokens := append([]T(nil), newArr...)
	components := runDiff(oldTokens, newTokens, equals, &Options{
		OneChangePerToken: options.OneChangePerToken,
		MaxEditLength:     options.MaxEditLength,
		Timeout:           options.Timeout,
	})
	if components == nil {
		return nil
	}
	changes := make([]ArrayChange[T], len(components))
	newPos, oldPos := 0, 0
	for i, c := range components {
		change := ArrayChange[T]{Count: c.count, Added: c.added, Removed: c.removed}
		if !c.removed {
			change.Value = append([]T{}, newTokens[newPos:newPos+c.count]...)
			newPos += c.count
			if !c.added {
				oldPos += c.count
			}
		} else {
			change.Value = append([]T{}, oldTokens[oldPos:oldPos+c.count]...)
			oldPos += c.count
		}
		changes[i] = change
	}
	return changes
}
