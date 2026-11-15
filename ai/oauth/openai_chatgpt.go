// Ported from packages/ai/src/auth/oauth/openai-chatgpt.ts (pi v1.5.2).

package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	// every login registers a new client with this ID; OpenAI returns the issued client ID in the callback
	ocDynamicClientId  = "dynamic_agent_client"
	ocAgentNameHint    = "Pi"
	ocAuthorizeUrl     = "https://auth.openai.com/api/accounts/authorize"
	ocTokenUrl         = "https://auth.openai.com/api/accounts/oauth/token"
	ocResource         = "https://api.openai.com/v1"
	ocCallbackPort     = 1455
	ocCallbackPath     = "/auth/callback"
	ocRedirectUri      = "http://127.0.0.1:1455/auth/callback"
	ocDirectTokenScope = "chatgpt.tokens.use.direct"
	ocScope            = "openid profile email offline_access resource.invoke " + ocDirectTokenScope
	// Refresh this long before the real expiry so a request never starts with a token about to expire.
	ocExpiryMarginMs = 3 * 60 * 1000
)

var ocUuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type ocAuthorizationResult struct {
	code     string
	clientId string
}

type ocCallbackServerResult struct {
	val ocAuthorizationResult
	err error
}

type ocCallbackServer struct {
	server *http.Server
	result <-chan ocCallbackServerResult
	wg     sync.WaitGroup
}

func ocGetCallbackHost() string {
	if host := ai.GetProviderEnvValue("PI_OAUTH_CALLBACK_HOST", nil); host != "" {
		return host
	}
	return "127.0.0.1"
}

func ocRandomValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func ocAuthorizationResultFromCallback(u *url.URL, expectedState string) (ocAuthorizationResult, error) {
	q := u.Query()
	code := q.Get("code")
	if code == "" {
		return ocAuthorizationResult{}, errors.New("Missing authorization code")
	}
	state := q.Get("state")
	if state == "" {
		return ocAuthorizationResult{}, errors.New("Missing OAuth state")
	}
	if state != expectedState {
		return ocAuthorizationResult{}, errors.New("OAuth state mismatch")
	}
	clientId := js.Trim(q.Get("client_id"))
	if clientId == "" {
		return ocAuthorizationResult{}, errors.New("OpenAI OAuth registration callback did not contain an issued client ID")
	}
	return ocAuthorizationResult{code: code, clientId: clientId}, nil
}

func ocAuthorizationResultFromManualInput(input, expectedState string) (ocAuthorizationResult, error) {
	norm := TrustedHttpUrl(js.Trim(input))
	if norm == nil {
		return ocAuthorizationResult{}, errors.New("Paste the full callback URL from the browser")
	}
	parsed, err := url.Parse(*norm)
	if err != nil {
		return ocAuthorizationResult{}, errors.New("Paste the full callback URL from the browser")
	}
	expected, _ := url.Parse(ocRedirectUri)
	if parsed.Scheme != expected.Scheme || parsed.Host != expected.Host || parsed.Path != expected.Path {
		return ocAuthorizationResult{}, fmt.Errorf("The pasted callback URL must start with %s", ocRedirectUri)
	}
	if errParam := parsed.Query().Get("error"); errParam != "" {
		return ocAuthorizationResult{}, fmt.Errorf("ChatGPT authorization failed: %s", errParam)
	}
	return ocAuthorizationResultFromCallback(parsed, expectedState)
}

func ocSendHtml(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
}

func ocStartCallbackServer(expectedState string) (*ocCallbackServer, error) {
	listenAddr := net.JoinHostPort(ocGetCallbackHost(), strconv.Itoa(ocCallbackPort))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}

	resultCh := make(chan ocCallbackServerResult, 1)
	var once sync.Once
	sendOnce := func(res ocCallbackServerResult) {
		once.Do(func() {
			resultCh <- res
		})
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != ocCallbackPath {
				ocSendHtml(w, http.StatusNotFound, ai.OauthErrorHtml("Callback route not found."))
				return
			}
			if errCode := r.URL.Query().Get("error"); errCode != "" {
				ocSendHtml(w, http.StatusBadRequest, ai.OauthErrorHtml("ChatGPT was not connected.", "Error: "+errCode))
				sendOnce(ocCallbackServerResult{err: fmt.Errorf("ChatGPT authorization failed: %s", errCode)})
				return
			}
			authRes, err := ocAuthorizationResultFromCallback(r.URL, expectedState)
			if err != nil {
				ocSendHtml(w, http.StatusBadRequest, ai.OauthErrorHtml(err.Error()))
				return
			}
			ocSendHtml(w, http.StatusOK, ai.OauthSuccessHtml("ChatGPT authentication completed. You can close this window."))
			sendOnce(ocCallbackServerResult{val: authRes})
		}),
	}

	s := &ocCallbackServer{
		server: srv,
		result: resultCh,
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			sendOnce(ocCallbackServerResult{err: serveErr})
		}
	}()
	return s, nil
}

