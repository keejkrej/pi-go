package interactive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

type imaLoginOption struct {
	providerID string
	name       string
	authType   ai.AuthType
}

// imaGetAuthCredential extracts the bearer token or API key from a resolved AuthResult.
func imaGetAuthCredential(auth *ai.AuthResult) *string {
	if auth == nil {
		return nil
	}
	if auth.Auth.ApiKey != nil {
		trimmed := strings.TrimSpace(*auth.Auth.ApiKey)
		if trimmed != "" {
			return &trimmed
		}
	}
	if auth.Auth.Headers != nil {
		for _, k := range auth.Auth.Headers.Keys() {
			if strings.EqualFold(k, "authorization") {
				v, ok := auth.Auth.Headers.Get(k)
				if !ok || v == nil {
					continue
				}
				trimmed := strings.TrimSpace(*v)
				if len(trimmed) > 7 && strings.EqualFold(trimmed[:7], "bearer ") {
					tok := strings.TrimSpace(trimmed[7:])
					if tok != "" {
						return &tok
					}
				}
				if trimmed != "" {
					return &trimmed
				}
			}
		}
	}
	return nil
}

func (m *InteractiveMode) imaCollectAuthProviders(ctx context.Context, mode string) []components.AuthSelectorProvider {
	mr := m.imModelRuntime()
	if mr == nil {
		return nil
	}
	providers := mr.GetProviders()

	credTypes := make(map[string]map[ai.AuthType]bool)
	if creds, err := mr.ListCredentials(ctx, nil); err == nil {
		for _, c := range creds {
			byType := credTypes[c.ProviderId]
			if byType == nil {
				byType = make(map[ai.AuthType]bool)
				credTypes[c.ProviderId] = byType
			}
			byType[c.Type] = true
		}
	}

	var out []components.AuthSelectorProvider
	for _, prov := range providers {
		if prov == nil {
			continue
		}
		var status *ai.AuthCheck
		if check, err := mr.CheckAuth(ctx, prov.Id, nil); err == nil {
			status = check
		}

		if mode == "logout" {
			types := credTypes[prov.Id]
			if types[ai.AuthTypeOAuth] && prov.Auth.OAuth != nil {
				sub := prov.Auth.OAuth.IsSubscription
				out = append(out, components.AuthSelectorProvider{
					Id:           prov.Id,
					Name:         prov.Name,
					AuthType:     ai.AuthTypeOAuth,
					OAuth:        prov.Auth.OAuth,
					Status:       status,
					Subscription: &sub,
				})
			}
			if types[ai.AuthTypeApiKey] && prov.Auth.ApiKey != nil {
				out = append(out, components.AuthSelectorProvider{
					Id:       prov.Id,
					Name:     prov.Name,
					AuthType: ai.AuthTypeApiKey,
					ApiKey:   prov.Auth.ApiKey,
					Status:   status,
				})
			}
			continue
		}

		if prov.Auth.OAuth != nil {
			sub := prov.Auth.OAuth.IsSubscription
			out = append(out, components.AuthSelectorProvider{
				Id:           prov.Id,
				Name:         prov.Name,
				AuthType:     ai.AuthTypeOAuth,
				OAuth:        prov.Auth.OAuth,
				Status:       status,
				Subscription: &sub,
			})
		}
		if prov.Auth.ApiKey != nil {
			out = append(out, components.AuthSelectorProvider{
				Id:       prov.Id,
				Name:     prov.Name,
				AuthType: ai.AuthTypeApiKey,
				ApiKey:   prov.Auth.ApiKey,
				Status:   status,
			})
		}
	}
	return out
}

