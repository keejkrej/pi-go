package jsdiff

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Change is a jsdiff change object (ChangeObject<string>).
type Change struct {
	// Value is the concatenated content of all the tokens represented by this change
	// object - i.e. generally the text that is either added, deleted, or common, as a
	// single string. In cases where tokens are considered common but are non-identical
	// (e.g. because an option like IgnoreCase or a custom Comparator was used), the value
	// from the *new* string is provided here.
	Value string `json:"value"`
	// Added is true if the value was inserted into the new string.
	Added bool `json:"added"`
	// Removed is true if the value was removed from the old string.
	Removed bool `json:"removed"`
	// Count is how many tokens (e.g. chars for DiffChars, lines for DiffLines) the value
	// in the change object consists of.
	Count int `json:"count"`
}

// MarshalJSON writes the keys in the order jsdiff creates them: count, added, removed,
// value.
func (c Change) MarshalJSON() ([]byte, error) {
	return marshalChange(c.Count, c.Added, c.Removed, c.Value)
}

// ArrayChange is a change object of DiffArrays (ChangeObject<T[]>).
type ArrayChange[T any] struct {
	Value   []T  `json:"value"`
	Added   bool `json:"added"`
	Removed bool `json:"removed"`
	Count   int  `json:"count"`
}

// MarshalJSON writes the keys in the order jsdiff creates them: count, added, removed,
// value.
func (c ArrayChange[T]) MarshalJSON() ([]byte, error) {
	value := c.Value
	if value == nil {
		value = []T{}
	}
	return marshalChange(c.Count, c.Added, c.Removed, value)
}

func marshalChange(count int, added, removed bool, value any) ([]byte, error) {
	v, err := jsonx.Marshal(value)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, len(v)+64)
	b = append(b, `{"count":`...)
	b = strconv.AppendInt(b, int64(count), 10)
	b = append(b, `,"added":`...)
	b = strconv.AppendBool(b, added)
	b = append(b, `,"removed":`...)
	b = strconv.AppendBool(b, removed)
	b = append(b, `,"value":`...)
	b = append(b, v...)
	return append(b, '}'), nil
}

// Options is the union of the options accepted by the diffing functions (TS
// AllDiffOptions plus the abortable options). The README notes which options are usable
// with which functions; using an option with a diffing function that doesn't support it
// might yield unreasonable results.
type Options struct {
	// OneChangePerToken makes the result contain one change object per token (e.g. one
	// per line for DiffLines) instead of runs of consecutive tokens that are all added /
	// all removed / all conserved being combined into a single change object.
	OneChangePerToken bool
	// MaxEditLength is the maximum edit distance to consider between the old and new
	// texts. If it is exceeded the diff gives up and returns nil (TS: undefined).
	MaxEditLength *int
	// Timeout is a number of milliseconds after which the diffing algorithm aborts and
	// returns nil (TS: undefined).
	Timeout *int64
	// IgnoreCase makes the uppercase and lowercase forms of a character equal
	// (DiffChars, DiffWords, DiffLines, ...).
	IgnoreCase bool
	// Comparator replaces the default token equality (not used by DiffWords).
	Comparator func(left, right string) bool
	// StripTrailingCr removes all trailing CR (\r) characters before performing a line
	// diff, like GNU diff's --strip-trailing-cr.
	StripTrailingCr bool
	// NewlineIsToken treats the newline character at the end of each line as its own
	// token in DiffLines. It may not be used with the patch-generation functions.
	NewlineIsToken bool
	// IgnoreNewlineAtEof ignores a missing newline character at the end of the last line
	// when comparing it to other lines. Ignored if IgnoreWhitespace or NewlineIsToken is
	// also true.
	IgnoreNewlineAtEof bool
	// IgnoreWhitespace ignores leading and trailing whitespace when comparing lines.
	// For DiffWords an explicit false selects DiffWordsWithSpace (undocumented TS
	// behavior kept for compatibility); nil means undefined.
	IgnoreWhitespace *bool
	// IntlSegmenter is used by DiffWords to split the text into words. Its granularity
	// must be "word".
	IntlSegmenter Segmenter
	// StringifyReplacer is DiffJson's replacer, applied to every value like the
	// replacer parameter of JSON.stringify (key "" for the root). keep=false means the
	// replacer returned undefined.
	StringifyReplacer func(key string, value any) (replaced any, keep bool)
}

// Diff is the jsdiff Diff base class for string inputs. Each hook corresponds to an
// overridable method of the TS class; a nil hook uses the base implementation.
type Diff struct {
	// CastInput massages the input prior to running (base: identity).
	CastInput func(value string, options *Options) string
	// Tokenize splits a string into tokens (base: Array.from, i.e. code points).
	Tokenize func(value string, options *Options) []string
	// Equals compares an old and a new token (base: BaseEquals).
	Equals func(left, right string, options *Options) bool
	// RemoveEmpty filters the token list (base: drop empty tokens).
	RemoveEmpty func(tokens []string) []string
	// Join concatenates tokens into a change value (base: plain concatenation).
	Join func(tokens []string) string
	// PostProcess rewrites the final change list (base: identity).
	PostProcess func(changes []Change, options *Options) []Change
	// UseLongestToken makes a common run take, token by token, the longer (in UTF-16
	// units) of the old and new token.
	UseLongestToken bool
}

