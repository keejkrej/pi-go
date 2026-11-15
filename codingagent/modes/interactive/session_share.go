// Ported from packages/coding-agent/src/modes/interactive/session-share.ts (pi v1.5.2).

package interactive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/tui"
)

// SessionShareContext provides the session and UI hooks required by ShareSession.
type SessionShareContext struct {
	Session         *core.AgentSession
	UI              tui.TUI
	EditorContainer *tui.Container
	Editor          tui.Component
	ShowStatus      func(message string)
	ShowError       func(message string)
	HttpClient      *http.Client
	GatewayUrl      *string
}

type ssShareTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  *typebox.Schema `json:"parameters"`
}

type ssShareEntryData struct {
	SystemPrompt string        `json:"systemPrompt"`
	Tools        []ssShareTool `json:"tools"`
}

type ssTrailingCustomEntry struct {
	Type       string           `json:"type"`
	CustomType string           `json:"customType"`
	Id         string           `json:"id"`
	ParentId   *string          `json:"parentId"`
	Timestamp  string           `json:"timestamp"`
	Data       ssShareEntryData `json:"data"`
}

type ssRadiusArtifactResponse struct {
	Artifact *struct {
		CanonicalUrl string `json:"canonical_url"`
	} `json:"artifact,omitzero"`
	Error *string `json:"error,omitzero"`
}

func ssShortID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	id := ai.Uuidv7()
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func ssRunUI(ui tui.TUI, fn func()) {
	if ui == nil {
		fn()
		return
	}
	done := make(chan struct{})
	ui.Post(func() {
		defer close(done)
		fn()
	})
	<-done
}

func ssRestoreEditor(loader *components.BorderedLoader, shareCtx *SessionShareContext) {
	if loader != nil {
		loader.Dispose()
	}
	if shareCtx.EditorContainer != nil {
		shareCtx.EditorContainer.Clear()
		if shareCtx.Editor != nil {
			shareCtx.EditorContainer.AddChild(shareCtx.Editor)
		}
	}
	if shareCtx.UI != nil && shareCtx.Editor != nil {
		shareCtx.UI.SetFocus(shareCtx.Editor)
	}
}

func ssNotifyError(shareCtx *SessionShareContext, message string) {
	if shareCtx != nil && shareCtx.ShowError != nil {
		shareCtx.ShowError(message)
	}
}

func ssNotifyStatus(shareCtx *SessionShareContext, message string) {
	if shareCtx != nil && shareCtx.ShowStatus != nil {
		shareCtx.ShowStatus(message)
	}
}

// CreateShareTrailingEntries produces the trailing `pi.share` custom entry carrying
// the active system prompt and tool schemas for the session viewer.
func CreateShareTrailingEntries(session *core.AgentSession, parentId *string, timestamp string) []any {
	var systemPrompt string
	var tools []ssShareTool
	if session != nil && session.Agent != nil {
		st := session.State()
		systemPrompt = st.SystemPrompt
		tools = make([]ssShareTool, 0, len(st.Tools))
		for _, tool := range st.Tools {
			if tool == nil {
				continue
			}
			tools = append(tools, ssShareTool{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			})
		}
	}
	if tools == nil {
		tools = []ssShareTool{}
	}
	return []any{
		ssTrailingCustomEntry{
			Type:       "custom",
			CustomType: "pi.share",
			Id:         ssShortID(),
			ParentId:   parentId,
			Timestamp:  timestamp,
			Data: ssShareEntryData{
				SystemPrompt: systemPrompt,
				Tools:        tools,
			},
		},
	}
}

// ExportSessionForShare exports the active branch with trailing presentation metadata for Radius.
func ExportSessionForShare(filePath string, session *core.AgentSession) error {
	if session == nil || session.SessionManager == nil {
		return fmt.Errorf("missing active session")
	}
	_, err := core.ExportSessionToJsonlWithTrailing(
		session.SessionManager,
		filePath,
		func(parentId *string, timestamp string) []any {
			return CreateShareTrailingEntries(session, parentId, timestamp)
		},
	)
	return err
}

