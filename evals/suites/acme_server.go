// Ported from packages/evals/evals/acme-server.ts (pi v1.0.0).

package suites

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	OpenaiProviderId    = "acme"
	OpenaiModelId       = "acme-chat"
	OpenaiProbePrompt   = "Reply with ACME_OK."
	OpenaiProbeResponse = "ACME_OK"
	StreamProviderId    = "acme-stream"
	StreamModelId       = "acme-stream-chat"
	StreamProbePrompt   = "Reply with ACME_STREAM_OK."
	StreamProbeResponse = "ACME_STREAM_OK"
)

// StreamApiDocumentation describes the Acme Streaming NDJSON API expected by custom-provider evals.
var StreamApiDocumentation = acmeBuildStreamApiDocumentation()

func acmeBuildStreamApiDocumentation() string {
	doc := jsonx.ObjectOf(
		"name", "Acme Streaming API",
		"request", jsonx.ObjectOf(
			"method", "POST",
			"path", "/generate",
			"headers", jsonx.ObjectOf(
				"content-type", "application/json",
				"x-acme-key", "resolved credential",
			),
			"body", jsonx.ObjectOf(
				"model", StreamModelId,
				"messages", []any{
					jsonx.ObjectOf("role", "user", "content", "Hello"),
				},
				"stream", true,
			),
		),
		"response", jsonx.ObjectOf(
			"contentType", "application/x-ndjson",
			"events", []any{
				jsonx.ObjectOf("type", "text_delta", "text", "Hello"),
				jsonx.ObjectOf("type", "usage", "input_tokens", 3, "output_tokens", 2),
				jsonx.ObjectOf("type", "done", "reason", "stop"),
			},
		),
	)
	encoded, err := jsonx.MarshalIndent(doc, "  ")
	if err != nil {
		panic(fmt.Sprintf("acmeBuildStreamApiDocumentation: %v", err))
	}
	return string(encoded) + "\n"
}

// AcmeServerMode selects which fixture protocol an AcmeServer serves.
type AcmeServerMode string

const (
	AcmeServerModeOpenai AcmeServerMode = "openai"
	AcmeServerModeStream AcmeServerMode = "stream"
)

// AcmeServer is a local HTTP test server validating custom-provider and OpenAI-compatible probes.
type AcmeServer interface {
	Start() error
	Stop() error
	Reset()
	Origin() string
	BaseUrl() string
	ValidRequestReceived() bool
}

type acmeServer struct {
	mode                 AcmeServerMode
	mu                   sync.Mutex
	server               *http.Server
	listener             net.Listener
	serverOrigin         string
	receivedValidRequest bool
}

// CreateAcmeServer creates a local fixture HTTP server for the given mode.
func CreateAcmeServer(mode AcmeServerMode) AcmeServer {
	return &acmeServer{mode: mode}
}

func acmeRejectRequest(w http.ResponseWriter, status int, message string) {
	w.Header().Set("content-type", "text/plain")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message)
}

func (s *acmeServer) setReceivedValid(valid bool) {
	s.mu.Lock()
	s.receivedValidRequest = valid
	s.mu.Unlock()
}

