package jsre

// SyntaxError is returned by Compile for an invalid pattern or flags string.
// Message is the exact text of the V8 SyntaxError message, for example
// "Invalid regular expression: /(/: Unterminated group".
type SyntaxError struct {
	Message string
}

func (e *SyntaxError) Error() string { return e.Message }

// Name returns the JS error class name.
func (e *SyntaxError) Name() string { return "SyntaxError" }

// regexpError mirrors V8's RegExpError enumeration (src/regexp/regexp-error.h).
type regexpError uint8

const (
	errUnterminatedGroup regexpError = iota
	errUnmatchedParen
	errEscapeAtEndOfPattern
	errInvalidPropertyName
	errInvalidEscape
	errInvalidDecimalEscape
	errInvalidUnicodeEscape
	errNothingToRepeat
	errLoneQuantifierBrackets
	errRangeOutOfOrder
	errIncompleteQuantifier
	errInvalidQuantifier
	errInvalidGroup
	errMultipleFlagDashes
	errRepeatedFlag
	errInvalidFlagGroup
	errTooManyCaptures
	errInvalidCaptureGroupName
	errDuplicateCaptureGroupName
	errInvalidNamedReference
	errInvalidNamedCaptureReference
	errInvalidClassPropertyName
	errInvalidCharacterClass
	errUnterminatedCharacterClass
	errOutOfOrderCharacterClass
	errInvalidClassSetOperation
	errInvalidCharacterInClass
	errNegatedCharacterClassWithStrings
)

var regexpErrorText = [...]string{
	errUnterminatedGroup:                "Unterminated group",
	errUnmatchedParen:                   "Unmatched ')'",
	errEscapeAtEndOfPattern:             "\\ at end of pattern",
	errInvalidPropertyName:              "Invalid property name",
	errInvalidEscape:                    "Invalid escape",
	errInvalidDecimalEscape:             "Invalid decimal escape",
	errInvalidUnicodeEscape:             "Invalid Unicode escape",
	errNothingToRepeat:                  "Nothing to repeat",
	errLoneQuantifierBrackets:           "Lone quantifier brackets",
	errRangeOutOfOrder:                  "numbers out of order in {} quantifier",
	errIncompleteQuantifier:             "Incomplete quantifier",
	errInvalidQuantifier:                "Invalid quantifier",
	errInvalidGroup:                     "Invalid group",
	errMultipleFlagDashes:               "Multiple dashes in flag group",
	errRepeatedFlag:                     "Repeated flag in flag group",
	errInvalidFlagGroup:                 "Invalid flag group",
	errTooManyCaptures:                  "Too many captures",
	errInvalidCaptureGroupName:          "Invalid capture group name",
	errDuplicateCaptureGroupName:        "Duplicate capture group name",
	errInvalidNamedReference:            "Invalid named reference",
	errInvalidNamedCaptureReference:     "Invalid named capture referenced",
	errInvalidClassPropertyName:         "Invalid property name in character class",
	errInvalidCharacterClass:            "Invalid character class",
	errUnterminatedCharacterClass:       "Unterminated character class",
	errOutOfOrderCharacterClass:         "Range out of order in character class",
	errInvalidClassSetOperation:         "Invalid set operation in character class",
	errInvalidCharacterInClass:          "Invalid character in character class",
	errNegatedCharacterClassWithStrings: "Negated character class may contain strings",
}

func (e regexpError) String() string { return regexpErrorText[e] }

// parseFailure is the panic payload used inside the parser; Compile recovers it.
type parseFailure struct{ code regexpError }

func invalidFlagsError(flags string) *SyntaxError {
	return &SyntaxError{Message: "Invalid flags supplied to RegExp constructor '" + flags + "'"}
}

func patternError(source, flags string, code regexpError) *SyntaxError {
	return &SyntaxError{Message: "Invalid regular expression: /" + source + "/" + flags + ": " + code.String()}
}
