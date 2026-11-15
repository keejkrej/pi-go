package oauth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/catalog"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const tgcoTestAccessToken = "tid=test;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;"

type tgcoRoundTripFunc func(*http.Request) (*http.Response, error)

func (f tgcoRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type tgcoInteraction struct {
	mu          sync.Mutex
	events      []ai.AuthEvent
	promptReply string
}

func (i *tgcoInteraction) Notify(event ai.AuthEvent) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.events = append(i.events, event)
}

func (i *tgcoInteraction) Prompt(_ context.Context, _ ai.AuthPrompt) (string, error) {
	return i.promptReply, nil
}

func (i *tgcoInteraction) snapshotEvents() []ai.AuthEvent {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]ai.AuthEvent, len(i.events))
	copy(out, i.events)
	return out
}

func tgcoResponse(status int, body string, headers ...http.Header) *http.Response {
	h := http.Header{"Content-Type": {"application/json"}}
	for _, extra := range headers {
		for k, vals := range extra {
			for _, v := range vals {
				h.Add(k, v)
			}
		}
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func tgcoRequireCatalogModelIds(t *testing.T, count int) []string {
	t.Helper()
	vals := catalog.GithubCopilotModels.Values()
	if len(vals) < count {
		t.Fatalf("expected at least %d Copilot catalog models, got %d", count, len(vals))
	}
	out := make([]string, count)
	for i := range count {
		out[i] = vals[i].Id
	}
	return out
}

func tgcoExtraStrings(t *testing.T, cred *ai.OAuthCredential, key string) []string {
	t.Helper()
	if cred == nil || cred.Extra == nil {
		return nil
	}
	arr, ok := cred.Extra.GetArray(key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestGithubCopilotOAuth_PickerCatalogAndIndividualPolicyFallback(t *testing.T) {
	// L3 note: ai/oauth tests availableModelIds & policy updates; provider-level FilterModels lives in ai/providers.
	modelIds := tgcoRequireCatalogModelIds(t, 3)
	pickerId, disabledId, hiddenId := modelIds[0], modelIds[1], modelIds[2]

	// 1. Picker catalog filters out disabled/hidden and tool-incapable models, and skips non-unconfigured policy updates.
	catPicker, err := gcParseGitHubCopilotModelCatalog(tgcoParseJson(t, `{
		"data": [
			{"id": "`+pickerId+`", "model_picker_enabled": true, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "`+disabledId+`", "model_picker_enabled": true, "policy": {"state": "disabled"}, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "`+hiddenId+`", "model_picker_enabled": false, "policy": {"state": "enabled"}, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "no-tools", "model_picker_enabled": true, "capabilities": {"supports": {"tool_calls": false}}}
		]
	}`), true)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !slices.Equal(catPicker.availableModelIds, []string{pickerId}) {
		t.Fatalf("availableModelIds = %v, want [%s]", catPicker.availableModelIds, pickerId)
	}

	// 2. Individual accounts fall back to explicitly enabled policy models when picker catalog is empty.
	catIndividualFallback, err := gcParseGitHubCopilotModelCatalog(tgcoParseJson(t, `{
		"data": [
			{"id": "`+pickerId+`", "model_picker_enabled": false, "policy": {"state": "enabled"}, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "policy-disabled", "model_picker_enabled": false, "policy": {"state": "disabled"}, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "unconfigured", "model_picker_enabled": false, "capabilities": {"supports": {"tool_calls": true}}},
			{"id": "tool-incapable", "model_picker_enabled": false, "policy": {"state": "enabled"}, "capabilities": {"supports": {"tool_calls": false}}}
		]
	}`), true)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !slices.Equal(catIndividualFallback.availableModelIds, []string{pickerId}) {
		t.Fatalf("individual fallback availableModelIds = %v, want [%s]", catIndividualFallback.availableModelIds, pickerId)
	}

	// 3. Business accounts do NOT fall back to policy models when picker catalog is empty.
	businessClient := &http.Client{
		Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.String(), "/copilot_internal/v2/token") {
				return tgcoResponse(http.StatusOK, `{"token":"tid=biz;exp=9999999999;proxy-ep=proxy.business.githubcopilot.com;","expires_at":9999999999}`), nil
			}
			return tgcoResponse(http.StatusOK, `{
				"data": [
					{"id": "gpt-4.1", "model_picker_enabled": false, "policy": {"state": "enabled"}, "capabilities": {"supports": {"tool_calls": true}}}
				]
			}`), nil
		}),
	}
	credBiz, err := GithubCopilotOAuth.Refresh(tuWithHttpClient(context.Background(), businessClient), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "ghu_biz"},
	})
	if err != nil {
		t.Fatalf("business Refresh() failed: %v", err)
	}
	if got := tgcoExtraStrings(t, credBiz, "availableModelIds"); len(got) != 0 {
		t.Fatalf("expected empty availableModelIds for Business account, got %v", got)
	}
}

