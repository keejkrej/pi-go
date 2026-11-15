// Ported from packages/ai/src/auth/oauth/openai-codex.ts (pi v1.5.2).

package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	ocoClientId                 = "app_EMoamEEZ73f0CkXaXp7hrann"
	ocoAuthBaseUrl              = "https://auth.openai.com"
	ocoAuthorizeUrl             = ocoAuthBaseUrl + "/oauth/authorize"
	ocoTokenUrl                 = ocoAuthBaseUrl + "/oauth/token"
	ocoRedirectUri              = "http://localhost:1455/auth/callback"
	ocoDeviceUserCodeUrl        = ocoAuthBaseUrl + "/api/accounts/deviceauth/usercode"
	ocoDeviceTokenUrl           = ocoAuthBaseUrl + "/api/accounts/deviceauth/token"
	ocoDeviceVerificationUri    = ocoAuthBaseUrl + "/codex/device"
	ocoDeviceRedirectUri        = ocoAuthBaseUrl + "/deviceauth/callback"
	ocoDeviceCodeTimeoutSeconds = float64(15 * 60)
	ocoBrowserLoginMethod       = "browser"
	ocoDeviceCodeLoginMethod    = "device_code"
	ocoScope                    = "openid profile email offline_access"
	ocoJwtClaimPath             = "https://api.openai.com/auth"
)

type ocoOAuthToken struct {
	access  string
	refresh string
	expires float64
}

type ocoDeviceAuthInfo struct {
	deviceAuthId    string
	userCode        string
	intervalSeconds float64
}

type ocoDeviceTokenSuccess struct {
	authorizationCode string
	codeVerifier      string
}

func ocoGetCallbackHost() string {
	if host := ai.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

func ocoCreateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ocoParseAuthorizationInput(input string) (code, state string) {
	value := js.Trim(input)
	if value == "" {
		return "", ""
	}
	if u, err := url.Parse(value); err == nil && u.Scheme != "" {
		q := u.Query()
		return q.Get("code"), q.Get("state")
	}
	if left, right, ok := strings.Cut(value, "#"); ok {
		return left, right
	}
	if strings.Contains(value, "code=") {
		if q, err := url.ParseQuery(value); err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return value, ""
}

func ocoDecodeJwt(token string) *jsonx.Object {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	if payload == "" {
		return nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(payload, "="))
	if err != nil {
		// Also accept standard base64 if caller constructed non-URL payload.
		decoded, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil
		}
	}
	parsed, err := jsonx.Parse(decoded)
	if err != nil {
		return nil
	}
	obj, _ := parsed.(*jsonx.Object)
	return obj
}

func ocoDoWithLoginCancellation(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	return resp, nil
}

func ocoReadTokenResponse(resp *http.Response, operation string) (ocoOAuthToken, error) {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ocoOAuthToken{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := string(raw)
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return ocoOAuthToken{}, fmt.Errorf("OpenAI Codex token %s failed (%d): %s", operation, resp.StatusCode, detail)
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return ocoOAuthToken{}, err
	}
	obj, ok := parsed.(*jsonx.Object)
	var access, refresh string
	var expiresIn float64
	var hasAccess, hasRefresh, hasExpires bool
	if ok && obj != nil {
		access, hasAccess = obj.GetString("access_token")
		refresh, hasRefresh = obj.GetString("refresh_token")
		expiresIn, hasExpires = obj.GetNumber("expires_in")
	}
	if !hasAccess || access == "" || !hasRefresh || refresh == "" || !hasExpires {
		serialized, _ := jsonx.Stringify(parsed)
		return ocoOAuthToken{}, fmt.Errorf("OpenAI Codex token %s response missing fields: %s", operation, serialized)
	}
	return ocoOAuthToken{
		access:  access,
		refresh: refresh,
		expires: float64(js.DateNow()) + expiresIn*1000,
	}, nil
}

func ocoExchangeAuthorizationCode(ctx context.Context, code, verifier, redirectUri string) (ocoOAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", ocoClientId)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectUri)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ocoTokenUrl, strings.NewReader(form.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return ocoOAuthToken{}, errors.New("Login cancelled")
		}
		return ocoOAuthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := ocoDoWithLoginCancellation(ctx, req)
	if err != nil {
		return ocoOAuthToken{}, err
	}
	defer resp.Body.Close()
	return ocoReadTokenResponse(resp, "exchange")
}

