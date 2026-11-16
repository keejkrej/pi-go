// Ported from packages/mcp/test/input-schema.test.ts (pi v1.5.2).

package mcp

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestInputSchema_NormalizeAddsMissingDefaultsAndKeepsKeywords(t *testing.T) {
	if got := NormalizeMcpInputSchema(jsonx.NewObject()); !jsonx.DeepEqual(got, jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject())) {
		t.Fatalf("empty schema = %v", got)
	}
	if got := NormalizeMcpInputSchema(jsonx.ObjectOf("type", "object")); !jsonx.DeepEqual(got, jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject())) {
		t.Fatalf("type-only schema = %v", got)
	}

	schema := jsonx.ObjectOf(
		"type", "object",
		"properties", jsonx.ObjectOf("query", jsonx.ObjectOf("type", "string")),
		"required", []any{"query"},
		"additionalProperties", false,
	)
	if got := NormalizeMcpInputSchema(schema); !jsonx.DeepEqual(got, schema) {
		t.Fatalf("full schema = %v, want %v", got, schema)
	}

	withDefs := jsonx.ObjectOf("properties", jsonx.ObjectOf("a", jsonx.NewObject()), "$defs", jsonx.ObjectOf("x", jsonx.NewObject()))
	wantWithDefs := jsonx.ObjectOf("properties", jsonx.ObjectOf("a", jsonx.NewObject()), "$defs", jsonx.ObjectOf("x", jsonx.NewObject()), "type", "object")
	if got := NormalizeMcpInputSchema(withDefs); !jsonx.DeepEqual(got, wantWithDefs) {
		t.Fatalf("withDefs = %v, want %v", got, wantWithDefs)
	}
}
