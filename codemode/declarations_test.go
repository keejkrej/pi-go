// Ported from packages/codemode/test/declarations.test.ts (pi v1.0.0).

package codemode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tdeclStrPtr(s string) *string { return &s }
func tdeclIntPtr(v int) *int       { return &v }
func tdeclExecute(_ context.Context, _ any) (any, error) {
	return nil, nil
}

func tdeclMcpResultSchema(structuredContent codemode.CodemodeJsonSchema) map[string]any {
	props := map[string]any{
		"content": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "object"},
		},
		"isError": map[string]any{"type": "boolean"},
		"_meta":   map[string]any{"type": "object"},
	}
	if structuredContent != nil {
		props["structuredContent"] = structuredContent
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   []string{"content"},
	}
}

func TestSchemaToType(t *testing.T) {
	t.Run("renders primitives, literals, and unions", func(t *testing.T) {
		if got := codemode.SchemaToType(map[string]any{"type": "string"}, nil); got != "string" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"type": "integer"}, nil); got != "number" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"type": []string{"string", "null"}}, nil); got != "string | null" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"const": "a"}, nil); got != `"a"` {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"enum": []any{"a", 1, nil}}, nil); got != `"a" | 1 | null` {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}},
		}, nil); got != "string | number" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"anyOf": []any{map[string]any{"type": "string"}, map[string]any{}},
		}, nil); got != "unknown" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"allOf": []any{
				map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}},
				map[string]any{"const": 1},
			},
		}, nil); got != "(string | number) & 1" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"$ref": "#/defs/x"}, nil); got != "unknown" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(true, nil); got != "unknown" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(false, nil); got != "never" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("renders objects on one line with sorted properties", func(t *testing.T) {
		got := codemode.SchemaToType(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city":      map[string]any{"type": "string"},
				"max-lines": map[string]any{"type": "number"},
			},
			"required":             []string{"city"},
			"additionalProperties": false,
		}, nil)
		if got != `{ city: string; "max-lines"?: number; }` {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "number"},
		}, nil); got != "{ [key: string]: number; }" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"type": "object"}, nil); got != "{ [key: string]: unknown; }" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		}, nil); got != "{}" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("puts property descriptions on comment lines", func(t *testing.T) {
		got := codemode.SchemaToType(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"weather": map[string]any{
					"type":        "array",
					"description": "look up weather for a given list of locations",
					"items": map[string]any{
						"type":       "object",
						"properties": map[string]any{"location": map[string]any{"type": "string"}},
						"required":   []string{"location"},
					},
				},
			},
			"required": []string{"weather"},
		}, nil)
		want := "{\n  // look up weather for a given list of locations\n  weather: Array<{ location: string; }>;\n}"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}

		got2 := codemode.SchemaToType(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"outer": map[string]any{
					"type":        "object",
					"description": "Outer",
					"properties": map[string]any{
						"inner": map[string]any{"type": "string", "description": "Inner"},
					},
				},
			},
		}, nil)
		want2 := "{\n  // Outer\n  outer?: {\n    // Inner\n    inner?: string;\n  };\n}"
		if got2 != want2 {
			t.Fatalf("got %q, want %q", got2, want2)
		}
	})

	t.Run("resolves local references and stops at recursive ones", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"item":   map[string]any{"$ref": "#/$defs/Item"},
				"legacy": map[string]any{"$ref": "#/definitions/Legacy"},
				"remote": map[string]any{"$ref": "https://example.com/schema.json"},
			},
			"required": []string{"item"},
			"$defs": map[string]any{
				"Item": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "string"},
						"parent": map[string]any{"$ref": "#/$defs/Item"},
					},
					"required": []string{"id"},
				},
			},
			"definitions": map[string]any{
				"Legacy": map[string]any{"enum": []string{"a", "b"}},
			},
		}
		got := codemode.SchemaToType(schema, nil)
		want := `{ item: { id: string; parent?: unknown; }; legacy?: "a" | "b"; remote?: unknown; }`
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}

		// Also verify parsed *jsonx.Object behaves identically.
		rawJSON, err := jsonx.Stringify(schema)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := jsonx.Parse([]byte(rawJSON))
		if err != nil {
			t.Fatal(err)
		}
		if gotJSONX := codemode.SchemaToType(parsed, nil); gotJSONX != want {
			t.Fatalf("jsonx schema got %q, want %q", gotJSONX, want)
		}
	})

	t.Run("renders arrays and tuples", func(t *testing.T) {
		if got := codemode.SchemaToType(map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, nil); got != "Array<string>" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{
			"type":        "array",
			"prefixItems": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}},
		}, nil); got != "[string, number]" {
			t.Fatalf("got %q", got)
		}
		if got := codemode.SchemaToType(map[string]any{"type": "array"}, nil); got != "unknown[]" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("renders types over the budget as unknown", func(t *testing.T) {
		props := make(map[string]any, 50)
		for i := range 50 {
			name := "field" + tdeclItoa(i)
			props[name] = map[string]any{"type": "string"}
		}
		schema := map[string]any{"type": "object", "properties": props}
		if got := codemode.SchemaToType(schema, &codemode.SchemaToTypeOptions{MaxChars: tdeclIntPtr(100)}); got != "unknown" {
			t.Fatalf("got %q, want unknown", got)
		}
		if got := codemode.SchemaToType(schema, nil); !strings.Contains(got, "field49?: string;") {
			t.Fatalf("expected field49?: string; in %q", got)
		}
	})
}

func tdeclItoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [12]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestToolDeclarations(t *testing.T) {
	t.Run("renders signatures with normalized identifiers", func(t *testing.T) {
		got := codemode.RenderToolSignature(&codemode.CodemodeTool{
			Name: "hidden-dynamic-tool",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"city": map[string]any{"type": "string"}},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
			OutputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
				"required":   []string{"ok"},
			},
		}, nil)
		if got != "hidden_dynamic_tool(args: { city: string; }): Promise<{ ok: boolean; }>;" {
			t.Fatalf("got %q", got)
		}

		if got := codemode.RenderToolSignature(&codemode.CodemodeTool{Name: "free"}, nil); got != "free(args: unknown): Promise<unknown>;" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("renders MCP CallToolResult output schemas as CallToolResult<T>", func(t *testing.T) {
		inputSchema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
		got := codemode.RenderToolSignature(&codemode.CodemodeTool{
			Name:        "mcp__sample__search",
			InputSchema: inputSchema,
			OutputSchema: tdeclMcpResultSchema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"results": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/definitions/Result~1item~0v1"},
					},
				},
				"required":             []string{"results"},
				"additionalProperties": false,
				"definitions": map[string]any{
					"Result/item~v1": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":    map[string]any{"type": "string"},
							"score": map[string]any{"type": "number"},
						},
						"required":             []string{"id", "score"},
						"additionalProperties": false,
					},
				},
			}),
		}, nil)
		want := "mcp__sample__search(args: {}): Promise<CallToolResult<{ results: Array<{ id: string; score: number; }>; }>>;"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}

		gotPlain := codemode.RenderToolSignature(&codemode.CodemodeTool{
			Name:         "plain",
			InputSchema:  inputSchema,
			OutputSchema: tdeclMcpResultSchema(nil),
		}, nil)
		if gotPlain != "plain(args: {}): Promise<CallToolResult>;" {
			t.Fatalf("got %q", gotPlain)
		}

		if sc := codemode.McpStructuredContentSchema(map[string]any{
			"type":       "object",
			"properties": map[string]any{"content": map[string]any{"type": "array"}},
		}); sc != nil {
			t.Fatalf("expected nil, got %v", sc)
		}
	})

	t.Run("renders the per-tool sample", func(t *testing.T) {
		got := codemode.RenderToolSample(&codemode.CodemodeTool{
			Name:        "foo",
			Description: tdeclStrPtr("bar"),
			InputSchema: map[string]any{"type": "string"},
		}, nil)
		want := "bar\n\ncodemode tool declaration:\n```ts\ndeclare const tools: { foo(args: string): Promise<unknown>; };\n```"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

func TestRenderDeclarations(t *testing.T) {
	t.Run("renders tools and globals", func(t *testing.T) {
		text := codemode.RenderDeclarations(&codemode.RenderDeclarationsOptions{
			Tools: []*codemode.CodemodeTool{
				{
					Name:         "read",
					Description:  tdeclStrPtr("Read a file.\nSecond line."),
					InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}},
					OutputSchema: map[string]any{"type": "string"},
					Execute:      tdeclExecute,
				},
				{Name: "remote-api", Execute: tdeclExecute},
			},
			Globals: []*codemode.CodemodeTool{
				{Name: "attach", Description: tdeclStrPtr("Attach it."), InputSchema: map[string]any{"type": "string"}, Execute: tdeclExecute},
			},
		})
		want := strings.Join([]string{
			"declare const tools: {",
			"  /**",
			"   * Read a file.",
			"   * Second line.",
			"   */",
			"  read(args: { path: string; }): Promise<string>;",
			"  remote_api(args: unknown): Promise<unknown>;",
			"};",
			"",
			"/** Attach it. */",
			"declare function attach(args: string): Promise<unknown>;",
		}, "\n")
		if text != want {
			t.Fatalf("got:\n%s\nwant:\n%s", text, want)
		}
	})

	t.Run("renders namespaced globals and explicit signatures", func(t *testing.T) {
		text := codemode.RenderDeclarations(&codemode.RenderDeclarationsOptions{
			Globals: []*codemode.CodemodeTool{
				{
					Name:        "models.list",
					Description: tdeclStrPtr("List models."),
					Signature:   tdeclStrPtr("(type: string): Promise<string[]>"),
					Execute:     tdeclExecute,
				},
				{Name: "models.get", InputSchema: map[string]any{"type": "string"}, Execute: tdeclExecute},
				{Name: "plain", Signature: tdeclStrPtr("(): void"), Execute: tdeclExecute},
			},
		})
		want := strings.Join([]string{
			"declare function plain(): void;",
			"",
			"declare const models: {",
			"  /** List models. */",
			"  list(type: string): Promise<string[]>;",
			"  get(args: string): Promise<unknown>;",
			"};",
		}, "\n")
		if text != want {
			t.Fatalf("got:\n%s\nwant:\n%s", text, want)
		}
	})

	t.Run("escapes comment terminators in descriptions", func(t *testing.T) {
		text := codemode.RenderDeclarations(&codemode.RenderDeclarationsOptions{
			Tools: []*codemode.CodemodeTool{{Name: "x", Description: tdeclStrPtr("a */ b"), Execute: tdeclExecute}},
		})
		if !strings.Contains(text, `/** a *\/ b */`) {
			t.Fatalf("got %q", text)
		}
	})
}