func ocoRefreshAccessToken(ctx context.Context, refreshToken string) (ocoOAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", ocoClientId)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ocoTokenUrl, strings.NewReader(form.Encode()))
	if err != nil {
		return ocoOAuthToken{}, fmt.Errorf("OpenAI Codex token refresh error: %s", err.Error())
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return ocoOAuthToken{}, fmt.Errorf("OpenAI Codex token refresh error: %s", err.Error())
	}
	defer resp.Body.Close()
	return ocoReadTokenResponse(resp, "refresh")
}

func ocoStartDeviceAuth(ctx context.Context) (ocoDeviceAuthInfo, error) {
	bodyObj := jsonx.NewObject()
	bodyObj.Set("client_id", ocoClientId)
	payload, err := jsonx.Marshal(bodyObj)
	if err != nil {
		return ocoDeviceAuthInfo{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ocoDeviceUserCodeUrl, strings.NewReader(string(payload)))
	if err != nil {
		if ctx.Err() != nil {
			return ocoDeviceAuthInfo{}, errors.New("Login cancelled")
		}
		return ocoDeviceAuthInfo{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ocoDoWithLoginCancellation(ctx, req)
	if err != nil {
		return ocoDeviceAuthInfo{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ocoDeviceAuthInfo{}, errors.New("Login cancelled")
		}
		return ocoDeviceAuthInfo{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusNotFound {
			return ocoDeviceAuthInfo{}, errors.New("OpenAI Codex device code login is not enabled for this server. Use browser login or verify the server URL.")
		}
		bodyText := string(raw)
		if bodyText != "" {
			return ocoDeviceAuthInfo{}, fmt.Errorf("OpenAI Codex device code request failed with status %d: %s", resp.StatusCode, bodyText)
		}
		return ocoDeviceAuthInfo{}, fmt.Errorf("OpenAI Codex device code request failed with status %d", resp.StatusCode)
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return ocoDeviceAuthInfo{}, err
	}
	obj, _ := parsed.(*jsonx.Object)
	var deviceAuthId, userCode string
	var intervalSeconds float64
	var hasInterval bool
	if obj != nil {
		deviceAuthId, _ = obj.GetString("device_auth_id")
		userCode, _ = obj.GetString("user_code")
		if v, ok := obj.GetNumber("interval"); ok {
			intervalSeconds = v
			hasInterval = true
		} else if s, ok := obj.GetString("interval"); ok {
			if parsedNum, err := strconv.ParseFloat(js.Trim(s), 64); err == nil {
				intervalSeconds = parsedNum
				hasInterval = true
			}
		}
	}
	if deviceAuthId == "" || userCode == "" || !hasInterval || math.IsNaN(intervalSeconds) || math.IsInf(intervalSeconds, 0) || intervalSeconds < 0 {
		serialized, _ := jsonx.Stringify(parsed)
		return ocoDeviceAuthInfo{}, fmt.Errorf("Invalid OpenAI Codex device code response: %s", serialized)
	}
	return ocoDeviceAuthInfo{
		deviceAuthId:    deviceAuthId,
		userCode:        userCode,
		intervalSeconds: intervalSeconds,
	}, nil
}

func ocoPollDeviceAuth(ctx context.Context, device ocoDeviceAuthInfo) (ocoDeviceTokenSuccess, error) {
	interval := device.intervalSeconds
	expires := ocoDeviceCodeTimeoutSeconds
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[ocoDeviceTokenSuccess]{
		IntervalSeconds:  &interval,
		ExpiresInSeconds: &expires,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[ocoDeviceTokenSuccess], error) {
			reqObj := jsonx.NewObject()
			reqObj.Set("device_auth_id", device.deviceAuthId)
			reqObj.Set("user_code", device.userCode)
			payload, err := jsonx.Marshal(reqObj)
			if err != nil {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, err
			}
			req, err := http.NewRequestWithContext(pollCtx, http.MethodPost, ocoDeviceTokenUrl, strings.NewReader(string(payload)))
			if err != nil {
				if pollCtx.Err() != nil {
					return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, errors.New("Login cancelled")
				}
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, err
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := ocoDoWithLoginCancellation(pollCtx, req)
			if err != nil {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				raw, err := io.ReadAll(resp.Body)
				if err != nil {
					return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, err
				}
				parsed, err := jsonx.Parse(raw)
				if err != nil {
					return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{}, err
				}
				obj, _ := parsed.(*jsonx.Object)
				var authCode, codeVerifier string
				if obj != nil {
					authCode, _ = obj.GetString("authorization_code")
					codeVerifier, _ = obj.GetString("code_verifier")
				}
				if authCode == "" || codeVerifier == "" {
					serialized, _ := jsonx.Stringify(parsed)
					return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{
						Status:  OAuthDeviceCodePollStatusFailed,
						Message: fmt.Sprintf("Invalid OpenAI Codex device auth token response: %s", serialized),
					}, nil
				}
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{
					Status: OAuthDeviceCodePollStatusComplete,
					Value: ocoDeviceTokenSuccess{
						authorizationCode: authCode,
						codeVerifier:      codeVerifier,
					},
				}, nil
			}

			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{Status: OAuthDeviceCodePollStatusPending}, nil
			}

			raw, _ := io.ReadAll(resp.Body)
			responseBody := string(raw)
			var errorCode string
			if parsed, err := jsonx.Parse(raw); err == nil {
				if obj, ok := parsed.(*jsonx.Object); ok && obj != nil {
					if s, ok := obj.GetString("error"); ok {
						errorCode = s
					} else if errObj, ok := obj.GetObject("error"); ok {
						errorCode, _ = errObj.GetString("code")
					}
				}
			}

			if errorCode == "deviceauth_authorization_pending" {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{Status: OAuthDeviceCodePollStatusPending}, nil
			}
			if errorCode == "slow_down" {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{Status: OAuthDeviceCodePollStatusSlowDown}, nil
			}
			if responseBody != "" {
				return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{
					Status:  OAuthDeviceCodePollStatusFailed,
					Message: fmt.Sprintf("OpenAI Codex device auth failed with status %d: %s", resp.StatusCode, responseBody),
				}, nil
			}
			return OAuthDeviceCodePollResult[ocoDeviceTokenSuccess]{
				Status:  OAuthDeviceCodePollStatusFailed,
				Message: fmt.Sprintf("OpenAI Codex device auth failed with status %d", resp.StatusCode),
			}, nil
		},
	})
}

func ocoCreateAuthorizationFlow(originatorOpt *string) (verifier, state, authUrl string, err error) {
	originator := "pi"
	if originatorOpt != nil {
		originator = *originatorOpt
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return "", "", "", err
	}
	state, err = ocoCreateState()
	if err != nil {
		return "", "", "", err
	}

	u, err := url.Parse(ocoAuthorizeUrl)
	if err != nil {
		return "", "", "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", ocoClientId)
	q.Set("redirect_uri", ocoRedirectUri)
	q.Set("scope", ocoScope)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", originator)
	u.RawQuery = q.Encode()

	return pkce.Verifier, state, u.String(), nil
}

func ocoGetAccountId(accessToken string) string {
	payload := ocoDecodeJwt(accessToken)
	if payload == nil {
		return ""
	}
	authObj, ok := payload.GetObject(ocoJwtClaimPath)
	if !ok {
		return ""
	}
	accountId, _ := authObj.GetString("chatgpt_account_id")
	return accountId
}

func ocoCredentialsFromToken(token ocoOAuthToken) (*ai.OAuthCredential, error) {
	accountId := ocoGetAccountId(token.access)
	if accountId == "" {
		return nil, errors.New("Failed to extract accountId from token")
	}
	extra := jsonx.NewObject()
	extra.Set("accountId", accountId)
	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  token.access,
			Refresh: token.refresh,
			Expires: token.expires,
			Extra:   extra,
		},
	}, nil
}

