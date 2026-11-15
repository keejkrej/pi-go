// Ported from packages/coding-agent/src/modes/interactive/bug-report.ts (pi v1.5.2).

package interactive

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/tui"
)

const (
	brDisclaimer     = "This report goes to the Pi developers (Earendil) and is not shared publicly. It includes your pi version, operating system, the current model and provider configuration (without API keys), loaded extensions, settings, and provider error diagnostics from this session."
	brTranscriptNote = "The transcript contains your messages, model output, tool calls and their results, including file contents and command output read during this session."
)

// BugReportContext provides the session and UI hooks required by ReportBug.
type BugReportContext struct {
	Session         *core.AgentSession
	UI              tui.TUI
	EditorContainer *tui.Container
	Editor          tui.Component
	Keybindings     *core.KeybindingsManager
	ShowStatus      func(message string)
	ShowError       func(message string)
	HttpClient      *http.Client
	GatewayUrl      *string
}

type brOptions struct {
	hint           *string
	includeSession bool
	includeSummary bool
	delivery       string // "upload" | "zip"
}

func brRunUI(ui tui.TUI, fn func()) {
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

func brShowOverlay(bugCtx *BugReportContext, component tui.Component) {
	if bugCtx.EditorContainer != nil {
		bugCtx.EditorContainer.Clear()
		bugCtx.EditorContainer.AddChild(component)
	}
	if bugCtx.UI != nil {
		bugCtx.UI.SetFocus(component)
		bugCtx.UI.RequestRender(nil)
	}
}

func brRestoreEditor(bugCtx *BugReportContext, component tui.Component) {
	if d, ok := component.(interface{ Dispose() }); ok {
		d.Dispose()
	}
	if bugCtx.EditorContainer != nil {
		bugCtx.EditorContainer.Clear()
		if bugCtx.Editor != nil {
			bugCtx.EditorContainer.AddChild(bugCtx.Editor)
		}
	}
	if bugCtx.UI != nil {
		bugCtx.UI.SetFocus(bugCtx.Editor)
		bugCtx.UI.RequestRender(nil)
	}
}

func brShowStatus(bugCtx *BugReportContext, message string) {
	if bugCtx != nil && bugCtx.ShowStatus != nil {
		bugCtx.ShowStatus(message)
	}
}

func brShowError(bugCtx *BugReportContext, message string) {
	if bugCtx != nil && bugCtx.ShowError != nil {
		bugCtx.ShowError(message)
	}
}

func brShowLoader(bugCtx *BugReportContext, message string) *components.BorderedLoader {
	var loader *components.BorderedLoader
	brRunUI(bugCtx.UI, func() {
		loader = components.NewBorderedLoader(bugCtx.UI, theme.CurrentTheme(), message)
		brShowOverlay(bugCtx, loader)
	})
	return loader
}

func brInput(ctx context.Context, bugCtx *BugReportContext, title, description string, initialValue *string) *string {
	type reply struct {
		val *string
	}
	ch := make(chan reply, 1)
	var comp *components.ExtensionEditorComponent
	var extCmd *string
	if bugCtx.Session != nil && bugCtx.Session.SettingsManager != nil {
		if cmd := bugCtx.Session.SettingsManager.GetExternalEditorCommand(); cmd != "" {
			extCmd = &cmd
		}
	}

	brRunUI(bugCtx.UI, func() {
		var done atomic.Bool
		finish := func(val *string) {
			if !done.CompareAndSwap(false, true) {
				return
			}
			brRestoreEditor(bugCtx, comp)
			ch <- reply{val: val}
		}
		desc := description
		comp = components.NewExtensionEditorComponent(
			bugCtx.UI,
			bugCtx.Keybindings,
			title,
			initialValue,
			func(value string) {
				v := value
				finish(&v)
			},
			func() {
				finish(nil)
			},
			&components.ExtensionEditorOptions{
				Description: &desc,
			},
			extCmd,
		)
		brShowOverlay(bugCtx, comp)
	})

	select {
	case res := <-ch:
		return res.val
	case <-ctx.Done():
		brRunUI(bugCtx.UI, func() {
			brRestoreEditor(bugCtx, comp)
		})
		return nil
	}
}

func brChoose(ctx context.Context, bugCtx *BugReportContext, title string, options []string, description *string) *string {
	type reply struct {
		val *string
	}
	ch := make(chan reply, 1)
	var comp *components.ExtensionSelectorComponent

	brRunUI(bugCtx.UI, func() {
		var done atomic.Bool
		finish := func(val *string) {
			if !done.CompareAndSwap(false, true) {
				return
			}
			brRestoreEditor(bugCtx, comp)
			ch <- reply{val: val}
		}
		comp = components.NewExtensionSelectorComponent(
			title,
			options,
			func(value string) {
				v := value
				finish(&v)
			},
			func() {
				finish(nil)
			},
			&components.ExtensionSelectorOptions{
				Tui:         bugCtx.UI,
				Description: description,
			},
		)
		brShowOverlay(bugCtx, comp)
	})

	select {
	case res := <-ch:
		return res.val
	case <-ctx.Done():
		brRunUI(bugCtx.UI, func() {
			brRestoreEditor(bugCtx, comp)
		})
		return nil
	}
}

func brPromptForOptions(ctx context.Context, bugCtx *BugReportContext, initialHint *string) *brOptions {
	hint := brInput(ctx, bugCtx, "Report a bug", brDisclaimer+"\n\nWhat went wrong? (optional)", initialHint)
	if hint == nil {
		return nil
	}

	transcriptNote := brTranscriptNote
	transcript := brChoose(ctx, bugCtx, "Include the session transcript?", []string{"Yes, include the transcript", "No"}, &transcriptNote)
	if transcript == nil {
		return nil
	}

	includeSession := *transcript != "No"
	includeSummary := false
	if !includeSession {
		model := bugCtx.Session.Model()
		modelName := "the current model"
		providerName := "your provider"
		if model != nil {
			if model.Name != "" {
				modelName = model.Name
			}
			if model.Provider != "" {
				providerName = string(model.Provider)
			}
		}
		summaryDesc := fmt.Sprintf("The transcript is sent to %s with your credentials and tokens. Only the generated summary is attached; the transcript stays on your machine.", providerName)
		summary := brChoose(
			ctx,
			bugCtx,
			fmt.Sprintf("Attach a summary written by %s instead?", modelName),
			[]string{"Yes, generate a summary", "No"},
			&summaryDesc,
		)
		if summary == nil {
			return nil
		}
		includeSummary = *summary != "No"
	}

	description := strings.TrimSpace(*hint)
	descDisplay := description
	if descDisplay == "" {
		descDisplay = "none"
	}
	transcriptDisplay := "not included"
	if includeSession {
		transcriptDisplay = "included"
	}
	summaryDisplay := "none"
	if includeSummary {
		modelName := "the current model"
		if m := bugCtx.Session.Model(); m != nil && m.Name != "" {
			modelName = m.Name
		}
		summaryDisplay = "written by " + modelName
	}

	gatewayHost := core.GetRadiusGatewayUrl()
	if bugCtx.GatewayUrl != nil && strings.TrimSpace(*bugCtx.GatewayUrl) != "" {
		gatewayHost = *bugCtx.GatewayUrl
	}
	if parsed, err := url.Parse(gatewayHost); err == nil && parsed.Host != "" {
		gatewayHost = parsed.Host
	}

	deliveryDesc := fmt.Sprintf(
		"Description: %s\nTranscript: %s\nSummary: %s\n\nUpload sends the report to %s. Export writes a zip archive to the current directory instead.",
		descDisplay,
		transcriptDisplay,
		summaryDisplay,
		gatewayHost,
	)
	delivery := brChoose(ctx, bugCtx, "Bug report", []string{"Upload Report", "Export as Zip", "Cancel"}, &deliveryDesc)
	if delivery == nil || *delivery == "Cancel" {
		return nil
	}

	var optHint *string
	if description != "" {
		optHint = &description
	}
	del := "zip"
	if *delivery == "Upload Report" {
		del = "upload"
	}
	return &brOptions{
		hint:           optHint,
		includeSession: includeSession,
		includeSummary: includeSummary,
		delivery:       del,
	}
}

func brBuildBundle(session *core.AgentSession, options *brOptions, summary *string) core.BugReportBundle {
	var extResult core.LoadExtensionsResult
	if rl := session.ResourceLoader(); rl != nil {
		extResult = rl.GetExtensions()
	}
	extErrors := make([]core.BugReportExtensionError, 0, len(extResult.Errors))
	for _, e := range extResult.Errors {
		extErrors = append(extErrors, core.BugReportExtensionError{
			Path:  e.Path,
			Error: e.Error,
		})
	}

	var globalSettings, projectSettings core.Settings
	if session.SettingsManager != nil {
		globalSettings = session.SettingsManager.GetGlobalSettings()
		projectSettings = session.SettingsManager.GetProjectSettings()
	}

	var cwd string
	if session.SessionManager != nil {
		cwd = session.SessionManager.GetCwd()
	}

	var sessionJSONL *string
	if options.includeSession && session.SessionManager != nil {
		serialized := core.SerializeSessionBranch(
			session.SessionManager,
			func(parentId *string, timestamp string) []any {
				return CreateShareTrailingEntries(session, parentId, timestamp)
			},
		)
		sessionJSONL = &serialized
	}

	return core.BugReportBundle{
		Metadata: core.CollectBugReportMetadata(core.CollectBugReportMetadataOptions{
			Hint:            options.hint,
			SessionId:       session.SessionId(),
			Cwd:             cwd,
			IncludeSession:  options.includeSession,
			IncludeSummary:  summary != nil,
			MessageCount:    len(session.Messages()),
			Model:           session.Model(),
			ModelRuntime:    session.ModelRuntime(),
			ThinkingLevel:   session.ThinkingLevel(),
			Extensions:      extResult.Extensions,
			ExtensionErrors: extErrors,
			GlobalSettings:  globalSettings,
			ProjectSettings: projectSettings,
		}),
		Diagnostics:  core.CollectBugReportDiagnostics(session.SessionManager, core.ReadCrashLog()...),
		Summary:      summary,
		SessionJsonl: sessionJSONL,
	}
}

func brRecordInSession(session *core.AgentSession, bundle core.BugReportBundle, delivery string, archivePath *string) {
	if session != nil && session.SessionManager != nil {
		_, _ = session.SessionManager.AppendCustomEntry(core.BugReportCustomEntryType, core.BugReportSessionEntryData{
			Id:              bundle.Metadata.Id,
			CreatedAt:       bundle.Metadata.CreatedAt,
			Hint:            bundle.Metadata.Hint,
			SessionIncluded: bundle.Metadata.Session.Included,
			SummaryIncluded: bundle.Metadata.Session.SummaryIncluded,
			Delivery:        delivery,
			Path:            archivePath,
		})
	}
	if len(bundle.Diagnostics.Crashes) > 0 {
		core.ClearCrashLog()
	}
}

func brUpload(ctx context.Context, bugCtx *BugReportContext, bundle core.BugReportBundle) *string {
	loader := brShowLoader(bugCtx, "Uploading bug report...")
	opCtx := ctx
	if loader != nil && loader.Signal() != nil {
		var cancel context.CancelFunc
		opCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-loader.Signal().Done():
				cancel()
			case <-opCtx.Done():
			}
		}()
	}

	var token *string
	if mr := bugCtx.Session.ModelRuntime(); mr != nil && mr.GetProvider(core.RadiusProviderId) != nil {
		minValidity := int64(5 * 60_000)
		if auth, err := mr.GetAuth(opCtx, core.RadiusProviderId, &ai.AuthResolutionOverrides{
			MinOAuthValidityMs: &minValidity,
		}); err == nil {
			token = imaGetAuthCredential(auth)
		}
	}

	result, err := core.UploadBugReport(opCtx, bundle, &core.UploadBugReportOptions{
		Token:      token,
		GatewayUrl: bugCtx.GatewayUrl,
		HttpClient: bugCtx.HttpClient,
	})

	aborted := loader != nil && loader.Signal() != nil && loader.Signal().Err() != nil
	if err == nil && !aborted {
		brRunUI(bugCtx.UI, func() {
			brRestoreEditor(bugCtx, loader)
			brRecordInSession(bugCtx.Session, bundle, "upload", nil)
			brShowStatus(bugCtx, fmt.Sprintf("Bug report uploaded. Report ID: %s", result.Id))
		})
		return nil
	}

	brRunUI(bugCtx.UI, func() {
		brRestoreEditor(bugCtx, loader)
		if aborted {
			brShowStatus(bugCtx, "Bug report cancelled")
		}
	})
	if aborted {
		return nil
	}
	msg := brErrorMessage(err)
	return &msg
}

