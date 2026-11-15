// Ported from packages/coding-agent/src/cli/auth-login.ts (pi v1.5.2).
// Note: Radius MCP server configuration is omitted in the barebone build.

package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

// AuthLoginOptions supplies dependencies to RunAuthLogin so tests can script I/O.
type AuthLoginOptions struct {
	ModelRuntime *core.ModelRuntime
	Terminal     LoginTerminal
	AgentDir     string
	Cwd          string
	GetDeviceId  func() string
}

const alAuthLoginUsage = utils.AppName + " auth login <provider> [--api-key] [--no-browser]"

type alLoginRequest struct {
	providerRef string
	apiKey      bool
	browser     bool
}

// RunAuthLogin runs `pi auth login <provider>` in the terminal and returns a CLI exit code.
func RunAuthLogin(ctx context.Context, args []string, options AuthLoginOptions) int {
	if ctx == nil {
		ctx = context.Background()
	}
	modelRuntime := options.ModelRuntime
	terminal := options.Terminal

	req, errMsg := alParseLoginRequest(args)
	if errMsg != "" {
		terminal.Error(errMsg)
		return 1
	}

	matches := core.FindMatchingProviders(modelRuntime.GetProviders(), req.providerRef)
	if len(matches) == 0 || matches[0] == nil {
		var loginProviders []string
		for _, candidate := range modelRuntime.GetProviders() {
			if candidate != nil && (candidate.Auth.OAuth != nil || (candidate.Auth.ApiKey != nil && candidate.Auth.ApiKey.Login != nil)) {
				loginProviders = append(loginProviders, candidate.Id)
			}
		}
		sort.Strings(loginProviders)
		terminal.Error(fmt.Sprintf(`Unknown provider "%s".`, tui.ReplaceControlCharacters(req.providerRef)))
		terminal.Error("Providers: " + strings.Join(loginProviders, ", "))
		return 1
	}
	provider := matches[0]

	if !terminal.Interactive() {
		terminal.Error(fmt.Sprintf(`"%s" requires an interactive terminal`, alAuthLoginUsage))
		return 1
	}

	authType, unavailMsg := alSelectAuthType(provider, req.apiKey)
	if unavailMsg != "" {
		terminal.Error(unavailMsg)
		return 1
	}

	interaction := alNewLoginInteraction(ctx, provider.Id, authType, req, terminal)
	_, err := modelRuntime.Login(ctx, provider.Id, authType, interaction, &ai.LoginOptions{
		GetDeviceId: options.GetDeviceId,
	})
	if err != nil {
		var syncErr *core.CredentialSynchronizationError
		if errors.As(err, &syncErr) {
			terminal.Error(fmt.Sprintf("Logged in to %s, but local model state could not be synchronized: %v", provider.Name, err))
		} else if alIsCancelled(ctx, err) {
			terminal.Error("Login cancelled.")
		} else {
			terminal.Error(fmt.Sprintf("Failed to log in to %s: %v", provider.Name, err))
		}
		return 1
	}

	prefix := fmt.Sprintf("Saved API key for %s", provider.Name)
	if authType == ai.AuthTypeOAuth {
		prefix = fmt.Sprintf("Logged in to %s", provider.Name)
	}
	terminal.Write(fmt.Sprintf("%s. Credentials saved to %s", prefix, utils.GetAuthPath()))

	alRefreshProviderCatalog(ctx, provider, modelRuntime, terminal)
	return 0
}

func alIsCancelled(ctx context.Context, err error) bool {
	if ctx != nil && ctx.Err() != nil {
		return true
	}
	if err == nil {
		return false
	}
	var abortErr *utils.AbortError
	if errors.Is(err, context.Canceled) || errors.As(err, &abortErr) {
		return true
	}
	return err.Error() == "Login cancelled"
}

func alParseLoginRequest(args []string) (alLoginRequest, string) {
	usage := "Usage: " + alAuthLoginUsage
	req := alLoginRequest{browser: true}
	for _, arg := range args {
		switch {
		case arg == "--api-key":
			req.apiKey = true
		case arg == "--no-browser":
			req.browser = false
		case strings.HasPrefix(arg, "-") || req.providerRef != "":
			return alLoginRequest{}, usage
		default:
			req.providerRef = arg
		}
	}
	if req.providerRef == "" {
		return alLoginRequest{}, usage
	}
	if req.apiKey && !req.browser {
		return alLoginRequest{}, "--no-browser only applies to account sign-in; omit it with --api-key."
	}
	return req, ""
}

