// Ported from packages/ai/test/bedrock-{credentials,custom-headers,endpoint-resolution,response-headers}.test.ts (pi v1.5.2).

package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/api"
	"github.com/keejkrej/pi-go/internal/omap"
)

func tbtheHeadersModelAndStreamServer(t *testing.T, useTLS bool, status int, respHeaders http.Header, bodyBytes []byte) (*httptest.Server, *http.Header, *string) {
	t.Helper()
	var capturedHeaders http.Header
	var capturedPath string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		capturedPath = r.URL.Path
		for k, vals := range respHeaders {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(bodyBytes)
	})
	var srv *httptest.Server
	if useTLS {
		srv = httptest.NewTLSServer(handler)
	} else {
		srv = httptest.NewServer(handler)
	}
	return srv, &capturedHeaders, &capturedPath
}

func tbtheMinimalSuccessStream() []byte {
	var b []byte
	b = append(b, tbdEncodeEventFrame("messageStart", `{"role":"assistant"}`)...)
	b = append(b, tbdEncodeEventFrame("messageStop", `{"stopReason":"end_turn"}`)...)
	return b
}

func TestBedrockBearerAuthSigV4SkipAuthAndCustomHeaders(t *testing.T) {
	// Clear ambient AWS_* env so host machine credentials never affect tests.
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_BEDROCK_SKIP_AUTH", "AWS_REGION", "AWS_DEFAULT_REGION",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// 1. Custom headers + option headers + header suppression + protected header filtering + Response headers callback.
	respHdr := make(http.Header)
	respHdr.Set("Content-Type", "application/vnd.amazon.eventstream")
	respHdr.Set("X-RateLimit-Remaining", "42")
	srv, seenHdrs, seenPath := tbtheHeadersModelAndStreamServer(t, true, http.StatusOK, respHdr, tbtheMinimalSuccessStream())
	defer srv.Close()

	model := tbdModelWithHeaders(srv.URL, nil)

	optHeaders := omap.NewMap[string, *string]()
	optHeaders.Set("Authorization", tbdStrPtr("Bearer bad-override"))
	optHeaders.Set("HOST", tbdStrPtr("evil.invalid"))
	optHeaders.Set("X-Amz-Security-Token", tbdStrPtr("injected"))
	optHeaders.Set("x-custom-opt", tbdStrPtr("opt-val"))
	optHeaders.Set("x-suppress", nil)
	optHeaders.Set("x-amz-date", tbdStrPtr("20200101T000000Z"))
	optHeaders.Set("User-Agent", tbdStrPtr("custom-ua/1.0"))

	var onResp *ai.ProviderResponse
	msg, err := api.StreamBedrockConverseStream(context.Background(), model, tbdContext(), &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				HTTPClient: srv.Client(),
				ApiKey:     tbdStrPtr("bedrock-bearer-secret"),
				Headers:    optHeaders,
				Env:        ai.ProviderEnv{"AWS_REGION": "us-east-1"},
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					onResp = resp
					return nil
				},
			},
		},
	}).Result(context.Background())
	if err != nil || msg.StopReason != ai.StopReasonStop {
		errStr := ""
		if msg.ErrorMessage != nil {
			errStr = *msg.ErrorMessage
		}
		t.Fatalf("bearer stream failed: err=%v stop=%q errMsg=%q", err, msg.StopReason, errStr)
	}
	if !strings.Contains(*seenPath, "/converse-stream") {
		t.Fatalf("expected converse-stream path, got %q", *seenPath)
	}
	if seenHdrs.Get("Authorization") != "Bearer bedrock-bearer-secret" {
		t.Fatalf("expected Bearer token auth without override, got %q", seenHdrs.Get("Authorization"))
	}
	if seenHdrs.Get("X-Amz-Security-Token") != "" || seenHdrs.Get("X-Amz-Date") != "" {
		t.Fatalf("expected custom x-amz-* headers stripped, got sec=%q date=%q", seenHdrs.Get("X-Amz-Security-Token"), seenHdrs.Get("X-Amz-Date"))
	}
	if seenHdrs.Get("x-custom-opt") != "opt-val" || seenHdrs.Get("x-suppress") != "" || seenHdrs.Get("User-Agent") != "custom-ua/1.0" {
		t.Fatalf("unexpected custom headers: %+v", *seenHdrs)
	}
	if onResp == nil || onResp.Status != 200 || onResp.Headers["x-ratelimit-remaining"] != "42" {
		t.Fatalf("unexpected OnResponse capture: %+v", onResp)
	}

	// 2. Scoped SigV4 credentials in options.Env produce AWS4-HMAC-SHA256 Authorization.
	msgSig, err := api.StreamBedrockConverseStream(context.Background(), tbdModel("us.anthropic.claude-opus-4-8", "", srv.URL, nil), tbdContext(), &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				HTTPClient: srv.Client(),
				ApiKey:     tbdStrPtr("<authenticated>"), // sentinel falls through to standard AWS credentials
				Env: ai.ProviderEnv{
					"AWS_ACCESS_KEY_ID":     "AKIATESTKEY",
					"AWS_SECRET_ACCESS_KEY": "secret-access-key",
					"AWS_SESSION_TOKEN":     "sess-tok",
					"AWS_REGION":            "us-west-2",
				},
			},
		},
	}).Result(context.Background())
	if err != nil || msgSig.StopReason != ai.StopReasonStop {
		t.Fatalf("SigV4 stream failed: err=%v msg=%+v", err, msgSig)
	}
	if !strings.HasPrefix(seenHdrs.Get("Authorization"), "AWS4-HMAC-SHA256 ") || seenHdrs.Get("X-Amz-Security-Token") != "sess-tok" {
		t.Fatalf("expected AWS4 SigV4 auth header + session token, got auth=%q sec=%q",
			seenHdrs.Get("Authorization"), seenHdrs.Get("X-Amz-Security-Token"))
	}

	// 3. AWS_BEDROCK_SKIP_AUTH=1 uses dummy static SigV4 credentials and ignores Bearer token.
	msgSkip, err := api.StreamBedrockConverseStream(context.Background(), tbdModel("us.anthropic.claude-opus-4-8", "", srv.URL, nil), tbdContext(), &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				HTTPClient: srv.Client(),
				Env: ai.ProviderEnv{
					"AWS_BEDROCK_SKIP_AUTH":    "1",
					"AWS_BEARER_TOKEN_BEDROCK": "should-be-ignored",
					"AWS_REGION":               "us-east-1",
				},
			},
		},
	}).Result(context.Background())
	if err != nil || msgSkip.StopReason != ai.StopReasonStop {
		t.Fatalf("skip-auth stream failed: err=%v msg=%+v", err, msgSkip)
	}
	if !strings.Contains(seenHdrs.Get("Authorization"), "Credential=dummy-access-key/") {
		t.Fatalf("expected dummy-access-key SigV4 auth with AWS_BEDROCK_SKIP_AUTH=1, got %q", seenHdrs.Get("Authorization"))
	}
}