func brExportZip(bugCtx *BugReportContext, bundle core.BugReportBundle) {
	cwd, err := os.Getwd()
	if err != nil && bugCtx.Session != nil {
		cwd = bugCtx.Session.Cwd()
	}
	archivePath := filepath.Join(cwd, core.BugReportArchiveFileName(bundle.Metadata.Id))
	if err := core.WriteBugReportArchive(bundle, archivePath); err != nil {
		brRunUI(bugCtx.UI, func() {
			brShowError(bugCtx, fmt.Sprintf("Failed to write bug report: %s", brErrorMessage(err)))
		})
		return
	}
	brRunUI(bugCtx.UI, func() {
		p := archivePath
		brRecordInSession(bugCtx.Session, bundle, "zip", &p)
		brShowStatus(bugCtx, fmt.Sprintf("Bug report exported to: %s\nReport ID: %s", archivePath, bundle.Metadata.Id))
	})
}

func brErrorMessage(err error) string {
	if err == nil {
		return "Unknown error"
	}
	return err.Error()
}

// ReportBug runs the `/bug` flow: consent, optional summary, then upload or zip export.
// Callers running on the TUI goroutine must invoke ReportBug in a background goroutine.
func ReportBug(ctx context.Context, bugCtx *BugReportContext, initialHint *string) {
	if bugCtx == nil || bugCtx.Session == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	options := brPromptForOptions(ctx, bugCtx, initialHint)
	if options == nil {
		brRunUI(bugCtx.UI, func() {
			brShowStatus(bugCtx, "Bug report cancelled")
		})
		return
	}
	if options.delivery == "upload" && os.Getenv("PI_OFFLINE") != "" {
		brRunUI(bugCtx.UI, func() {
			brShowError(bugCtx, "Uploading bug reports requires online mode. Use Export as Zip instead.")
		})
		return
	}

	var summary *string
	if options.includeSummary {
		modelName := "the current model"
		if m := bugCtx.Session.Model(); m != nil && m.Name != "" {
			modelName = m.Name
		}
		loader := brShowLoader(bugCtx, fmt.Sprintf("Writing summary with %s...", modelName))
		sumCtx := ctx
		if loader != nil && loader.Signal() != nil {
			var cancel context.CancelFunc
			sumCtx, cancel = context.WithCancel(ctx)
			defer cancel()
			go func() {
				select {
				case <-loader.Signal().Done():
					cancel()
				case <-sumCtx.Done():
				}
			}()
		}

		s, err := bugCtx.Session.SummarizeForBugReport(sumCtx, &core.SummarizeForBugReportOptions{
			Hint: options.hint,
		})
		aborted := loader != nil && loader.Signal() != nil && loader.Signal().Err() != nil
		if err != nil {
			brRunUI(bugCtx.UI, func() {
				brRestoreEditor(bugCtx, loader)
				if aborted {
					brShowStatus(bugCtx, "Bug report cancelled")
				} else {
					brShowError(bugCtx, fmt.Sprintf("Failed to write bug report summary: %s", brErrorMessage(err)))
				}
			})
			return
		}
		brRunUI(bugCtx.UI, func() {
			brRestoreEditor(bugCtx, loader)
			if aborted {
				brShowStatus(bugCtx, "Bug report cancelled")
			}
		})
		if aborted {
			return
		}
		summary = &s
	}

	bundle := brBuildBundle(bugCtx.Session, options, summary)
	if options.delivery == "upload" {
		failure := brUpload(ctx, bugCtx, bundle)
		if failure == nil {
			return
		}
		desc := fmt.Sprintf("%s\n\nExport the report as a zip archive instead?", *failure)
		fallback := brChoose(ctx, bugCtx, "Upload failed", []string{"Export as Zip", "Cancel"}, &desc)
		if fallback == nil || *fallback != "Export as Zip" {
			brRunUI(bugCtx.UI, func() {
				brShowStatus(bugCtx, "Bug report cancelled")
			})
			return
		}
	}
	brExportZip(bugCtx, bundle)
}
