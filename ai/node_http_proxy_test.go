package ai_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

var testProxyEnvKeys = []string{
	"HTTP_PROXY",
	"HTTPS_PROXY",
	"NO_PROXY",
	"ALL_PROXY",
	"http_proxy",
	"https_proxy",
	"no_proxy",
	"all_proxy",
	"npm_config_http_proxy",
	"npm_config_https_proxy",
	"npm_config_proxy",
	"npm_config_no_proxy",
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range testProxyEnvKeys {
		t.Setenv(k, "")
	}
}

func TestResolveHttpProxyUrlForTarget(t *testing.T) {
	t.Run("respects NO_PROXY exclusions", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
		t.Setenv("NO_PROXY", "bedrock-runtime.us-east-1.amazonaws.com")

		u, err := ai.ResolveHttpProxyUrlForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u != nil {
			t.Fatalf("got %v, want nil", u)
		}
	})

	t.Run("resolves HTTP and HTTPS proxy URLs", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example:8080")

		u, err := ai.ResolveHttpProxyUrlForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u == nil || u.String() != "http://proxy.example:8080" {
			t.Fatalf("got %v, want http://proxy.example:8080", u)
		}
	})

	t.Run("prefers scoped proxy env aliases before process env aliases", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("https_proxy", "http://process-proxy.example:8080")

		u, err := ai.ResolveHttpProxyUrlForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", ai.ProviderEnv{
			"HTTPS_PROXY": "http://scoped-proxy.example:8080",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u == nil || u.String() != "http://scoped-proxy.example:8080" {
			t.Fatalf("got %v, want http://scoped-proxy.example:8080", u)
		}
	})

	t.Run("rejects SOCKS and PAC proxy URLs explicitly", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "socks5://proxy.example:1080")

		_, err := ai.ResolveHttpProxyUrlForTarget("https://bedrock-runtime.us-east-1.amazonaws.com", nil)
		if err == nil || !strings.Contains(err.Error(), ai.UnsupportedProxyProtocolMessage) {
			t.Fatalf("err = %v, want substring %q", err, ai.UnsupportedProxyProtocolMessage)
		}
	})

	t.Run("handles subdomain wildcards, IPv6, and ports in NO_PROXY", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example:8080")
		t.Setenv("NO_PROXY", "example.com, .wildcard.org, *.star.net, ::1, [2001:db8::1], 127.0.0.1:8080")

		bypassed := []string{
			"https://example.com",
			"https://api.example.com",
			"https://wildcard.org",
			"https://api.wildcard.org",
			"https://star.net",
			"https://api.star.net",
			"https://[::1]:80",
			"https://[2001:db8::1]",
			"https://127.0.0.1:8080",
		}
		for _, target := range bypassed {
			u, err := ai.ResolveHttpProxyUrlForTarget(target, nil)
			if err != nil {
				t.Fatalf("target %s unexpected error: %v", target, err)
			}
			if u != nil {
				t.Errorf("target %s: got %v, want nil (bypassed)", target, u)
			}
		}

		proxied := []string{
			"https://notexample.com",
			"https://127.0.0.1:3000",
		}
		for _, target := range proxied {
			u, err := ai.ResolveHttpProxyUrlForTarget(target, nil)
			if err != nil {
				t.Fatalf("target %s unexpected error: %v", target, err)
			}
			if u == nil || u.String() != "http://proxy.example:8080" {
				t.Errorf("target %s: got %v, want http://proxy.example:8080", target, u)
			}
		}
	})
}
