// Ported from packages/ai/src/auth/oauth/kimi-coding.ts (pi v1.5.2).

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
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	kcClientId                   = "17e5f671-d194-4dfb-9706-5516cb48c098"
	kcDefaultOauthHost           = "https://auth.kimi.com"
	kcDeviceCodeTimeoutSeconds   = 15 * 60
	kcDefaultPollIntervalSeconds = 5
	kcRequestTimeout             = 30 * time.Second
	kcRefreshMaxRetries          = 3
)

type kcDeviceAuthorization struct {
	deviceCode              string
	userCode                string
	verificationUri         string
	verificationUriComplete string
	intervalSeconds         float64
	expiresInSeconds        float64
}

type kcTokenResponse struct {
	access  string
	refresh string
	expires float64
}

func kcGetOauthHost() string {
	override := ai.GetProviderEnvValue("KIMI_CODE_OAUTH_HOST", nil)
	if override == "" {
		override = ai.GetProviderEnvValue("KIMI_OAUTH_HOST", nil)
	}
	if override == "" {
		override = kcDefaultOauthHost
	}
	return strings.TrimRight(override, "/")
}

func kcReadJson(resp *http.Response) *jsonx.Object {
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

func kcStartDeviceAuthorization(ctx context.Context, oauthHost string) (kcDeviceAuthorization, error) {
	reqCtx, cancel := context.WithTimeout(ctx, kcRequestTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("client_id", kcClientId)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, oauthHost+"/api/oauth/device_authorization", strings.NewReader(form.Encode()))
	if err != nil {
		return kcDeviceAuthorization{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return kcDeviceAuthorization{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		text := string(raw)
		if text != "" {
			return kcDeviceAuthorization{}, fmt.Errorf("Kimi Code device authorization failed with status %d: %s", resp.StatusCode, text)
		}
		return kcDeviceAuthorization{}, fmt.Errorf("Kimi Code device authorization failed with status %d", resp.StatusCode)
	}

	obj := kcReadJson(resp)
	var deviceCode, userCode string
	var verificationUri, verificationUriComplete *string
	if obj != nil {
		deviceCode, _ = obj.GetString("device_code")
		userCode, _ = obj.GetString("user_code")
		if v, ok := obj.Get("verification_uri"); ok {
			verificationUri = TrustedHttpUrl(v)
		}
		if v, ok := obj.Get("verification_uri_complete"); ok {
			verificationUriComplete = TrustedHttpUrl(v)
		}
	}
	if deviceCode == "" || userCode == "" || verificationUri == nil || verificationUriComplete == nil {
		serialized, _ := jsonx.Stringify(obj)
		return kcDeviceAuthorization{}, fmt.Errorf("Invalid Kimi Code device authorization response: %s", serialized)
	}

	intervalSeconds := float64(kcDefaultPollIntervalSeconds)
	if v, ok := obj.GetNumber("interval"); ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 {
		intervalSeconds = v
	}
	expiresInSeconds := float64(kcDeviceCodeTimeoutSeconds)
	if v, ok := obj.GetNumber("expires_in"); ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 {
		expiresInSeconds = v
	}

	return kcDeviceAuthorization{
		deviceCode:              deviceCode,
		userCode:                userCode,
		verificationUri:         *verificationUri,
		verificationUriComplete: *verificationUriComplete,
		intervalSeconds:         intervalSeconds,
		expiresInSeconds:        expiresInSeconds,
	}, nil
}

func kcParseTokenResponse(obj *jsonx.Object, operation string) (kcTokenResponse, error) {
	var accessToken, refreshToken string
	var expiresIn float64
	var hasAccess, hasRefresh, hasExpires bool
	if obj != nil {
		accessToken, hasAccess = obj.GetString("access_token")
		refreshToken, hasRefresh = obj.GetString("refresh_token")
		expiresIn, hasExpires = obj.GetNumber("expires_in")
	}
	if !hasAccess || accessToken == "" || !hasRefresh || refreshToken == "" || !hasExpires || math.IsNaN(expiresIn) || math.IsInf(expiresIn, 0) || expiresIn <= 0 {
		serialized, _ := jsonx.Stringify(obj)
		return kcTokenResponse{}, fmt.Errorf("Kimi Code token %s response missing fields: %s", operation, serialized)
	}
	return kcTokenResponse{
		access:  accessToken,
		refresh: refreshToken,
		expires: float64(js.DateNow()) + expiresIn*1000,
	}, nil
}

func kcPollForToken(ctx context.Context, oauthHost string, device kcDeviceAuthorization) (kcTokenResponse, error) {
	interval := device.intervalSeconds
	expires := device.expiresInSeconds
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[kcTokenResponse]{
		IntervalSeconds:     &interval,
		ExpiresInSeconds:    &expires,
		WaitBeforeFirstPoll: true,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[kcTokenResponse], error) {
			reqCtx, cancel := context.WithTimeout(pollCtx, kcRequestTimeout)
			defer cancel()

			form := url.Values{}
			form.Set("client_id", kcClientId)
			form.Set("device_code", device.deviceCode)
			form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

			req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, oauthHost+"/api/oauth/token", strings.NewReader(form.Encode()))
			if err != nil {
				return OAuthDeviceCodePollResult[kcTokenResponse]{}, err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")

			resp, err := tuHttpClient(ctx).Do(req)
			if err != nil {
				return OAuthDeviceCodePollResult[kcTokenResponse]{}, err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 500 {
				raw, _ := io.ReadAll(resp.Body)
				text := string(raw)
				if text != "" {
					return OAuthDeviceCodePollResult[kcTokenResponse]{
						Status:  OAuthDeviceCodePollStatusFailed,
						Message: fmt.Sprintf("Kimi Code device token request failed with status %d: %s", resp.StatusCode, text),
					}, nil
				}
				return OAuthDeviceCodePollResult[kcTokenResponse]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: fmt.Sprintf("Kimi Code device token request failed with status %d", resp.StatusCode),
				}, nil
			}

			obj := kcReadJson(resp)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 && obj != nil {
				if _, ok := obj.GetString("access_token"); ok {
					tok, err := kcParseTokenResponse(obj, "poll")
					if err != nil {
						return OAuthDeviceCodePollResult[kcTokenResponse]{
							Status:  OAuthDeviceCodePollStatusFailed,
							Message: err.Error(),
						}, nil
					}
					return OAuthDeviceCodePollResult[kcTokenResponse]{
						Status: OAuthDeviceCodePollStatusComplete,
						Value:  tok,
					}, nil
				}
			}

			var errCode, desc string
			if obj != nil {
				errCode, _ = obj.GetString("error")
				if d, ok := obj.GetString("error_description"); ok {
					desc = ": " + d
				}
			}
			switch errCode {
			case "authorization_pending":
				return OAuthDeviceCodePollResult[kcTokenResponse]{Status: OAuthDeviceCodePollStatusPending}, nil
			case "slow_down":
				var nextInterval *float64
				if v, ok := obj.GetNumber("interval"); ok && v > 0 {
					nextInterval = &v
				}
				return OAuthDeviceCodePollResult[kcTokenResponse]{
					Status:          OAuthDeviceCodePollStatusSlowDown,
					IntervalSeconds: nextInterval,
				}, nil
			case "expired_token":
				return OAuthDeviceCodePollResult[kcTokenResponse]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Kimi Code device authorization expired. Please restart login.",
				}, nil
			case "access_denied":
				return OAuthDeviceCodePollResult[kcTokenResponse]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: "Kimi Code login was denied.",
				}, nil
			default:
				detail := ""
				if errCode != "" {
					detail = ": " + errCode + desc
				}
				return OAuthDeviceCodePollResult[kcTokenResponse]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: fmt.Sprintf("Kimi Code device token request failed (status %d)%s", resp.StatusCode, detail),
				}, nil
			}
		},
	})
}