func ocRequestToken(ctx context.Context, body url.Values) (*jsonx.Object, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ocTokenUrl, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := tuHttpClient(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := string(raw)
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("OpenAI OAuth token request failed (%d): %s", resp.StatusCode, detail)
	}

	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("OpenAI OAuth token response must be an object")
	}
	return obj, nil
}

func ocRequireTokenString(obj *jsonx.Object, field string) (string, error) {
	val, ok := obj.GetString(field)
	if !ok || len(js.Trim(val)) == 0 {
		return "", fmt.Errorf("OpenAI OAuth token response has invalid %s", field)
	}
	return val, nil
}

func ocCredentialFromTokenResponse(token *jsonx.Object, clientId string) (*ai.OAuthCredential, error) {
	access, err := ocRequireTokenString(token, "access_token")
	if err != nil {
		return nil, err
	}
	refresh, err := ocRequireTokenString(token, "refresh_token")
	if err != nil {
		return nil, err
	}
	scope, err := ocRequireTokenString(token, "scope")
	if err != nil {
		return nil, err
	}
	expiresIn, ok := token.GetNumber("expires_in")
	if !ok || math.IsNaN(expiresIn) || math.IsInf(expiresIn, 0) || expiresIn <= 0 {
		return nil, errors.New("OpenAI OAuth token response has invalid expires_in")
	}

	scopes := strings.Fields(js.Trim(scope))
	if !slices.Contains(scopes, ocDirectTokenScope) {
		return nil, fmt.Errorf("OpenAI OAuth grant did not include %s", ocDirectTokenScope)
	}
	scopesAny := make([]any, len(scopes))
	for i, s := range scopes {
		scopesAny[i] = s
	}
	extra := jsonx.NewObject()
	extra.Set("clientId", clientId)
	extra.Set("scopes", scopesAny)

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Access:  access,
			Refresh: refresh,
			Expires: float64(js.DateNow()) + expiresIn*1000 - ocExpiryMarginMs,
			Extra:   extra,
		},
	}, nil
}

func ocExchangeAuthorizationCode(ctx context.Context, code, verifier, clientId string) (*ai.OAuthCredential, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientId)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", ocRedirectUri)
	form.Set("resource", ocResource)

	token, err := ocRequestToken(ctx, form)
	if err != nil {
		return nil, err
	}
	// Pi does not use the ID token to identify the user or read profile data.
	// Keep the presence check as part of the token-response contract.
	idToken, ok := token.GetString("id_token")
	if !ok || len(js.Trim(idToken)) == 0 {
		return nil, errors.New("OpenAI OAuth token response did not contain an ID token")
	}
	return ocCredentialFromTokenResponse(token, clientId)
}

