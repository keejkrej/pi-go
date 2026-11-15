// Ported from packages/coding-agent/test/session-share.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func TestExportSessionForShareIncludesTrailingMetadata(t *testing.T) {
	fx := tithNewRuntimeFixture(t, nil)
	session := fx.Runtime.Session

	fx.Faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("shared answer"), nil)},
	})
	if err := session.Prompt(context.Background(), "hello share", nil); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "share.jsonl")
	if err := ExportSessionForShare(outPath, session); err != nil {
		t.Fatalf("ExportSessionForShare: %v", err)
	}
	rawBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", outPath, err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawBytes)), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected session header + messages + trailing pi.share entry, got %d lines", len(lines))
	}
	var trailing map[string]any
	if err := jsonx.Unmarshal([]byte(lines[len(lines)-1]), &trailing); err != nil {
		t.Fatalf("Unmarshal trailing line: %v", err)
	}
	if trailing["type"] != "custom" || trailing["customType"] != "pi.share" {
		t.Fatalf("unexpected trailing share entry: %+v", trailing)
	}
}

func TestShareSessionViaRadiusAndMissingAuthError(t *testing.T) {
	radKey := "rad-test-token"
	radProv := string(core.RadiusProviderId)
	fx := tithNewRuntimeFixture(t, &tithRuntimeOptions{
		ExtraAuthKeys: map[string]ai.Credential{
			radProv: &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &radKey},
		},
	})
	session := fx.Runtime.Session
	if _, err := session.SessionManager.AppendSessionInfo("shared"); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}

	// Register Radius provider in ModelRegistry so ssTryShareViaRadius takes the Radius path.
	radBase := "https://radius.invalid"
	radApi := string(ai.KnownApiOpenaiCompletions)
	if err := fx.Runtime.Services.ModelRegistry.RegisterProvider(radProv, core.ProviderConfigInput{
		BaseUrl: &radBase,
		Api:     &radApi,
		ApiKey:  &radKey,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:   "rad-1",
					Name: "Rad 1",
				},
			},
		},
	}); err != nil {
		t.Fatalf("RegisterProvider(radius): %v", err)
	}

	var reqMu sync.Mutex
	var authHeader string
	var bodyLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqMu.Lock()
		authHeader = r.Header.Get("Authorization")
		bodyLen = len(b)
		reqMu.Unlock()
		if r.URL.Path != "/v1/artifacts" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"https://radius.example/artifacts/share-42"}}`))
	}))
	defer srv.Close()

	term := tithNewVirtualTerminal(80, 24)
	t.Cleanup(term.tithClose)
	ui := CreateInteractiveTui(InteractiveTuiOptions{TuiMode: tui.TuiModeRegular, Terminal: term})
	editorContainer := tui.NewContainer()
	editor := &tithStubComponent{lines: []string{">"}}
	editorContainer.AddChild(editor)
	ui.AddChild(editorContainer)
	ui.Start()
	t.Cleanup(func() { ui.Stop(nil) })

	var mu sync.Mutex
	var statuses []string
	var errs []string
	ShareSession(context.Background(), &SessionShareContext{
		Session:         session,
		UI:              ui,
		EditorContainer: editorContainer,
		Editor:          editor,
		HttpClient:      srv.Client(),
		GatewayUrl:      &srv.URL,
		ShowStatus: func(m string) {
			mu.Lock()
			statuses = append(statuses, m)
			mu.Unlock()
		},
		ShowError: func(m string) {
			mu.Lock()
			errs = append(errs, m)
			mu.Unlock()
		},
	})

	reqMu.Lock()
	gotAuth := authHeader
	gotBodyLen := bodyLen
	reqMu.Unlock()
	if gotAuth != "Bearer rad-test-token" || gotBodyLen == 0 {
		t.Fatalf("unexpected Radius upload request: auth=%q bodyLen=%d", gotAuth, gotBodyLen)
	}
	mu.Lock()
	joinedStatus := strings.Join(statuses, "\n")
	joinedErr := strings.Join(errs, "\n")
	mu.Unlock()
	if joinedErr != "" {
		t.Fatalf("unexpected share error: %s", joinedErr)
	}
	if !strings.Contains(joinedStatus, "https://radius.example/artifacts/share-42") {
		t.Fatalf("expected share URL in status, got:\n%s", joinedStatus)
	}

	// Verify explicit radius destination reports login hint when Radius auth is missing.
	fxNoAuth := tithNewRuntimeFixture(t, nil)
	fxNoAuth.Runtime.Session.SettingsManager.SetSessionShareDestination(core.SessionShareDestinationRadius)
	var errMsg string
	ShareSession(context.Background(), &SessionShareContext{
		Session:   fxNoAuth.Runtime.Session,
		ShowError: func(m string) { errMsg = m },
	})
	if !strings.Contains(errMsg, "Radius authentication is not configured") {
		t.Fatalf("expected missing Radius auth error, got %q", errMsg)
	}
}