func TestBedrockEndpointAndRegionResolutionAndErrorResponseHeaders(t *testing.T) {
	for _, key := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_BEARER_TOKEN_BEDROCK",
		"AWS_BEDROCK_SKIP_AUTH", "AWS_REGION", "AWS_DEFAULT_REGION",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	// ARN model ID extracts region (including GovCloud ARN) for SigV4 CredentialScope even when AWS_REGION differs.
	respErrHdr := make(http.Header)
	respErrHdr.Set("x-amzn-errortype", "ValidationException")
	respErrHdr.Set("x-amzn-requestid", "req-err-1")
	srv, seenHdrs, _ := tbtheHeadersModelAndStreamServer(t, false, http.StatusBadRequest, respErrHdr, []byte(`{"message":"bad bedrock input"}`))
	defer srv.Close()

	govArn := "arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:application-inference-profile/abc123"
	var errResp *ai.ProviderResponse
	msgErr, err := api.StreamBedrockConverseStream(context.Background(), tbdModel(govArn, "Claude Opus", srv.URL, nil), tbdContext(), &api.BedrockOptions{
		StreamOptions: ai.StreamOptions{
			ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{
				Env: ai.ProviderEnv{
					"AWS_ACCESS_KEY_ID":     "AKIATEST",
					"AWS_SECRET_ACCESS_KEY": "SECRET",
					"AWS_REGION":            "us-east-1", // GovCloud ARN must override this for signing region
				},
				OnResponse: func(resp *ai.ProviderResponse, m *ai.Model) error {
					errResp = resp
					return nil
				},
			},
		},
	}).Result(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenHdrs.Get("Authorization"), "/us-gov-west-1/bedrock/") {
		t.Fatalf("expected SigV4 signing scope to use us-gov-west-1 from ARN, got %q", seenHdrs.Get("Authorization"))
	}
	if errResp == nil || errResp.Status != 400 || errResp.Headers["x-amzn-requestid"] != "req-err-1" {
		t.Fatalf("expected OnResponse invoked on 400 error, got %+v", errResp)
	}
	if msgErr.StopReason != ai.StopReasonError || msgErr.ErrorMessage == nil || !strings.Contains(*msgErr.ErrorMessage, "Validation error:") {
		t.Fatalf("expected formatted Validation error, got %+v", msgErr)
	}
}