// BaseEquals is the equals method of the Diff base class: the comparator if one is
// given, otherwise strict equality or, with IgnoreCase, equality after toLowerCase.
func BaseEquals(left, right string, options *Options) bool {
	if options.Comparator != nil {
		return options.Comparator(left, right)
	}
	return left == right || (options.IgnoreCase && jsToLower(left) == jsToLower(right))
}

// Diff computes the diff between oldStr and newStr. It returns nil if MaxEditLength or
// Timeout was exceeded, and a non-nil (possibly empty) slice otherwise.
func (d *Diff) Diff(oldStr, newStr string, options *Options) []Change {
	if options == nil {
		options = &Options{}
	}
	// Allow subclasses to massage the input prior to running
	oldString := d.castInput(oldStr, options)
	newString := d.castInput(newStr, options)
	oldTokens := d.removeEmpty(d.tokenize(oldString, options))
	newTokens := d.removeEmpty(d.tokenize(newString, options))
	return d.diffWithOptionsObj(oldTokens, newTokens, options)
}

func (d *Diff) castInput(value string, options *Options) string {
	if d.CastInput != nil {
		return d.CastInput(value, options)
	}
	return value
}

func (d *Diff) tokenize(value string, options *Options) []string {
	if d.Tokenize != nil {
		return d.Tokenize(value, options)
	}
	return arrayFrom(value)
}

// arrayFrom is Array.from(value): one element per code point.
func arrayFrom(value string) []string {
	tokens := make([]string, 0, len(value))
	for i := 0; i < len(value); {
		_, size := decodeChar(value, i)
		tokens = append(tokens, value[i:i+size])
		i += size
	}
	return tokens
}

func (d *Diff) equals(left, right string, options *Options) bool {
	if d.Equals != nil {
		return d.Equals(left, right, options)
	}
	return BaseEquals(left, right, options)
}

func (d *Diff) removeEmpty(tokens []string) []string {
	if d.RemoveEmpty != nil {
		return d.RemoveEmpty(tokens)
	}
	ret := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t != "" {
			ret = append(ret, t)
		}
	}
	return ret
}

func (d *Diff) join(tokens []string) string {
	if d.Join != nil {
		return d.Join(tokens)
	}
	return strings.Join(tokens, "")
}

func (d *Diff) postProcess(changes []Change, options *Options) []Change {
	if d.PostProcess != nil {
		return d.PostProcess(changes, options)
	}
	return changes
}

func (d *Diff) diffWithOptionsObj(oldTokens, newTokens []string, options *Options) []Change {
	equals := func(left, right string) bool { return d.equals(left, right, options) }
	components := runDiff(oldTokens, newTokens, equals, options)
	if components == nil {
		return nil
	}
	return d.postProcess(d.buildValues(components, newTokens, oldTokens), options)
}

func (d *Diff) buildValues(components []*component, newTokens, oldTokens []string) []Change {
	changes := make([]Change, len(components))
	newPos, oldPos := 0, 0
	for i, c := range components {
		change := Change{Count: c.count, Added: c.added, Removed: c.removed}
		if !c.removed {
			if !c.added && d.UseLongestToken {
				value := make([]string, c.count)
				for k, tok := range newTokens[newPos : newPos+c.count] {
					oldValue := oldTokens[oldPos+k]
					if utf16Len(oldValue) > utf16Len(tok) {
						value[k] = oldValue
					} else {
						value[k] = tok
					}
				}
				change.Value = d.join(value)
			} else {
				change.Value = d.join(newTokens[newPos : newPos+c.count])
			}
			newPos += c.count
			// Common case
			if !c.added {
				oldPos += c.count
			}
		} else {
			change.Value = d.join(oldTokens[oldPos : oldPos+c.count])
			oldPos += c.count
		}
		changes[i] = change
	}
	return changes
}

// component is a node of the reversed linked list of change runs built by the Myers
// search.
type component struct {
	count             int
	added             bool
	removed           bool
	previousComponent *component
}

type diffPath struct {
	oldPos        int
	lastComponent *component
}