func imaMatchProvider(providers []components.AuthSelectorProvider, ref string) (*components.AuthSelectorProvider, []components.AuthSelectorProvider) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return nil, providers
	}
	lower := strings.ToLower(trimmed)
	var exact []*components.AuthSelectorProvider
	var prefix []*components.AuthSelectorProvider
	for i := range providers {
		p := &providers[i]
		idLower := strings.ToLower(p.Id)
		nameLower := strings.ToLower(p.Name)
		if idLower == lower || nameLower == lower {
			exact = append(exact, p)
		} else if strings.HasPrefix(idLower, lower) || strings.HasPrefix(nameLower, lower) {
			prefix = append(prefix, p)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		copied := make([]components.AuthSelectorProvider, 0, len(exact))
		for _, p := range exact {
			copied = append(copied, *p)
		}
		return nil, copied
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	if len(prefix) > 1 {
		copied := make([]components.AuthSelectorProvider, 0, len(prefix))
		for _, p := range prefix {
			copied = append(copied, *p)
		}
		return nil, copied
	}
	return nil, providers
}

func (m *InteractiveMode) imaHandleLoginCommand(ctx context.Context, providerRef string) {
	if ctx == nil {
		ctx = m.bgCtx
	}
	providers := m.imaCollectAuthProviders(ctx, "login")
	if len(providers) == 0 {
		m.imPostUI(func() {
			m.ShowWarning("No authentication providers available")
		})
		return
	}

	var chosenID string
	var chosenAuthType ai.AuthType
	var initialSearch string

	if strings.TrimSpace(providerRef) != "" {
		matched, filtered := imaMatchProvider(providers, providerRef)
		if matched != nil {
			chosenID = matched.Id
			chosenAuthType = matched.AuthType
		} else {
			initialSearch = strings.TrimSpace(providerRef)
			providers = filtered
		}
	}

	if chosenID == "" {
		var searchPtr *string
		if initialSearch != "" {
			searchPtr = &initialSearch
		}
		var cancelled bool
		m.imlcRunSelectorDialog(ctx, func(done func()) {
			ui := m.imCurrentUI()
			selector := components.NewOAuthSelectorComponent(
				"login",
				providers,
				func(providerID string, authType ai.AuthType) {
					chosenID = providerID
					chosenAuthType = authType
					done()
				},
				func() {
					cancelled = true
					done()
				},
				searchPtr,
				ui,
			)
			m.imlcShowTemporarySelector(selector)
		})
		if cancelled || chosenID == "" {
			return
		}
	}

	m.imaRunLoginFlow(ctx, chosenID, chosenAuthType)
}

func (m *InteractiveMode) imaRunLoginFlow(ctx context.Context, providerID string, authType ai.AuthType) {
	mr := m.imModelRuntime()
	if mr == nil {
		return
	}
	prov := mr.GetProvider(providerID)
	displayName := providerID
	if prov != nil && prov.Name != "" {
		displayName = prov.Name
	}

	ui := m.imCurrentUI()
	var dialog *components.LoginDialogComponent

	m.imCallUI(ctx, func() {
		dialog = components.NewLoginDialogComponent(ui, providerID, func(bool, *string) {}, displayName)
		m.imlcShowTemporarySelector(dialog)
	})
	if dialog == nil {
		return
	}

	defer func() {
		m.imCallUI(ctx, func() {
			m.imlcRestoreEditor()
		})
	}()

	loginCtx, cancelLogin := context.WithCancel(ctx)
	defer cancelLogin()

	go func() {
		select {
		case <-dialog.Signal().Done():
			cancelLogin()
		case <-loginCtx.Done():
		}
	}()

	interaction := &imaLoginInteraction{
		mode:   m,
		dialog: dialog,
	}

	var deviceIDFn func() string
	if sm := m.imSettingsManager(); sm != nil {
		deviceIDFn = sm.GetOrCreateDeviceId
	}
	agentName := utils.AppName
	_, err := mr.Login(loginCtx, providerID, authType, interaction, &ai.LoginOptions{
		GetDeviceId: deviceIDFn,
		AgentName:   &agentName,
	})
	if err != nil {
		if errors.Is(err, components.ErrLoginCancelled) || errors.Is(err, context.Canceled) {
			m.imPostUI(func() {
				m.ShowStatus("Login cancelled")
			})
			return
		}
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Login failed for %s: %v", displayName, err))
		})
		return
	}

	m.imPostUI(func() {
		m.imlcRefreshFooter()
		m.imcUpdateAvailableProviderCount()
		m.imlcUpdateEditorBorderColor()
	})

	if strings.Contains(strings.ToLower(providerID), "llama") {
		refreshRes, _ := mr.Refresh(ctx, &ai.ModelsRefreshOptions{Providers: []string{providerID}})
		_ = refreshRes
		loaded := 0
		for _, mod := range mr.GetModels(&providerID) {
			if mod != nil {
				loaded++
			}
		}
		msg := imhLlamaCppPostLoginGuidance(fmt.Sprintf("Logged in to %s", displayName), loaded)
		m.imPostUI(func() {
			m.ShowStatus(msg)
		})
		return
	}

	m.imPostUI(func() {
		m.ShowStatus(fmt.Sprintf("Logged in to %s", displayName))
	})
}

