// Ported from packages/ai/src/auth/types.ts (pi v1.5.2).

package ai

import (
	"context"
	"fmt"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// AuthType distinguishes stored/configured auth methods.
type AuthType string

const (
	AuthTypeApiKey AuthType = "api_key"
	AuthTypeOAuth  AuthType = "oauth"
)

// ModelAuth is request auth for a single model request.
type ModelAuth struct {
	ApiKey  *string         `json:"apiKey,omitzero"`
	Headers ProviderHeaders `json:"headers,omitzero"`
	BaseUrl *string         `json:"baseUrl,omitzero"`
}

// Credential is a sealed union of *ApiKeyCredential and *OAuthCredential.
type Credential interface {
	isCredential()
	CredentialType() AuthType
}

// ApiKeyCredential is a stored api-key credential.
type ApiKeyCredential struct {
	Type AuthType    `json:"type"`
	Key  *string     `json:"key,omitzero"`
	Env  ProviderEnv `json:"env,omitzero"`
}

func (*ApiKeyCredential) isCredential()            {}
func (*ApiKeyCredential) CredentialType() AuthType { return AuthTypeApiKey }

// MarshalJSON encodes ApiKeyCredential preserving "type": "api_key".
func (c ApiKeyCredential) MarshalJSON() ([]byte, error) {
	o := jsonx.NewObject()
	typ := c.Type
	if typ == "" {
		typ = AuthTypeApiKey
	}
	o.Set("type", string(typ))
	if c.Key != nil {
		o.Set("key", *c.Key)
	}
	if len(c.Env) > 0 {
		o.Set("env", c.Env)
	}
	return jsonx.Marshal(o)
}

// OAuthCredentials holds OAuth token data returned by extension compatibility flows,
// preserving any additional provider-specific fields.
type OAuthCredentials struct {
	Refresh string        `json:"refresh"`
	Access  string        `json:"access"`
	Expires float64       `json:"expires"`
	Extra   *jsonx.Object `json:"-"`
}

func (c OAuthCredentials) MarshalJSON() ([]byte, error) {
	o := jsonx.NewObject()
	o.Set("refresh", c.Refresh)
	o.Set("access", c.Access)
	o.Set("expires", c.Expires)
	if c.Extra != nil {
		for k, v := range c.Extra.All() {
			if k == "refresh" || k == "access" || k == "expires" {
				continue
			}
			o.Set(k, v)
		}
	}
	return jsonx.Marshal(o)
}

func (c *OAuthCredentials) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return fmt.Errorf("expected OAuthCredentials object")
	}
	return c.decodeObject(obj)
}

func (c *OAuthCredentials) decodeObject(obj *jsonx.Object) error {
	c.Refresh, _ = obj.GetString("refresh")
	c.Access, _ = obj.GetString("access")
	c.Expires, _ = obj.GetNumber("expires")
	var extra *jsonx.Object
	for k, v := range obj.All() {
		if k == "type" || k == "refresh" || k == "access" || k == "expires" {
			continue
		}
		if extra == nil {
			extra = jsonx.NewObject()
		}
		extra.Set(k, v)
	}
	c.Extra = extra
	return nil
}

// OAuthCredential is a stored canonical OAuth credential ("type": "oauth").
type OAuthCredential struct {
	Type AuthType `json:"type"`
	OAuthCredentials
}

func (*OAuthCredential) isCredential()            {}
func (*OAuthCredential) CredentialType() AuthType { return AuthTypeOAuth }

// MarshalJSON encodes OAuthCredential preserving "type": "oauth" and extra properties.
func (c OAuthCredential) MarshalJSON() ([]byte, error) {
	o := jsonx.NewObject()
	typ := c.Type
	if typ == "" {
		typ = AuthTypeOAuth
	}
	o.Set("type", string(typ))
	o.Set("refresh", c.Refresh)
	o.Set("access", c.Access)
	o.Set("expires", c.Expires)
	if c.Extra != nil {
		for k, v := range c.Extra.All() {
			if k == "type" || k == "refresh" || k == "access" || k == "expires" {
				continue
			}
			o.Set(k, v)
		}
	}
	return jsonx.Marshal(o)
}

// UnmarshalJSON decodes OAuthCredential including arbitrary extra properties from auth.json.
func (c *OAuthCredential) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return fmt.Errorf("expected OAuthCredential object")
	}
	if typ, ok := obj.GetString("type"); ok && typ != "" {
		c.Type = AuthType(typ)
	} else {
		c.Type = AuthTypeOAuth
	}
	return c.OAuthCredentials.decodeObject(obj)
}