// runDiff is the token-type independent part of Diff.diffWithOptionsObj (the Myers
// O(ND) search). It returns the change runs in order, or nil when MaxEditLength or
// Timeout was exceeded.
func runDiff[T any](oldTokens, newTokens []T, equals func(left, right T) bool, options *Options) []*component {
	oneChangePerToken := options.OneChangePerToken
	newLen, oldLen := len(newTokens), len(oldTokens)
	editLength := 1
	maxEditLength := newLen + oldLen
	if options.MaxEditLength != nil {
		maxEditLength = min(maxEditLength, *options.MaxEditLength)
	}
	hasDeadline := options.Timeout != nil
	var abortAfterTimestamp int64
	if hasDeadline {
		now := time.Now().UnixMilli()
		if *options.Timeout > math.MaxInt64-now {
			hasDeadline = false
		} else {
			abortAfterTimestamp = now + *options.Timeout
		}
	}

	addToPath := func(path *diffPath, added, removed bool, oldPosInc int) *diffPath {
		last := path.lastComponent
		if last != nil && !oneChangePerToken && last.added == added && last.removed == removed {
			return &diffPath{
				oldPos:        path.oldPos + oldPosInc,
				lastComponent: &component{count: last.count + 1, added: added, removed: removed, previousComponent: last.previousComponent},
			}
		}
		return &diffPath{
			oldPos:        path.oldPos + oldPosInc,
			lastComponent: &component{count: 1, added: added, removed: removed, previousComponent: last},
		}
	}
	extractCommon := func(basePath *diffPath, diagonalPath int) int {
		oldPos := basePath.oldPos
		newPos := oldPos - diagonalPath
		commonCount := 0
		for newPos+1 < newLen && oldPos+1 < oldLen && equals(oldTokens[oldPos+1], newTokens[newPos+1]) {
			newPos++
			oldPos++
			commonCount++
			if oneChangePerToken {
				basePath.lastComponent = &component{count: 1, previousComponent: basePath.lastComponent}
			}
		}
		if commonCount != 0 && !oneChangePerToken {
			basePath.lastComponent = &component{count: commonCount, previousComponent: basePath.lastComponent}
		}
		basePath.oldPos = oldPos
		return newPos
	}

	// bestPath is indexed by diagonal, which can be negative.
	offset := newLen + oldLen + 2
	bestPath := make([]*diffPath, 2*offset+1)
	bestPath[offset] = &diffPath{oldPos: -1}

	// Seed editLength = 0, i.e. the content starts with the same values
	newPos := extractCommon(bestPath[offset], 0)
	if bestPath[offset].oldPos+1 >= oldLen && newPos+1 >= newLen {
		// Identity per the equality and tokenizer
		return buildComponents(bestPath[offset].lastComponent)
	}

	// Once we hit the right edge of the edit graph on some diagonal k, we can definitely
	// reach the end of the edit graph in no more than k edits, so there's no point in
	// considering any moves to diagonal k+1 any more (from which we're guaranteed to need
	// at least k+1 more edits). Similarly, once we've reached the bottom of the edit
	// graph, there's no point considering moves to lower diagonals. We record this fact
	// by setting minDiagonalToConsider and maxDiagonalToConsider to some finite value once
	// we've hit the edge of the edit graph.
	minDiagonalToConsider, maxDiagonalToConsider := math.MinInt, math.MaxInt

	// Main worker method. checks all permutations of a given edit length for acceptance.
	execEditLength := func() (*component, bool) {
		for diagonalPath := max(minDiagonalToConsider, -editLength); diagonalPath <= min(maxDiagonalToConsider, editLength); diagonalPath += 2 {
			var basePath *diffPath
			removePath, addPath := bestPath[offset+diagonalPath-1], bestPath[offset+diagonalPath+1]
			if removePath != nil {
				// No one else is going to attempt to use this value, clear it
				bestPath[offset+diagonalPath-1] = nil
			}
			canAdd := false
			if addPath != nil {
				// what newPos will be after we do an insertion:
				addPathNewPos := addPath.oldPos - diagonalPath
				canAdd = 0 <= addPathNewPos && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				// If this path is a terminal then prune
				bestPath[offset+diagonalPath] = nil
				continue
			}
			// Select the diagonal that we want to branch from. We select the prior path
			// whose position in the old string is the farthest from the origin and does
			// not pass the bounds of the diff graph
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				basePath = addToPath(addPath, true, false, 0)
			} else {
				basePath = addToPath(removePath, false, true, 1)
			}
			newPos = extractCommon(basePath, diagonalPath)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				// If we have hit the end of both strings, then we are done
				return basePath.lastComponent, true
			}
			bestPath[offset+diagonalPath] = basePath
			if basePath.oldPos+1 >= oldLen {
				maxDiagonalToConsider = min(maxDiagonalToConsider, diagonalPath-1)
			}
			if newPos+1 >= newLen {
				minDiagonalToConsider = max(minDiagonalToConsider, diagonalPath+1)
			}
		}
		editLength++
		return nil, false
	}

	// Loops over execEditLength until a value is produced, or until the edit length
	// exceeds options.maxEditLength (if given), in which case it will return undefined.
	for editLength <= maxEditLength && (!hasDeadline || time.Now().UnixMilli() <= abortAfterTimestamp) {
		if last, done := execEditLength(); done {
			return buildComponents(last)
		}
	}
	return nil
}

// buildComponents converts the linked list of components in reverse order to a slice in
// the right order (the first half of buildValues). The result is never nil.
func buildComponents(lastComponent *component) []*component {
	components := []*component{}
	for c := lastComponent; c != nil; c = c.previousComponent {
		components = append(components, c)
	}
	for i, j := 0, len(components)-1; i < j; i, j = i+1, j-1 {
		components[i], components[j] = components[j], components[i]
	}
	return components
}
