// Ported from packages/ai/test/bedrock-models.test.ts (pi v1.5.2).

package compat_test

import (
	"testing"

	"github.com/keejkrej/pi-go/ai/compat"
)

func TestBedrockModels_CatalogAndInferenceProfiles(t *testing.T) {
	t.Parallel()
	models := compat.GetModels("amazon-bedrock")
	if len(models) == 0 {
		t.Fatal("expected available amazon-bedrock models")
	}

	var hasGlobalOpus5, hasDirectOpus5 bool
	for _, m := range models {
		if m == nil {
			continue
		}
		if m.Id == "global.anthropic.claude-opus-5" {
			hasGlobalOpus5 = true
		}
		if m.Id == "anthropic.claude-opus-5" {
			hasDirectOpus5 = true
		}
	}
	if !hasGlobalOpus5 {
		t.Fatal("expected global.anthropic.claude-opus-5")
	}
	if hasDirectOpus5 {
		t.Fatal("did not expect non-inference-profile anthropic.claude-opus-5")
	}
	// Live credential BEDROCK_EXTENSIVE_MODEL_TEST skipped per docs/porting.md §3.4.
}