func tgcoParseJson(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGithubCopilotOAuth_DeviceCodeEventsPolicyRetryAndBudget(t *testing.T) {
	ids := tgcoRequireCatalogModelIds(t, 3)
	configuredId, unconfiguredId, toolIncapableId := ids[0], ids[1], ids[2]

	// 1. Emits AuthEventDeviceCode & enables only known, tool-capable, unconfigured policies.
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var policyModelIds []string
		catalogCount := 0

		client := &http.Client{
			Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				u := req.URL.String()
				switch {
				case strings.HasSuffix(u, "/login/device/code"):
					return tgcoResponse(http.StatusOK, `{"device_code":"dc","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
				case strings.HasSuffix(u, "/login/oauth/access_token"):
					return tgcoResponse(http.StatusOK, `{"access_token":"ghu_refresh"}`), nil
				case strings.Contains(u, "/copilot_internal/v2/token"):
					return tgcoResponse(http.StatusOK, `{"token":"`+tgcoTestAccessToken+`","expires_at":9999999999}`), nil
				case strings.HasSuffix(u, "/models"):
					mu.Lock()
					catalogCount += 1
					mu.Unlock()
					return tgcoResponse(http.StatusOK, `{
						"data": [
							{"id": "`+configuredId+`", "model_picker_enabled": true, "policy": {"state": "enabled"}, "capabilities": {"supports": {"tool_calls": true}}},
							{"id": "`+unconfiguredId+`", "model_picker_enabled": true, "policy": {"state": "unconfigured"}, "capabilities": {"supports": {"tool_calls": true}}},
							{"id": "remote-only-model", "model_picker_enabled": true, "policy": {"state": "unconfigured"}, "capabilities": {"supports": {"tool_calls": true}}},
							{"id": "`+toolIncapableId+`", "model_picker_enabled": true, "policy": {"state": "unconfigured"}, "capabilities": {"supports": {"tool_calls": false}}}
						]
					}`), nil
				case strings.Contains(u, "/models/") && strings.HasSuffix(u, "/policy"):
					trimmed := strings.TrimSuffix(u, "/policy")
					id := trimmed[strings.LastIndex(trimmed, "/")+1:]
					mu.Lock()
					policyModelIds = append(policyModelIds, id)
					mu.Unlock()
					return tgcoResponse(http.StatusOK, ""), nil
				default:
					t.Fatalf("unexpected URL: %s", u)
					return nil, nil
				}
			}),
		}

		interaction := &tgcoInteraction{}
		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := GithubCopilotOAuth.Login(tuWithHttpClient(context.Background(), client), interaction, nil)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		events := interaction.snapshotEvents()
		if len(events) < 1 {
			t.Fatal("expected AuthEventDeviceCode")
		}
		devEv, ok := events[0].(*ai.AuthEventDeviceCode)
		if !ok || devEv.UserCode != "ABCD-EFGH" || devEv.VerificationUri != "https://github.com/login/device" {
			t.Fatalf("unexpected device event: %+v", events[0])
		}

		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("Login() failed: %v", res.err)
		}
		if catalogCount != 1 {
			t.Fatalf("catalogCount = %d, want 1", catalogCount)
		}
		if !slices.Equal(policyModelIds, []string{unconfiguredId}) {
			t.Fatalf("policyModelIds = %v, want [%s]", policyModelIds, unconfiguredId)
		}
	})

	// 2. Retries throttled 429 policy update after exact Retry-After seconds.
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		policyRequests := 0

		client := &http.Client{
			Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				u := req.URL.String()
				switch {
				case strings.HasSuffix(u, "/login/device/code"):
					return tgcoResponse(http.StatusOK, `{"device_code":"dc","user_code":"U","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
				case strings.HasSuffix(u, "/login/oauth/access_token"):
					return tgcoResponse(http.StatusOK, `{"access_token":"ghu_refresh"}`), nil
				case strings.Contains(u, "/copilot_internal/v2/token"):
					return tgcoResponse(http.StatusOK, `{"token":"`+tgcoTestAccessToken+`","expires_at":9999999999}`), nil
				case strings.HasSuffix(u, "/models"):
					return tgcoResponse(http.StatusOK, `{"data":[{"id":"`+configuredId+`","model_picker_enabled":true,"policy":{"state":"unconfigured"}}]}`), nil
				case strings.Contains(u, "/models/") && strings.HasSuffix(u, "/policy"):
					mu.Lock()
					policyRequests += 1
					n := policyRequests
					mu.Unlock()
					if n == 1 {
						return tgcoResponse(http.StatusTooManyRequests, `{"error":"too many requests"}`, http.Header{"Retry-After": {"1"}}), nil
					}
					return tgcoResponse(http.StatusOK, ""), nil
				default:
					t.Fatalf("unexpected URL: %s", u)
					return nil, nil
				}
			}),
		}

		done := make(chan error, 1)
		go func() {
			_, err := GithubCopilotOAuth.Login(tuWithHttpClient(context.Background(), client), &tgcoInteraction{}, nil)
			done <- err
		}()

		synctest.Wait()
		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()

		mu.Lock()
		c1 := policyRequests
		mu.Unlock()
		if c1 != 1 {
			t.Fatalf("policyRequests after poll = %d, want 1", c1)
		}

		time.Sleep(999 * time.Millisecond)
		synctest.Wait()
		mu.Lock()
		c2 := policyRequests
		mu.Unlock()
		if c2 != 1 {
			t.Fatalf("policyRequests after 999ms = %d, want 1", c2)
		}

		time.Sleep(1 * time.Millisecond)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatalf("Login() failed: %v", err)
		}
		if policyRequests != 2 {
			t.Fatalf("policyRequests total = %d, want 2", policyRequests)
		}
	})

	// 3. Continues after policy transport failure; stops when Retry-After exceeds login budget (5s) and persists credential.
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var transportPolicyIds []string
		transportClient := &http.Client{
			Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				u := req.URL.String()
				switch {
				case strings.HasSuffix(u, "/login/device/code"):
					return tgcoResponse(http.StatusOK, `{"device_code":"dc","user_code":"U","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
				case strings.HasSuffix(u, "/login/oauth/access_token"):
					return tgcoResponse(http.StatusOK, `{"access_token":"ghu_refresh"}`), nil
				case strings.Contains(u, "/copilot_internal/v2/token"):
					return tgcoResponse(http.StatusOK, `{"token":"`+tgcoTestAccessToken+`","expires_at":9999999999}`), nil
				case strings.HasSuffix(u, "/models"):
					return tgcoResponse(http.StatusOK, `{"data":[{"id":"`+configuredId+`","model_picker_enabled":true,"policy":{"state":"unconfigured"}},{"id":"`+unconfiguredId+`","model_picker_enabled":true,"policy":{"state":"unconfigured"}}]}`), nil
				case strings.Contains(u, "/models/") && strings.HasSuffix(u, "/policy"):
					trimmed := strings.TrimSuffix(u, "/policy")
					id := trimmed[strings.LastIndex(trimmed, "/")+1:]
					mu.Lock()
					transportPolicyIds = append(transportPolicyIds, id)
					first := len(transportPolicyIds) == 1
					mu.Unlock()
					if first {
						return nil, errors.New("fetch failed")
					}
					return tgcoResponse(http.StatusOK, ""), nil
				default:
					return nil, errors.New("unexpected")
				}
			}),
		}
		done := make(chan error, 1)
		go func() {
			_, err := GithubCopilotOAuth.Login(tuWithHttpClient(context.Background(), transportClient), &tgcoInteraction{}, nil)
			done <- err
		}()
		synctest.Wait()
		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatalf("transport continue Login() failed: %v", err)
		}
		if !slices.Equal(transportPolicyIds, []string{configuredId, unconfiguredId}) {
			t.Fatalf("transportPolicyIds = %v, want [%s %s]", transportPolicyIds, configuredId, unconfiguredId)
		}

		// Exceeds budget with Retry-After: 5.
		var budgetPolicyIds []string
		budgetClient := &http.Client{
			Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				u := req.URL.String()
				switch {
				case strings.HasSuffix(u, "/login/device/code"):
					return tgcoResponse(http.StatusOK, `{"device_code":"dc","user_code":"U","verification_uri":"https://github.com/login/device","interval":1,"expires_in":900}`), nil
				case strings.HasSuffix(u, "/login/oauth/access_token"):
					return tgcoResponse(http.StatusOK, `{"access_token":"ghu_refresh"}`), nil
				case strings.Contains(u, "/copilot_internal/v2/token"):
					return tgcoResponse(http.StatusOK, `{"token":"`+tgcoTestAccessToken+`","expires_at":9999999999}`), nil
				case strings.HasSuffix(u, "/models"):
					return tgcoResponse(http.StatusOK, `{"data":[{"id":"`+configuredId+`","model_picker_enabled":true,"policy":{"state":"unconfigured"}},{"id":"`+unconfiguredId+`","model_picker_enabled":true,"policy":{"state":"unconfigured"}}]}`), nil
				case strings.Contains(u, "/models/") && strings.HasSuffix(u, "/policy"):
					trimmed := strings.TrimSuffix(u, "/policy")
					id := trimmed[strings.LastIndex(trimmed, "/")+1:]
					mu.Lock()
					budgetPolicyIds = append(budgetPolicyIds, id)
					mu.Unlock()
					return tgcoResponse(http.StatusTooManyRequests, `{"error":"too many requests"}`, http.Header{"Retry-After": {"5"}}), nil
				default:
					return nil, errors.New("unexpected")
				}
			}),
		}
		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		doneBudget := make(chan resT, 1)
		go func() {
			cred, err := GithubCopilotOAuth.Login(tuWithHttpClient(context.Background(), budgetClient), &tgcoInteraction{}, nil)
			doneBudget <- resT{cred, err}
		}()
		synctest.Wait()
		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()
		res := <-doneBudget
		if res.err != nil {
			t.Fatalf("budget exceed Login() failed: %v", res.err)
		}
		if res.cred.Access != tgcoTestAccessToken {
			t.Fatalf("unexpected cred: %+v", res.cred)
		}
		if !slices.Equal(budgetPolicyIds, []string{configuredId}) {
			t.Fatalf("budgetPolicyIds = %v, want [%s]", budgetPolicyIds, configuredId)
		}
	})
}

func TestGithubCopilotOAuth_RefreshNoRetryOn429AndEnterpriseDomain(t *testing.T) {
	catalogRequests := 0
	client := &http.Client{
		Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.String(), "/copilot_internal/v2/token") {
				return tgcoResponse(http.StatusOK, `{"token":"`+tgcoTestAccessToken+`","expires_at":9999999999}`), nil
			}
			catalogRequests += 1
			return tgcoResponse(http.StatusTooManyRequests, `{"error":"too many requests"}`, http.Header{"Retry-After": {"0"}}), nil
		}),
	}

	_, err := GithubCopilotOAuth.Refresh(tuWithHttpClient(context.Background(), client), &ai.OAuthCredential{
		OAuthCredentials: ai.OAuthCredentials{Refresh: "ghu_refresh"},
	})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 error on Refresh, got %v", err)
	}
	if catalogRequests != 1 {
		t.Fatalf("catalogRequests = %d, want 1 (no retry during refresh)", catalogRequests)
	}

	// Enterprise domain normalization + ToAuth baseUrl.
	synctest.Test(t, func(t *testing.T) {
		var seenUrls []string
		entClient := &http.Client{
			Transport: tgcoRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				seenUrls = append(seenUrls, req.URL.String())
				switch {
				case strings.HasSuffix(req.URL.String(), "/login/device/code"):
					return tgcoResponse(http.StatusOK, `{"device_code":"dc","user_code":"ENT-1","verification_uri":"https://company.ghe.com/login/device","interval":1,"expires_in":900}`), nil
				case strings.HasSuffix(req.URL.String(), "/login/oauth/access_token"):
					return tgcoResponse(http.StatusOK, `{"access_token":"ghu_ent"}`), nil
				case strings.Contains(req.URL.String(), "/copilot_internal/v2/token"):
					return tgcoResponse(http.StatusOK, `{"token":"ent-tok-no-proxy","expires_at":9999999999}`), nil
				case strings.HasSuffix(req.URL.String(), "/models"):
					return tgcoResponse(http.StatusOK, `{"data":[{"id":"gpt-4o","model_picker_enabled":true}]}`), nil
				default:
					return nil, errors.New("unexpected")
				}
			}),
		}

		type resT struct {
			cred *ai.OAuthCredential
			err  error
		}
		done := make(chan resT, 1)
		go func() {
			cred, err := GithubCopilotOAuth.Login(
				tuWithHttpClient(context.Background(), entClient),
				&tgcoInteraction{promptReply: "https://company.ghe.com/settings"},
				nil,
			)
			done <- resT{cred, err}
		}()

		synctest.Wait()
		time.Sleep(1000 * time.Millisecond)
		synctest.Wait()

		res := <-done
		if res.err != nil {
			t.Fatalf("enterprise Login() failed: %v", res.err)
		}
		if ent, ok := res.cred.Extra.GetString("enterpriseUrl"); !ok || ent != "company.ghe.com" {
			t.Fatalf("Extra.enterpriseUrl = %q (ok=%v), want company.ghe.com", ent, ok)
		}
		if !slices.Contains(seenUrls, "https://api.company.ghe.com/copilot_internal/v2/token") ||
			!slices.Contains(seenUrls, "https://copilot-api.company.ghe.com/models") {
			t.Fatalf("unexpected enterprise URLs: %v", seenUrls)
		}
		modelAuth, err := GithubCopilotOAuth.ToAuth(context.Background(), res.cred)
		if err != nil {
			t.Fatalf("ToAuth() failed: %v", err)
		}
		if modelAuth.BaseUrl == nil || *modelAuth.BaseUrl != "https://copilot-api.company.ghe.com" {
			t.Fatalf("ToAuth().BaseUrl = %v, want https://copilot-api.company.ghe.com", modelAuth.BaseUrl)
		}
	})

	// Invalid enterprise URL/domain fails cleanly.
	_, err = GithubCopilotOAuth.Login(context.Background(), &tgcoInteraction{promptReply: "ftp://bad.host"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Invalid GitHub Enterprise URL/domain") {
		t.Fatalf("expected invalid enterprise domain error, got %v", err)
	}
}
