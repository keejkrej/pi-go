// Ported from packages/coding-agent/src/cli/project-trust.ts (pi v1.5.2).

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/keejkrej/pi-go/codingagent/core"
)

// PromptProjectTrustOptions configures startup project-trust resolution.
type PromptProjectTrustOptions struct {
	Cwd              string
	AgentDir         string
	SettingsManager  *core.SettingsManager
	TrustOverride    *bool
	ExtensionsResult *core.LoadExtensionsResult
	Mode             core.ExtensionMode
	HasUI            bool
	ErrOut           io.Writer
	UiOptions        *StartupUiOptions
	OnExtensionError func(message string)
}

type ptCliTrustUI struct {
	errOut    io.Writer
	uiOptions *StartupUiOptions
}

// NewCliProjectTrustUI constructs a core.ProjectTrustUIContext backed by startup TUI dialogs.
func NewCliProjectTrustUI(errOut io.Writer, uiOptions ...*StartupUiOptions) core.ProjectTrustUIContext {
	if errOut == nil {
		errOut = os.Stderr
	}
	var opt *StartupUiOptions
	if len(uiOptions) > 0 {
		opt = uiOptions[0]
	}
	return &ptCliTrustUI{errOut: errOut, uiOptions: opt}
}

func (u *ptCliTrustUI) Select(ctx context.Context, title string, options []string, _ ...*core.ExtensionUIDialogOptions) (*string, error) {
	return ShowStartupSelect(ctx, title, options, nil, u.uiOptions)
}

func (u *ptCliTrustUI) Confirm(ctx context.Context, title, message string, _ ...*core.ExtensionUIDialogOptions) (bool, error) {
	return ShowStartupConfirm(ctx, title, message, u.uiOptions)
}

func (u *ptCliTrustUI) Input(ctx context.Context, title string, placeholder *string, _ ...*core.ExtensionUIDialogOptions) (*string, error) {
	return ShowStartupInput(ctx, title, placeholder, u.uiOptions)
}

func (u *ptCliTrustUI) Notify(message string, notifyType ...string) {
	prefix := ""
	if len(notifyType) > 0 && notifyType[0] != "" {
		prefix = notifyType[0] + ": "
	}
	_, _ = fmt.Fprintln(u.errOut, prefix+message)
}

// ResolveStartupProjectTrust resolves whether cwd's project-local resources are trusted,
// prompting via interactive TUI when policy is ask and hasUI is true.
func ResolveStartupProjectTrust(ctx context.Context, options PromptProjectTrustOptions) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	store := core.NewProjectTrustStore(options.AgentDir)
	var defaultTrust *core.DefaultProjectTrust
	if options.SettingsManager != nil {
		dt := options.SettingsManager.GetDefaultProjectTrust()
		defaultTrust = &dt
	}
	errOut := options.ErrOut
	if errOut == nil {
		errOut = os.Stderr
	}
	onExtErr := options.OnExtensionError
	if onExtErr == nil {
		onExtErr = func(msg string) {
			_, _ = fmt.Fprintln(errOut, msg)
		}
	}

	ptCtx := core.ProjectTrustContext{
		Cwd:   options.Cwd,
		Mode:  options.Mode,
		HasUI: options.HasUI,
	}
	if options.HasUI {
		ptCtx.Ui = NewCliProjectTrustUI(errOut, options.UiOptions)
	}

	trusted, err := core.ResolveProjectTrusted(ctx, core.ResolveProjectTrustedOptions{
		Cwd:                 options.Cwd,
		TrustStore:          store,
		TrustOverride:       options.TrustOverride,
		DefaultProjectTrust: defaultTrust,
		ExtensionsResult:    options.ExtensionsResult,
		ProjectTrustContext: ptCtx,
		OnExtensionError:    onExtErr,
	})
	if err != nil {
		return false, err
	}
	if options.SettingsManager != nil {
		options.SettingsManager.SetProjectTrusted(trusted)
	}
	return trusted, nil
}
