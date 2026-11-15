// Ported from packages/ai/src/auth/oauth/xai.ts (pi v1.5.2).

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

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	xaiClientId                    = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiScope                       = "openid profile email offline_access grok-cli:access api:access"
	xaiDeviceCodeUrl               = "https://auth.x.ai/oauth2/device/code"
	xaiTokenUrl                    = "https://auth.x.ai/oauth2/token"
	xaiRefreshSkewMs               = 5 * 60 * 1000
	xaiDefaultTokenLifetimeSeconds = 3600
)

type xaiHttpResponse struct {
	ok     bool
	status int
	body   *jsonx.Object
}

type xaiDeviceCode struct {
	deviceCode              string
	userCode                string
	verificationUri         string
	verificationUriComplete *string
	intervalSeconds         *float64
	expiresInSeconds        float64
}

func xaiRequiredString(body *jsonx.Object, field string) (string, error) {
	if body != nil {
		if val, ok := body.GetString(field); ok && len(val) > 0 {
			return val, nil
		}
	}
	return "", fmt.Errorf("Invalid xAI OAuth response field: %s", field)
}

func xaiPositiveNumber(body *jsonx.Object, field string) (float64, error) {
	if body != nil {
		if val, ok := body.GetNumber(field); ok && !math.IsNaN(val) && !math.IsInf(val, 0) && val > 0 {
			return val, nil
		}
	}
	return 0, fmt.Errorf("Invalid xAI OAuth response field: %s", field)
}

// The verification URI is opened in the user's browser; force it to be an https URL
// so a malicious response cannot make `open` launch something else.
func xaiValidateVerificationUri(raw string) (string, error) {
	norm := TrustedHttpUrl(raw)
	if norm == nil || !strings.HasPrefix(*norm, "https://") {
		return "", errors.New("Untrusted verification URI in xAI OAuth response")
	}
	return *norm, nil
}

func xaiPostForm(ctx context.Context, targetUrl string, fields url.Values) (xaiHttpResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetUrl, strings.NewReader(fields.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return xaiHttpResponse{}, errors.New("Login cancelled")
		}
		return xaiHttpResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return xaiHttpResponse{}, errors.New("Login cancelled")
		}
		return xaiHttpResponse{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return xaiHttpResponse{}, errors.New("Login cancelled")
		}
		return xaiHttpResponse{}, err
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		if ctx.Err() != nil {
			return xaiHttpResponse{}, errors.New("Login cancelled")
		}
		return xaiHttpResponse{}, fmt.Errorf("xAI OAuth returned invalid JSON (HTTP %d)", resp.StatusCode)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		obj = jsonx.NewObject()
	}
	return xaiHttpResponse{
		ok:     resp.StatusCode >= 200 && resp.StatusCode < 300,
		status: resp.StatusCode,
		body:   obj,
	}, nil
}

func xaiRequestFailure(action string, response xaiHttpResponse) error {
	var parts []string
	if e, ok := response.body.GetString("error"); ok && e != "" {
		parts = append(parts, e)
	}
	if d, ok := response.body.GetString("error_description"); ok && d != "" {
		parts = append(parts, d)
	}
	detail := strings.Join(parts, ": ")
	if detail != "" {
		return fmt.Errorf("xAI OAuth %s failed (HTTP %d): %s", action, response.status, detail)
	}
	return fmt.Errorf("xAI OAuth %s failed (HTTP %d)", action, response.status)
}