func ocoExchangeAuthorizationCodeForCredentials(ctx context.Context, code, verifier, redirectUri string) (*ai.OAuthCredential, error) {
	token, err := ocoExchangeAuthorizationCode(ctx, code, verifier, redirectUri)
	if err != nil {
		return nil, err
	}
	return ocoCredentialsFromToken(token)
}

func ocoLoginDeviceCode(ctx context.Context, interaction ai.AuthInteraction) (*ai.OAuthCredential, error) {
	device, err := ocoStartDeviceAuth(ctx)
	if err != nil {
		return nil, err
	}
	intervalInt := int(device.intervalSeconds)
	expiresInt := int(ocoDeviceCodeTimeoutSeconds)
	interaction.Notify(&ai.AuthEventDeviceCode{
		Type:             ai.AuthEventKindDeviceCode,
		UserCode:         device.userCode,
		VerificationUri:  ocoDeviceVerificationUri,
		IntervalSeconds:  &intervalInt,
		ExpiresInSeconds: &expiresInt,
	})
	code, err := ocoPollDeviceAuth(ctx, device)
	if err != nil {
		return nil, err
	}
	return ocoExchangeAuthorizationCodeForCredentials(ctx, code.authorizationCode, code.codeVerifier, ocoDeviceRedirectUri)
}

