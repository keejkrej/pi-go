// Ported from packages/evals/test/acme-server.test.ts (pi v1.0.0).

package suites_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/evals/suites"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tasFixture struct {
	mode                suites.AcmeServerMode
	url                 func(server suites.AcmeServer) string
	headers             map[string]string
	unauthorizedHeaders map[string]string
	model               string
	prompt              string
	response            string
	contentType         string
	unauthorized        string
	invalid             string
}

func tasBody(model string, prompt string, stream bool) *jsonx.Object {
	return jsonx.ObjectOf(
		"model", model,
		"messages", []any{
			jsonx.ObjectOf("role", "user", "content", prompt),
		},
		"stream", stream,
	)
}

func tasPost(t *testing.T, url string, headers map[string]string, payload *jsonx.Object) (int, http.Header, string) {
	t.Helper()
	bodyBytes, err := jsonx.Marshal(payload)
	if err != nil {
		t.Fatalf("jsonx.Marshal failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP POST failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	return resp.StatusCode, resp.Header, string(raw)
}

func tasStreamedText(t *testing.T, mode suites.AcmeServerMode, raw string) string {
	t.Helper()
	var out strings.Builder
	lines := strings.Split(raw, "\n")
	if mode == suites.AcmeServerModeStream {
		for _, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			parsed, err := jsonx.ParseString(line)
			if err != nil {
				t.Fatalf("invalid ndjson line %q: %v", line, err)
			}
			obj, ok := parsed.(*jsonx.Object)
			if !ok {
				continue
			}
			if typ, _ := obj.GetString("type"); typ == "text_delta" {
				if text, ok := obj.GetString("text"); ok {
					out.WriteString(text)
				}
			}
		}
		return out.String()
	}

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		parsed, err := jsonx.ParseString(strings.TrimPrefix(line, "data: "))
		if err != nil {
			t.Fatalf("invalid SSE data line %q: %v", line, err)
		}
		obj, ok := parsed.(*jsonx.Object)
		if !ok {
			continue
		}
		rawChoices, ok := obj.Get("choices")
		if !ok {
			continue
		}
		choices, ok := rawChoices.([]any)
		if !ok || len(choices) == 0 {
			continue
		}
		choice0, ok := choices[0].(*jsonx.Object)
		if !ok {
			continue
		}
		rawDelta, ok := choice0.Get("delta")
		if !ok {
			continue
		}
		delta, ok := rawDelta.(*jsonx.Object)
		if !ok {
			continue
		}
		if content, ok := delta.GetString("content"); ok {
			out.WriteString(content)
		}
	}
	return out.String()
}

func TestAcmeServer(t *testing.T) {
	fixtures := []tasFixture{
		{
			mode:                suites.AcmeServerModeOpenai,
			url:                 func(s suites.AcmeServer) string { return s.BaseUrl() + "/chat/completions" },
			headers:             map[string]string{"authorization": "Bearer resolved-acme-key"},
			unauthorizedHeaders: map[string]string{"authorization": "Bearer wrong-key"},
			model:               suites.OpenaiModelId,
			prompt:              suites.OpenaiProbePrompt,
			response:            suites.OpenaiProbeResponse,
			contentType:         "text/event-stream",
			unauthorized:        "Invalid Acme credential",
			invalid:             "Invalid OpenAI-compatible request",
		},
		{
			mode:                suites.AcmeServerModeStream,
			url:                 func(s suites.AcmeServer) string { return s.Origin() + "/generate" },
			headers:             map[string]string{"x-acme-key": "resolved-stream-key"},
			unauthorizedHeaders: map[string]string{"x-acme-key": "wrong-key"},
			model:               suites.StreamModelId,
			prompt:              suites.StreamProbePrompt,
			response:            suites.StreamProbeResponse,
			contentType:         "application/x-ndjson",
			unauthorized:        "Invalid Acme Stream credential",
			invalid:             "Invalid Acme Stream request",
		},
	}

	for _, fixture := range fixtures {
		t.Run(string(fixture.mode)+" fixture", func(t *testing.T) {
			server := suites.CreateAcmeServer(fixture.mode)
			if err := server.Start(); err != nil {
				t.Fatalf("server.Start failed: %v", err)
			}
			t.Cleanup(func() {
				_ = server.Stop()
			})

			t.Run("accepts the probe request and records it", func(t *testing.T) {
				server.Reset()
				status, hdrs, body := tasPost(t, fixture.url(server), fixture.headers, tasBody(fixture.model, fixture.prompt, true))
				if status != 200 {
					t.Fatalf("status = %d, want 200 (body=%s)", status, body)
				}
				if ct := hdrs.Get("content-type"); !strings.Contains(ct, fixture.contentType) {
					t.Fatalf("content-type = %q, want substring %q", ct, fixture.contentType)
				}
				if got := tasStreamedText(t, fixture.mode, body); got != fixture.response {
					t.Fatalf("streamedText = %q, want %q", got, fixture.response)
				}
				if !server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = true")
				}
			})

			t.Run("rejects a bad credential with 401 and does not record a probe", func(t *testing.T) {
				server.Reset()
				status, _, body := tasPost(t, fixture.url(server), fixture.unauthorizedHeaders, tasBody(fixture.model, fixture.prompt, true))
				if status != 401 {
					t.Fatalf("status = %d, want 401", status)
				}
				if body != fixture.unauthorized {
					t.Fatalf("body = %q, want %q", body, fixture.unauthorized)
				}
				if server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = false")
				}
			})

			t.Run("rejects a malformed request with 422 and does not record a probe", func(t *testing.T) {
				server.Reset()
				status, _, body := tasPost(t, fixture.url(server), fixture.headers, tasBody(fixture.model, fixture.prompt, false))
				if status != 422 {
					t.Fatalf("status = %d, want 422", status)
				}
				if body != fixture.invalid {
					t.Fatalf("body = %q, want %q", body, fixture.invalid)
				}
				if server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = false")
				}
			})

			t.Run("does not treat a non-probe success as the probe", func(t *testing.T) {
				server.Reset()
				status, _, _ := tasPost(t, fixture.url(server), fixture.headers, tasBody(fixture.model, "hello", true))
				if status != 200 {
					t.Fatalf("status = %d, want 200", status)
				}
				if server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = false")
				}
			})

			t.Run("clears the probe flag on reset", func(t *testing.T) {
				server.Reset()
				_, _, _ = tasPost(t, fixture.url(server), fixture.headers, tasBody(fixture.model, fixture.prompt, true))
				if !server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = true")
				}
				server.Reset()
				if server.ValidRequestReceived() {
					t.Fatalf("expected ValidRequestReceived() = false after Reset")
				}
			})
		})
	}
}