func alSelectAuthType(provider *ai.Provider, apiKey bool) (ai.AuthType, string) {
	hasApiKeyLogin := provider.Auth.ApiKey != nil && provider.Auth.ApiKey.Login != nil
	if !apiKey && provider.Auth.OAuth != nil {
		return ai.AuthTypeOAuth, ""
	}
	if hasApiKeyLogin {
		return ai.AuthTypeApiKey, ""
	}
	if apiKey && provider.Auth.OAuth != nil {
		return "", fmt.Sprintf("%s has no API key login. Omit --api-key to sign in.", provider.Name)
	}
	method := "ambient credentials"
	if provider.Auth.ApiKey != nil && provider.Auth.ApiKey.Name != "" {
		method = provider.Auth.ApiKey.Name
	}
	return "", fmt.Sprintf("%s is configured outside %s (%s).", provider.Name, utils.AppName, method)
}

type alMethodChoice string

const (
	alMethodAsk     alMethodChoice = "ask"
	alMethodBrowser alMethodChoice = "browser"
	alMethodOther   alMethodChoice = "other"
)

type alLoginInteraction struct {
	rootCtx        context.Context
	providerId     string
	req            alLoginRequest
	terminal       LoginTerminal
	methodChoice   alMethodChoice
	skippedMethods []string
}

func alNewLoginInteraction(
	rootCtx context.Context,
	providerId string,
	authType ai.AuthType,
	req alLoginRequest,
	terminal LoginTerminal,
) *alLoginInteraction {
	choice := alMethodOther
	if authType == ai.AuthTypeApiKey {
		choice = alMethodAsk
	} else if req.browser {
		choice = alMethodBrowser
	}
	return &alLoginInteraction{
		rootCtx:      rootCtx,
		providerId:   providerId,
		req:          req,
		terminal:     terminal,
		methodChoice: choice,
	}
}

func alMergeContext(base, promptCtx context.Context) (context.Context, context.CancelFunc) {
	if base == nil {
		base = context.Background()
	}
	if promptCtx == nil || promptCtx == base {
		return base, func() {}
	}
	merged, cancel := context.WithCancelCause(base)
	stop := context.AfterFunc(promptCtx, func() {
		cancel(context.Cause(promptCtx))
	})
	return merged, func() {
		stop()
		cancel(nil)
	}
}

func (i *alLoginInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	promptCtx, cancel := alMergeContext(ctx, prompt.Context())
	defer cancel()

	switch p := prompt.(type) {
	case *ai.AuthPromptSelect:
		return i.selectMethod(promptCtx, p)
	case *ai.AuthPromptSecret:
		return i.terminal.Secret(promptCtx, alPromptMessage(p.Message, p.Placeholder))
	case *ai.AuthPromptText:
		return i.terminal.Question(promptCtx, alPromptMessage(p.Message, p.Placeholder))
	case *ai.AuthPromptManualCode:
		return i.terminal.Question(promptCtx, alPromptMessage(p.Message, p.Placeholder))
	default:
		return "", fmt.Errorf("unsupported auth prompt: %T", prompt)
	}
}

func alPromptMessage(msg string, placeholder *string) string {
	if placeholder != nil && *placeholder != "" {
		return fmt.Sprintf("%s (%s): ", msg, *placeholder)
	}
	return msg + ": "
}

func (i *alLoginInteraction) selectMethod(ctx context.Context, prompt *ai.AuthPromptSelect) (string, error) {
	if len(prompt.Options) == 0 {
		return alSelectInTerminal(ctx, prompt.Message, prompt.Options, i.terminal)
	}
	first := prompt.Options[0]
	others := prompt.Options[1:]
	if i.methodChoice == alMethodAsk {
		return alSelectInTerminal(ctx, prompt.Message, prompt.Options, i.terminal)
	}
	if i.methodChoice == alMethodBrowser {
		i.skippedMethods = make([]string, len(others))
		for idx, opt := range others {
			i.skippedMethods[idx] = opt.Label
		}
		return first.Id, nil
	}
	// A provider whose only method is browser sign-in still uses it without opening a browser.
	if len(others) == 0 {
		return first.Id, nil
	}
	if len(others) > 1 {
		return alSelectInTerminal(ctx, prompt.Message, others, i.terminal)
	}
	i.terminal.Write(fmt.Sprintf("%s %s", prompt.Message, others[0].Label))
	return others[0].Id, nil
}