func (s *acmeServer) handle(w http.ResponseWriter, r *http.Request) {
	expectedPath := "/generate"
	if s.mode == AcmeServerModeOpenai {
		expectedPath = "/v1/chat/completions"
	}
	if r.URL.Path != expectedPath {
		acmeRejectRequest(w, http.StatusNotFound, "Unknown endpoint")
		return
	}
	if r.Method != http.MethodPost {
		acmeRejectRequest(w, http.StatusMethodNotAllowed, "Expected POST")
		return
	}
	if !strings.HasPrefix(r.Header.Get("content-type"), "application/json") {
		acmeRejectRequest(w, http.StatusUnsupportedMediaType, "Expected application/json")
		return
	}
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		acmeRejectRequest(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	parsed, err := jsonx.Parse(rawBody)
	if err != nil {
		acmeRejectRequest(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	payload, ok := parsed.(*jsonx.Object)
	if !ok || payload == nil {
		acmeRejectRequest(w, http.StatusUnprocessableEntity, "Expected a JSON object")
		return
	}

	var prompt string
	var hasPrompt bool
	if rawMessages, ok := payload.Get("messages"); ok {
		if messages, ok := rawMessages.([]any); ok {
			for _, item := range messages {
				msgObj, ok := item.(*jsonx.Object)
				if !ok || msgObj == nil {
					continue
				}
				if role, ok := msgObj.GetString("role"); ok && role == "user" {
					if content, ok := msgObj.GetString("content"); ok {
						prompt = content
						hasPrompt = true
					}
					break
				}
			}
		}
	}

	modelVal, _ := payload.GetString("model")
	streamVal, hasStream := payload.GetBool("stream")

	if s.mode == AcmeServerModeStream {
		if r.Header.Get("x-acme-key") != "resolved-stream-key" {
			acmeRejectRequest(w, http.StatusUnauthorized, "Invalid Acme Stream credential")
			return
		}
		if modelVal != StreamModelId || !hasPrompt || !hasStream || !streamVal {
			acmeRejectRequest(w, http.StatusUnprocessableEntity, "Invalid Acme Stream request")
			return
		}
		s.setReceivedValid(prompt == StreamProbePrompt)
		w.Header().Set("content-type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		line1, _ := jsonx.Stringify(jsonx.ObjectOf("type", "text_delta", "text", "ACME_"))
		line2, _ := jsonx.Stringify(jsonx.ObjectOf("type", "text_delta", "text", "STREAM_OK"))
		line3, _ := jsonx.Stringify(jsonx.ObjectOf("type", "usage", "input_tokens", 4, "output_tokens", 3))
		line4, _ := jsonx.Stringify(jsonx.ObjectOf("type", "done", "reason", "stop"))
		_, _ = io.WriteString(w, line1+"\n"+line2+"\n"+line3+"\n"+line4+"\n")
		return
	}

	if r.Header.Get("authorization") != "Bearer resolved-acme-key" {
		acmeRejectRequest(w, http.StatusUnauthorized, "Invalid Acme credential")
		return
	}
	if modelVal != OpenaiModelId || !hasPrompt || !hasStream || !streamVal {
		acmeRejectRequest(w, http.StatusUnprocessableEntity, "Invalid OpenAI-compatible request")
		return
	}
	s.setReceivedValid(prompt == OpenaiProbePrompt)
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.WriteHeader(http.StatusOK)

	chunk1, _ := jsonx.Stringify(jsonx.ObjectOf(
		"id", "chatcmpl-acme",
		"object", "chat.completion.chunk",
		"created", 0,
		"model", OpenaiModelId,
		"choices", []any{
			jsonx.ObjectOf(
				"index", 0,
				"delta", jsonx.ObjectOf("role", "assistant", "content", OpenaiProbeResponse),
				"finish_reason", nil,
			),
		},
	))
	chunk2, _ := jsonx.Stringify(jsonx.ObjectOf(
		"id", "chatcmpl-acme",
		"object", "chat.completion.chunk",
		"created", 0,
		"model", OpenaiModelId,
		"choices", []any{
			jsonx.ObjectOf(
				"index", 0,
				"delta", jsonx.NewObject(),
				"finish_reason", "stop",
			),
		},
		"usage", jsonx.ObjectOf("prompt_tokens", 3, "completion_tokens", 2),
	))
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", chunk1, chunk2)
}

func (s *acmeServer) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return errors.New("Acme fixture server did not bind a TCP port.")
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(s.handle),
	}
	s.mu.Lock()
	s.listener = ln
	s.server = srv
	s.serverOrigin = fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port)
	s.mu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

func (s *acmeServer) Stop() error {
	s.mu.Lock()
	srv := s.server
	s.server = nil
	s.listener = nil
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(context.Background())
}

func (s *acmeServer) Reset() {
	s.setReceivedValid(false)
}

func (s *acmeServer) Origin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverOrigin
}

func (s *acmeServer) BaseUrl() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverOrigin + "/v1"
}

func (s *acmeServer) ValidRequestReceived() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.receivedValidRequest
}
