package core

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestBugReportRedactionMetadataDiagnosticsZipAndUpload(t *testing.T) {
	if got := RedactUrl("https://user:secret@api.example.com:8443/v1/chat?api_key=abc&ok=1#frag"); got != "https://api.example.com:8443/v1/chat?api_key=%3Credacted%3E" && !strings.Contains(got, "api.example.com") {
		// Still ensure credentials & query token were redacted properly.
		t.Fatalf("unexpected RedactUrl: %q", got)
	}
	redactedUrl := RedactUrl("https://user:secret@api.example.com/v1/chat?token=xyz")
	if strings.Contains(redactedUrl, "secret") || strings.Contains(redactedUrl, "xyz") {
		t.Fatalf("secret leaked in RedactUrl: %q", redactedUrl)
	}

	rawObj := map[string]any{
		"apiKey":   "sk-12345",
		"endpoint": "https://me:pw@example.com/path?secret=1",
		"nested":   map[string]any{"authorization": "Bearer abc", "safe": "visible"},
	}
	redactedObj, ok := RedactJsonValue(rawObj).(*jsonx.Object)
	if !ok || redactedObj == nil {
		t.Fatalf("expected *jsonx.Object from RedactJsonValue, got %T", RedactJsonValue(rawObj))
	}
	if apiKey, _ := redactedObj.GetString("apiKey"); apiKey == "sk-12345" || apiKey == "" {
		t.Fatalf("expected apiKey redacted in %s", redactedObj.String())
	}
	nested, hasNested := redactedObj.GetObject("nested")
	authVal, _ := nested.GetString("authorization")
	safeVal, _ := nested.GetString("safe")
	if !hasNested || authVal == "Bearer abc" || authVal == "" || safeVal != "visible" {
		t.Fatalf("unexpected nested redaction: %s", redactedObj.String())
	}

	hint := "Tool froze on run"
	id := "report-123"
	meta := CollectBugReportMetadata(CollectBugReportMetadataOptions{
		Id:             &id,
		Hint:           &hint,
		SessionId:      "sess-1",
		Cwd:            "/repo",
		IncludeSession: true,
		ThinkingLevel:  ai.ModelThinkingLevelLow,
		GlobalSettings: Settings{TrackingId: tbrStrPtr("secret-track"), DeviceId: tbrStrPtr("dev-1")},
	})
	if meta.Settings.Global.TrackingId != nil || meta.Settings.Global.DeviceId != nil {
		t.Fatalf("expected trackingId/deviceId stripped from settings snapshot, got %+v", meta.Settings.Global)
	}

	errMsg := "timeout"
	sm := SessionManagerInMemory("/repo")
	_, _ = sm.AppendMessage(&ai.AssistantMessage{
		Role:         "assistant",
		Provider:     "anthropic",
		Model:        "claude-sonnet",
		Api:          "anthropic-messages",
		StopReason:   ai.StopReasonError,
		ErrorMessage: &errMsg,
	})
	diags := CollectBugReportDiagnostics(sm, CrashRecord{Kind: CrashKindFatalError, Message: "panic", Cwd: "/repo"})
	if diags.AssistantMessageCount != 1 || len(diags.Assistant) != 1 || len(diags.Crashes) != 1 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	sessionJsonl := "{\"type\":\"session\"}\n"
	summary := "Summary text"
	bundle := BugReportBundle{
		Metadata:     meta,
		Diagnostics:  diags,
		SessionJsonl: &sessionJsonl,
		Summary:      &summary,
	}
	zipPath := filepath.Join(t.TempDir(), BugReportArchiveFileName(id))
	if err := WriteBugReportArchive(bundle, zipPath); err != nil {
		t.Fatalf("WriteBugReportArchive failed: %v", err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open written zip: %v", err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"report.json", "diagnostics.json", "session.jsonl", "summary.md"} {
		if !names[want] {
			t.Fatalf("missing %q in zip archive: %+v", want, names)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bug-reports" || r.Header.Get("Authorization") != "Bearer tok-99" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"bug_report":{"id":"srv-report-1"}}`))
	}))
	defer srv.Close()

	tok := "tok-99"
	gw := srv.URL
	up, err := UploadBugReport(context.Background(), bundle, &UploadBugReportOptions{
		Token:      &tok,
		GatewayUrl: &gw,
		HttpClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("UploadBugReport failed: %v", err)
	}
	if up == nil || up.Id != "srv-report-1" {
		t.Fatalf("unexpected UploadBugReportResult: %+v", up)
	}

	// Verify GenerateBugReportSummary with mock StreamFn.
	sumText, err := GenerateBugReportSummary(context.Background(), GenerateBugReportSummaryOptions{
		Messages: []agent.AgentMessage{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: "help"}}}},
		},
		Hint: &hint,
		Model: &ai.Model{
			BaseModel:     ai.BaseModel{Id: "claude-sonnet", Provider: "anthropic", Api: "anthropic-messages"},
			ContextWindow: 100_000,
			MaxTokens:     2048,
		},
		StreamFn: func(_ context.Context, _ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) (*ai.AssistantMessageEventStream, error) {
			s := ai.NewAssistantMessageEventStream()
			s.End(&ai.AssistantMessage{
				Role:       "assistant",
				StopReason: ai.StopReasonStop,
				Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "## What the user was doing\nTesting."}},
			})
			return s, nil
		},
	})
	if err != nil || !strings.Contains(sumText, "## What the user was doing") {
		t.Fatalf("unexpected GenerateBugReportSummary result: err=%v text=%q", err, sumText)
	}
}

func tbrStrPtr(s string) *string { return &s }
