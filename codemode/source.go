// Ported from packages/codemode/src/source.ts (pi v1.0.0).

package codemode

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

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

const sourceMaxSafeInteger float64 = 9007199254740991

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

func sourceIsSafeInteger(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok {
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, false
	}
	if f < -sourceMaxSafeInteger || f > sourceMaxSafeInteger {
		return 0, false
	}
	return int64(f), true
}

// ParseCodemodeSource splits an optional first-line `// @options: {...}` from the script.
// It returns *CodemodeSourceError for empty input and invalid options.
func ParseCodemodeSource(input string) (*ParsedCodemodeSource, error) {
	if js.Trim(input) == "" {
		return nil, NewCodemodeSourceError(
			"Expected JavaScript source text (non-empty). Provide JS only, optionally with a first line `// @options: {\"max_output_tokens\": 1000}`.",
		)
	}

	newline := strings.IndexByte(input, '\n')
	firstLine := input
	if newline != -1 {
		firstLine = input[:newline]
	}
	firstLine = strings.TrimSuffix(firstLine, "\r")
	trimmed := js.TrimStart(firstLine)
	if !strings.HasPrefix(trimmed, CodemodeOptionsPrefix) {
		return &ParsedCodemodeSource{Code: input}, nil
	}

	// Keep the line break so runtime error line numbers stay aligned with the model's input.
	code := ""
	if newline != -1 {
		code = input[newline:]
	}
	if js.Trim(code) == "" {
		return nil, NewCodemodeSourceError("The @options line must be followed by JavaScript source on subsequent lines")
	}

	directive := js.Trim(trimmed[len(CodemodeOptionsPrefix):])
	if directive == "" {
		return nil, NewCodemodeSourceError("@options must be a JSON object with supported fields " + sourceSupportedFieldsText)
	}

	parsed, err := jsonx.Parse([]byte(directive))
	if err != nil {
		return nil, NewCodemodeSourceError(
			fmt.Sprintf("@options must be valid JSON with supported fields %s: %s", sourceSupportedFieldsText, jsonx.ParseErrorMessage(err, directive)),
		)
	}

	fields, ok := parsed.(*jsonx.Object)
	if !ok || fields == nil {
		return nil, NewCodemodeSourceError("@options must be a JSON object with supported fields " + sourceSupportedFieldsText)
	}

	for _, key := range fields.Keys() {
		if !slices.Contains(sourceSupportedFields, key) {
			return nil, NewCodemodeSourceError(fmt.Sprintf("@options only supports %s; got `%s`", sourceSupportedFieldsText, key))
		}
	}

	var options CodemodeSourceOptions
	if rawMaxTokens, exists := fields.Get("max_output_tokens"); exists {
		n, valid := sourceIsSafeInteger(rawMaxTokens)
		if !valid || n < 0 {
			return nil, NewCodemodeSourceError("@options field `max_output_tokens` must be a non-negative safe integer")
		}
		v := int(n)
		options.MaxOutputTokens = &v
	}
	if rawTimeoutMs, exists := fields.Get("timeout_ms"); exists {
		n, valid := sourceIsSafeInteger(rawTimeoutMs)
		if !valid || n < 1 || n > sourceMaxTimeoutMs {
			return nil, NewCodemodeSourceError(fmt.Sprintf("@options field `timeout_ms` must be a positive integer up to %d", sourceMaxTimeoutMs))
		}
		options.TimeoutMs = &n
	}

	return &ParsedCodemodeSource{
		Code:    code,
		Options: options,
	}, nil
}
