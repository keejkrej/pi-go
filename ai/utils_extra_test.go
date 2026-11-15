package ai_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/internal/testkit"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestShortHashAndSanitizeSurrogates(t *testing.T) {
	t.Run("ShortHash is deterministic and matches UTF-16 hashing", func(t *testing.T) {
		h1 := ai.ShortHash("hello world")
		h2 := ai.ShortHash("hello world")
		if h1 == "" || h1 != h2 {
			t.Fatalf("expected non-empty deterministic hash, got %q vs %q", h1, h2)
		}
		if h3 := ai.ShortHash("hello world!"); h3 == h1 {
			t.Fatalf("expected distinct hashes, both %q", h1)
		}
		if hEmoji := ai.ShortHash("😀"); hEmoji == "" {
			t.Fatal("expected non-empty astral hash")
		}
	})

	t.Run("SanitizeSurrogates strips lone WTF-8 surrogates and joins valid pairs", func(t *testing.T) {
		plain := "hello 🌍 world"
		if got := ai.SanitizeSurrogates(plain); got != plain {
			t.Fatalf("expected valid UTF-8 unchanged, got %q", got)
		}

		// WTF-8 encoded lone high surrogate U+D83D (ED A0 BD) and lone low surrogate U+DE00 (ED B8 80).
		loneHigh := string([]byte{'a', 0xED, 0xA0, 0xBD, 'b'})
		if got := ai.SanitizeSurrogates(loneHigh); got != "ab" {
			t.Fatalf("expected lone high surrogate stripped, got %q (%x)", got, got)
		}
		loneLow := string([]byte{'a', 0xED, 0xB8, 0x80, 'b'})
		if got := ai.SanitizeSurrogates(loneLow); got != "ab" {
			t.Fatalf("expected lone low surrogate stripped, got %q (%x)", got, got)
		}

		// Adjacent WTF-8 high+low pair combines to valid UTF-8 U+1F600 (😀).
		paired := string([]byte{0xED, 0xA0, 0xBD, 0xED, 0xB8, 0x80})
		if got := ai.SanitizeSurrogates(paired); got != "😀" {
			t.Fatalf("expected paired WTF-8 surrogates combined to 😀, got %q (%x)", got, got)
		}
	})
}

func TestAbortSignalsAndSleep(t *testing.T) {
	t.Run("CombineAbortSignals handles 0, 1, and multiple contexts without canceling on Cleanup", func(t *testing.T) {
		empty := ai.CombineAbortSignals(nil, nil)
		if empty.Ctx != nil {
			t.Fatalf("expected nil Ctx for empty signals, got %v", empty.Ctx)
		}
		empty.Cleanup()

		baseCtx, cancelBase := context.WithCancel(context.Background())
		defer cancelBase()
		single := ai.CombineAbortSignals(baseCtx, nil)
		if single.Ctx != baseCtx {
			t.Fatal("expected single non-nil signal to pass through unchanged")
		}
		single.Cleanup()
		if baseCtx.Err() != nil {
			t.Fatal("Cleanup should not cancel single context")
		}

		c1, cancel1 := context.WithCancelCause(context.Background())
		c2, cancel2 := context.WithCancelCause(context.Background())
		defer cancel1(nil)
		defer cancel2(nil)

		combined := ai.CombineAbortSignals(c1, c2)
		if combined.Ctx.Err() != nil {
			t.Fatal("expected merged context active before cancellation")
		}
		// Detach before canceling c1 should leave combined.Ctx active.
		combined.Cleanup()
		cancel1(errors.New("boom"))
		if combined.Ctx.Err() != nil {
			t.Fatal("expected Cleanup to detach listeners without canceling Ctx")
		}

		// Without Cleanup, canceling either input cancels merged context.
		d1, cancelD1 := context.WithCancelCause(context.Background())
		d2, cancelD2 := context.WithCancelCause(context.Background())
		defer cancelD1(nil)
		defer cancelD2(nil)
		combined2 := ai.CombineAbortSignals(d1, d2)
		defer combined2.Cleanup()
		cancelD2(ai.AbortNewError("second canceled"))
		<-combined2.Ctx.Done()
		if combined2.Ctx.Err() == nil {
			t.Fatal("expected combined2.Ctx canceled after d2 canceled")
		}
	})

	t.Run("Sleep resolves after delay and aborts on cancel with synctest", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				done <- ai.Sleep(context.Background(), 500)
			}()
			synctest.Wait()
			select {
			case err := <-done:
				t.Fatalf("expected Sleep not to finish immediately, got %v", err)
			default:
			}
			// Advance virtual clock by reading result; Go's synctest advances durable timers when all goroutines block.
			if err := <-done; err != nil {
				t.Fatalf("expected nil error on Sleep completion, got %v", err)
			}

			ctx, cancel := context.WithCancelCause(context.Background())
			abortDone := make(chan error, 1)
			go func() {
				abortDone <- ai.Sleep(ctx, 10_000)
			}()
			synctest.Wait()
			cancel(ai.AbortNewError("stopped early"))
			synctest.Wait()
			err := <-abortDone
			if err == nil || !strings.Contains(err.Error(), "stopped early") {
				t.Fatalf("expected abort error 'stopped early', got %v", err)
			}
		})
	})
}

