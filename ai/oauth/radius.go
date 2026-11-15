// Ported from packages/ai/src/auth/oauth/radius.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	radiusCallbackHost          = "127.0.0.1"
	radiusCallbackPort          = 1456
	radiusCallbackPath          = "/oauth/callback"
	radiusRedirectUri           = "http://127.0.0.1:1456/oauth/callback"
	radiusTokenExpirySkewMs     = 60_000
	radiusLoginMethodBrowser    = "browser"
	radiusLoginMethodDeviceCode = "device-code"
	radiusOauthClientId         = "pi-gateway"
	radiusOauthScope            = "gateway offline_access"
	radiusOauthDeviceCodeGrant  = "urn:ietf:params:oauth:grant-type:device_code"
)

type radiusOAuthDiscovery struct {
	authorizationEndpoint string
}

type radiusDeviceAuthorizationResponse struct {
	deviceCode      string
	userCode        string
	verificationUri string
	expiresIn       float64
	interval        *float64
}

type radiusOAuthResponseError struct {
	status     int
	oauthError string
	message    string
}

func (e *radiusOAuthResponseError) Error() string {
	return e.message
}

func radiusResolveUrl(gateway, path string) (string, error) {
	base, err := url.Parse(gateway)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(&url.URL{Path: path}).String(), nil
}

func radiusLoadOAuthDiscovery(ctx context.Context, gateway string) (radiusOAuthDiscovery, error) {
	target, err := radiusResolveUrl(gateway, "/v1/oauth")
	if err != nil {
		return radiusOAuthDiscovery{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return radiusOAuthDiscovery{}, err
	}
	req.Header.Set("accept", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return radiusOAuthDiscovery{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return radiusOAuthDiscovery{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return radiusOAuthDiscovery{}, fmt.Errorf("Could not load Radius OAuth config from %s: %d %s", gateway, resp.StatusCode, string(raw))
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return radiusOAuthDiscovery{}, fmt.Errorf("Invalid Radius OAuth config from %s", gateway)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return radiusOAuthDiscovery{}, fmt.Errorf("Invalid Radius OAuth config from %s", gateway)
	}
	epRaw, _ := obj.Get("authorizationEndpoint")
	ep := TrustedHttpUrl(epRaw)
	if ep == nil {
		return radiusOAuthDiscovery{}, fmt.Errorf("Invalid Radius OAuth config from %s", gateway)
	}
	return radiusOAuthDiscovery{authorizationEndpoint: *ep}, nil
}

func radiusNewOAuthResponseError(status int, oauthError, description, message string) *radiusOAuthResponseError {
	var detail string
	if oauthError != "" {
		if description != "" {
			detail = oauthError + ": " + description
		} else {
			detail = oauthError
		}
	} else if description != "" {
		detail = description
	} else {
		detail = strconv.Itoa(status)
	}
	return &radiusOAuthResponseError{
		status:     status,
		oauthError: oauthError,
		message:    message + ": " + detail,
	}
}

func radiusReadOAuthResponseError(resp *http.Response, raw []byte, message string) *radiusOAuthResponseError {
	text := string(raw)
	var oauthError, description string
	if text != "" {
		if parsed, err := jsonx.Parse(raw); err == nil {
			if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
				oauthError, _ = obj.GetString("error")
				description, _ = obj.GetString("error_description")
			}
		} else {
			description = text
		}
	}
	return radiusNewOAuthResponseError(resp.StatusCode, oauthError, description, message)
}

func radiusRequestOAuthToken(ctx context.Context, gateway string, body url.Values) (*ai.OAuthCredential, error) {
	target, err := radiusResolveUrl(gateway, "/v1/oauth/token")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, radiusReadOAuthResponseError(resp, raw, "Radius OAuth token request failed")
	}

	var data struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Scope        *string `json:"scope"`
	}
	if err := jsonx.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	var extra *jsonx.Object
	if data.Scope != nil {
		extra = jsonx.NewObject()
		extra.Set("scope", *data.Scope)
	}
	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  data.AccessToken,
			Refresh: data.RefreshToken,
			Expires: float64(js.DateNow()) + data.ExpiresIn*1000 - radiusTokenExpirySkewMs,
			Extra:   extra,
		},
	}, nil
}

