// Ported from packages/ai/src/utils/models-error.ts (pi v1.5.2).

package ai

import "strings"

// ModelsErrorCode categorizes errors returned by the Models registry and runtime.
type ModelsErrorCode string

const (
	ModelsErrorCodeModelSource     ModelsErrorCode = "model_source"
	ModelsErrorCodeModelValidation ModelsErrorCode = "model_validation"
	ModelsErrorCodeProvider        ModelsErrorCode = "provider"
	ModelsErrorCodeStream          ModelsErrorCode = "stream"
	ModelsErrorCodeAuth            ModelsErrorCode = "auth"
	ModelsErrorCodeOAuth           ModelsErrorCode = "oauth"
)

// ModelsError is the structured error returned by Models operations.
type ModelsError struct {
	Code    ModelsErrorCode
	Message string
	Cause   error
}

// NewModelsError builds a ModelsError and folds any non-empty cause detail into Message.
func NewModelsError(code ModelsErrorCode, message string, cause ...error) *ModelsError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &ModelsError{
		Code:    code,
		Message: umeWithCauseDetail(message, c),
		Cause:   c,
	}
}

func (e *ModelsError) Error() string { return e.Message }
func (e *ModelsError) Name() string  { return "ModelsError" }
func (e *ModelsError) Unwrap() error { return e.Cause }

func umeWithCauseDetail(message string, cause error) string {
	if cause == nil {
		return message
	}
	detail := strings.TrimSpace(FormatThrownValue(cause))
	if detail == "" || strings.Contains(message, detail) {
		return message
	}
	return message + ": " + detail
}
