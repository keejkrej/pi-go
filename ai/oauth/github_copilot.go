// Ported from packages/ai/src/auth/oauth/github-copilot.ts (pi v1.5.2).

package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const gcCopilotApiVersion = "2026-06-01"

var (
	gcClientId       = gcDecode("SXYxLmI1MDdhMDhjODdlY2ZlOTg=")
	gcProxyEpRe      = regexp.MustCompile(`proxy-ep=([^;]+)`)
	gcCopilotHeaders = map[string]string{
		"User-Agent":             "GitHubCopilotChat/0.35.0",
		"Editor-Version":         "vscode/1.107.0",
		"Editor-Plugin-Version":  "copilot-chat/0.35.0",
		"Copilot-Integration-Id": "vscode-chat",
	}
)

func gcDecode(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

type gcDeviceCodeResponse struct {
	deviceCode      string
	userCode        string
	verificationUri string
	interval        *float64
	expiresIn       float64
}

type gcRetryPolicy struct {
	maxRetries   int
	maxElapsedMs int64
}

type gcParsedCatalog struct {
	availableModelIds []string
	policyModelIds    []string
}

func gcNormalizeDomain(input string) string {
	trimmed := js.Trim(input)
	if trimmed == "" {
		return ""
	}
	raw := trimmed
	if !strings.Contains(trimmed, "://") {
		raw = "https://" + trimmed
	}
	norm := TrustedHttpUrl(raw)
	if norm == nil {
		return ""
	}
	u, err := url.Parse(*norm)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

type gcUrls struct {
	deviceCodeUrl   string
	accessTokenUrl  string
	copilotTokenUrl string
}

func gcGetUrls(domain string) gcUrls {
	return gcUrls{
		deviceCodeUrl:   "https://" + domain + "/login/device/code",
		accessTokenUrl:  "https://" + domain + "/login/oauth/access_token",
		copilotTokenUrl: "https://api." + domain + "/copilot_internal/v2/token",
	}
}

func gcGetBaseUrlFromToken(token string) string {
	m := gcProxyEpRe.FindStringSubmatch(token)
	if len(m) < 2 {
		return ""
	}
	proxyHost := m[1]
	apiHost := strings.TrimPrefix(proxyHost, "proxy.")
	if apiHost != proxyHost {
		apiHost = "api." + apiHost
	}
	return "https://" + apiHost
}

func gcGetGitHubCopilotBaseUrl(token, enterpriseDomain string) string {
	if token != "" {
		if fromToken := gcGetBaseUrlFromToken(token); fromToken != "" {
			return fromToken
		}
	}
	if enterpriseDomain != "" {
		return "https://copilot-api." + enterpriseDomain
	}
	return "https://api.individual.githubcopilot.com"
}

func gcParseGitHubCopilotModelCatalog(raw any, allowPolicyFallback bool) (gcParsedCatalog, error) {
	root, ok := raw.(*jsonx.Object)
	if !ok || root == nil {
		return gcParsedCatalog{}, errors.New("Invalid Copilot models response")
	}
	data, ok := root.GetArray("data")
	if !ok || data == nil {
		return gcParsedCatalog{}, errors.New("Invalid Copilot models response")
	}

	type acctModel struct {
		id            string
		pickerEnabled bool
		policyState   string
	}
	accountModels := make([]acctModel, 0, len(data))
	for _, rawItem := range data {
		item, ok := rawItem.(*jsonx.Object)
		if !ok || item == nil {
			continue
		}
		id, ok := item.GetString("id")
		if !ok {
			continue
		}
		if caps, ok := item.GetObject("capabilities"); ok {
			if supports, ok := caps.GetObject("supports"); ok {
				if tc, ok := supports.GetBool("tool_calls"); ok && !tc {
					continue
				}
			}
		}
		pickerEnabled, _ := item.GetBool("model_picker_enabled")
		policyState := ""
		if pol, ok := item.GetObject("policy"); ok {
			policyState, _ = pol.GetString("state")
		}
		accountModels = append(accountModels, acctModel{
			id:            id,
			pickerEnabled: pickerEnabled,
			policyState:   policyState,
		})
	}

	var pickerModelIds []string
	for _, m := range accountModels {
		if m.pickerEnabled && m.policyState != "disabled" {
			pickerModelIds = append(pickerModelIds, m.id)
		}
	}
	usePolicyFallback := allowPolicyFallback && len(pickerModelIds) == 0

	var availableModelIds []string
	if len(pickerModelIds) > 0 || !allowPolicyFallback {
		availableModelIds = pickerModelIds
	} else {
		for _, m := range accountModels {
			if m.policyState == "enabled" {
				availableModelIds = append(availableModelIds, m.id)
			}
		}
	}

	var policyModelIds []string
	for _, m := range accountModels {
		if m.policyState == "unconfigured" && catalog.GithubCopilotModels.Has(m.id) && (m.pickerEnabled || usePolicyFallback) {
			policyModelIds = append(policyModelIds, m.id)
		}
	}

	return gcParsedCatalog{
		availableModelIds: availableModelIds,
		policyModelIds:    policyModelIds,
	}, nil
}

func gcFetchWithRateLimitRetry(
	ctx context.Context,
	method string,
	targetUrl string,
	headers map[string]string,
	body []byte,
	retryPolicy gcRetryPolicy,
) (*http.Response, error) {
	requestCtx := ctx
	var budgetCancel context.CancelFunc
	var hasDeadline bool
	var retryDeadline time.Time
	if retryPolicy.maxRetries > 0 && retryPolicy.maxElapsedMs > 0 {
		dur := time.Duration(retryPolicy.maxElapsedMs) * time.Millisecond
		requestCtx, budgetCancel = context.WithTimeout(ctx, dur)
		defer budgetCancel()
		hasDeadline = true
		retryDeadline = time.Now().Add(dur)
	}

	for retry := 0; ; retry++ {
		attemptCtx, attemptCancel := context.WithTimeout(requestCtx, 5000*time.Millisecond)
		var bodyReader io.Reader
		if body != nil {
			bodyReader = strings.NewReader(string(body))
		}
		req, err := http.NewRequestWithContext(attemptCtx, method, targetUrl, bodyReader)
		if err != nil {
			attemptCancel()
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := tuHttpClient(ctx).Do(req)
		if err != nil {
			attemptCancel()
			return nil, err
		}
		if resp.StatusCode != http.StatusTooManyRequests || retry == retryPolicy.maxRetries {
			// Read body before cancelling attemptCtx.
			raw, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			attemptCancel()
			if readErr != nil {
				return nil, readErr
			}
			resp.Body = io.NopCloser(strings.NewReader(string(raw)))
			return resp, nil
		}

		retryAfter := resp.Header.Get("retry-after")
		delayMs := float64(int64(500) << retry)
		if retryAfter != "" {
			seconds := js.ParseFloat(retryAfter)
			if math.IsNaN(seconds) {
				delayMs = js.DateParse(retryAfter) - float64(js.DateNow())
			} else {
				delayMs = seconds * 1000
			}
			if math.IsNaN(delayMs) || math.IsInf(delayMs, 0) {
				raw, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				attemptCancel()
				resp.Body = io.NopCloser(strings.NewReader(string(raw)))
				return resp, nil
			}
		}
		if delayMs < 0 {
			delayMs = 0
		}
		if hasDeadline && delayMs >= float64(retryDeadline.Sub(time.Now()).Milliseconds()) {
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			attemptCancel()
			resp.Body = io.NopCloser(strings.NewReader(string(raw)))
			return resp, nil
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		attemptCancel()

		if err := ai.Sleep(requestCtx, int64(delayMs)); err != nil {
			return nil, err
		}
	}
}

func gcFetchGitHubCopilotModels(
	ctx context.Context,
	copilotToken string,
	enterpriseDomain string,
	retryPolicy gcRetryPolicy,
) (gcParsedCatalog, error) {
	baseUrl := gcGetGitHubCopilotBaseUrl(copilotToken, enterpriseDomain)
	// Some Individual accounts return false for every picker flag despite explicit enabled policies.
	// Limit the fallback to that endpoint so other account types keep strict picker semantics.
	allowPolicyFallback := baseUrl == "https://api.individual.githubcopilot.com"
	headers := map[string]string{
		"Accept":               "application/json",
		"Authorization":        "Bearer " + copilotToken,
		"X-GitHub-Api-Version": gcCopilotApiVersion,
	}
	for k, v := range gcCopilotHeaders {
		headers[k] = v
	}

	resp, err := gcFetchWithRateLimitRetry(ctx, http.MethodGet, baseUrl+"/models", headers, nil, retryPolicy)
	if err != nil {
		return gcParsedCatalog{}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return gcParsedCatalog{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return gcParsedCatalog{}, fmt.Errorf("%d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(raw))
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return gcParsedCatalog{}, err
	}
	return gcParseGitHubCopilotModelCatalog(parsed, allowPolicyFallback)
}

func gcFetchJson(ctx context.Context, method, targetUrl string, headers map[string]string, body string) (any, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetUrl, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
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
		return nil, fmt.Errorf("%d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(raw))
	}
	return jsonx.Parse(raw)
}

func gcStartDeviceFlow(ctx context.Context, domain string) (gcDeviceCodeResponse, error) {
	urls := gcGetUrls(domain)
	form := url.Values{}
	form.Set("client_id", gcClientId)
	form.Set("scope", "read:user")

	data, err := gcFetchJson(ctx, http.MethodPost, urls.deviceCodeUrl, map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/x-www-form-urlencoded",
		"User-Agent":   "GitHubCopilotChat/0.35.0",
	}, form.Encode())
	if err != nil {
		return gcDeviceCodeResponse{}, err
	}
	obj, ok := data.(*jsonx.Object)
	if !ok || obj == nil {
		return gcDeviceCodeResponse{}, errors.New("Invalid device code response")
	}

	deviceCode, okDev := obj.GetString("device_code")
	userCode, okUser := obj.GetString("user_code")
	rawVerificationUri, okUri := obj.GetString("verification_uri")
	expiresIn, okExp := obj.GetNumber("expires_in")
	var interval *float64
	if obj.Has("interval") {
		rawInterval, okInt := obj.GetNumber("interval")
		if !okInt {
			return gcDeviceCodeResponse{}, errors.New("Invalid device code response fields")
		}
		interval = &rawInterval
	}
	if !okDev || !okUser || !okUri || !okExp {
		return gcDeviceCodeResponse{}, errors.New("Invalid device code response fields")
	}

	trustedUri := TrustedHttpUrl(rawVerificationUri)
	if trustedUri == nil {
		return gcDeviceCodeResponse{}, errors.New("Untrusted verification_uri in device code response")
	}

	return gcDeviceCodeResponse{
		deviceCode:      deviceCode,
		userCode:        userCode,
		verificationUri: *trustedUri,
		interval:        interval,
		expiresIn:       expiresIn,
	}, nil
}

func gcPollForGitHubAccessToken(ctx context.Context, domain string, device gcDeviceCodeResponse) (string, error) {
	urls := gcGetUrls(domain)
	expires := device.expiresIn
	return PollOAuthDeviceCodeFlow(ctx, OAuthDeviceCodePollOptions[string]{
		IntervalSeconds:     device.interval,
		ExpiresInSeconds:    &expires,
		WaitBeforeFirstPoll: true,
		Poll: func(pollCtx context.Context) (OAuthDeviceCodePollResult[string], error) {
			form := url.Values{}
			form.Set("client_id", gcClientId)
			form.Set("device_code", device.deviceCode)
			form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

			raw, err := gcFetchJson(pollCtx, http.MethodPost, urls.accessTokenUrl, map[string]string{
				"Accept":       "application/json",
				"Content-Type": "application/x-www-form-urlencoded",
				"User-Agent":   "GitHubCopilotChat/0.35.0",
			}, form.Encode())
			if err != nil {
				return OAuthDeviceCodePollResult[string]{}, err
			}

			obj, _ := raw.(*jsonx.Object)
			if obj != nil {
				if accessToken, ok := obj.GetString("access_token"); ok {
					return OAuthDeviceCodePollResult[string]{
						Status: OAuthDeviceCodePollStatusComplete,
						Value:  accessToken,
					}, nil
				}
				if errCode, ok := obj.GetString("error"); ok {
					switch errCode {
					case "authorization_pending":
						return OAuthDeviceCodePollResult[string]{Status: OAuthDeviceCodePollStatusPending}, nil
					case "slow_down":
						var interval *float64
						if v, ok := obj.GetNumber("interval"); ok {
							interval = &v
						}
						return OAuthDeviceCodePollResult[string]{
							Status:          OAuthDeviceCodePollStatusSlowDown,
							IntervalSeconds: interval,
						}, nil
					default:
						descSuffix := ""
						if desc, ok := obj.GetString("error_description"); ok && desc != "" {
							descSuffix = ": " + desc
						}
						return OAuthDeviceCodePollResult[string]{
							Status:  OAuthDeviceCodePollStatusFailed,
							Message: fmt.Sprintf("Device flow failed: %s%s", errCode, descSuffix),
						}, nil
					}
				}
			}

			return OAuthDeviceCodePollResult[string]{
				Status:  OAuthDeviceCodePollStatusFailed,
				Message: "Invalid device token response",
			}, nil
		},
	})
}

func gcBuildExtra(enterpriseDomain string, availableModelIds []string) *jsonx.Object {
	var extra *jsonx.Object
	ensure := func() *jsonx.Object {
		if extra == nil {
			extra = jsonx.NewObject()
		}
		return extra
	}
	if enterpriseDomain != "" {
		ensure().Set("enterpriseUrl", enterpriseDomain)
	}
	if availableModelIds != nil {
		idsAny := make([]any, len(availableModelIds))
		for i, id := range availableModelIds {
			idsAny[i] = id
		}
		ensure().Set("availableModelIds", idsAny)
	}
	return extra
}

func gcRefreshGitHubCopilotAccessToken(ctx context.Context, refreshToken, enterpriseDomain string) (*ai.OAuthCredential, error) {
	domain := enterpriseDomain
	if domain == "" {
		domain = "github.com"
	}
	urls := gcGetUrls(domain)
	headers := map[string]string{
		"Accept":        "application/json",
		"Authorization": "Bearer " + refreshToken,
	}
	for k, v := range gcCopilotHeaders {
		headers[k] = v
	}

	raw, err := gcFetchJson(ctx, http.MethodGet, urls.copilotTokenUrl, headers, "")
	if err != nil {
		return nil, err
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("Invalid Copilot token response")
	}
	token, okTok := obj.GetString("token")
	expiresAt, okExp := obj.GetNumber("expires_at")
	if !okTok || !okExp {
		return nil, errors.New("Invalid Copilot token response fields")
	}

	return &ai.OAuthCredential{
		Type: ai.AuthTypeOAuth,
		OAuthCredentials: ai.OAuthCredentials{
			Refresh: refreshToken,
			Access:  token,
			Expires: expiresAt*1000 - 5*60*1000,
			Extra:   gcBuildExtra(enterpriseDomain, nil),
		},
	}, nil
}

func gcRefreshGitHubCopilotToken(ctx context.Context, refreshToken, enterpriseDomain string) (*ai.OAuthCredential, error) {
	credentials, err := gcRefreshGitHubCopilotAccessToken(ctx, refreshToken, enterpriseDomain)
	if err != nil {
		return nil, err
	}
	cat, err := gcFetchGitHubCopilotModels(ctx, credentials.Access, enterpriseDomain, gcRetryPolicy{
		maxRetries:   0,
		maxElapsedMs: 0,
	})
	if err != nil {
		return nil, err
	}
	ids := cat.availableModelIds
	if ids == nil {
		ids = []string{}
	}
	credentials.Extra = gcBuildExtra(enterpriseDomain, ids)
	return credentials, nil
}

func gcEnableGitHubCopilotModel(ctx context.Context, token, modelId, enterpriseDomain string) (bool, error) {
	baseUrl := gcGetGitHubCopilotBaseUrl(token, enterpriseDomain)
	targetUrl := fmt.Sprintf("%s/models/%s/policy", baseUrl, modelId)

	headers := map[string]string{
		"Content-Type":       "application/json",
		"Authorization":      "Bearer " + token,
		"openai-intent":      "chat-policy",
		"x-interaction-type": "chat-policy",
	}
	for k, v := range gcCopilotHeaders {
		headers[k] = v
	}
	bodyObj := jsonx.NewObject()
	bodyObj.Set("state", "enabled")
	payload, err := jsonx.Marshal(bodyObj)
	if err != nil {
		return false, err
	}

	resp, err := gcFetchWithRateLimitRetry(ctx, http.MethodPost, targetUrl, headers, payload, gcRetryPolicy{
		maxRetries:   2,
		maxElapsedMs: 5000,
	})
	if err != nil {
		if ctx.Err() != nil {
			return false, err
		}
		return false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		raw, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("%d %s: %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(raw))
	}
	return resp.StatusCode >= 200 && resp.StatusCode < 300, nil
}

func gcEnableGitHubCopilotModels(ctx context.Context, token string, modelIds []string, enterpriseDomain string) ([]string, error) {
	var enabledModelIds []string
	for _, modelId := range modelIds {
		ok, err := gcEnableGitHubCopilotModel(ctx, token, modelId, enterpriseDomain)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			break
		}
		if ok {
			enabledModelIds = append(enabledModelIds, modelId)
		}
	}
	return enabledModelIds, nil
}

func gcUniqueStrings(parts ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, list := range parts {
		for _, item := range list {
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func gcLogin(ctx context.Context, interaction ai.AuthInteraction, _ *ai.LoginOptions) (*ai.OAuthCredential, error) {
	placeholder := "company.ghe.com"
	input, err := interaction.Prompt(ctx, &ai.AuthPromptText{
		Type:          ai.AuthPromptKindText,
		Message:       "GitHub Enterprise URL/domain (blank for github.com)",
		Placeholder:   &placeholder,
		PromptContext: ctx,
	})
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, errors.New("Login cancelled")
	}

	trimmed := js.Trim(input)
	enterpriseDomain := gcNormalizeDomain(input)
	if trimmed != "" && enterpriseDomain == "" {
		return nil, errors.New("Invalid GitHub Enterprise URL/domain")
	}
	domain := enterpriseDomain
	if domain == "" {
		domain = "github.com"
	}

	device, err := gcStartDeviceFlow(ctx, domain)
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

	githubAccessToken, err := gcPollForGitHubAccessToken(ctx, domain, device)
	if err != nil {
		return nil, err
	}
	credentials, err := gcRefreshGitHubCopilotAccessToken(ctx, githubAccessToken, enterpriseDomain)
	if err != nil {
		return nil, err
	}
	models, err := gcFetchGitHubCopilotModels(ctx, credentials.Access, enterpriseDomain, gcRetryPolicy{
		maxRetries:   2,
		maxElapsedMs: 5000,
	})
	if err != nil {
		return nil, err
	}

	var enabledModelIds []string
	if len(models.policyModelIds) > 0 {
		interaction.Notify(&ai.AuthEventProgress{
			Type:    ai.AuthEventKindProgress,
			Message: "Enabling models...",
		})
		enabledModelIds, err = gcEnableGitHubCopilotModels(ctx, credentials.Access, models.policyModelIds, enterpriseDomain)
		if err != nil {
			return nil, err
		}
	}

	credentials.Extra = gcBuildExtra(enterpriseDomain, gcUniqueStrings(models.availableModelIds, enabledModelIds))
	return credentials, nil
}

func gcEnterpriseDomain(credential *ai.OAuthCredential) string {
	if credential == nil || credential.Extra == nil {
		return ""
	}
	enterpriseUrl, ok := credential.Extra.GetString("enterpriseUrl")
	if !ok || enterpriseUrl == "" {
		return ""
	}
	return gcNormalizeDomain(enterpriseUrl)
}

// GithubCopilotOAuth implements the GitHub Copilot subscription device-code OAuth and model-policy flow.
var GithubCopilotOAuth = &ai.OAuthAuth{
	Name:           "GitHub Copilot",
	IsSubscription: true,
	Login:          gcLogin,
	Refresh: func(ctx context.Context, credential *ai.OAuthCredential) (*ai.OAuthCredential, error) {
		return gcRefreshGitHubCopilotToken(ctx, credential.Refresh, gcEnterpriseDomain(credential))
	},
	ToAuth: func(_ context.Context, credential *ai.OAuthCredential) (ai.ModelAuth, error) {
		access := credential.Access
		baseUrl := gcGetGitHubCopilotBaseUrl(credential.Access, gcEnterpriseDomain(credential))
		return ai.ModelAuth{
			ApiKey:  &access,
			BaseUrl: &baseUrl,
		}, nil
	},
}