func ocRefreshAccessToken(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
	var clientId string
	if credential != nil && credential.Extra != nil {
		clientId, _ = credential.Extra.GetString("clientId")
	}
	if len(js.Trim(clientId)) == 0 {
		return nil, errors.New("Stored OpenAI OAuth credential does not contain an issued client ID; reconnect ChatGPT")
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", clientId)
	form.Set("refresh_token", credential.Refresh)
	form.Set("resource", ocResource)

	token, err := ocRequestToken(ctx, form)
	if err != nil {
		return nil, err
	}
	return ocCredentialFromTokenResponse(token, clientId)
}

func ocAgentHostId(deviceId string) (string, error) {
	if deviceId == "" || !ocUuidPattern.MatchString(deviceId) {
		return "", errors.New("Sign in with ChatGPT requires a device ID (UUID) for this installation")
	}
	return "urn:uuid:" + strings.ToLower(deviceId), nil
}

func ocLogin(ctx context.Context, interaction ai.AuthInteraction, options *ai.LoginOptions) (*ai.OAuthCredential, error) {
	deviceId := ""
	if options != nil && options.GetDeviceId != nil {
		deviceId = options.GetDeviceId()
	}
	hostId, err := ocAgentHostId(deviceId)
	if err != nil {
		return nil, err
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, err
	}
	state, err := ocRandomValue()
	if err != nil {
		return nil, err
	}
	nonce, err := ocRandomValue()
	if err != nil {
		return nil, err
	}

	// Without this server, the browser's callback would reach whatever else holds the port (another
	// pending login or the Codex CLI), which rejects it as a state mismatch. Fail with a clear error instead.
	callback, err := ocStartCallbackServer(state)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, fmt.Errorf("Port %d is in use, probably by an unfinished login in another pi session or by the Codex CLI. Cancel that login and try again.", ocCallbackPort)
		}
		return nil, err
	}
	defer func() {
		// server.Close() closes the listener and all active/idle connections immediately, preventing
		// spare browser connections from receiving a later login's callback.
		_ = callback.server.Close()
		callback.wg.Wait()
	}()

	agentNameHint := ocAgentNameHint
	if options != nil && options.AgentName != nil {
		agentNameHint = *options.AgentName
	}

	params := url.Values{}
	params.Set("client_id", ocDynamicClientId)
	params.Set("agent_name_hint", agentNameHint)
	params.Set("ext_agent_host_id", hostId)
	params.Set("response_type", "code")
	params.Set("redirect_uri", ocRedirectUri)
	params.Set("resource", ocResource)
	params.Set("scope", ocScope)
	params.Set("state", state)
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("nonce", nonce)

	instructions := "Complete sign-in in your browser. If the callback does not complete, paste the final redirect URL here."
	interaction.Notify(&ai.AuthEventAuthUrl{
		Type:         ai.AuthEventKindAuthUrl,
		Url:          ocAuthorizeUrl + "?" + params.Encode(),
		Instructions: &instructions,
	})

	manualCtx, manualCancel := context.WithCancel(ctx)
	defer manualCancel()

	type manualRaceRes struct {
		val ocAuthorizationResult
		err error
	}
	manualCh := make(chan manualRaceRes, 1)
	go func() {
		placeholder := ocRedirectUri
		input, err := interaction.Prompt(manualCtx, &ai.AuthPromptManualCode{
			Type:          ai.AuthPromptKindManualCode,
			Message:       "Complete login in your browser, or paste the final redirect URL here:",
			Placeholder:   &placeholder,
			PromptContext: manualCtx,
		})
		if err != nil {
			manualCh <- manualRaceRes{err: err}
			return
		}
		authRes, err := ocAuthorizationResultFromManualInput(input, state)
		manualCh <- manualRaceRes{val: authRes, err: err}
	}()

	var authRes ocAuthorizationResult
	select {
	case cb := <-callback.result:
		if cb.err != nil {
			if ctx.Err() != nil {
				return nil, errors.New("Login cancelled")
			}
			return nil, cb.err
		}
		authRes = cb.val
	case man := <-manualCh:
		if man.err != nil {
			if ctx.Err() != nil {
				return nil, errors.New("Login cancelled")
			}
			return nil, man.err
		}
		authRes = man.val
	case <-ctx.Done():
		return nil, errors.New("Login cancelled")
	}

	manualCancel()
	interaction.Notify(&ai.AuthEventProgress{
		Type:    ai.AuthEventKindProgress,
		Message: "Exchanging authorization code for tokens...",
	})
	cred, err := ocExchangeAuthorizationCode(ctx, authRes.code, pkce.Verifier, authRes.clientId)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("Login cancelled")
		}
		return nil, err
	}
	return cred, nil
}

var ocLoginLabel = "Sign in with ChatGPT"

// OpenaiChatGPTOAuth implements the OpenAI ChatGPT direct-token dynamic-client OAuth flow.
var OpenaiChatGPTOAuth = &ai.OAuthAuth{
	Name:           "OpenAI (ChatGPT subscription)",
	IsSubscription: true,
	LoginLabel:     &ocLoginLabel,
	Login:          ocLogin,
	Refresh:        ocRefreshAccessToken,
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		return ai.ModelAuth{ApiKey: &access}, nil
	},
}