func (m *InteractiveMode) imaHandleLogoutCommand(ctx context.Context, providerRef string) {
	if ctx == nil {
		ctx = m.bgCtx
	}
	mr := m.imModelRuntime()
	if mr == nil {
		return
	}

	providers := m.imaCollectAuthProviders(ctx, "logout")
	if len(providers) == 0 {
		m.imPostUI(func() {
			m.ShowWarning("No logged-in providers")
		})
		return
	}

	var chosenID string
	var chosenName string
	var initialSearch string

	if strings.TrimSpace(providerRef) != "" {
		matched, filtered := imaMatchProvider(providers, providerRef)
		if matched != nil {
			chosenID = matched.Id
			chosenName = matched.Name
		} else {
			initialSearch = strings.TrimSpace(providerRef)
			providers = filtered
		}
	}

	if chosenID == "" {
		var searchPtr *string
		if initialSearch != "" {
			searchPtr = &initialSearch
		}
		var cancelled bool
		m.imlcRunSelectorDialog(ctx, func(done func()) {
			ui := m.imCurrentUI()
			selector := components.NewOAuthSelectorComponent(
				"logout",
				providers,
				func(providerID string, _ ai.AuthType) {
					chosenID = providerID
					done()
				},
				func() {
					cancelled = true
					done()
				},
				searchPtr,
				ui,
			)
			m.imlcShowTemporarySelector(selector)
		})
		if cancelled || chosenID == "" {
			return
		}
	}

	if chosenName == "" {
		if prov := mr.GetProvider(chosenID); prov != nil && prov.Name != "" {
			chosenName = prov.Name
		} else {
			chosenName = chosenID
		}
	}

	if err := mr.Logout(ctx, chosenID, nil); err != nil {
		m.imPostUI(func() {
			m.ShowError(fmt.Sprintf("Logout failed for %s: %v", chosenName, err))
		})
		return
	}

	m.imPostUI(func() {
		m.imlcRefreshFooter()
		m.imcUpdateAvailableProviderCount()
		m.ShowStatus(fmt.Sprintf("Logged out of %s", chosenName))
	})
}

type imaLoginInteraction struct {
	mode   *InteractiveMode
	dialog *components.LoginDialogComponent
}

func (a *imaLoginInteraction) Prompt(ctx context.Context, prompt ai.AuthPrompt) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch p := prompt.(type) {
	case *ai.AuthPromptText:
		var args []string
		if p.Placeholder != nil && *p.Placeholder != "" {
			args = append(args, *p.Placeholder)
		}
		return a.dialog.ShowPrompt(ctx, p.Message, args...)
	case *ai.AuthPromptSecret:
		var args []string
		if p.Placeholder != nil && *p.Placeholder != "" {
			args = append(args, *p.Placeholder)
		}
		return a.dialog.ShowPrompt(ctx, p.Message, args...)
	case *ai.AuthPromptManualCode:
		return a.dialog.ShowManualInput(ctx, p.Message)
	case *ai.AuthPromptSelect:
		return a.imaPromptSelect(ctx, p)
	default:
		return "", fmt.Errorf("unsupported auth prompt")
	}
}

func (a *imaLoginInteraction) imaPromptSelect(ctx context.Context, p *ai.AuthPromptSelect) (string, error) {
	if len(p.Options) == 0 {
		return "", components.ErrLoginCancelled
	}
	labels := make([]string, 0, len(p.Options))
	for _, opt := range p.Options {
		label := opt.Label
		if opt.Description != nil && *opt.Description != "" {
			label += " — " + *opt.Description
		}
		labels = append(labels, label)
	}

	var chosenID string
	var cancelled bool
	doneCh := make(chan struct{})
	var closeOnce sync.Once
	finish := func() {
		closeOnce.Do(func() { close(doneCh) })
	}

	a.mode.imCallUI(ctx, func() {
		sel := components.NewExtensionSelectorComponent(
			p.Message,
			labels,
			func(selectedLabel string) {
				for i, lbl := range labels {
					if lbl == selectedLabel {
						chosenID = p.Options[i].Id
						break
					}
				}
				finish()
			},
			func() {
				cancelled = true
				finish()
			},
		)
		a.mode.imlcShowTemporarySelector(sel)
	})

	select {
	case <-doneCh:
	case <-ctx.Done():
		cancelled = true
	case <-a.dialog.Signal().Done():
		cancelled = true
	}

	a.mode.imCallUI(ctx, func() {
		a.mode.imlcShowTemporarySelector(a.dialog)
	})

	if cancelled || chosenID == "" {
		return "", components.ErrLoginCancelled
	}
	return chosenID, nil
}

func (a *imaLoginInteraction) Notify(event ai.AuthEvent) {
	if event == nil || a.dialog == nil {
		return
	}
	switch ev := event.(type) {
	case *ai.AuthEventAuthUrl:
		var instr []string
		if ev.Instructions != nil && *ev.Instructions != "" {
			instr = append(instr, *ev.Instructions)
		}
		a.mode.imPostUI(func() {
			a.dialog.ShowAuth(ev.Url, instr...)
		})
	case *ai.AuthEventDeviceCode:
		copyEv := *ev
		a.mode.imPostUI(func() {
			a.dialog.ShowDeviceCode(copyEv)
		})
	case *ai.AuthEventInfo:
		links := append([]ai.AuthInfoLink(nil), ev.Links...)
		msg := ev.Message
		a.mode.imPostUI(func() {
			a.dialog.ShowInfo(msg, links)
		})
	case *ai.AuthEventProgress:
		msg := ev.Message
		a.mode.imPostUI(func() {
			a.dialog.ShowProgress(msg)
		})
	}
}
