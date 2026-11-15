// Ported from packages/ai/src/auth/oauth/anthropic.ts (pi v1.5.2).

package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	anthAuthorizeUrl           = "https://claude.ai/oauth/authorize"
	anthTokenUrl               = "https://platform.claude.com/v1/oauth/token"
	anthCallbackPort           = 53692
	anthCallbackPath           = "/callback"
	anthRedirectUri            = "http://localhost:53692/callback"
	anthCopyCodeRedirectUri    = "https://platform.claude.com/oauth/code/callback"
	anthBrowserLoginMethod     = "browser"
	anthCopyCodeLoginMethod    = "copy_code"
	anthScopes                 = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	anthRequestTimeoutDuration = 30 * time.Second
)

var anthClientId = anthDecode("OWQxYzI1MGEtZTYxYi00NGQ5LTg4ZWQtNTk0NGQxOTYyZjVl")

func anthDecode(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

func anthCallbackHost() string {
	if host := ai.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

func anthParseAuthorizationInput(input string) (code, state string) {
	value := js.Trim(input)
	if value == "" {
		return "", ""
	}

	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		q := parsed.Query()
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

func anthPostJson(ctx context.Context, targetUrl string, body *jsonx.Object) (string, error) {
	payload, err := jsonx.Marshal(body)
	if err != nil {
		return "", err
	}
	reqCtx, cancel := context.WithTimeout(ctx, anthRequestTimeoutDuration)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, targetUrl, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	respBody := string(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP request failed. status=%d; url=%s; body=%s", resp.StatusCode, targetUrl, respBody)
	}
	return respBody, nil
}

type anthTokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	ExpiresIn    float64 `json:"expires_in"`
}

func anthExchangeAuthorizationCode(ctx context.Context, code, state, verifier, redirectUri string) (*ai.OAuthCredential, error) {
	body := jsonx.NewObject()
	body.Set("grant_type", "authorization_code")
	body.Set("client_id", anthClientId)
	body.Set("code", code)
	body.Set("state", state)
	body.Set("redirect_uri", redirectUri)
	body.Set("code_verifier", verifier)

	responseBody, err := anthPostJson(ctx, anthTokenUrl, body)
	if err != nil {
		return nil, fmt.Errorf("Token exchange request failed. url=%s; redirect_uri=%s; response_type=authorization_code; details=%s", anthTokenUrl, redirectUri, js.ErrorString(err))
	}

	var tokenData anthTokenResponse
	if err := jsonx.Unmarshal([]byte(responseBody), &tokenData); err != nil {
		return nil, fmt.Errorf("Token exchange returned invalid JSON. url=%s; body=%s; details=%s", anthTokenUrl, responseBody, js.ErrorString(err))
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Refresh: tokenData.RefreshToken,
			Access:  tokenData.AccessToken,
			Expires: float64(js.DateNow()) + tokenData.ExpiresIn*1000 - 5*60*1000,
		},
	}, nil
}

func anthBuildAuthorizeUrl(challenge, redirectUri, verifier string) string {
	params := url.Values{}
	params.Set("code", "true")
	params.Set("client_id", anthClientId)
	params.Set("response_type", "code")
	params.Set("redirect_uri", redirectUri)
	params.Set("scope", anthScopes)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", verifier)
	return anthAuthorizeUrl + "?" + params.Encode()
}

func anthLoginBrowser(ctx context.Context, interaction ai.AuthInteraction) (*ai.OAuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	host := anthCallbackHost()
	redirectHost := "localhost"
	startServer := func(port int) (*OAuthCallbackServer[string], error) {
		return StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[string]{
			ProviderName: "Anthropic",
			Host:         host,
			Port:         port,
			Path:         anthCallbackPath,
			RedirectHost: &redirectHost,
			State:        &pkce.Verifier,
			Complete: func(ctx context.Context, code string) (string, error) {
				return code, nil
			},
		})
	}
	// Without a callback server, login continues with the pasted redirect URL.
	callback, err := startServer(anthCallbackPort)
	if err != nil {
		callback, err = startServer(0)
		if err != nil {
			callback = nil
		}
	}
	redirectUri := anthRedirectUri
	if callback != nil {
		redirectUri = callback.RedirectUri
		defer callback.Close()
	}

	instructions := "Complete login in your browser. If the browser is on another machine, paste the final redirect URL here."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          anthBuildAuthorizeUrl(pkce.Challenge, redirectUri, pkce.Verifier),
		Instructions: &instructions,
	})

	result, err := WaitForCallbackOrManualInput(ctx, interaction, callback, ManualInputPrompt{
		Message:     "Complete login in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: redirectUri,
	})
	if err != nil {
		return nil, err
	}

	var code string
	state := pkce.Verifier
	if result.Type == CallbackOrManualInputKindCallback {
		code = result.Value
	} else {
		parsedCode, parsedState := anthParseAuthorizationInput(result.Input)
		if parsedState != "" && parsedState != pkce.Verifier {
			return nil, errors.New("OAuth state mismatch")
		}
		code = parsedCode
		if parsedState != "" {
			state = parsedState
		}
	}

	if code == "" {
		return nil, errors.New("Missing authorization code")
	}
	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Exchanging authorization code for tokens...",
	})
	return anthExchangeAuthorizationCode(ctx, code, state, pkce.Verifier, redirectUri)
}

