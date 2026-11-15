// Ported from packages/ai/src/utils/error-body.ts (pi v1.5.2).

package ai

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// MaxProviderErrorBodyChars caps extracted HTTP error bodies.
const MaxProviderErrorBodyChars = 4000

const uebMaxCauseDepth = 5

// NormalizedProviderError holds the status, body, and formatted message extracted from a provider error.
type NormalizedProviderError struct {
	Status             *int
	Body               *string
	Message            string
	MessageCarriesBody bool
}

// ProviderSdkError describes SDK-shaped HTTP/transport error fields for NormalizeProviderError.
type ProviderSdkError struct {
	ErrName      string
	Message      string
	Cause        error
	ErrorCodeVal any
	StatusCode   *int
	Status       *int
	Body         any
	ParsedError  any
	Metadata     *ProviderSdkMetadata
	Response     *ProviderSdkResponse
}

// ProviderSdkMetadata models AWS SDK `$metadata.httpStatusCode`.
type ProviderSdkMetadata struct {
	HttpStatusCode *int
}

// ProviderSdkResponse models AWS SDK `$response` fields.
type ProviderSdkResponse struct {
	StatusCode *int
	Body       any
}

func (e *ProviderSdkError) Error() string { return e.Message }
func (e *ProviderSdkError) Name() string {
	if e.ErrName != "" {
		return e.ErrName
	}
	return "Error"
}
func (e *ProviderSdkError) Unwrap() error  { return e.Cause }
func (e *ProviderSdkError) ErrorCode() any { return e.ErrorCodeVal }

// ProviderStatus implements ProviderError when Status or StatusCode is set.
func (e *ProviderSdkError) ProviderStatus() *int {
	if e.StatusCode != nil {
		return e.StatusCode
	}
	if e.Status != nil {
		return e.Status
	}
	if e.Metadata != nil && e.Metadata.HttpStatusCode != nil {
		return e.Metadata.HttpStatusCode
	}
	if e.Response != nil && e.Response.StatusCode != nil {
		return e.Response.StatusCode
	}
	return nil
}

// NormalizeProviderError extracts HTTP status, body text, and cause detail from provider errors.
func NormalizeProviderError(value any) NormalizedProviderError {
	err, isErr := value.(error)
	if !isErr || err == nil {
		return NormalizedProviderError{
			Message:            SafeJsonStringify(value),
			MessageCarriesBody: false,
		}
	}

	status := uebExtractStatus(err)
	body := uebExtractBody(err)
	rawMsg := err.Error()
	messageCarriesBody := body == nil || strings.Contains(rawMsg, *body)
	message := rawMsg
	if status == nil && body == nil {
		message = WithCauseDetail(err)
	}
	return NormalizedProviderError{
		Status:             status,
		Body:               body,
		Message:            message,
		MessageCarriesBody: messageCarriesBody,
	}
}

// WithCauseDetail appends the deepest cause message (up to depth 5) and optional error code
// when not already included in err.Error().
func WithCauseDetail(err error) string {
	if err == nil {
		return ""
	}
	var deepest error
	current := errors.Unwrap(err)
	for depth := 0; depth < uebMaxCauseDepth && current != nil; depth++ {
		deepest = current
		current = errors.Unwrap(current)
	}
	topMsg := err.Error()
	if deepest == nil {
		return topMsg
	}

	detail := deepest.Error()
	if detail == "" {
		if named, ok := deepest.(interface{ Name() string }); ok {
			detail = named.Name()
		}
	}
	detail = js.Trim(detail)

	if code := uebExtractErrorCode(deepest); code != "" && !strings.Contains(detail, code) {
		if detail != "" {
			detail = detail + " (" + code + ")"
		} else {
			detail = code
		}
	}

	if detail == "" || strings.Contains(topMsg, detail) {
		return topMsg
	}
	if topMsg != "" {
		return topMsg + ": " + detail
	}
	return detail
}

func uebExtractErrorCode(err error) string {
	if coded, ok := err.(interface{ ErrorCode() any }); ok {
		switch c := coded.ErrorCode().(type) {
		case string:
			return c
		case int:
			return strconv.Itoa(c)
		case int64:
			return strconv.FormatInt(c, 10)
		case float64:
			return jsonx.FormatNumber(c)
		}
	}
	return ""
}

