// Ported from packages/ai/src/auth/oauth/openrouter.ts (pi v1.5.2).

package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	openAuthorizeUrl          = "https://openrouter.ai/auth"
	openTokenUrl              = "https://openrouter.ai/api/v1/auth/keys"
	openLoginTimeoutMs  int64 = 5 * 60 * 1000
	openExchangeTimeout       = 30 * time.Second
	openMaxSafeInteger        = float64(9007199254740991)
)

func openGetCallbackHost() string {
	if host := ai.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

func openParseAuthorizationInput(input string) string {
	value := js.Trim(input)
	if value == "" {
		return ""
	}
	if u, err := url.Parse(value); err == nil && u.Scheme != "" {
		return u.Query().Get("code")
	}
	if strings.Contains(value, "code=") {
		if q, err := url.ParseQuery(value); err == nil {
			return q.Get("code")
		}
	}
	return value
}

func openErrorDetail(body *jsonx.Object) string {
	if body == nil {
		return ""
	}
	if s, ok := body.GetString("error_description"); ok {
		return s
	}
	if s, ok := body.GetString("message"); ok {
		return s
	}
	if s, ok := body.GetString("error"); ok {
		return s
	}
	if errObj, ok := body.GetObject("error"); ok {
		if msg, ok := errObj.GetString("message"); ok {
			return msg
		}
	}
	return ""
}

func openExchangeAuthorizationCode(ctx context.Context, code, verifier string) (*ai.OAuthCredential, error) {
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}

	reqCtx, cancel := context.WithTimeout(ctx, openExchangeTimeout)
	defer cancel()

	reqBody := jsonx.NewObject()
	reqBody.Set("code", code)
	reqBody.Set("code_verifier", verifier)
	reqBody.Set("code_challenge_method", "S256")
	payload, err := jsonx.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, openTokenUrl, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		if errors.Is(err, context.DeadlineExceeded) || reqCtx.Err() == context.DeadlineExceeded {
			return nil, errors.New("OpenRouter OAuth token exchange timed out")
		}
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		if errors.Is(err, context.DeadlineExceeded) || reqCtx.Err() == context.DeadlineExceeded {
			return nil, errors.New("OpenRouter OAuth token exchange timed out")
		}
		return nil, err
	}

	var body *jsonx.Object
	parsed, parseErr := jsonx.Parse(raw)
	if parseErr != nil {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil, errors.New("OpenRouter OAuth returned invalid JSON")
		}
	} else if obj, ok := parsed.(*jsonx.Object); ok {
		body = obj
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := openErrorDetail(body)
		if detail != "" {
			return nil, fmt.Errorf("OpenRouter OAuth key exchange failed (HTTP %d): %s", resp.StatusCode, detail)
		}
		return nil, fmt.Errorf("OpenRouter OAuth key exchange failed (HTTP %d)", resp.StatusCode)
	}

	var key string
	if body != nil {
		key, _ = body.GetString("key")
	}
	if key == "" {
		return nil, errors.New(`OpenRouter OAuth response carries no "key"`)
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  key,
			Refresh: "",
			Expires: openMaxSafeInteger,
		},
	}, nil
}

func openLogin(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	timeoutMs := openLoginTimeoutMs
	// OpenRouter sends no `state`; the random path keeps stray requests from completing the sign-in.
	callback, err := StartOAuthCallbackServer(ctx, OAuthCallbackServerOptions[*ai.OAuthCredential]{
		ProviderName: "OpenRouter",
		Host:         openGetCallbackHost(),
		Port:         0,
		Path:         "/oauth/callback/" + uuid.NewString(),
		Complete: func(cbCtx context.Context, code string) (*ai.OAuthCredential, error) {
			return openExchangeAuthorizationCode(cbCtx, code, pkce.Verifier)
		},
		TimeoutMs: &timeoutMs,
	})
	if err != nil {
		return nil, err
	}
	defer callback.Close()

	params := url.Values{}
	params.Set("callback_url", callback.RedirectUri)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	authUrl := openAuthorizeUrl + "?" + params.Encode()

	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Listening for OpenRouter OAuth callback on " + callback.RedirectUri,
	})
	instructions := "Complete sign-in in your browser. If the browser is on another machine, paste the final redirect URL here."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          authUrl,
		Instructions: &instructions,
	})

	result, err := WaitForCallbackOrManualInput(ctx, interaction, callback, ManualInputPrompt{
		Message:     "Complete sign-in in your browser, or paste the authorization code / redirect URL here:",
		Placeholder: callback.RedirectUri,
	})
	if err != nil {
		return nil, err
	}
	if result.Type == CallbackOrManualInputKindCallback {
		return result.Value, nil
	}
	code := openParseAuthorizationInput(result.Input)
	if code == "" {
		return nil, errors.New("Missing authorization code")
	}
	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Exchanging authorization code for an API key...",
	})
	return openExchangeAuthorizationCode(ctx, code, pkce.Verifier)
}

var openLoginLabel = "Sign in with OpenRouter"

// OpenRouterOAuth implements the OpenRouter PKCE key-exchange OAuth flow.
var OpenRouterOAuth = &ai.OAuthAuth{
	Name:       "OpenRouter OAuth",
	LoginLabel: &openLoginLabel,
	Login:      openLogin,
	Refresh: func(_ context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return credential, nil
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
