// Ported from packages/ai/src/compat/extension-oauth-types.ts (pi v1.0.0).

package compat

import (
	"context"

	"github.com/keejkrej/pi-go/ai"
)

// OAuthCredentials is the credential object returned by extension OAuth logins.
// The struct is declared in package ai (auth/types.ts).
type OAuthCredentials = ai.OAuthCredentials

// OAuthPrompt is a legacy extension OAuth prompt.
type OAuthPrompt struct {
	Message     string  `json:"message"`
	Placeholder *string `json:"placeholder,omitzero"`
	AllowEmpty  *bool   `json:"allowEmpty,omitzero"`
}

// OAuthAuthInfo is a legacy extension OAuth authorization link.
type OAuthAuthInfo struct {
	Url          string  `json:"url"`
	Instructions *string `json:"instructions,omitzero"`
}

// OAuthDeviceCodeInfo is a legacy extension OAuth device-code notification.
type OAuthDeviceCodeInfo struct {
	UserCode         string `json:"userCode"`
	VerificationUri  string `json:"verificationUri"`
	IntervalSeconds  *int   `json:"intervalSeconds,omitzero"`
	ExpiresInSeconds *int   `json:"expiresInSeconds,omitzero"`
}

// OAuthSelectOption is one choice in an OAuth select prompt.
type OAuthSelectOption struct {
	Id    string `json:"id"`
	Label string `json:"label"`
}

// OAuthSelectPrompt asks the user to pick one option.
// OnSelect returns a nil *string when the user dismisses the prompt.
type OAuthSelectPrompt struct {
	Message string              `json:"message"`
	Options []OAuthSelectOption `json:"options"`
}

// OAuthLoginCallbacks is the callback surface retained for coding-agent
// extension compatibility. Optional TS methods are required here; a caller
// that does not use them supplies a no-op. Blocking methods take ctx in place
// of the TS signal field. Login functions take that same ctx.
type OAuthLoginCallbacks interface {
	OnAuth(info *OAuthAuthInfo)
	OnDeviceCode(info *OAuthDeviceCodeInfo)
	OnPrompt(ctx context.Context, prompt *OAuthPrompt) (string, error)
	OnProgress(message string)
	OnManualCodeInput(ctx context.Context) (string, error)
	OnSelect(ctx context.Context, prompt *OAuthSelectPrompt) (*string, error)
}
