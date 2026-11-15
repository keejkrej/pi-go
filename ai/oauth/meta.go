// Ported from packages/ai/src/auth/oauth/meta.ts (pi v1.5.2).

package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	metaClientId               = "1031625952748946"
	metaAuthHost               = "https://auth.meta.com"
	metaDeviceAuthorizationUrl = metaAuthHost + "/oidc/device/authorization/"
	metaDeviceTokenUrl         = metaAuthHost + "/oidc/device/token/"
	metaApiKeyMintUrl          = "https://api.meta.ai/muse-code/key"
	metaApiKeyLifetimeMs       = 24 * 60 * 60 * 1000
	metaRequestTimeout         = 30 * time.Second
)

type metaDeviceAuthorization struct {
	deviceCode       string
	userCode         string
	verificationUri  string
	intervalSeconds  *float64
	expiresInSeconds *float64
}

func metaReadJson(resp *http.Response) *jsonx.Object {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok {
		return nil
	}
	return obj
}

func metaErrorDetail(obj *jsonx.Object) string {
	if obj == nil {
		return ""
	}
	for _, key := range []string{"error_description", "detail", "message", "error"} {
		if val, ok := obj.GetString(key); ok {
			if trimmed := js.Trim(val); trimmed != "" {
				return ": " + trimmed
			}
		}
	}
	return ""
}

func metaPositiveNumber(obj *jsonx.Object, key string) *float64 {
	if obj == nil {
		return nil
	}
	val, ok := obj.GetNumber(key)
	if !ok || math.IsNaN(val) || math.IsInf(val, 0) || val <= 0 {
		return nil
	}
	return &val
}

func metaStartDeviceAuthorization(ctx context.Context) (metaDeviceAuthorization, error) {
	reqCtx, cancel := context.WithTimeout(ctx, metaRequestTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("client_id", metaClientId)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, metaDeviceAuthorizationUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return metaDeviceAuthorization{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return metaDeviceAuthorization{}, err
	}
	defer resp.Body.Close()

	obj := metaReadJson(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return metaDeviceAuthorization{}, fmt.Errorf("Meta device authorization failed with status %d%s", resp.StatusCode, metaErrorDetail(obj))
	}

	var deviceCode, userCode string
	var verificationUri *string
	if obj != nil {
		deviceCode, _ = obj.GetString("device_code")
		userCode, _ = obj.GetString("user_code")
		if rawComplete, ok := obj.Get("verification_uri_complete"); ok {
			verificationUri = TrustedHttpUrl(rawComplete)
		}
		if verificationUri == nil {
			if rawUri, ok := obj.Get("verification_uri"); ok {
				verificationUri = TrustedHttpUrl(rawUri)
			}
		}
	}
	if deviceCode == "" || userCode == "" || verificationUri == nil {
		serialized, _ := jsonx.Stringify(obj)
		return metaDeviceAuthorization{}, fmt.Errorf("Invalid Meta device authorization response: %s", serialized)
	}

	return metaDeviceAuthorization{
		deviceCode:       deviceCode,
		userCode:         userCode,
		verificationUri:  *verificationUri,
		intervalSeconds:  metaPositiveNumber(obj, "interval"),
		expiresInSeconds: metaPositiveNumber(obj, "expires_in"),
	}, nil
}

func metaPollForIdentityToken(ctx context.Context, device metaDeviceAuthorization) (string, error) {
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[string]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    device.expiresInSeconds,
		WaitBeforeFirstPoll: true,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[string], error) {
			reqCtx, cancel := context.WithTimeout(pollCtx, metaRequestTimeout)
			defer cancel()

			form := url.Values{}
			form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
			form.Set("device_code", device.deviceCode)
			form.Set("client_id", metaClientId)

			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, metaDeviceTokenUrl, strings.NewReader(form.Encode()))
			if err != nil {
				return OAuthDeviceCodePollResult[string]{}, err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")

			resp, err := tuHttpClient(ctx).Do(req)
			if err != nil {
				return OAuthDeviceCodePollResult[string]{}, err
			}
			defer resp.Body.Close()

			obj := metaReadJson(resp)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 && obj != nil {
				if tok, ok := obj.GetString("access_token"); ok && tok != "" {
					return OAuthDeviceCodePollResult[string]{
						Status: OAuthDeviceCodePollStatusComplete,
						Value:  tok,
					}, nil
				}
			}

			var errCode string
			if obj != nil {
				errCode, _ = obj.GetString("error")
			}
			switch errCode {
			case "authorization_pending":
				return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusPending}, nil
			case "slow_down":
				return OAuthDeviceCodePollResult[string]{
					Status:          OAuthDeviceCodePollStatusSlowDown,
					IntervalSeconds: metaPositiveNumber(obj, "interval"),
				}, nil
			case "access_denied":
				return OAuthDeviceCodePollResult[string]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Meta login was denied.",
				}, nil
			case "expired_token":
				return OAuthDeviceCodePollResult[string]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Meta device authorization expired. Please restart login.",
				}, nil
			default:
				return OAuthDeviceCodePollResult[string]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: fmt.Sprintf("Meta device token request failed with status %d%s", resp.StatusCode, metaErrorDetail(obj)),
				}, nil
			}
		},
	})
}