// ShareSession uploads the current session through Radius, falling back to a private GitHub gist.
// Callers running on the TUI goroutine must run ShareSession in a background goroutine.
func ShareSession(ctx context.Context, shareCtx *SessionShareContext) {
	if shareCtx == nil || shareCtx.Session == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tempDir, err := os.MkdirTemp("", "pi-share-")
	if err != nil {
		ssRunUI(shareCtx.UI, func() {
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to export session: %v", err))
		})
		return
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	jsonlFile := filepath.Join(tempDir, "session.jsonl")
	htmlFile := filepath.Join(tempDir, "session.html")

	if err := ExportSessionForShare(jsonlFile, shareCtx.Session); err != nil {
		ssRunUI(shareCtx.UI, func() {
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to export session: %v", err))
		})
		return
	}

	destination := core.SessionShareDestinationAuto
	if shareCtx.Session.SettingsManager != nil {
		destination = shareCtx.Session.SettingsManager.GetSessionShareDestination()
	}

	if destination != core.SessionShareDestinationGist {
		if ssTryShareViaRadius(ctx, jsonlFile, shareCtx) {
			return
		}
		if destination == core.SessionShareDestinationRadius {
			ssRunUI(shareCtx.UI, func() {
				ssNotifyError(shareCtx, "Radius authentication is not configured. Run '/login radius' first.")
			})
			return
		}
	}

	authCmd := exec.CommandContext(ctx, "gh", "auth", "status")
	if err := authCmd.Run(); err != nil {
		if _, lookErr := exec.LookPath("gh"); lookErr != nil {
			ssRunUI(shareCtx.UI, func() {
				ssNotifyError(shareCtx, "GitHub CLI (gh) is not installed. Install it from https://cli.github.com/")
			})
			return
		}
		ssRunUI(shareCtx.UI, func() {
			ssNotifyError(shareCtx, "GitHub CLI is not logged in. Run 'gh auth login' first.")
		})
		return
	}

	thName := "dark"
	if th := theme.CurrentTheme(); th != nil && th.Name != "" {
		thName = th.Name
	}
	if _, err := shareCtx.Session.ExportToHtml(htmlFile, &core.ExportToHtmlOptions{ThemeName: &thName}); err != nil {
		ssRunUI(shareCtx.UI, func() {
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to export session: %v", err))
		})
		return
	}

	ssShareViaGist(ctx, htmlFile, shareCtx)
}

func ssTryShareViaRadius(ctx context.Context, tmpFile string, shareCtx *SessionShareContext) bool {
	mr := shareCtx.Session.ModelRuntime()
	if mr == nil || mr.GetProvider(core.RadiusProviderId) == nil {
		return false
	}
	minValidity := int64(5 * 60_000)
	auth, err := mr.GetAuth(ctx, core.RadiusProviderId, &ai.AuthResolutionOverrides{
		MinOAuthValidityMs: &minValidity,
	})
	if err != nil {
		return false
	}
	tokenPtr := imaGetAuthCredential(auth)
	if tokenPtr == nil || *tokenPtr == "" {
		return false
	}
	token := *tokenPtr

	opCtx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()

	var loader *components.BorderedLoader
	var cancelled atomic.Bool
	ssRunUI(shareCtx.UI, func() {
		loader = components.NewBorderedLoader(shareCtx.UI, theme.CurrentTheme(), "Uploading to Radius...")
		loader.OnAbort = func() {
			cancelled.Store(true)
			cancelOp()
			ssRestoreEditor(loader, shareCtx)
			ssNotifyStatus(shareCtx, "Share cancelled")
		}
		if shareCtx.EditorContainer != nil {
			shareCtx.EditorContainer.Clear()
			shareCtx.EditorContainer.AddChild(loader)
		}
		if shareCtx.UI != nil {
			shareCtx.UI.SetFocus(loader)
			shareCtx.UI.RequestRender(nil)
		}
	})

	reqCtx := opCtx
	if loader != nil && loader.Signal() != nil {
		var stopCancel context.CancelFunc
		reqCtx, stopCancel = context.WithCancel(opCtx)
		go func() {
			select {
			case <-loader.Signal().Done():
				cancelled.Store(true)
				stopCancel()
			case <-opCtx.Done():
				stopCancel()
			}
		}()
	}

	body, readErr := os.ReadFile(tmpFile)
	if readErr != nil {
		ssRunUI(shareCtx.UI, func() {
			if cancelled.Load() || reqCtx.Err() != nil {
				return
			}
			ssRestoreEditor(loader, shareCtx)
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to upload Radius artifact: %v", readErr))
		})
		return true
	}

	baseGateway := oauth.DefaultRadiusGateway
	if shareCtx.GatewayUrl != nil && strings.TrimSpace(*shareCtx.GatewayUrl) != "" {
		baseGateway = *shareCtx.GatewayUrl
	}
	targetURL, urlErr := url.Parse(baseGateway)
	if urlErr != nil {
		ssRunUI(shareCtx.UI, func() {
			if cancelled.Load() || reqCtx.Err() != nil {
				return
			}
			ssRestoreEditor(loader, shareCtx)
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to upload Radius artifact: %v", urlErr))
		})
		return true
	}
	targetURL = targetURL.ResolveReference(&url.URL{Path: "/v1/artifacts"})
	q := targetURL.Query()
	q.Set("visibility", "organization")
	q.Set("title", "Pi session")
	targetURL.RawQuery = q.Encode()

	req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, targetURL.String(), bytes.NewReader(body))
	if reqErr != nil {
		ssRunUI(shareCtx.UI, func() {
			if cancelled.Load() || reqCtx.Err() != nil {
				return
			}
			ssRestoreEditor(loader, shareCtx)
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to upload Radius artifact: %v", reqErr))
		})
		return true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.ContentLength = int64(len(body))

	client := shareCtx.HttpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, doErr := client.Do(req)
	if doErr != nil {
		ssRunUI(shareCtx.UI, func() {
			if cancelled.Load() || (loader != nil && loader.Signal() != nil && loader.Signal().Err() != nil) {
				return
			}
			ssRestoreEditor(loader, shareCtx)
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to upload Radius artifact: %v", doErr))
		})
		return true
	}
	respBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	var payload ssRadiusArtifactResponse
	hasPayload := jsonx.Unmarshal(respBytes, &payload) == nil

	ssRunUI(shareCtx.UI, func() {
		if cancelled.Load() || (loader != nil && loader.Signal() != nil && loader.Signal().Err() != nil) {
			return
		}
		ssRestoreEditor(loader, shareCtx)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || !hasPayload || payload.Artifact == nil || payload.Artifact.CanonicalUrl == "" {
			errMsg := ""
			if hasPayload && payload.Error != nil && *payload.Error != "" {
				errMsg = *payload.Error
			} else if resp.Status != "" {
				errMsg = http.StatusText(resp.StatusCode)
				if errMsg == "" {
					errMsg = fmt.Sprintf("%d", resp.StatusCode)
				}
			} else {
				errMsg = fmt.Sprintf("%d", resp.StatusCode)
			}
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to upload Radius artifact: %s", errMsg))
			return
		}
		shareURL := payload.Artifact.CanonicalUrl
		ssNotifyStatus(shareCtx, fmt.Sprintf("Share URL: %s", tui.Hyperlink(shareURL, shareURL)))
	})
	return true
}

func ssShareViaGist(ctx context.Context, tmpFile string, shareCtx *SessionShareContext) {
	opCtx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()

	var loader *components.BorderedLoader
	var cancelled atomic.Bool
	ssRunUI(shareCtx.UI, func() {
		loader = components.NewBorderedLoader(shareCtx.UI, theme.CurrentTheme(), "Creating gist...")
		loader.OnAbort = func() {
			cancelled.Store(true)
			cancelOp()
			ssRestoreEditor(loader, shareCtx)
			ssNotifyStatus(shareCtx, "Share cancelled")
		}
		if shareCtx.EditorContainer != nil {
			shareCtx.EditorContainer.Clear()
			shareCtx.EditorContainer.AddChild(loader)
		}
		if shareCtx.UI != nil {
			shareCtx.UI.SetFocus(loader)
			shareCtx.UI.RequestRender(nil)
		}
	})

	cmdCtx := opCtx
	if loader != nil && loader.Signal() != nil {
		var stopCancel context.CancelFunc
		cmdCtx, stopCancel = context.WithCancel(opCtx)
		go func() {
			select {
			case <-loader.Signal().Done():
				cancelled.Store(true)
				stopCancel()
			case <-opCtx.Done():
				stopCancel()
			}
		}()
	}

	cmd := exec.CommandContext(cmdCtx, "gh", "gist", "create", "--public=false", tmpFile)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	runErr := cmd.Run()

	ssRunUI(shareCtx.UI, func() {
		if cancelled.Load() || (loader != nil && loader.Signal() != nil && loader.Signal().Err() != nil) {
			return
		}
		ssRestoreEditor(loader, shareCtx)
		if runErr != nil {
			msg := strings.TrimSpace(stderrBuf.String())
			if msg == "" {
				msg = runErr.Error()
			}
			if msg == "" {
				msg = "Unknown error"
			}
			ssNotifyError(shareCtx, fmt.Sprintf("Failed to create gist: %s", msg))
			return
		}
		gistURL := strings.TrimSpace(stdoutBuf.String())
		parts := strings.Split(gistURL, "/")
		gistID := ""
		if len(parts) > 0 {
			gistID = parts[len(parts)-1]
		}
		if gistID == "" {
			ssNotifyError(shareCtx, "Failed to parse gist ID from gh output")
			return
		}
		previewURL := utils.GetShareViewerUrl(gistID)
		ssNotifyStatus(shareCtx, fmt.Sprintf("Share URL: %s\nGist: %s", tui.Hyperlink(previewURL, previewURL), tui.Hyperlink(gistURL, gistURL)))
	})
}
