package core

import (
	"os"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/omap"
)

func TestResolveConfigValueEnvTemplatesAndLiterals(t *testing.T) {
	t.Run("literal and escaped dollars", func(t *testing.T) {
		if got := ResolveConfigValue("plain-secret"); got == nil || *got != "plain-secret" {
			t.Fatalf("got %v", got)
		}
		if got := ResolveConfigValue("$$100"); got == nil || *got != "$100" {
			t.Fatalf("$$100 -> %v", got)
		}
		if got := ResolveConfigValue("$!echo hi"); got == nil || *got != "!echo hi" {
			t.Fatalf("$!echo hi -> %v", got)
		}
		if got := ResolveConfigValue("trailing$"); got == nil || *got != "trailing$" {
			t.Fatalf("trailing$ -> %v", got)
		}
	})

	t.Run("env var templates with scoped map priority", func(t *testing.T) {
		t.Setenv("PI_RCV_HOST", "os.example")
		env := map[string]string{
			"PI_RCV_HOST":  "scoped.example",
			"PI_RCV_TOKEN": "tok-123",
		}
		got := ResolveConfigValue("https://$PI_RCV_HOST/v1?k=${PI_RCV_TOKEN}&lit=$123", env)
		if got == nil || *got != "https://scoped.example/v1?k=tok-123&lit=$123" {
			t.Fatalf("got %v", got)
		}

		missing := ResolveConfigValue("prefix-${PI_RCV_MISSING_XYZ}", env)
		if missing != nil {
			t.Fatalf("expected nil on missing env var, got %q", *missing)
		}
	})

	t.Run("malformed brace keeps literal", func(t *testing.T) {
		got := ResolveConfigValue("a-${unclosed")
		if got == nil || *got != "a-${unclosed" {
			t.Fatalf("got %v", got)
		}
		gotBadName := ResolveConfigValue("${123bad}")
		if gotBadName == nil || *gotBadName != "${123bad}" {
			t.Fatalf("got %v", gotBadName)
		}
	})
}

func TestResolveConfigValueCommandsAndCache(t *testing.T) {
	ClearConfigValueCache()
	t.Cleanup(ClearConfigValueCache)

	tmp := t.TempDir() + "/counter.txt"
	cmd := "!sh -c 'printf x >> " + tmp + " && wc -c < " + tmp + " | tr -d \" \"'"

	first := ResolveConfigValue(cmd)
	second := ResolveConfigValue(cmd)
	if first == nil || *first != "1" {
		t.Fatalf("first = %v, want 1", first)
	}
	if second == nil || *second != "1" {
		t.Fatalf("second should be cached 1, got %v", second)
	}

	uncached := ResolveConfigValueUncached(cmd)
	if uncached == nil || *uncached != "2" {
		t.Fatalf("uncached = %v, want 2", uncached)
	}

	ClearConfigValueCache()
	afterClear := ResolveConfigValue(cmd)
	if afterClear == nil || *afterClear != "3" {
		t.Fatalf("afterClear = %v, want 3", afterClear)
	}
}

func TestResolveConfigValueIntrospectionAndOrThrow(t *testing.T) {
	if !IsCommandConfigValue("!echo hi") || IsCommandConfigValue("$VAR") {
		t.Fatal("IsCommandConfigValue mismatch")
	}
	if got := GetConfigValueEnvVarName("$MY_VAR"); got == nil || *got != "MY_VAR" {
		t.Fatalf("GetConfigValueEnvVarName($MY_VAR) = %v", got)
	}
	if got := GetConfigValueEnvVarName("${MY_VAR}"); got == nil || *got != "MY_VAR" {
		t.Fatalf("GetConfigValueEnvVarName(${MY_VAR}) = %v", got)
	}
	if got := GetConfigValueEnvVarName("prefix-$MY_VAR"); got != nil {
		t.Fatalf("expected nil for composite template, got %v", *got)
	}

	names := GetConfigValueEnvVarNames("a=$ONE b=${TWO} c=$ONE")
	if len(names) != 2 || names[0] != "ONE" || names[1] != "TWO" {
		t.Fatalf("GetConfigValueEnvVarNames = %v", names)
	}
	if cmdNames := GetConfigValueEnvVarNames("!$ONE"); len(cmdNames) != 0 {
		t.Fatalf("expected empty for command, got %v", cmdNames)
	}

	missing := GetMissingConfigValueEnvVarNames("$ONE $TWO", map[string]string{"ONE": "1"})
	if len(missing) != 1 || missing[0] != "TWO" {
		t.Fatalf("missing = %v", missing)
	}
	if IsConfigValueConfigured("$ONE $TWO", map[string]string{"ONE": "1"}) {
		t.Fatal("expected unconfigured")
	}
	if !IsConfigValueConfigured("$ONE", map[string]string{"ONE": "1"}) {
		t.Fatal("expected configured")
	}

	_, err := ResolveConfigValueOrThrow("$MISSING_A", "API key", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "environment variable: MISSING_A") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, errMulti := ResolveConfigValueOrThrow("$MISSING_A/$MISSING_B", "token", map[string]string{})
	if errMulti == nil || !strings.Contains(errMulti.Error(), "environment variables: MISSING_A, MISSING_B") {
		t.Fatalf("unexpected error: %v", errMulti)
	}
	_, errCmd := ResolveConfigValueOrThrow("!exit 1", "token")
	if errCmd == nil || !strings.Contains(errCmd.Error(), "shell command: exit 1") {
		t.Fatalf("unexpected cmd error: %v", errCmd)
	}
	okVal, err := ResolveConfigValueOrThrow("Bearer $TOK", "header", map[string]string{"TOK": "abc"})
	if err != nil || okVal != "Bearer abc" {
		t.Fatalf("got (%q, %v)", okVal, err)
	}
}

func TestResolveHeaders(t *testing.T) {
	if ResolveHeaders(nil) != nil {
		t.Fatal("expected nil for nil headers")
	}
	h := omap.NewMap[string, string]()
	h.Set("Authorization", "Bearer $TOKEN")
	h.Set("X-Missing", "$UNSET_VAR_XYZ")
	_ = os.Unsetenv("UNSET_VAR_XYZ")

	resolved := ResolveHeaders(h, map[string]string{"TOKEN": "secret"})
	if resolved == nil || resolved.Len() != 1 {
		t.Fatalf("resolved = %v", resolved)
	}
	if v, _ := resolved.Get("Authorization"); v != "Bearer secret" {
		t.Fatalf("Authorization = %q", v)
	}

	if _, err := ResolveHeadersOrThrow(h, "provider"); err == nil {
		t.Fatal("expected error from ResolveHeadersOrThrow with missing var")
	}
}
