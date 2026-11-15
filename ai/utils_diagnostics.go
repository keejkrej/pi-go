// Ported from packages/ai/src/utils/diagnostics.ts (pi v1.5.2).

package ai

import (
	"fmt"
	"time"
)

// DiagnosticErrorInfo captures structured error details stored in assistant diagnostics.
type DiagnosticErrorInfo struct {
	Name    *string `json:"name,omitzero"`
	Message string  `json:"message"`
	Stack   *string `json:"stack,omitzero"`
	Code    any     `json:"code,omitzero"`
}

// AssistantMessageDiagnostic records redacted provider/runtime failure or recovery diagnostics.
type AssistantMessageDiagnostic struct {
	Type      string               `json:"type"`
	Timestamp int64                `json:"timestamp"`
	Error     *DiagnosticErrorInfo `json:"error,omitzero"`
	Details   JsonObject           `json:"details,omitzero"`
}

// FormatThrownValue formats an error or arbitrary thrown value identically to TS formatThrownValue.
func FormatThrownValue(value any) string {
	if value == nil {
		return "undefined"
	}
	if err, ok := value.(error); ok {
		if msg := err.Error(); msg != "" {
			return msg
		}
		if named, ok := value.(interface{ Name() string }); ok && named.Name() != "" {
			return named.Name()
		}
		return "Error"
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

// ExtractDiagnosticError extracts structured fields from a thrown error or value.
func ExtractDiagnosticError(value any) DiagnosticErrorInfo {
	err, isErr := value.(error)
	if !isErr || err == nil {
		name := "ThrownValue"
		return DiagnosticErrorInfo{
			Name:    &name,
			Message: FormatThrownValue(value),
		}
	}

	var namePtr *string
	errName := "Error"
	if named, ok := err.(interface{ Name() string }); ok {
		errName = named.Name()
	}
	if errName != "" {
		n := errName
		namePtr = &n
	}

	msg := err.Error()
	if msg == "" {
		msg = errName
	}

	var stackPtr *string
	if stacked, ok := err.(interface{ Stack() string }); ok {
		if s := stacked.Stack(); s != "" {
			stackPtr = &s
		}
	}

	var codeVal any
	if coded, ok := err.(interface{ ErrorCode() any }); ok {
		switch c := coded.ErrorCode().(type) {
		case string, int, int64, float64:
			codeVal = c
		}
	}

	return DiagnosticErrorInfo{
		Name:    namePtr,
		Message: msg,
		Stack:   stackPtr,
		Code:    codeVal,
	}
}

// CreateAssistantMessageDiagnostic creates a timestamped diagnostic entry.
func CreateAssistantMessageDiagnostic(typ string, err any, details JsonObject) *AssistantMessageDiagnostic {
	info := ExtractDiagnosticError(err)
	return &AssistantMessageDiagnostic{
		Type:      typ,
		Timestamp: time.Now().UnixMilli(),
		Error:     &info,
		Details:   details,
	}
}

// AppendAssistantMessageDiagnostic appends diagnostic to message.Diagnostics.
func AppendAssistantMessageDiagnostic(message *AssistantMessage, diagnostic *AssistantMessageDiagnostic) {
	if message == nil || diagnostic == nil {
		return
	}
	message.Diagnostics = append(message.Diagnostics, *diagnostic)
}