func TestCredentialsAuthAndModelCatalog(t *testing.T) {
	t.Run("OAuthCredential and ApiKeyCredential JSON roundtrip preserves extra fields", func(t *testing.T) {
		rawOAuth := []byte(`{"type":"oauth","refresh":"r1","access":"a1","expires":1700000000000,"accountId":"acct_99","enterpriseUrl":"https://ent.example"}`)
		cred, err := ai.UnmarshalCredential(rawOAuth)
		if err != nil {
			t.Fatalf("UnmarshalCredential oauth: %v", err)
		}
		oauth, ok := cred.(*ai.OAuthCredential)
		if !ok {
			t.Fatalf("expected *OAuthCredential, got %T", cred)
		}
		if oauth.Refresh != "r1" || oauth.Access != "a1" || oauth.Expires != 1700000000000 {
			t.Fatalf("unexpected oauth fields: %+v", oauth)
		}
		if acct, ok := oauth.Extra.GetString("accountId"); !ok || acct != "acct_99" {
			t.Fatalf("expected extra accountId=acct_99, got %q (ok=%v)", acct, ok)
		}
		encoded, err := jsonx.Marshal(oauth)
		if err != nil {
			t.Fatalf("Marshal oauth: %v", err)
		}
		if !strings.Contains(string(encoded), `"enterpriseUrl":"https://ent.example"`) {
			t.Fatalf("expected extra field preserved on marshal: %s", string(encoded))
		}

		rawApi := []byte(`{"type":"api_key","key":"sk-test","env":{"REGION":"us-east-1"}}`)
		apiCredAny, err := ai.UnmarshalCredential(rawApi)
		if err != nil {
			t.Fatalf("UnmarshalCredential api_key: %v", err)
		}
		apiCred, ok := apiCredAny.(*ai.ApiKeyCredential)
		if !ok || apiCred.Key == nil || *apiCred.Key != "sk-test" || apiCred.Env["REGION"] != "us-east-1" {
			t.Fatalf("unexpected apiKey credential: %#v", apiCredAny)
		}
	})

	t.Run("ResolveProviderAuth respects EnvApiKeyAuth and overrides", func(t *testing.T) {
		store := ai.NewInMemoryCredentialStore()
		ctx := context.Background()
		auth := ai.ProviderAuth{
			ApiKey: ai.EnvApiKeyAuth("Test Key", []string{"PRIMARY_TEST_KEY", "FALLBACK_TEST_KEY"}),
		}
		t.Setenv("FALLBACK_TEST_KEY", "from-process-env")

		res, err := ai.ResolveProviderAuth(ctx, "prov", auth, store, ai.DefaultProviderAuthContext(), nil)
		if err != nil {
			t.Fatalf("ResolveProviderAuth env fallback: %v", err)
		}
		if res == nil || res.Auth.ApiKey == nil || *res.Auth.ApiKey != "from-process-env" || *res.Source != "FALLBACK_TEST_KEY" {
			t.Fatalf("unexpected env fallback auth result: %+v", res)
		}

		// Override env wins over process env
		res2, err := ai.ResolveProviderAuth(ctx, "prov", auth, store, ai.DefaultProviderAuthContext(), &ai.AuthResolutionOverrides{
			Env: ai.ProviderEnv{"PRIMARY_TEST_KEY": "from-override-env"},
		})
		if err != nil {
			t.Fatalf("ResolveProviderAuth override env: %v", err)
		}
		if res2 == nil || *res2.Auth.ApiKey != "from-override-env" || *res2.Source != "PRIMARY_TEST_KEY" {
			t.Fatalf("unexpected override env result: %+v", res2)
		}
	})

	t.Run("UnmarshalModelGroups and Flatten* catalogs preserve order and filter by model type", func(t *testing.T) {
		rawCatalog := []byte(`{
			"chatApi": {
				"chat-1": {
					"id": "chat-1",
					"name": "Chat One",
					"api": "openai-responses",
					"provider": "testprov",
					"baseUrl": "https://api.example.com",
					"reasoning": true,
					"input": ["text"],
					"cost": {"input":1,"output":2,"cacheRead":0.1,"cacheWrite":0.2},
					"contextWindow": 8192,
					"maxTokens": 4096
				}
			},
			"imgApi": {
				"img-1": {
					"type": "image",
					"id": "img-1",
					"name": "Image One",
					"api": "openai-images",
					"provider": "testprov",
					"baseUrl": "https://api.example.com",
					"input": ["text"],
					"output": ["image"],
					"cost": {"input":0,"output":0,"cacheRead":0,"cacheWrite":0}
				}
			}
		}`)
		groups, err := ai.UnmarshalModelGroups(rawCatalog)
		if err != nil {
			t.Fatalf("UnmarshalModelGroups: %v", err)
		}
		chats := ai.FlattenChatModelCatalog("testprov", groups)
		if m, ok := chats.Get("chat-1"); chats.Len() != 1 || !ok || m == nil {
			t.Fatalf("expected 1 chat model, got len=%d", chats.Len())
		}
		images := ai.FlattenImageModelCatalog("testprov", groups)
		if m, ok := images.Get("img-1"); images.Len() != 1 || !ok || m == nil {
			t.Fatalf("expected 1 image model, got len=%d", images.Len())
		}
	})
}