func kcIsRetryableRefreshFailure(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func kcRefreshAttempt(ctx context.Context, oauthHost, refreshTokenValue string) (int, *jsonx.Object, error) {
	reqCtx, cancel := context.WithTimeout(ctx, kcRequestTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("client_id", kcClientId)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshTokenValue)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, oauthHost+"/api/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	// Decode the JSON body before defer cancel() cancels reqCtx.
	obj := kcReadJson(resp)
	return resp.StatusCode, obj, nil
}

func kcRefreshToken(ctx context.Context, oauthHost, refreshTokenValue string) (kcTokenResponse, error) {
	var lastError error
	for attempt := 0; attempt <= kcRefreshMaxRetries; attempt++ {
		if attempt > 0 {
			backoffMs := int64(1000) << (attempt - 1)
			if err := AbortableSleep(ctx, backoffMs, "Kimi Code token refresh aborted"); err != nil {
				return kcTokenResponse{}, err
			}
		}
		if ctx.Err() != nil {
			return kcTokenResponse{}, errors.New("Kimi Code token refresh aborted")
		}

		status, obj, err := kcRefreshAttempt(ctx, oauthHost, refreshTokenValue)
		if err != nil {
			lastError = err
			continue
		}
		if status >= 200 && status < 300 {
			return kcParseTokenResponse(obj, "refresh")
		}

		var errCode, desc string
		if obj != nil {
			errCode, _ = obj.GetString("error")
			if d, ok := obj.GetString("error_description"); ok {
				desc = ": " + d
			}
		}
		// Unauthorized: the stored credential is dead; Models clears it and prompts re-login.
		if status == http.StatusUnauthorized || status == http.StatusForbidden || errCode == "invalid_grant" {
			return kcTokenResponse{}, fmt.Errorf("Kimi Code token refresh unauthorized (status %d)%s", status, desc)
		}

		if kcIsRetryableRefreshFailure(status) && attempt < kcRefreshMaxRetries {
			lastError = fmt.Errorf("Kimi Code token refresh failed with status %d", status)
			continue
		}

		serialized, _ := jsonx.Stringify(obj)
		if serialized != "" {
			return kcTokenResponse{}, fmt.Errorf("Kimi Code token refresh failed with status %d: %s", status, serialized)
		}
		return kcTokenResponse{}, fmt.Errorf("Kimi Code token refresh failed with status %d", status)
	}

	if lastError != nil {
		return kcTokenResponse{}, lastError
	}
	return kcTokenResponse{}, errors.New("Kimi Code token refresh failed")
}

func kcLogin(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
	oauthHost := kcGetOauthHost()
	device, err := kcStartDeviceAuthorization(ctx, oauthHost)
	if err != nil {
		return nil, err
	}
	intervalInt := int(device.intervalSeconds)
	expiresInt := int(device.expiresInSeconds)
	interaction.Notify(&ai.AuthEventDeviceCode{
		Type:             ai.AuthEventKindDeviceCode,
		UserCode:         device.userCode,
		VerificationUri:  device.verificationUriComplete,
		IntervalSeconds:  &intervalInt,
		ExpiresInSeconds: &expiresInt,
	})
	tok, err := kcPollForToken(ctx, oauthHost, device)
	if err != nil {
		return nil, err
	}
	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  tok.access,
			Refresh: tok.refresh,
			Expires: tok.expires,
		},
	}, nil
}

var kcLoginLabel = "Sign in with Kimi Code"

// KimiCodingOAuth implements the Kimi Code subscription device-code OAuth flow.
var KimiCodingOAuth = &ai.OAuthAuth{
	Name:           "Kimi Code (subscription)",
	IsSubscription: true,
	LoginLabel:     &kcLoginLabel,
	Login:          kcLogin,
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		tok, err := kcRefreshToken(ctx, kcGetOauthHost(), credential.Refresh)
		if err != nil {
			return nil, err
		}
		return &ai.OAuthCredential{
			Type: ai.AuthTypeOAuth,
			OAuthCredentials: ai.OAuthCredentials{
				Access:  tok.access,
				Refresh: tok.refresh,
				Expires: tok.expires,
			},
		}, nil
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		bearer := "Bearer " + credential.Access
		headers := omap.NewMap[string, *string]()
		headers.Set("Authorization", &bearer)
		return ai.ModelAuth{Headers: headers}, nil
	},
}
