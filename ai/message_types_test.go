package ai_test

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestMessageJSONTypes(t *testing.T) {
	objectDetails := jsonx.ObjectOf(
		"path", "a.ts",
		"items", []any{"first"},
		"summary", jsonx.ObjectOf("lines", 3),
	)
	objectMsg := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "call-1",
		ToolName:   "read",
		Content:    ai.ContentList{},
		Details:    jsonx.Some[ai.JsonValue](objectDetails),
		IsError:    false,
		Timestamp:  1,
	}
	arrayMsg := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "call-1",
		ToolName:   "read",
		Content:    ai.ContentList{},
		Details:    jsonx.Some[ai.JsonValue]([]any{"a", "b"}),
		IsError:    false,
		Timestamp:  1,
	}
	primitiveMsg := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "call-1",
		ToolName:   "read",
		Content:    ai.ContentList{},
		Details:    jsonx.Some[ai.JsonValue]("diagnostic"),
		IsError:    false,
		Timestamp:  1,
	}
	nullMsg := &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: "call-1",
		ToolName:   "read",
		Content:    ai.ContentList{},
		Details:    jsonx.Null[ai.JsonValue](),
		IsError:    false,
		Timestamp:  1,
	}

	ctx := ai.Context{
		Messages: ai.MessageList{objectMsg, arrayMsg, primitiveMsg, nullMsg},
	}

	raw, err := jsonx.Marshal(ctx)
	if err != nil {
		t.Fatalf("jsonx.Marshal(ctx): %v", err)
	}
	var roundTripped ai.Context
	if err := jsonx.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("jsonx.Unmarshal(ctx): %v", err)
	}
	if len(roundTripped.Messages) != 4 {
		t.Fatalf("len(Messages) = %d, want 4", len(roundTripped.Messages))
	}

	rt0 := roundTripped.Messages[0].(*ai.ToolResultMessage)
	if v, ok := rt0.Details.Get(); !ok || !jsonx.DeepEqual(v, objectDetails) {
		t.Fatalf("object details = (%v, %v), want %v", v, ok, objectDetails)
	}
	rt1 := roundTripped.Messages[1].(*ai.ToolResultMessage)
	if v, ok := rt1.Details.Get(); !ok || !jsonx.DeepEqual(v, []any{"a", "b"}) {
		t.Fatalf("array details = (%v, %v), want [a b]", v, ok)
	}
	rt2 := roundTripped.Messages[2].(*ai.ToolResultMessage)
	if v, ok := rt2.Details.Get(); !ok || v != "diagnostic" {
		t.Fatalf("primitive details = (%v, %v), want diagnostic", v, ok)
	}
	rt3 := roundTripped.Messages[3].(*ai.ToolResultMessage)
	if !rt3.Details.IsNull() {
		t.Fatalf("null details IsNull = false, state = %+v", rt3.Details)
	}
}
