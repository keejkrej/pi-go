package ai_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type sdkHttpResponseStream struct{}

func (sdkHttpResponseStream) Pipe() {}

type sdkHttpResponseBody struct {
	Locked bool
}

type sdkInnerError struct {
	Code string
}

type codedError struct {
	msg  string
	code string
}

func (e *codedError) Error() string  { return e.msg }
func (e *codedError) ErrorCode() any { return e.code }

func TestNormalizeProviderError(t *testing.T) {
	t.Run("extracts status and body from a Mistral-shaped error", func(t *testing.T) {
		status := 403
		err := &ai.ProviderSdkError{
			Message:    "Mistral request failed",
			StatusCode: &status,
			Body:       `{"error":"blocked by gateway WAF"}`,
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 403 {
			t.Fatalf("Status = %v, want 403", norm.Status)
		}
		if norm.Body == nil || *norm.Body != `{"error":"blocked by gateway WAF"}` {
			t.Fatalf("Body = %v", norm.Body)
		}
		if norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = false")
		}
	})

	t.Run("reads the parsed body off an openai APIError when the message is opaque", func(t *testing.T) {
		status := 403
		parsed := jsonx.NewObject()
		parsed.Set("error", "blocked by gateway WAF")
		err := &ai.ProviderSdkError{
			Message:     "403 status code (no body)",
			Status:      &status,
			ParsedError: parsed,
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 403 {
			t.Fatalf("Status = %v, want 403", norm.Status)
		}
		if norm.Body == nil || *norm.Body != `{"error":"blocked by gateway WAF"}` {
			t.Fatalf("Body = %v", norm.Body)
		}
		if norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = false")
		}
	})

	t.Run("preserves the message when genai already folds the body into it", func(t *testing.T) {
		body := `{"error":{"code":403,"message":"Permission denied"}}`
		status := 403
		err := &ai.ProviderSdkError{
			Message: body,
			Status:  &status,
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 403 {
			t.Fatalf("Status = %v, want 403", norm.Status)
		}
		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
		if norm.Message != body {
			t.Fatalf("Message = %q, want %q", norm.Message, body)
		}
	})

	t.Run("extracts status and body from a Bedrock-shaped ServiceException", func(t *testing.T) {
		status := 403
		err := &ai.ProviderSdkError{
			ErrName:  "UnknownError",
			Message:  "UnknownError",
			Metadata: &ai.ProviderSdkMetadata{HttpStatusCode: &status},
			Response: &ai.ProviderSdkResponse{StatusCode: &status, Body: `{"message":"blocked by gateway WAF"}`},
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 403 {
			t.Fatalf("Status = %v, want 403", norm.Status)
		}
		if norm.Body == nil || *norm.Body != `{"message":"blocked by gateway WAF"}` {
			t.Fatalf("Body = %v", norm.Body)
		}
		if norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = false")
		}
	})

	t.Run("ignores a Bedrock response stream instead of serializing its internals", func(t *testing.T) {
		status := 400
		err := &ai.ProviderSdkError{
			ErrName:  "ValidationException",
			Message:  "Invocation of model ID anthropic.claude-opus-5 with on-demand throughput isn't supported.",
			Metadata: &ai.ProviderSdkMetadata{HttpStatusCode: &status},
			Response: &ai.ProviderSdkResponse{StatusCode: &status, Body: sdkHttpResponseStream{}},
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 400 {
			t.Fatalf("Status = %v, want 400", norm.Status)
		}
		if norm.Body != nil {
			t.Fatalf("expected nil Body, got %q", *norm.Body)
		}
		if !strings.Contains(norm.Message, "on-demand throughput isn't supported") {
			t.Fatalf("Message = %q", norm.Message)
		}
		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
	})

	t.Run("ignores a struct response body without a pipe method instead of serializing it", func(t *testing.T) {
		status := 400
		err := &ai.ProviderSdkError{
			ErrName:  "ValidationException",
			Message:  "Input is too long for requested model.",
			Metadata: &ai.ProviderSdkMetadata{HttpStatusCode: &status},
			Response: &ai.ProviderSdkResponse{StatusCode: &status, Body: &sdkHttpResponseBody{}},
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Status == nil || *norm.Status != 400 {
			t.Fatalf("Status = %v, want 400", norm.Status)
		}
		if norm.Body != nil {
			t.Fatalf("expected nil Body, got %q", *norm.Body)
		}
		if !strings.Contains(norm.Message, "Input is too long") {
			t.Fatalf("Message = %q", norm.Message)
		}
		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
	})

	t.Run("ignores a struct ParsedError field instead of serializing it", func(t *testing.T) {
		status := 502
		err := &ai.ProviderSdkError{
			Message:     "TLS handshake failed",
			Status:      &status,
			ParsedError: &sdkInnerError{Code: "EPROTO"},
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Body != nil {
			t.Fatalf("expected nil Body, got %q", *norm.Body)
		}
		if norm.Message != "TLS handshake failed" {
			t.Fatalf("Message = %q", norm.Message)
		}
		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
	})

	t.Run("still surfaces a plain parsed JSON body object", func(t *testing.T) {
		status := 400
		parsed := jsonx.NewObject()
		parsed.Set("message", "schema validation failed")
		parsed.Set("field", "tools[0]")
		err := &ai.ProviderSdkError{
			Message:     "400 status code (no body)",
			Status:      &status,
			ParsedError: parsed,
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Body == nil || *norm.Body != `{"message":"schema validation failed","field":"tools[0]"}` {
			t.Fatalf("Body = %v", norm.Body)
		}
		if norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = false")
		}
	})

	t.Run("JSON-stringifies a non-error thrown value", func(t *testing.T) {
		val := jsonx.NewObject()
		val.Set("reason", "boom")

		norm := ai.NormalizeProviderError(val)

		if norm.Status != nil || norm.Body != nil {
			t.Fatalf("expected nil Status and Body, got %+v", norm)
		}
		if norm.Message != `{"reason":"boom"}` {
			t.Fatalf("Message = %q", norm.Message)
		}
		if norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = false")
		}
	})

	t.Run("treats an empty parsed body object as no body", func(t *testing.T) {
		status := 403
		err := &ai.ProviderSdkError{
			Message:     "403 status code (no body)",
			Status:      &status,
			ParsedError: jsonx.NewObject(),
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Body != nil {
			t.Fatalf("expected nil Body, got %q", *norm.Body)
		}
		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
	})

	t.Run("truncates the body at the cap", func(t *testing.T) {
		status := 500
		longBody := strings.Repeat("x", ai.MaxProviderErrorBodyChars+50)
		err := &ai.ProviderSdkError{
			Message:    "failed",
			StatusCode: &status,
			Body:       longBody,
		}

		norm := ai.NormalizeProviderError(err)

		if norm.Body == nil || !strings.Contains(*norm.Body, "... [truncated 50 chars]") {
			t.Fatalf("Body = %v", norm.Body)
		}
		if len(*norm.Body) >= len(longBody) {
			t.Fatalf("expected truncated length < %d, got %d", len(longBody), len(*norm.Body))
		}
	})

	t.Run("sets messageCarriesBody when the message already contains the extracted body", func(t *testing.T) {
		status := 500
		err := &ai.ProviderSdkError{
			Message:    "500: upstream exploded",
			StatusCode: &status,
			Body:       "upstream exploded",
		}

		norm := ai.NormalizeProviderError(err)

		if !norm.MessageCarriesBody {
			t.Fatal("expected MessageCarriesBody = true")
		}
	})
}

func TestFormatProviderError(t *testing.T) {
	t.Run("surfaces status and body without a prefix", func(t *testing.T) {
		status := 403
		parsed := jsonx.NewObject()
		parsed.Set("error", "blocked by gateway WAF")
		norm := ai.NormalizeProviderError(&ai.ProviderSdkError{
			Message:     "403 status code (no body)",
			Status:      &status,
			ParsedError: parsed,
		})

		formatted := ai.FormatProviderError(norm)

		if !strings.Contains(formatted, "403") || !strings.Contains(formatted, "blocked by gateway WAF") || formatted == "403 status code (no body)" {
			t.Fatalf("formatted = %q", formatted)
		}
	})

	t.Run("applies a provider prefix with status and body", func(t *testing.T) {
		status := 403
		parsed := jsonx.NewObject()
		parsed.Set("error", "blocked by gateway WAF")
		norm := ai.NormalizeProviderError(&ai.ProviderSdkError{
			Message:     "403 status code (no body)",
			Status:      &status,
			ParsedError: parsed,
		})

		got := ai.FormatProviderError(norm, "OpenAI API error")
		want := `OpenAI API error (403): {"error":"blocked by gateway WAF"}`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("preserves the message with prefix and status when it already carries the body", func(t *testing.T) {
		body := `{"error":{"message":"Permission denied"}}`
		status := 403
		norm := ai.NormalizeProviderError(&ai.ProviderSdkError{
			Message: body,
			Status:  &status,
		})

		got := ai.FormatProviderError(norm, "OpenAI API error")
		want := "OpenAI API error (403): " + body
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("returns the bare message for a non-error value", func(t *testing.T) {
		val := jsonx.NewObject()
		val.Set("reason", "boom")
		norm := ai.NormalizeProviderError(val)

		if got := ai.FormatProviderError(norm); got != `{"reason":"boom"}` {
			t.Fatalf("got %q", got)
		}
	})
}

func TestTruncateErrorText(t *testing.T) {
	body := strings.Repeat("<html>", 10_000)
	kept := ai.TruncateErrorText(body, ai.MaxProviderErrorBodyChars)
	if !strings.HasPrefix(kept, "<html><html>") {
		t.Fatalf("unexpected prefix: %q", kept[:20])
	}
	wantSuffix := fmt.Sprintf("... [truncated %d chars]", len(body)-ai.MaxProviderErrorBodyChars)
	if !strings.HasSuffix(kept, wantSuffix) {
		t.Fatalf("unexpected suffix: %q", kept[len(kept)-40:])
	}
}

func TestConnectionErrorCauseDetail(t *testing.T) {
	t.Run("appends the deepest cause of a connection error with no status or body", func(t *testing.T) {
		socketErr := &codedError{msg: "connect ECONNREFUSED 127.0.0.1:8080", code: "ECONNREFUSED"}
		fetchErr := fmt.Errorf("fetch failed: %w", socketErr)
		topErr := &ai.ProviderSdkError{
			Message: "Connection error.",
			Cause:   fetchErr,
		}

		got := ai.FormatProviderError(ai.NormalizeProviderError(topErr))
		want := "Connection error.: connect ECONNREFUSED 127.0.0.1:8080"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("adds the error code when the cause message lacks it", func(t *testing.T) {
		cause := &codedError{msg: "other side closed", code: "UND_ERR_SOCKET"}
		err := fmt.Errorf("Connection error.: %w", cause)
		// Wrap without duplicating cause text in top Message so WithCauseDetail appends it.
		top := &ai.ProviderSdkError{Message: "Connection error.", Cause: err}

		got := ai.WithCauseDetail(top)
		want := "Connection error.: other side closed (UND_ERR_SOCKET)"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("leaves errors without a cause unchanged", func(t *testing.T) {
		if got := ai.WithCauseDetail(errors.New("Connection error.")); got != "Connection error." {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("surfaces connection failure details from a closed local TCP listener", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()

		resp, reqErr := http.Get("http://" + addr + "/v1/chat/completions")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if reqErr == nil {
			t.Fatal("expected connection error")
		}
		top := &ai.ProviderSdkError{
			Message: "Connection error.",
			Cause:   reqErr,
		}
		got := ai.FormatProviderError(ai.NormalizeProviderError(top))
		if !strings.HasPrefix(got, "Connection error.: ") || !strings.Contains(got, "refused") {
			t.Fatalf("got %q, want connection refused detail", got)
		}
	})
}
