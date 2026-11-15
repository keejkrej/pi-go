// Ported from packages/coding-agent/test/config-schemas.test.ts (pi v1.5.2).

package core

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func tpiasParseJSON(t *testing.T, raw string) any {
	t.Helper()
	val, err := jsonx.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("jsonx.Parse failed: %v", err)
	}
	return val
}

func tpiasCompile(t *testing.T, schema *typebox.Schema) *typebox.Validator {
	t.Helper()
	v, err := typebox.Compile(schema)
	if err != nil {
		t.Fatalf("typebox.Compile failed: %v", err)
	}
	return v
}

func TestPiAiSchemasValidation(t *testing.T) {
	costV := tpiasCompile(t, ModelCostSchema)
	if !costV.Check(tpiasParseJSON(t, `{"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 0.2}`)) {
		t.Fatalf("expected full ModelCost object to pass ModelCostSchema")
	}
	if costV.Check(tpiasParseJSON(t, `{"input": 1}`)) {
		t.Fatalf("expected partial cost object to fail ModelCostSchema (required fields missing)")
	}

	partialCostV := tpiasCompile(t, PartialModelCostSchema)
	if !partialCostV.Check(tpiasParseJSON(t, `{"input": 1}`)) {
		t.Fatalf("expected partial cost object to pass PartialModelCostSchema")
	}
	if !partialCostV.Check(tpiasParseJSON(t, `{}`)) {
		t.Fatalf("expected empty object to pass PartialModelCostSchema")
	}

	compatV := tpiasCompile(t, ProviderCompatSchema)
	if !compatV.Check(tpiasParseJSON(t, `{
		"zaiToolStream": true,
		"thinkingTokenBudgetField": "thinking_budget",
		"chatTemplateKwargs": {"budget": {"$var": "thinking.budget"}},
		"supportsMidConvoSystemMessages": true
	}`)) {
		t.Fatalf("expected valid compat object to pass ProviderCompatSchema")
	}

	limitsV := tpiasCompile(t, ModelInputLimitsSchema)
	if !limitsV.Check(tpiasParseJSON(t, `{"maxRequestBytes": 1024, "images": {"maxPerRequest": 2}}`)) {
		t.Fatalf("expected valid ModelInputLimits to pass")
	}

	modalityV := tpiasCompile(t, ModelInputModalitySchema)
	if !modalityV.Check("text") || !modalityV.Check("image") {
		t.Fatalf("expected text and image to pass ModelInputModalitySchema")
	}
	if modalityV.Check("video") {
		t.Fatalf("expected video to fail ModelInputModalitySchema")
	}

	cacheV := tpiasCompile(t, ModelPromptCacheSchema)
	if !cacheV.Check(tpiasParseJSON(t, `{"short": 300}`)) {
		t.Fatalf("expected ModelPromptCache object to pass")
	}

	thinkingV := tpiasCompile(t, ModelThinkingLevelSchema)
	for _, lvl := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !thinkingV.Check(lvl) {
			t.Fatalf("expected %q to pass ModelThinkingLevelSchema", lvl)
		}
	}
	if thinkingV.Check("turbo") {
		t.Fatalf("expected invalid thinking level to fail ModelThinkingLevelSchema")
	}

	thinkingMapV := tpiasCompile(t, ThinkingLevelMapSchema)
	if !thinkingMapV.Check(tpiasParseJSON(t, `{"high": "max", "low": null}`)) {
		t.Fatalf("expected ThinkingLevelMap object to pass")
	}

	if piasWithoutRequired(nil).Len() != 0 {
		t.Fatalf("expected piasWithoutRequired(nil) to return empty object")
	}
}