func (i *alLoginInteraction) Notify(event ai.AuthEvent) {
	switch e := event.(type) {
	case *ai.AuthEventAuthUrl:
		i.terminal.Write(fmt.Sprintf("Open this URL to sign in:\n  %s", utils.DisplayUrl(e.Url)))
		if e.Instructions != nil && *e.Instructions != "" {
			i.terminal.Write(*e.Instructions)
		}
		if target := utils.WebUrl(e.Url); target != nil && i.req.browser {
			i.terminal.OpenUrl(*target)
		}
		rerun := fmt.Sprintf(`"%s auth login %s --no-browser"`, utils.AppName, i.providerId)
		if len(i.skippedMethods) == 1 {
			i.terminal.Write(fmt.Sprintf(`To use "%s" instead, run %s`, i.skippedMethods[0], rerun))
		} else if len(i.skippedMethods) > 1 {
			quoted := make([]string, len(i.skippedMethods))
			for idx, label := range i.skippedMethods {
				quoted[idx] = fmt.Sprintf(`"%s"`, label)
			}
			i.terminal.Write(fmt.Sprintf("Other sign-in methods: %s. Run %s to choose one.", strings.Join(quoted, ", "), rerun))
		}
	case *ai.AuthEventDeviceCode:
		i.terminal.Write(fmt.Sprintf("Open %s and enter code: %s", utils.DisplayUrl(e.VerificationUri), e.UserCode))
		openBrowser := e.OpenBrowserOnConfirm == nil || *e.OpenBrowserOnConfirm
		if target := utils.WebUrl(e.VerificationUri); target != nil && i.req.browser && openBrowser {
			i.terminal.OpenUrl(*target)
		}
		i.terminal.Write("Waiting for authentication...")
	case *ai.AuthEventInfo:
		i.terminal.Write(e.Message)
		for _, link := range e.Links {
			prefix := ""
			if link.Label != nil && *link.Label != "" {
				prefix = *link.Label + ": "
			}
			i.terminal.Write("  " + prefix + utils.DisplayUrl(link.Url))
		}
	case *ai.AuthEventProgress:
		i.terminal.Write(e.Message)
	}
}

func alDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func alSelectInTerminal(
	ctx context.Context,
	message string,
	options []ai.AuthSelectOption,
	terminal LoginTerminal,
) (string, error) {
	terminal.Write(message)
	for idx, option := range options {
		desc := ""
		if option.Description != nil && *option.Description != "" {
			desc = " - " + *option.Description
		}
		terminal.Write(fmt.Sprintf("  %d. %s%s", idx+1, option.Label, desc))
	}
	for {
		raw, err := terminal.Question(ctx, fmt.Sprintf("Select 1-%d: ", len(options)))
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimSpace(raw)
		if alDigitsOnly(trimmed) {
			if n, convErr := strconv.Atoi(trimmed); convErr == nil && n >= 1 && n <= len(options) {
				return options[n-1].Id, nil
			}
		}
		terminal.Write(fmt.Sprintf("Enter a number from 1 to %d.", len(options)))
	}
}

func alRefreshProviderCatalog(
	ctx context.Context,
	provider *ai.Provider,
	modelRuntime *core.ModelRuntime,
	terminal LoginTerminal,
) {
	cancelled := func() {
		terminal.Write(fmt.Sprintf("Skipped the %s model catalog refresh; %s refreshes it on next start.", provider.Name, utils.AppName))
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	result, err := modelRuntime.Refresh(refreshCtx, &ai.ModelsRefreshOptions{
		Providers: []string{provider.Id},
	})
	if ctx.Err() != nil {
		cancelled()
		return
	}
	if err != nil {
		terminal.Write(fmt.Sprintf("The %s model catalog could not be refreshed: %v", provider.Name, err))
		return
	}
	if result.Aborted {
		terminal.Write(fmt.Sprintf("The %s model catalog refresh timed out; %s refreshes it on next start.", provider.Name, utils.AppName))
		return
	}
	if result.Errors != nil && result.Errors.Len() > 0 {
		terminal.Write(fmt.Sprintf("The %s model catalog could not be refreshed; %s retries on next start.", provider.Name, utils.AppName))
	}
}
