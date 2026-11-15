// Ported from packages/ai/test/compat-schema.test.ts (pi v1.5.2).

package providers_test

import (
	"testing"

	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tcsDefaults(t *testing.T, schema *typebox.Schema) map[string]any {
	t.Helper()
	props, ok := schema.GetObject("properties")
	if !ok {
		t.Fatalf("schema missing properties object: %v", schema)
	}
	out := make(map[string]any)
	for name, val := range props.All() {
		sub, ok := val.(*typebox.Schema)
		if !ok {
			if obj, okObj := val.(*jsonx.Object); okObj {
				if def, hasDef := obj.Get("default"); hasDef {
					out[name] = def
				}
			}
			continue
		}
		if def, hasDef := sub.Get("default"); hasDef {
			out[name] = def
		}
	}
	return out
}

func TestCompatSchema_PreservesApiSpecificDefaults(t *testing.T) {
	completions := tcsDefaults(t, providers.OpenAICompletionsCompatSchema)
	if _, ok := completions["thinkingFormat"]; ok {
		t.Fatal("expected OpenAICompletionsCompatSchema not to have thinkingFormat default")
	}
	if _, ok := completions["supportsLongCacheRetention"]; ok {
		t.Fatal("expected OpenAICompletionsCompatSchema not to have supportsLongCacheRetention default")
	}

	responses := tcsDefaults(t, providers.OpenAIResponsesCompatSchema)
	if v, ok := responses["supportsDeveloperRole"]; !ok || v != true {
		t.Fatalf("expected OpenAIResponsesCompatSchema supportsDeveloperRole default true, got %v (present=%v)", v, ok)
	}

	anthropic := tcsDefaults(t, providers.AnthropicMessagesCompatSchema)
	if _, ok := anthropic["sendSessionAffinityHeaders"]; ok {
		t.Fatal("expected AnthropicMessagesCompatSchema not to have sendSessionAffinityHeaders default")
	}
	if v, ok := anthropic["supportsLongCacheRetention"]; !ok || v != true {
		t.Fatalf("expected AnthropicMessagesCompatSchema supportsLongCacheRetention default true, got %v (present=%v)", v, ok)
	}

	bedrock := tcsDefaults(t, providers.BedrockCompatSchema)
	if v, ok := bedrock["supportsStrictMode"]; !ok || v != false {
		t.Fatalf("expected BedrockCompatSchema supportsStrictMode default false, got %v (present=%v)", v, ok)
	}
}

func TestCompatSchema_ProviderSupersetOmitsApiSpecificDefaults(t *testing.T) {
	provider := tcsDefaults(t, providers.ProviderCompatSchema)
	for _, key := range []string{
		"supportsDeveloperRole",
		"sendSessionAffinityHeaders",
		"supportsLongCacheRetention",
		"supportsStrictMode",
	} {
		if _, ok := provider[key]; ok {
			t.Fatalf("expected ProviderCompatSchema not to have default for %s", key)
		}
	}
	if providers.CompatSchemasByApi.Len() != 9 {
		t.Fatalf("expected 9 entries in CompatSchemasByApi, got %d", providers.CompatSchemasByApi.Len())
	}
}