func ocoLoginBrowser(ctx context.Context, interaction ai.AuthInteraction, options *ai.LoginOptions) (*ai.OAuthCredential, error) {
	var originatorOpt *string
	if options != nil {
		originatorOpt = options.AgentName
	}
	verifier, state, authUrl, err := ocoCreateAuthorizationFlow(originatorOpt)
	if err != nil {
		return nil, err
	}
	// Port 1455 is shared with the Codex CLI; when it is taken, fall back to the pasted redirect URL.
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[string]{
		ProviderName: "OpenAI",
		Host:         ocoGetCallbackHost(),
		Port:         1455,
		Path:         "/auth/callback",
		State:        &state,
		Complete: func(_ context.Context, code string) (string, error) {
			return code, nil
		},
	})
	if err != nil {
		callback = nil
	} else {
		defer callback.Close()
	}

	instructions := "A browser window should open. Complete login to finish."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          authUrl,
		Instructions: &instructions,
	})

	result, err := WaitForCallbackOrManualInput(ctx, interaction, callback, ManualInputPrompt{
		Message:     "Complete login in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: ocoRedirectUri,
	})
	if err != nil {
		return nil, err
	}

	var code string
	if result.Type == CallbackOrManualInputKindCallback {
		code = result.Value
	} else {
		parsedCode, parsedState := ocoParseAuthorizationInput(result.Input)
		if parsedState != "" && parsedState != state {
			return nil, errors.New("State mismatch")
		}
		code = parsedCode
	}

	if code == "" {
		return nil, errors.New("Missing authorization code")
	}
	return ocoExchangeAuthorizationCodeForCredentials(ctx, code, verifier, ocoRedirectUri)
}

func ocoRefreshToken(ctx context.Context, refreshToken string) (*ai.OAuthCredential, error) {
	token, err := ocoRefreshAccessToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	return ocoCredentialsFromToken(token)
}

// OpenaiCodexOAuth implements the OpenAI ChatGPT Plus/Pro (Codex) OAuth login and refresh flow.
var OpenaiCodexOAuth = &ai.OAuthAuth{
	Name:           "OpenAI (ChatGPT Plus/Pro)",
	IsSubscription: true,
	Login: func(ctx context.Context, interaction ai.AuthInteraction, options *ai.LoginOptions) (*ai.OAuthCredential, error) {
		method, err := interaction.Prompt(ctx, &ai.AuthPromptSelect{
			Type:    ai.AuthPromptKindSelect,
			Message: "Select OpenAI Codex login method:",
			Options: []ai.AuthSelectOption{
				{Id: ocoBrowserLoginMethod, Label: "Browser login (default)"},
				{Id: ocoDeviceCodeLoginMethod, Label: "Device code login (headless)"},
			},
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		if method == ocoDeviceCodeLoginMethod {
			return ocoLoginDeviceCode(ctx, interaction)
		}
		if method != ocoBrowserLoginMethod {
			return nil, fmt.Errorf("Unknown OpenAI Codex login method: %s", method)
		}
		return ocoLoginBrowser(ctx, interaction, options)
	},
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return ocoRefreshToken(ctx, credential.Refresh)
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