func xaiParseDeviceCode(body *jsonx.Object) (xaiDeviceCode, error) {
	var intervalSeconds *float64
	if interval, ok := body.GetNumber("interval"); ok && !math.IsNaN(interval) && !math.IsInf(interval, 0) && interval > 0 {
		v := interval
		intervalSeconds = &v
	}

	var verificationUriComplete *string
	if rawComplete, ok := body.GetString("verification_uri_complete"); ok && len(rawComplete) > 0 {
		validated, err := xaiValidateVerificationUri(rawComplete)
		if err != nil {
			return xaiDeviceCode{}, err
		}
		verificationUriComplete = &validated
	}

	deviceCode, err := xaiRequiredString(body, "device_code")
	if err != nil {
		return xaiDeviceCode{}, err
	}
	userCode, err := xaiRequiredString(body, "user_code")
	if err != nil {
		return xaiDeviceCode{}, err
	}
	rawVerificationUri, err := xaiRequiredString(body, "verification_uri")
	if err != nil {
		return xaiDeviceCode{}, err
	}
	verificationUri, err := xaiValidateVerificationUri(rawVerificationUri)
	if err != nil {
		return xaiDeviceCode{}, err
	}
	expiresInSeconds, err := xaiPositiveNumber(body, "expires_in")
	if err != nil {
		return xaiDeviceCode{}, err
	}

	return xaiDeviceCode{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationUri:         verificationUri,
		verificationUriComplete: verificationUriComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

func xaiCredentialsFromTokenResponse(body *jsonx.Object, previousRefreshToken string) (*ai.OAuthCredential, error) {
	access, err := xaiRequiredString(body, "access_token")
	if err != nil {
		return nil, err
	}

	var refresh string
	if !body.Has("refresh_token") && previousRefreshToken != "" {
		refresh = previousRefreshToken
	} else {
		refresh, err = xaiRequiredString(body, "refresh_token")
		if err != nil {
			return nil, err
		}
	}

	expiresInSeconds := float64(xaiDefaultTokenLifetimeSeconds)
	if body.Has("expires_in") {
		expiresInSeconds, err = xaiPositiveNumber(body, "expires_in")
		if err != nil {
			return nil, err
		}
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  access,
			Refresh: refresh,
			Expires: float64(js.DateNow()) + expiresInSeconds*1000 - xaiRefreshSkewMs,
		},
	}, nil
}

func xaiRequestDeviceCode(ctx context.Context) (xaiDeviceCode, error) {
	fields := url.Values{}
	fields.Set("client_id", xaiClientId)
	fields.Set("scope", xaiScope)
	fields.Set("referrer", "pi")

	response, err := xaiPostForm(ctx, xaiDeviceCodeUrl, fields)
	if err != nil {
		return xaiDeviceCode{}, err
	}
	if !response.ok {
		return xaiDeviceCode{}, xaiRequestFailure("device authorization", response)
	}
	return xaiParseDeviceCode(response.body)
}

func xaiPollForTokens(ctx context.Context, device xaiDeviceCode) (*ai.OAuthCredential, error) {
	expires := device.expiresInSeconds
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[*ai.OAuthCredential]{
		IntervalSeconds:     device.intervalSeconds,
		ExpiresInSeconds:    &expires,
		WaitBeforeFirstPoll: true,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[*ai.OAuthCredential], error) {
			fields := url.Values{}
			fields.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
			fields.Set("client_id", xaiClientId)
			fields.Set("device_code", device.deviceCode)

			response, err := xaiPostForm(pollCtx, xaiTokenUrl, fields)
			if err != nil {
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{}, err
			}
			if response.ok {
				cred, err := xaiCredentialsFromTokenResponse(response.body, "")
				if err != nil {
					return OAuthDeviceCodePollResult[*ai.OAuthCredential]{}, err
				}
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status: OAuthDeviceCodePollStatusComplete,
					Value:  cred,
				}, nil
			}

			errCode, _ := response.body.GetString("error")
			switch errCode {
			case "authorization_pending":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{Status: OAuthDeviceCodePollStatusPending}, nil
			case "slow_down":
				var interval *float64
				if v, ok := response.body.GetNumber("interval"); ok {
					interval = &v
				}
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:          OAuthDeviceCodePollStatusSlowDown,
					IntervalSeconds: interval,
				}, nil
			case "access_denied", "authorization_denied":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "xAI device authorization was denied",
				}, nil
			case "expired_token":
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "xAI device code expired",
				}, nil
			default:
				return OAuthDeviceCodePollResult[*ai.OAuthCredential]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: xaiRequestFailure("device token polling", response).Error(),
				}, nil
			}
		},
	})
}

func xaiLogin(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
	device, err := xaiRequestDeviceCode(ctx)
	if err != nil {
		return nil, err
	}
	verificationUri := device.verificationUri
	if device.verificationUriComplete != nil {
		verificationUri = *device.verificationUriComplete
	}
	var intervalInt *int
	if device.intervalSeconds != nil {
		v := int(*device.intervalSeconds)
		intervalInt = &v
	}
	expiresInt := int(device.expiresInSeconds)
	interaction.Notify(&ai.AuthEventDeviceCode{
		Type:             ai.AuthEventKindDeviceCode,
		UserCode:         device.userCode,
		VerificationUri:  verificationUri,
		IntervalSeconds:  intervalInt,
		ExpiresInSeconds: &expiresInt,
	})
	return xaiPollForTokens(ctx, device)
}

func xaiRefreshToken(ctx context.Context, refreshToken string) (*ai.OAuthCredential, error) {
	fields := url.Values{}
	fields.Set("grant_type", "refresh_token")
	fields.Set("client_id", xaiClientId)
	fields.Set("refresh_token", refreshToken)

	response, err := xaiPostForm(ctx, xaiTokenUrl, fields)
	if err != nil {
		return nil, err
	}
	if !response.ok {
		return nil, xaiRequestFailure("token refresh", response)
	}
	return xaiCredentialsFromTokenResponse(response.body, refreshToken)
}

var xaiLoginLabel = "Sign in with SuperGrok or X Premium"

// XaiOAuth implements the xAI Grok/X subscription device-code OAuth flow.
var XaiOAuth = &ai.OAuthAuth{
	Name:           "xAI (Grok/X subscription)",
	IsSubscription: true,
	LoginLabel:     &xaiLoginLabel,
	Login:          xaiLogin,
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return xaiRefreshToken(ctx, credential.Refresh)
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