// DecodeCredential decodes a parsed JSON value into *ApiKeyCredential or *OAuthCredential.
func DecodeCredential(v any) (Credential, error) {
	if v == nil {
		return nil, nil
	}
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, fmt.Errorf("expected credential object")
	}
	typ, _ := obj.GetString("type")
	switch AuthType(typ) {
	case AuthTypeApiKey:
		var out ApiKeyCredential
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		if out.Type == "" {
			out.Type = AuthTypeApiKey
		}
		return &out, nil
	case AuthTypeOAuth:
		var out OAuthCredential
		out.Type = AuthTypeOAuth
		if err := out.OAuthCredentials.decodeObject(obj); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return nil, fmt.Errorf("unknown credential type %q", typ)
	}
}

// UnmarshalCredential unmarshals auth.json credential JSON into a concrete Credential.
func UnmarshalCredential(data []byte) (Credential, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeCredential(v)
}

// CredentialInfo is non-secret credential metadata for enumeration.
type CredentialInfo struct {
	ProviderId string   `json:"providerId"`
	Type       AuthType `json:"type"`
}

// AuthOperationOptions is the options bag for public auth and credential operations.
// Cancellation uses ctx.
type AuthOperationOptions struct{}

// CredentialStore is app-owned persistent credential storage keyed by Provider.Id.
type CredentialStore interface {
	Read(ctx context.Context, providerId string, options *AuthOperationOptions) (Credential, error)
	List(ctx context.Context, options *AuthOperationOptions) ([]CredentialInfo, error)
	Modify(ctx context.Context, providerId string, fn func(current Credential) (Credential, error), options *AuthOperationOptions) (Credential, error)
	Delete(ctx context.Context, providerId string, options *AuthOperationOptions) error
}

// AuthContext provides environment and file existence access for auth resolution.
type AuthContext interface {
	Env(ctx context.Context, name string) (*string, error)
	FileExists(ctx context.Context, path string) (bool, error)
}

// AuthResult is the resolved auth and environment context for a provider or model.
type AuthResult struct {
	Auth   ModelAuth   `json:"auth"`
	Env    ProviderEnv `json:"env,omitzero"`
	Source *string     `json:"source,omitzero"`
}

// AuthCheck is the availability check metadata for a provider.
type AuthCheck struct {
	Source *string  `json:"source,omitzero"`
	Type   AuthType `json:"type"`
}

// AuthPromptKind identifies an AuthPrompt variant.
type AuthPromptKind string

const (
	AuthPromptKindText       AuthPromptKind = "text"
	AuthPromptKindSecret     AuthPromptKind = "secret"
	AuthPromptKindSelect     AuthPromptKind = "select"
	AuthPromptKindManualCode AuthPromptKind = "manual_code"
)

// AuthSelectOption is one choice in an AuthPromptSelect.
type AuthSelectOption struct {
	Id          string  `json:"id"`
	Label       string  `json:"label"`
	Description *string `json:"description,omitzero"`
}

// AuthPrompt is a sealed union of login prompts.
type AuthPrompt interface {
	isAuthPrompt()
	PromptKind() AuthPromptKind
	Context() context.Context
}

type AuthPromptText struct {
	Type          AuthPromptKind  `json:"type"`
	Message       string          `json:"message"`
	Placeholder   *string         `json:"placeholder,omitzero"`
	PromptContext context.Context `json:"-"`
}

func (*AuthPromptText) isAuthPrompt()              {}
func (*AuthPromptText) PromptKind() AuthPromptKind { return AuthPromptKindText }
func (p *AuthPromptText) Context() context.Context { return p.PromptContext }

type AuthPromptSecret struct {
	Type          AuthPromptKind  `json:"type"`
	Message       string          `json:"message"`
	Placeholder   *string         `json:"placeholder,omitzero"`
	PromptContext context.Context `json:"-"`
}

func (*AuthPromptSecret) isAuthPrompt()              {}
func (*AuthPromptSecret) PromptKind() AuthPromptKind { return AuthPromptKindSecret }
func (p *AuthPromptSecret) Context() context.Context { return p.PromptContext }

type AuthPromptSelect struct {
	Type          AuthPromptKind     `json:"type"`
	Message       string             `json:"message"`
	Options       []AuthSelectOption `json:"options"`
	PromptContext context.Context    `json:"-"`
}

func (*AuthPromptSelect) isAuthPrompt()              {}
func (*AuthPromptSelect) PromptKind() AuthPromptKind { return AuthPromptKindSelect }
func (p *AuthPromptSelect) Context() context.Context { return p.PromptContext }

type AuthPromptManualCode struct {
	Type          AuthPromptKind  `json:"type"`
	Message       string          `json:"message"`
	Placeholder   *string         `json:"placeholder,omitzero"`
	PromptContext context.Context `json:"-"`
}

