// Ported from packages/codemode/src/source.ts (pi v1.0.0).

package codemode

// CodemodeOptionsPrefix is the first-line directive that carries script options.
const CodemodeOptionsPrefix = "// @options:"

// CodemodeSourceGrammar is the Lark grammar for providers with grammar-constrained
// tool input. It only fixes the shape of the options line; the options JSON and the
// code are checked by ParseCodemodeSource.
const CodemodeSourceGrammar = `
start: options_source | plain_source
options_source: OPTIONS_LINE NEWLINE SOURCE
plain_source: SOURCE

OPTIONS_LINE: /[ \t]*\/\/ @options:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

// sourceSupportedFields are the JSON keys @options accepts.
var sourceSupportedFields = []string{"max_output_tokens", "timeout_ms"}

const sourceSupportedFieldsText = "`max_output_tokens` and `timeout_ms`"

// sourceMaxTimeoutMs is the largest delay setTimeout supports, which bounds timeout_ms.
const sourceMaxTimeoutMs int64 = 2_147_483_647

// CodemodeSourceOptions is the parsed `// @options:` object.
// The wire keys are max_output_tokens and timeout_ms; these fields are the parsed names.
type CodemodeSourceOptions struct {
	// MaxOutputTokens is the token budget for the script's output.
	MaxOutputTokens *int `json:"maxOutputTokens,omitzero"`
	// TimeoutMs is the hard deadline for the whole script in milliseconds, including tool calls.
	TimeoutMs *int64 `json:"timeoutMs,omitzero"`
}

// ParsedCodemodeSource is a script split from its options line.
type ParsedCodemodeSource struct {
	// Code is the script with the options line replaced by an empty line, so line numbers are unchanged.
	Code string `json:"code"`
	// Options is empty when the script has no @options line.
	Options CodemodeSourceOptions `json:"options"`
}

// CodemodeSourceError is empty input or an invalid @options line.
type CodemodeSourceError struct {
	Message string
}

// NewCodemodeSourceError returns an error whose Name is "CodemodeSourceError".
func NewCodemodeSourceError(message string) *CodemodeSourceError {
	return &CodemodeSourceError{Message: message}
}

// Error returns the message.
func (e *CodemodeSourceError) Error() string { return e.Message }

// Name returns the TS error name.
func (e *CodemodeSourceError) Name() string { return "CodemodeSourceError" }

// ParseCodemodeSource splits an optional first-line `// @options: {...}` from the script.
// It returns *CodemodeSourceError for empty input and invalid options.
func ParseCodemodeSource(input string) (*ParsedCodemodeSource, error) {
	panic("unported: ParseCodemodeSource")
}
