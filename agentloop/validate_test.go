package agentloop

import (
	"strings"
	"testing"
)

func schemaTool(name string, params map[string]any) AgentTool {
	return &FuncTool{NameVal: name, ParametersVal: params}
}

func TestValidateRequiredMissing(t *testing.T) {
	tool := schemaTool("read", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	})
	_, err := ValidateToolArguments(tool, &ToolCall{Name: "read", Arguments: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for missing required property")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Fatalf("error should mention path: %v", err)
	}
	if !strings.Contains(err.Error(), `Validation failed for tool "read"`) {
		t.Fatalf("error should contain formatted header: %v", err)
	}
}

func TestValidateCoerceStringToNumber(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
		},
	})
	out, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{"limit": "5"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := out["limit"].(float64); !ok || got != 5 {
		t.Fatalf("limit not coerced to number: %#v", out["limit"])
	}
}

func TestValidateCoerceStringToBool(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flag": map[string]any{"type": "boolean"},
		},
	})
	out, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{"flag": "true"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, ok := out["flag"].(bool); !ok || !got {
		t.Fatalf("flag not coerced to bool: %#v", out["flag"])
	}
}

func TestValidateNestedObject(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"opts": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
				"required": []any{"count"},
			},
		},
		"required": []any{"opts"},
	})
	out, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{
		"opts": map[string]any{"count": "3"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	opts := out["opts"].(map[string]any)
	if got, ok := opts["count"].(float64); !ok || got != 3 {
		t.Fatalf("nested count not coerced: %#v", opts["count"])
	}
}

func TestValidateNestedObjectMissingRequired(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"opts": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"count": map[string]any{"type": "integer"},
				},
				"required": []any{"count"},
			},
		},
	})
	_, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{
		"opts": map[string]any{},
	}})
	if err == nil {
		t.Fatal("expected error for nested missing required")
	}
	if !strings.Contains(err.Error(), "opts.count") {
		t.Fatalf("error should contain nested path opts.count: %v", err)
	}
}

func TestValidateArrayItems(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"nums": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "number"},
			},
		},
	})
	out, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{
		"nums": []any{"1", "2", "3"},
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nums := out["nums"].([]any)
	for i, v := range nums {
		if f, ok := v.(float64); !ok || f != float64(i+1) {
			t.Fatalf("array item %d not coerced: %#v", i, v)
		}
	}
}

func TestValidatePassthroughValid(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "number"},
		},
		"required": []any{"path"},
	})
	in := map[string]any{"path": "a.go", "limit": float64(10)}
	out, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: in})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["path"] != "a.go" || out["limit"].(float64) != 10 {
		t.Fatalf("valid args not passed through: %#v", out)
	}
}

func TestValidateTypeMismatch(t *testing.T) {
	tool := schemaTool("t", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"obj": map[string]any{"type": "object"},
		},
	})
	// A value that cannot be coerced to object.
	_, err := ValidateToolArguments(tool, &ToolCall{Name: "t", Arguments: map[string]any{
		"obj": "not-an-object",
	}})
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
	if !strings.Contains(err.Error(), "obj") {
		t.Fatalf("error should mention obj path: %v", err)
	}
}
