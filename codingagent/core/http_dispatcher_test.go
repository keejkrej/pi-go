package core

import (
	"os"
	"testing"
)

func TestParseHttpIdleTimeoutMs(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want *int64
	}{
		{"default disabled string", "disabled", thdPtrInt64(0)},
		{"mixed case disabled", "  DiSaBlEd ", thdPtrInt64(0)},
		{"numeric string", "45000", thdPtrInt64(45000)},
		{"float truncated", 1234.9, thdPtrInt64(1234)},
		{"zero int", 0, thdPtrInt64(0)},
		{"negative int", -1, nil},
		{"negative string", "-5", nil},
		{"empty string", "", nil},
		{"underscore numeric string", "1_000", nil},
		{"inf string", "Infinity", nil},
		{"nan string", "NaN", nil},
		{"nil pointer", (*int64)(nil), nil},
		{"non-numeric", "bogus", nil},
		{"bool", true, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseHttpIdleTimeoutMs(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Fatalf("got %d, want %d", *got, *tc.want)
			}
		})
	}
}

func TestFormatHttpIdleTimeoutMs(t *testing.T) {
	if got := FormatHttpIdleTimeoutMs(30_000); got != "30 sec" {
		t.Fatalf("30_000 -> %q", got)
	}
	if got := FormatHttpIdleTimeoutMs(300_000); got != "5 min" {
		t.Fatalf("300_000 -> %q", got)
	}
	if got := FormatHttpIdleTimeoutMs(0); got != "disabled" {
		t.Fatalf("0 -> %q", got)
	}
	if got := FormatHttpIdleTimeoutMs(45_500); got != "45.5 sec" {
		t.Fatalf("45_500 -> %q", got)
	}
}

func TestApplyHttpProxySettings(t *testing.T) {
	t.Run("sets unset HTTP_PROXY and HTTPS_PROXY", func(t *testing.T) {
		origHTTP, hadHTTP := os.LookupEnv("HTTP_PROXY")
		origHTTPS, hadHTTPS := os.LookupEnv("HTTPS_PROXY")
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		t.Cleanup(func() {
			if hadHTTP {
				_ = os.Setenv("HTTP_PROXY", origHTTP)
			} else {
				_ = os.Unsetenv("HTTP_PROXY")
			}
			if hadHTTPS {
				_ = os.Setenv("HTTPS_PROXY", origHTTPS)
			} else {
				_ = os.Unsetenv("HTTPS_PROXY")
			}
		})

		proxy := " http://127.0.0.1:8080 "
		ApplyHttpProxySettings(&proxy)
		if got := os.Getenv("HTTP_PROXY"); got != "http://127.0.0.1:8080" {
			t.Fatalf("HTTP_PROXY = %q", got)
		}
		if got := os.Getenv("HTTPS_PROXY"); got != "http://127.0.0.1:8080" {
			t.Fatalf("HTTPS_PROXY = %q", got)
		}
	})

	t.Run("does not overwrite existing proxy env vars", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://existing-http:8888")
		t.Setenv("HTTPS_PROXY", "http://existing-https:8888")
		proxy := "http://override:9999"
		ApplyHttpProxySettings(&proxy)
		if got := os.Getenv("HTTP_PROXY"); got != "http://existing-http:8888" {
			t.Fatalf("HTTP_PROXY overwritten: %q", got)
		}
		if got := os.Getenv("HTTPS_PROXY"); got != "http://existing-https:8888" {
			t.Fatalf("HTTPS_PROXY overwritten: %q", got)
		}
	})
}

func TestConfigureHttpDispatcher(t *testing.T) {
	if err := ConfigureHttpDispatcher(); err != nil {
		t.Fatalf("default ConfigureHttpDispatcher failed: %v", err)
	}
	if err := ConfigureHttpDispatcher("disabled"); err != nil {
		t.Fatalf("disabled ConfigureHttpDispatcher failed: %v", err)
	}
	if err := ConfigureHttpDispatcher(-1); err == nil {
		t.Fatal("expected error for negative timeout")
	}
}

func thdPtrInt64(v int64) *int64 { return &v }