func (*AuthPromptManualCode) isAuthPrompt()              {}
func (*AuthPromptManualCode) PromptKind() AuthPromptKind { return AuthPromptKindManualCode }
func (p *AuthPromptManualCode) Context() context.Context { return p.PromptContext }

// AuthInfoLink is a link attached to an info AuthEvent.
type AuthInfoLink struct {
	Url   string  `json:"url"`
	Label *string `json:"label,omitzero"`
}

// AuthEventKind identifies an AuthEvent variant.
type AuthEventKind string

const (
	AuthEventKindInfo       AuthEventKind = "info"
	AuthEventKindAuthUrl    AuthEventKind = "auth_url"
	AuthEventKindDeviceCode AuthEventKind = "device_code"
	AuthEventKindProgress   AuthEventKind = "progress"
)

// AuthEvent is a sealed union of login progress notifications.
type AuthEvent interface {
	isAuthEvent()
	EventKind() AuthEventKind
}

type AuthEventInfo struct {
	Type    AuthEventKind  `json:"type"`
	Message string         `json:"message"`
	Links   []AuthInfoLink `json:"links,omitzero"`
}

func (*AuthEventInfo) isAuthEvent()             {}
func (*AuthEventInfo) EventKind() AuthEventKind { return AuthEventKindInfo }

type AuthEventAuthUrl struct {
	Type         AuthEventKind `json:"type"`
	Url          string        `json:"url"`
	Instructions *string       `json:"instructions,omitzero"`
}

func (*AuthEventAuthUrl) isAuthEvent()             {}
func (*AuthEventAuthUrl) EventKind() AuthEventKind { return AuthEventKindAuthUrl }

type AuthEventDeviceCode struct {
	Type                 AuthEventKind `json:"type"`
	UserCode             string        `json:"userCode"`
	VerificationUri      string        `json:"verificationUri"`
	OpenBrowserOnConfirm *bool         `json:"openBrowserOnConfirm,omitzero"`
	CopyCodeOnConfirm    *bool         `json:"copyCodeOnConfirm,omitzero"`
	IntervalSeconds      *int          `json:"intervalSeconds,omitzero"`
	ExpiresInSeconds     *int          `json:"expiresInSeconds,omitzero"`
}

func (*AuthEventDeviceCode) isAuthEvent()             {}
func (*AuthEventDeviceCode) EventKind() AuthEventKind { return AuthEventKindDeviceCode }

type AuthEventProgress struct {
	Type    AuthEventKind `json:"type"`
	Message string        `json:"message"`
}

func (*AuthEventProgress) isAuthEvent()             {}
func (*AuthEventProgress) EventKind() AuthEventKind { return AuthEventKindProgress }

// AuthInteraction handles interactive login prompts and progress notifications.
type AuthInteraction interface {
	Prompt(ctx context.Context, prompt AuthPrompt) (string, error)
	Notify(event AuthEvent)
}

// ApiKeyCheckInput is the input to ApiKeyAuth.Check.
type ApiKeyCheckInput struct {
	Ctx        AuthContext
	Credential *ApiKeyCredential
}

// ApiKeyResolveInput is the input to ApiKeyAuth.Resolve.
type ApiKeyResolveInput struct {
	Ctx        AuthContext
	Credential *ApiKeyCredential
}

// ApiKeyAuth implements api-key authentication and ambient credential resolution.
type ApiKeyAuth struct {
	Name    string
	Login   func(ctx context.Context, interaction AuthInteraction) (*ApiKeyCredential, error)
	Check   func(ctx context.Context, input ApiKeyCheckInput) (*AuthCheck, error)
	Resolve func(ctx context.Context, input ApiKeyResolveInput) (*AuthResult, error)
}

// LoginOptions is app-supplied context for Models.Login.
type LoginOptions struct {
	GetDeviceId func() string
	AgentName   *string
}

// OAuthAuth implements OAuth login, token refresh, and request-auth derivation.
type OAuthAuth struct {
	Name           string
	IsSubscription bool
	LoginLabel     *string
	Login          func(ctx context.Context, interaction AuthInteraction, options *LoginOptions) (*OAuthCredential, error)
	Refresh        func(ctx context.Context, credential *OAuthCredential) (*OAuthCredential, error)
	ToAuth         func(ctx context.Context, credential *OAuthCredential) (ModelAuth, error)
}

// ProviderAuth specifies a provider's supported authentication methods.
type ProviderAuth struct {
	ApiKey *ApiKeyAuth `json:"-"`
	OAuth  *OAuthAuth  `json:"-"`
}
