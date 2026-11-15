package ai_test

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func valSchemaFromJSON(t *testing.T, rawJSON string) *typebox.Schema {
	t.Helper()
	parsed, err := jsonx.ParseString(rawJSON)
	if err != nil {
		t.Fatalf("parse schema json: %v", err)
	}
	s, err := typebox.FromJSON(parsed)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	return s
}

func createToolCallWithPlainSchema(t *testing.T, propSchemaJSON string, value any) (*ai.Tool, *ai.ToolCall) {
	t.Helper()
	schema := valSchemaFromJSON(t, `{"type":"object","properties":{"value":`+propSchemaJSON+`},"required":["value"]}`)
	tool := &ai.Tool{
		Name:        "echo",
		Description: "Echo tool",
		Parameters:  schema,
	}
	args := jsonx.NewObject()
	args.Set("value", value)
	call := &ai.ToolCall{
		Type:      "toolCall",
		Id:        "tool-1",
		Name:      "echo",
		Arguments: args,
	}
	return tool, call
}

func TestValidateToolArguments(t *testing.T) {
	t.Run("coerces TypeBox schema arguments", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("count", typebox.Number())
		tool := &ai.Tool{
			Name:        "echo",
			Description: "Echo tool",
			Parameters:  typebox.Object(props),
		}
		args := jsonx.NewObject()
		args.Set("count", "42")
		call := &ai.ToolCall{Type: "toolCall", Id: "tool-1", Name: "echo", Arguments: args}

		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("count", 42.0)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("coerces serialized plain JSON schemas like TypeBox schemas", func(t *testing.T) {
		cases := []struct {
			schema   string
			input    any
			expected any
		}{
			{`{"type":"number"}`, "42", 42.0},
			{`{"type":"number"}`, true, 1.0},
			{`{"type":"number"}`, nil, 0.0},
			{`{"type":"integer"}`, "42", 42.0},
			{`{"type":"boolean"}`, "true", true},
			{`{"type":"boolean"}`, "false", false},
			{`{"type":"boolean"}`, 1.0, true},
			{`{"type":"boolean"}`, 0.0, false},
			{`{"type":"string"}`, nil, "null"},
			{`{"type":"string"}`, true, "true"},
			{`{"type":"null"}`, "", nil},
			{`{"type":"null"}`, 0.0, nil},
			{`{"type":"null"}`, false, nil},
			{`{"type":["number","string"]}`, "1", "1"},
			{`{"type":["boolean","number"]}`, "1", 1.0},
			{`{"type":"boolean"}`, "1", true},
			{`{"type":"boolean"}`, "0", false},
			{`{"type":"null"}`, "null", nil},
			{`{"type":"integer"}`, "42.1", 42.0},
			{`{"type":"array","items":{"type":"number"}}`, "7", []any{7.0}},
		}
		for _, tc := range cases {
			tool, call := createToolCallWithPlainSchema(t, tc.schema, tc.input)
			got, err := ai.ValidateToolArguments(tool, call)
			if err != nil {
				t.Fatalf("schema %s input %#v: unexpected err %v", tc.schema, tc.input, err)
			}
			want := jsonx.NewObject()
			want.Set("value", tc.expected)
			if !sjValueEqual(got, want) {
				t.Fatalf("schema %s input %#v: got %#v, want %#v", tc.schema, tc.input, got, want)
			}
		}
	})

	t.Run("treats null as omission for optional non-nullable properties", func(t *testing.T) {
		metaProps := omap.NewMap[string, *typebox.Schema]()
		metaProps.Set("enabled", typebox.Optional(typebox.Boolean()))
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("path", typebox.String())
		props.Set("offset", typebox.Optional(typebox.Number()))
		props.Set("nullable", typebox.Optional(typebox.Union([]*typebox.Schema{typebox.String(), typebox.Null()})))
		props.Set("metadata", typebox.Object(metaProps))
		tool := &ai.Tool{
			Name:        "echo",
			Description: "Echo tool",
			Parameters:  typebox.Object(props),
		}
		metaArgs := jsonx.NewObject()
		metaArgs.Set("enabled", nil)
		args := jsonx.NewObject()
		args.Set("path", "file.txt")
		args.Set("offset", nil)
		args.Set("nullable", nil)
		args.Set("metadata", metaArgs)
		call := &ai.ToolCall{Type: "toolCall", Id: "tool-1", Name: "echo", Arguments: args}

		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("path", "file.txt")
		want.Set("nullable", nil)
		want.Set("metadata", jsonx.NewObject())
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("preserves optional nulls whose referenced schema is nullable", func(t *testing.T) {
		schema := valSchemaFromJSON(t, `{"type":"object","properties":{"value":{"$ref":"#/$defs/value"}},"$defs":{"value":{"anyOf":[{"type":"number"},{"type":"null"}]}}}`)
		tool := &ai.Tool{Name: "echo", Description: "Echo tool", Parameters: schema}
		args := jsonx.NewObject()
		args.Set("value", nil)
		call := &ai.ToolCall{Type: "toolCall", Id: "tool-1", Name: "echo", Arguments: args}

		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("value", nil)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("preserves value already matching nullable union arm", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("value", typebox.Union([]*typebox.Schema{typebox.Number(), typebox.Null()}))
		tool := &ai.Tool{Name: "echo", Description: "Echo tool", Parameters: typebox.Object(props)}
		args := jsonx.NewObject()
		args.Set("value", nil)
		call := &ai.ToolCall{Type: "toolCall", Id: "tool-1", Name: "echo", Arguments: args}

		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("value", nil)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("preserves null matching oneOf nullable union arm", func(t *testing.T) {
		tool, call := createToolCallWithPlainSchema(t, `{"oneOf":[{"type":"number"},{"type":"null"}]}`, nil)
		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("value", nil)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("coerces nullable union when original value matches no arm", func(t *testing.T) {
		tool, call := createToolCallWithPlainSchema(t, `{"anyOf":[{"type":"number"},{"type":"null"}]}`, "42")
		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("value", 42.0)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("accepts null for nullable array schemas with items", func(t *testing.T) {
		tool, call := createToolCallWithPlainSchema(t, `{"type":["array","null"],"items":{"type":"string"}}`, nil)
		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		want := jsonx.NewObject()
		want.Set("value", nil)
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("rejects invalid coercions for serialized plain JSON schemas", func(t *testing.T) {
		cases := []struct {
			schema string
			input  any
		}{
			{`{"type":"boolean"}`, "yes"},
			{`{"type":"number"}`, "abc"},
			{`{"type":"null"}`, "nil"},
			{`{"type":"integer"}`, "4x"},
		}
		for _, tc := range cases {
			tool, call := createToolCallWithPlainSchema(t, tc.schema, tc.input)
			_, err := ai.ValidateToolArguments(tool, call)
			if err == nil || !strings.Contains(err.Error(), "Validation failed") {
				t.Fatalf("schema %s input %#v: expected Validation failed, got %v", tc.schema, tc.input, err)
			}
		}
	})

	t.Run("formats validation errors with property path and received arguments", func(t *testing.T) {
		tool, call := createToolCallWithPlainSchema(t, `{"type":"number"}`, "many")
		_, err := ai.ValidateToolArguments(tool, call)
		if err == nil || !strings.Contains(err.Error(), "Validation failed for tool \"echo\":\n  - value:") || !strings.Contains(err.Error(), "Received arguments:") {
			t.Fatalf("unexpected err: %v", err)
		}

		tool2, call2 := createToolCallWithPlainSchema(t, `{"type":"string"}`, "x")
		call2.Arguments = jsonx.NewObject()
		_, err2 := ai.ValidateToolArguments(tool2, call2)
		if err2 == nil || !strings.Contains(err2.Error(), "  - value:") {
			t.Fatalf("expected missing required property at value path, got %v", err2)
		}
	})

	t.Run("returns a copy of arguments also when nothing is coerced", func(t *testing.T) {
		tool, call := createToolCallWithPlainSchema(t, `{"type":"string"}`, "x")
		got, err := ai.ValidateToolArguments(tool, call)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got == call.Arguments {
			t.Fatal("expected distinct object copy")
		}
		want := jsonx.NewObject()
		want.Set("value", "x")
		if !sjValueEqual(got, want) {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("reports malformed arguments JSON instead of validating empty arguments", func(t *testing.T) {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("value", typebox.Optional(typebox.String()))
		tool := &ai.Tool{Name: "echo", Description: "Echo tool", Parameters: typebox.Object(props)}
		errMsg := "Unexpected token"
		call := &ai.ToolCall{
			Type:           "toolCall",
			Id:             "tool-1",
			Name:           "echo",
			Arguments:      jsonx.NewObject(),
			ArgumentsError: &errMsg,
		}
		_, err := ai.ValidateToolArguments(tool, call)
		if err == nil || err.Error() != `Invalid arguments for tool "echo": Unexpected token` {
			t.Fatalf("got err %v", err)
		}
	})

	t.Run("ValidateToolCall rejects unknown tools", func(t *testing.T) {
		_, err := ai.ValidateToolCall(nil, &ai.ToolCall{Name: "missing"})
		if err == nil || err.Error() != `Tool "missing" not found` {
			t.Fatalf("got err %v", err)
		}
	})
}
