// Ported from packages/coding-agent/src/core/project-trust.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

// AppMode identifies the active invocation mode passed into project trust hooks.
type AppMode string

const (
	AppModeInteractive AppMode = "interactive"
	AppModePrint       AppMode = "print"
	AppModeJson        AppMode = "json"
	AppModeRpc         AppMode = "rpc"
)

// ResolveProjectTrustedOptions configures ResolveProjectTrusted.
type ResolveProjectTrustedOptions struct {
	Cwd                 string
	TrustStore          *ProjectTrustStore
	TrustOverride       *bool
	DefaultProjectTrust *DefaultProjectTrust
	ExtensionsResult    *LoadExtensionsResult
	ProjectTrustContext ProjectTrustContext
	OnExtensionError    func(message string)
}

func ptFormatProjectTrustPrompt(cwd string) string {
	return fmt.Sprintf(
		"Trust project folder?\n%s\n\nThis allows %s to load %s settings and resources, install missing project packages, and execute project extensions.",
		cwd,
		utils.AppName,
		utils.ConfigDirName,
	)
}

func ptSelectProjectTrustOption(ctx context.Context, cwd string, ptctx ProjectTrustContext) (*ProjectTrustOption, error) {
	options := GetProjectTrustOptions(cwd, ProjectTrustOptionsConfig{IncludeSessionOnly: true})
	labels := make([]string, len(options))
	for i, opt := range options {
		labels[i] = opt.Label
	}
	selected, err := ptctx.Ui.Select(ctx, ptFormatProjectTrustPrompt(cwd), labels)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, nil
	}
	for i := range options {
		if options[i].Label == *selected {
			return &options[i], nil
		}
	}
	return nil, nil
}

func ptSaveProjectTrustPromptResult(trustStore *ProjectTrustStore, result ProjectTrustOption) error {
	if len(result.Updates) > 0 {
		return trustStore.SetMany(result.Updates)
	}
	return nil
}

// ResolveProjectTrusted resolves whether project-local configuration and extensions in cwd are trusted.
func ResolveProjectTrusted(ctx context.Context, options ResolveProjectTrustedOptions) (bool, error) {
	if options.TrustOverride != nil {
		return *options.TrustOverride, nil
	}
	if !HasTrustRequiringProjectResources(options.Cwd) {
		decision, err := options.TrustStore.Get(options.Cwd)
		if err != nil {
			return false, err
		}
		if decision != nil {
			return *decision, nil
		}
		return true, nil
	}

	if options.ExtensionsResult != nil {
		result, extErrors := EmitProjectTrustEvent(
			options.ExtensionsResult,
			ProjectTrustEvent{Type: "project_trust", Cwd: options.Cwd},
			options.ProjectTrustContext,
		)
		for _, extErr := range extErrors {
			if options.OnExtensionError != nil {
				options.OnExtensionError(fmt.Sprintf(`Extension "%s" project_trust error: %s`, extErr.ExtensionPath, extErr.Error))
			}
		}
		if result != nil {
			trusted := result.Trusted == "yes"
			if result.Remember != nil && *result.Remember {
				if err := options.TrustStore.Set(options.Cwd, &trusted); err != nil {
					return false, err
				}
			}
			return trusted, nil
		}
	}

	decision, err := options.TrustStore.Get(options.Cwd)
	if err != nil {
		return false, err
	}
	if decision != nil {
		return *decision, nil
	}

	defaultPolicy := SettingsDefaults.DefaultProjectTrust
	if options.DefaultProjectTrust != nil && *options.DefaultProjectTrust != "" {
		defaultPolicy = *options.DefaultProjectTrust
	}
	switch defaultPolicy {
	case DefaultProjectTrustAlways:
		return true, nil
	case DefaultProjectTrustNever:
		return false, nil
	case DefaultProjectTrustAsk:
	}

	if !options.ProjectTrustContext.HasUI || options.ProjectTrustContext.Ui == nil {
		return false, nil
	}

	selected, err := ptSelectProjectTrustOption(ctx, options.Cwd, options.ProjectTrustContext)
	if err != nil {
		return false, err
	}
	if selected != nil {
		if err := ptSaveProjectTrustPromptResult(options.TrustStore, *selected); err != nil {
			return false, err
		}
		return selected.Trusted, nil
	}
	return false, nil
}