func anthLoginCopyCode(ctx context.Context, interaction ai.AuthInteraction) (*ai.OAuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	instructions := "Complete login in your browser, then copy the code Anthropic shows and paste it here."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          anthBuildAuthorizeUrl(pkce.Challenge, anthCopyCodeRedirectUri, pkce.Verifier),
		Instructions: &instructions,
	})

	placeholder := "code#state"
	input, err := interaction.Prompt(ctx, &ai.AuthPromptManualCode{
		Type:          ai.AuthPromptKindManualCode,
		Message:       "Paste the code Anthropic shows after you sign in:",
		Placeholder:   &placeholder,
		PromptContext: ctx,
	})
	if err != nil {
		return nil, err
	}
	parsedCode, parsedState := anthParseAuthorizationInput(input)
	if parsedState != "" && parsedState != pkce.Verifier {
		return nil, errors.New("OAuth state mismatch")
	}
	if parsedCode == "" {
		return nil, errors.New("Missing authorization code")
	}
	state := pkce.Verifier
	if parsedState != "" {
		state = parsedState
	}
	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Exchanging authorization code for tokens...",
	})
	return anthExchangeAuthorizationCode(ctx, parsedCode, state, pkce.Verifier, anthCopyCodeRedirectUri)
}

func anthRefreshToken(ctx context.Context, refreshToken string) (*ai.OAuthCredential, error) {
	body := jsonx.NewObject()
	body.Set("grant_type", "refresh_token")
	body.Set("client_id", anthClientId)
	body.Set("refresh_token", refreshToken)

	responseBody, err := anthPostJson(ctx, anthTokenUrl, body)
	if err != nil {
		return nil, fmt.Errorf("Anthropic token refresh request failed. url=%s; details=%s", anthTokenUrl, js.ErrorString(err))
	}

	var data anthTokenResponse
	if err := jsonx.Unmarshal([]byte(responseBody), &data); err != nil {
		return nil, fmt.Errorf("Anthropic token refresh returned invalid JSON. url=%s; body=%s; details=%s", anthTokenUrl, responseBody, js.ErrorString(err))
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Refresh: data.RefreshToken,
			Access:  data.AccessToken,
			Expires: float64(js.DateNow()) + data.ExpiresIn*1000 - 5*60*1000,
		},
	}, nil
}

// AnthropicOAuth implements the Anthropic Claude Pro/Max OAuth login and refresh flow.
var AnthropicOAuth = &ai.OAuthAuth{
	Name:           "Anthropic (Claude Pro/Max)",
	IsSubscription: true,
	Login: func(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
		method, err := interaction.Prompt(ctx, &ai.AuthPromptSelect{
			Type:    ai.AuthPromptKindSelect,
			Message: "Select Anthropic login method:",
			Options: []ai.AuthSelectOption{
				{Id: anthBrowserLoginMethod, Label: "Browser login (default)"},
				{Id: anthCopyCodeLoginMethod, Label: "Copy code login (headless)"},
			},
			PromptContext: ctx,
		})
		if err != nil {
			return nil, err
		}
		if method == anthCopyCodeLoginMethod {
			return anthLoginCopyCode(ctx, interaction)
		}
		if method != anthBrowserLoginMethod {
			return nil, fmt.Errorf("Unknown Anthropic login method: %s", method)
		}
		return anthLoginBrowser(ctx, interaction)
	},
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return anthRefreshToken(ctx, credential.Refresh)
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
