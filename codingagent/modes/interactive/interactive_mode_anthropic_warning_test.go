// Ported from packages/coding-agent/test/interactive-mode-anthropic-warning.test.ts (pi v1.5.2).

package interactive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func timawSetupAnthropicOAuthMode(t *testing.T) *tithModeFixture {
	t.Helper()
	mf := tithNewModeFixture(t, &tithRuntimeOptions{
		ExtraAuthKeys: map[string]ai.Credential{
			"anthropic": &ai.OAuthCredential{
				Type:    ai.AuthTypeOAuth,
				Access:  "sk-ant-oat01-subscription-token",
				Refresh: "refresh-token",
				Expires: float64(time.Now().Add(time.Hour).UnixMilli()),
			},
		},
	}, nil)

	anthBase := "https://anthropic.invalid"
	anthApi := string(ai.KnownApiAnthropicMessages)
	anthKey := "sk-ant-oat01-subscription-token"
	if err := mf.Runtime.Services.ModelRegistry.RegisterProvider("anthropic", core.ProviderConfigInput{
		BaseUrl: &anthBase,
		Api:     &anthApi,
		ApiKey:  &anthKey,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:   "claude-sonnet-4-5",
					Name: "Claude Sonnet 4.5",
				},
			},
		},
	}); err != nil {
		t.Fatalf("RegisterProvider(anthropic): %v", err)
	}
	model := mf.Runtime.Services.ModelRegistry.Find("anthropic", "claude-sonnet-4-5")
	if model == nil {
		t.Fatalf("expected registered anthropic model")
	}
	if err := mf.Mode.Session().SetModel(context.Background(), model); err != nil {
		t.Fatalf("SetModel(anthropic): %v", err)
	}
	return mf
}

func TestAnthropicSubscriptionAuthWarningShownOncePerSession(t *testing.T) {
	mf := timawSetupAnthropicOAuthMode(t)
	mode := mf.Mode

	rateLimitErr := "429 rate limit exceeded"
	msg := &ai.AssistantMessage{
		Provider:     ai.ProviderId("anthropic"),
		Model:        "claude-sonnet-4-5",
		StopReason:   ai.StopReasonError,
		ErrorMessage: &rateLimitErr,
	}

	// Trigger warning evaluation on the UI loop twice cleanly.
	if !mode.imCallUI(context.Background(), func() {
		mode.imevMaybeWarnAnthropicSubscription(msg)
		mode.imevMaybeWarnAnthropicSubscription(msg)
	}) {
		t.Fatalf("imCallUI timed out")
	}

	chatText := tithRenderOnUI(t, mode.Ui(), mode.chatContainer, 100)
	count := strings.Count(chatText, "Anthropic OAuth/subscription request was rate-limited or rejected")
	if count != 1 {
		t.Fatalf("expected Anthropic subscription warning once in chat container, got %d occurrences:\n%s", count, chatText)
	}

	// Non-Anthropic provider does not warn.
	mf2 := tithNewModeFixture(t, nil, nil)
	if !mf2.Mode.imCallUI(context.Background(), func() {
		mf2.Mode.imevMaybeWarnAnthropicSubscription(&ai.AssistantMessage{
			Provider:     ai.ProviderId("custom-provider"),
			Model:        "faux-1",
			StopReason:   ai.StopReasonError,
			ErrorMessage: &rateLimitErr,
		})
	}) {
		t.Fatalf("imCallUI timed out on mode2")
	}
	mf2.Mode.mu.Lock()
	warned := mf2.Mode.hasShownAnthropicWarning
	mf2.Mode.mu.Unlock()
	if warned {
		t.Fatalf("did not expect Anthropic subscription warning for non-Anthropic provider")
	}
}

func TestAnthropicWarningTriggeredViaMessageEndEvent(t *testing.T) {
	mf := timawSetupAnthropicOAuthMode(t)
	mode := mf.Mode
	overloaded := "overloaded_error from API"
	_ = mode.imCallUI(context.Background(), func() {
		mode.imevHandleEvent(&core.AgentSessionEventMessageEnd{
			Message: &ai.AssistantMessage{
				Provider:     ai.ProviderId("anthropic"),
				Model:        "claude-sonnet-4-5",
				StopReason:   ai.StopReasonError,
				ErrorMessage: &overloaded,
			},
		})
	})
	mode.mu.Lock()
	warned := mode.hasShownAnthropicWarning
	mode.mu.Unlock()
	if !warned {
		t.Fatalf("expected hasShownAnthropicWarning to be set on rate-limited/overloaded MessageEnd")
	}
}