func radiusLoginWithBrowser(ctx context.Context, gateway, authorizationEndpoint string, interaction ai.AuthInteraction) (*ai.OAuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state := uuid.NewString()
	authorizeUrl, err := url.Parse(authorizationEndpoint)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", radiusOauthClientId)
	params.Set("redirect_uri", radiusRedirectUri)
	params.Set("scope", radiusOauthScope)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("handoff", "url")
	params.Set("state", state)
	authorizeUrl.RawQuery = params.Encode()

	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[*ai.OAuthCredential]{
		ProviderName: "Radius",
		Host:         radiusCallbackHost,
		Port:         radiusCallbackPort,
		Path:         radiusCallbackPath,
		State:        &state,
		Complete: func(cbCtx context.Context, code string) (*ai.OAuthCredential, error) {
			form := url.Values{}
			form.Set("grant_type", "authorization_code")
			form.Set("client_id", radiusOauthClientId)
			form.Set("redirect_uri", radiusRedirectUri)
			form.Set("code", code)
			form.Set("code_verifier", pkce.Verifier)
			return radiusRequestOAuthToken(cbCtx, gateway, form)
		},
	})
	if err != nil {
		return nil, err
	}
	defer callback.Close()

	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Listening for OAuth callback on " + radiusRedirectUri,
	})
	instructions := "Continue in your browser."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          authorizeUrl.String(),
		Instructions: &instructions,
	})

	cred, ok, err := callback.Wait(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || cred == nil {
		return nil, errors.New("OAuth callback did not complete.")
	}
	return cred, nil
}

func radiusRequestDeviceAuthorization(ctx context.Context, gateway string) (radiusDeviceAuthorizationResponse, error) {
	target, err := radiusResolveUrl(gateway, "/v1/oauth/device")
	if err != nil {
		return radiusDeviceAuthorizationResponse{}, err
	}
	form := url.Values{}
	form.Set("client_id", radiusOauthClientId)
	form.Set("scope", radiusOauthScope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return radiusDeviceAuthorizationResponse{}, errors.New("Login cancelled")
		}
		return radiusDeviceAuthorizationResponse{}, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return radiusDeviceAuthorizationResponse{}, errors.New("Login cancelled")
		}
		return radiusDeviceAuthorizationResponse{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return radiusDeviceAuthorizationResponse{}, errors.New("Login cancelled")
		}
		return radiusDeviceAuthorizationResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return radiusDeviceAuthorizationResponse{}, radiusReadOAuthResponseError(resp, raw, "Radius OAuth device authorization failed")
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return radiusDeviceAuthorizationResponse{}, err
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return radiusDeviceAuthorizationResponse{}, errors.New("Radius OAuth device authorization response is missing required fields")
	}
	deviceCode, _ := obj.GetString("device_code")
	userCode, _ := obj.GetString("user_code")
	rawVerificationUri, _ := obj.Get("verification_uri")
	expiresIn, _ := obj.GetNumber("expires_in")
	if deviceCode == "" || userCode == "" || rawVerificationUri == nil || math.IsNaN(expiresIn) || expiresIn == 0 {
		return radiusDeviceAuthorizationResponse{}, errors.New("Radius OAuth device authorization response is missing required fields")
	}
	verificationUri := TrustedHttpUrl(rawVerificationUri)
	if verificationUri == nil {
		return radiusDeviceAuthorizationResponse{}, errors.New("Untrusted verification_uri in Radius OAuth device authorization response")
	}
	var interval *float64
	if v, ok := obj.GetNumber("interval"); ok {
		interval = &v
	}

	return radiusDeviceAuthorizationResponse{
		deviceCode:      deviceCode,
		userCode:        userCode,
		verificationUri: *verificationUri,
		expiresIn:       expiresIn,
		interval:        interval,
	}, nil
}

