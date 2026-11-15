// Ported from packages/coding-agent/test/bug-report.test.ts (pi v1.5.2).

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
	"time"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
)

func tbrWaitContainerContains(t *testing.T, ui tui.TUI, container *tui.Container, substr string) {
	t.Helper()
	tithWaitUntil(t, 3*time.Second, "bug-report UI contains "+substr, func() bool {
		if container == nil {
			return false
		}
		found := false
		done := make(chan struct{})
		ui.Post(func() {
			for _, child := range container.Children() {
				if strings.Contains(strings.Join(child.Render(80), "\n"), substr) {
					found = true
					break
				}
			}
			close(done)
		})
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		return found
	})
}

func TestBugReportInteractiveUploadFlow(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	fx := tithNewRuntimeFixture(t, nil)
	t.Setenv("PI_OFFLINE", "")

	var reqMu sync.Mutex
	var uploadedPath string
	var uploadedBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqMu.Lock()
		uploadedPath = r.URL.Path
		uploadedBytes = body
		reqMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"bug_report":{"id":"rep-123"}}`))
	}))
	defer srv.Close()

	term := tithNewVirtualTerminal(100, 30)
	t.Cleanup(term.tithClose)
	ui := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:  tui.TuiModeRegular,
		Terminal: term,
	})
	editorContainer := tui.NewContainer()
	editor := &tithStubComponent{lines: []string{"> editor"}}
	editorContainer.AddChild(editor)
	ui.AddChild(editorContainer)
	ui.Start()
	t.Cleanup(func() { ui.Stop(nil) })

	var statusMu sync.Mutex
	var statuses []string
	var errs []string
	bugCtx := &BugReportContext{
		Session:         fx.Runtime.Session,
		UI:              ui,
		EditorContainer: editorContainer,
		Editor:          editor,
		Keybindings:     core.KeybindingsManagerCreate(),
		HttpClient:      srv.Client(),
		GatewayUrl:      &srv.URL,
		ShowStatus: func(msg string) {
			statusMu.Lock()
			statuses = append(statuses, msg)
			statusMu.Unlock()
		},
		ShowError: func(msg string) {
			statusMu.Lock()
			errs = append(errs, msg)
			statusMu.Unlock()
		},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ReportBug(context.Background(), bugCtx, tithStrPtr("UI froze on resize"))
	}()

	// 1. Description input (pre-filled with hint) -> Enter submits
	tbrWaitContainerContains(t, ui, editorContainer, "Report a bug")
	term.tithSendInput("\r")

	// 2. Include session transcript? -> Enter selects "Yes, include the transcript"
	tbrWaitContainerContains(t, ui, editorContainer, "Include the session transcript?")
	term.tithSendInput("\r")

	// 3. Delivery method -> Enter selects "Upload Report"
	tbrWaitContainerContains(t, ui, editorContainer, "Upload Report")
	term.tithSendInput("\r")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ReportBug did not finish in time")
	}

	reqMu.Lock()
	gotPath := uploadedPath
	gotLen := len(uploadedBytes)
	reqMu.Unlock()
	if gotPath == "" || gotLen == 0 {
		t.Fatalf("expected upload request to mock server, got path=%q len=%d", gotPath, gotLen)
	}

	statusMu.Lock()
	joinedStatuses := strings.Join(statuses, "\n")
	joinedErrs := strings.Join(errs, "\n")
	statusMu.Unlock()
	if joinedErrs != "" {
		t.Fatalf("unexpected bug report errors: %s", joinedErrs)
	}
	if !strings.Contains(joinedStatuses, "rep-123") {
		t.Fatalf("expected uploaded report ID in status, got:\n%s", joinedStatuses)
	}
}

func TestBugReportZipExportAndOfflineUploadGuard(t *testing.T) {
	fx := tithNewRuntimeFixture(t, nil)
	zipDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(zipDir); err != nil {
		t.Fatalf("Chdir(%s): %v", zipDir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	term := tithNewVirtualTerminal(100, 30)
	t.Cleanup(term.tithClose)
	ui := CreateInteractiveTui(InteractiveTuiOptions{
		TuiMode:  tui.TuiModeRegular,
		Terminal: term,
	})
	editorContainer := tui.NewContainer()
	editor := &tithStubComponent{lines: []string{">"}}
	editorContainer.AddChild(editor)
	ui.AddChild(editorContainer)
	ui.Start()
	t.Cleanup(func() { ui.Stop(nil) })

	var mu sync.Mutex
	var statuses []string
	bugCtx := &BugReportContext{
		Session:         fx.Runtime.Session,
		UI:              ui,
		EditorContainer: editorContainer,
		Editor:          editor,
		Keybindings:     core.KeybindingsManagerCreate(),
		ShowStatus: func(msg string) {
			mu.Lock()
			statuses = append(statuses, msg)
			mu.Unlock()
		},
		ShowError: func(string) {},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ReportBug(context.Background(), bugCtx, tithStrPtr("offline zip check"))
	}()

	tbrWaitContainerContains(t, ui, editorContainer, "Report a bug")
	term.tithSendInput("\r")
	tbrWaitContainerContains(t, ui, editorContainer, "Include the session transcript?")
	term.tithSendInput("\r")
	tbrWaitContainerContains(t, ui, editorContainer, "Export as Zip")
	// Move down to "Export as Zip" and confirm
	term.tithSendInput("\x1b[B")
	term.tithSendInput("\r")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("ReportBug zip flow timed out")
	}

	matches, err := filepath.Glob(filepath.Join(zipDir, "*.zip"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("expected zip archive written in %s (matches=%v err=%v)", zipDir, matches, err)
	}
	mu.Lock()
	joined := strings.Join(statuses, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "Bug report exported to:") {
		t.Fatalf("expected export status message, got:\n%s", joined)
	}

	// Verify session recorded a custom entry referencing zip delivery
	entries := fx.Runtime.Session.SessionManager.GetEntries()
	raw, _ := jsonx.Marshal(entries)
	if !strings.Contains(string(raw), "offline zip check") {
		t.Fatalf("expected bug report custom entry persisted in session, got %s", string(raw))
	}
}