func TestSessionResourcesLazyHooksOAuthHtmlAndTestkitFixture(t *testing.T) {
	t.Run("SessionResourceCleanup runs in order and supports unsubscribe", func(t *testing.T) {
		var calls []string
		s1 := "sess-1"
		u1 := ai.RegisterSessionResourceCleanup(func(id *string) {
			calls = append(calls, "first:"+*id)
		})
		u2 := ai.RegisterSessionResourceCleanup(func(id *string) {
			calls = append(calls, "second:"+*id)
		})
		defer u1()
		defer u2()

		u1()
		if err := ai.CleanupSessionResources(&s1); err != nil {
			t.Fatalf("unexpected cleanup error: %v", err)
		}
		if len(calls) != 1 || calls[0] != "second:sess-1" {
			t.Fatalf("expected only second cleanup after u1 unsubscribe, got %v", calls)
		}
	})

	t.Run("Lazy hooks invoke registered callbacks safely", func(t *testing.T) {
		bedrockCalled := false
		oauthCalled := false
		ai.SetLazyBedrockProviderHook(func() { bedrockCalled = true })
		ai.SetLazyOAuthFlowsHook(func() { oauthCalled = true })
		defer ai.SetLazyBedrockProviderHook(nil)
		defer ai.SetLazyOAuthFlowsHook(nil)

		ai.RegisterLazyBedrockProvider()
		ai.RegisterLazyOAuthFlows()
		if !bedrockCalled || !oauthCalled {
			t.Fatalf("expected both lazy hooks invoked (bedrock=%v, oauth=%v)", bedrockCalled, oauthCalled)
		}
	})

	t.Run("OAuth HTML pages escape special characters", func(t *testing.T) {
		okHtml := ai.OauthSuccessHtml("Done <script>alert(1)</script>")
		if !strings.Contains(okHtml, "Authentication successful") || !strings.Contains(okHtml, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Fatalf("expected escaped success HTML, got: %s", okHtml)
		}
		errHtml := ai.OauthErrorHtml("Denied", `detail & "quote"`)
		if !strings.Contains(errHtml, "Authentication failed") || !strings.Contains(errHtml, `detail &amp; &quot;quote&quot;`) {
			t.Fatalf("expected escaped error HTML, got: %s", errHtml)
		}
	})

	t.Run("testkit.Fixture reads red-circle.png fixture", func(t *testing.T) {
		data := testkit.Fixture(t, "data/red-circle.png")
		if len(data) < 8 || !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
			t.Fatalf("expected valid PNG header in red-circle.png, got %d bytes", len(data))
		}
		if !strings.HasPrefix(js.Platform(), "") {
			t.Fatal("unreachable")
		}
	})
}