func radiusLoginWithDeviceCode(ctx context.Context, gateway string, interaction ai.AuthInteraction) (*ai.OAuthCredential, error) {
	device, err := radiusRequestDeviceAuthorization(ctx, gateway)
	if err != nil {
		return nil, err
	}
	var intervalInt *int
	if device.interval != nil {
		v := int(*device.interval)
		intervalInt = &v
	}
	expiresInt := int(device.expiresIn)
	interaction.Notify(&ai.AuthEventDeviceCode{
		Type:             ai.AuthEventKindDeviceCode,
		UserCode:         device.userCode,
		VerificationUri:  device.verificationUri,
		IntervalSeconds:  intervalInt,
		ExpiresInSeconds: &expiresInt,
	})

	expires := device.expiresIn
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[*ai.OAuthCredential]{
		IntervalSeconds:  device.interval,
		ExpiresInSeconds: &expires,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[*ai.OAuthCredential], error) {
			form := url.Values{}
			form.Set("grant_type", radiusOauthDeviceCodeGrant)
			form.Set("client_id", radiusOauthClientId)
			form.Set("device_code", device.deviceCode)
			credentials, err := radiusRequestOAuthToken(pollCtx, gateway, form)
			if err == nil {
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status: OAuthDeviceCodePollStatusComplete,
					Value:  credentials,
				}, nil
			}
			var respErr *radiusOAuthResponseError
			if !errors.As(err, &respErr) {
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{}, err
			}
			switch respErr.oauthError {
			case "authorization_pending":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{Status: OAuthDeviceCodePollStatusPending}, nil
			case "slow_down":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{Status: OAuthDeviceCodePollStatusSlowDown}, nil
			case "expired_token":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Device authorization expired.",
				}, nil
			case "access_denied":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Device authorization was denied.",
				}, nil
			default:
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{}, respErr
			}
		},
	})
}

// RadiusOAuthOptions configures a Radius gateway OAuth flow.
type RadiusOAuthOptions struct {
	Name    string
	Gateway string
}

// CreateRadiusOAuth constructs an ai.OAuthAuth for the configured Radius gateway.
func CreateRadiusOAuth(options RadiusOAuthOptions) *ai.OAuthAuth {
	gateway := NormalizeRadiusGatewayUrl(options.Gateway)

	return &ai.OAuthAuth{
		Name: options.Name,
		Login: func(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
			loginMethod, err := interaction.Prompt(ctx, &ai.AuthPromptSelect{
				Type:    ai.AuthPromptKindSelect,
				Message: fmt.Sprintf("Sign in to %s:", options.Name),
				Options: []ai.AuthSelectOption{
					{Id: radiusLoginMethodBrowser, Label: "Sign in with browser (recommended)"},
					{
						Id:    radiusLoginMethodDeviceCode,
						Label: "Sign in with device code (when signing in from another device)",
					},
				},
				PromptContext: ctx,
			})
			if err != nil {
				return nil, err
			}
			if loginMethod == radiusLoginMethodDeviceCode {
				return radiusLoginWithDeviceCode(ctx, gateway, interaction)
			}
			if loginMethod == radiusLoginMethodBrowser {
				discovery, err := radiusLoadOAuthDiscovery(ctx, gateway)
				if err != nil {
					return nil, err
				}
				return radiusLoginWithBrowser(ctx, gateway, discovery.authorizationEndpoint, interaction)
			}
			return nil, fmt.Errorf("Unknown %s sign-in method: %s", options.Name, loginMethod)
		},
		Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
			form := url.Values{}
			form.Set("grant_type", "refresh_token")
			form.Set("client_id", radiusOauthClientId)
			form.Set("refresh_token", credential.Refresh)
			return radiusRequestOAuthToken(ctx, gateway, form)
		},
		ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
			access := credential.Access
			return ai.ModelAuth{ApiKey: &access}, nil
		},
	}
}