func uebExtractStatus(err error) *int {
	var sdkErr *ProviderSdkError
	if errors.As(err, &sdkErr) {
		return sdkErr.ProviderStatus()
	}
	if s, ok := err.(interface{ ProviderStatus() *int }); ok {
		if p := s.ProviderStatus(); p != nil {
			v := *p
			return &v
		}
	}
	if s, ok := err.(interface{ StatusCode() int }); ok {
		v := s.StatusCode()
		return &v
	}
	if s, ok := err.(interface{ HTTPStatusCode() int }); ok {
		v := s.HTTPStatusCode()
		return &v
	}
	return nil
}

func uebExtractBody(err error) *string {
	bodyText, ok := uebPickBodyText(err)
	if !ok {
		return nil
	}
	trimmed := js.Trim(bodyText)
	if trimmed == "" {
		return nil
	}
	truncated := TruncateErrorText(trimmed, MaxProviderErrorBodyChars)
	return &truncated
}

func uebPickBodyText(err error) (string, bool) {
	var sdkErr *ProviderSdkError
	if errors.As(err, &sdkErr) {
		if s, ok := sdkErr.Body.(string); ok {
			return s, true
		}
		if uebIsPlainNonEmptyObject(sdkErr.ParsedError) {
			return SafeJsonStringify(sdkErr.ParsedError), true
		}
		if sdkErr.Response != nil {
			rb := sdkErr.Response.Body
			if s, ok := rb.(string); ok {
				return s, true
			}
			if uebIsStreamLike(rb) {
				return "", false
			}
			if uebIsPlainNonEmptyObject(rb) {
				return SafeJsonStringify(rb), true
			}
		}
		return "", false
	}
	if b, ok := err.(interface{ ErrorBody() string }); ok {
		return b.ErrorBody(), true
	}
	return "", false
}

func uebIsStreamLike(v any) bool {
	if v == nil {
		return false
	}
	if _, ok := v.(interface{ Read([]byte) (int, error) }); ok {
		return true
	}
	if _, ok := v.(interface{ Pipe() }); ok {
		return true
	}
	return false
}

func uebIsPlainNonEmptyObject(v any) bool {
	if v == nil || uebIsStreamLike(v) {
		return false
	}
	switch obj := v.(type) {
	case *jsonx.Object:
		return obj != nil && obj.Len() > 0
	case map[string]any:
		return len(obj) > 0
	case map[string]string:
		return len(obj) > 0
	default:
		// Plain Go map values count as plain JSON objects; arbitrary structs (SDK wrapper types) do not.
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Map {
			return rv.Len() > 0
		}
		return false
	}
}

// FormatProviderError composes a display string from a NormalizedProviderError.
func FormatProviderError(norm NormalizedProviderError, prefix ...string) string {
	var pfx *string
	if len(prefix) > 0 {
		pfx = &prefix[0]
	}
	if norm.MessageCarriesBody || norm.Status == nil || norm.Body == nil {
		if pfx != nil && norm.Status != nil {
			return fmt.Sprintf("%s (%d): %s", *pfx, *norm.Status, norm.Message)
		}
		return norm.Message
	}
	if pfx != nil {
		return fmt.Sprintf("%s (%d): %s", *pfx, *norm.Status, *norm.Body)
	}
	return fmt.Sprintf("%d: %s", *norm.Status, *norm.Body)
}

// TruncateErrorText truncates text to maxChars UTF-16 code units without holding large backing buffers alive.
func TruncateErrorText(text string, maxChars int) string {
	n := js.Len(text)
	if n <= maxChars {
		return text
	}
	excerpt := strings.Clone(js.Slice(text, 0, maxChars))
	return fmt.Sprintf("%s... [truncated %d chars]", excerpt, n-maxChars)
}

// SafeJsonStringify converts value to JSON string, falling back to fmt.Sprint on error.
func SafeJsonStringify(value any) string {
	if value == nil {
		return "null"
	}
	s, err := jsonx.Stringify(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return s
}