func metaMintApiKey(ctx context.Context, identityToken string) (*ai.OAuthCredential, error) {
	reqCtx, cancel := context.WithTimeout(ctx, metaRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, metaApiKeyMintUrl, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+identityToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-version", "1.0.0")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	obj := metaReadJson(resp)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("Meta session expired (status %d). Run `/login meta` to sign in again.%s", resp.StatusCode, metaErrorDetail(obj))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Meta API key mint failed with status %d%s", resp.StatusCode, metaErrorDetail(obj))
	}

	var apiKey string
	if obj != nil {
		apiKey, _ = obj.GetString("api_key")
	}
	if apiKey == "" {
		var actionUrl *string
		if obj != nil {
			if rawAction, ok := obj.Get("action_url"); ok {
				actionUrl = TrustedHttpUrl(rawAction)
			}
		}
		if actionUrl != nil {
			return nil, fmt.Errorf("Meta did not issue an API key. Complete setup at %s", *actionUrl)
		}
		return nil, errors.New("Meta did not issue an API key.")
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Refresh: identityToken,
			Access:  apiKey,
			Expires: float64(js.DateNow()) + metaApiKeyLifetimeMs,
		},
	}, nil
}

func metaLogin(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
	run := func() (*ai.OAuthCredential, error) {
		device, err := metaStartDeviceAuthorization(ctx)
		if err != nil {
			return nil, err
		}
		var intervalInt, expiresInt *int
		if device.intervalSeconds != nil {
			v := int(*device.intervalSeconds)
			intervalInt = &v
		}
		if device.expiresInSeconds != nil {
			v := int(*device.expiresInSeconds)
			expiresInt = &v
		}
		interaction.Notify(&ai.AuthEventDeviceCode{
			Type:             ai.AuthEventKindDeviceCode,
			UserCode:         device.userCode,
			VerificationUri:  device.verificationUri,
			IntervalSeconds:  intervalInt,
			ExpiresInSeconds: expiresInt,
		})
		identityToken, err := metaPollForIdentityToken(ctx, device)
		if err != nil {
			return nil, err
		}
		interaction.Notify(&ai.AuthEventProgress{
			Type:    ai.AuthEventKindProgress,
			Message: "Enabling Meta Model API access...",
		})
		return metaMintApiKey(ctx, identityToken)
	}
	cred, err := run()
	if err != nil && ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}
	return cred, err
}

var metaLoginLabel = "Sign in with Meta"

// MetaOAuth implements the Meta Muse subscription device-code and API-key mint OAuth flow.
var MetaOAuth = &ai.OAuthAuth{
	Name:           "Meta (Muse subscription)",
	IsSubscription: true,
	LoginLabel:     &metaLoginLabel,
	Login:          metaLogin,
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return metaMintApiKey(ctx, credential.Refresh)
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
