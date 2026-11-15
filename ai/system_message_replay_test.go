package ai_test

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func replayTool(name string, desc ...string) *ai.Tool {
	d := name + " tool"
	if len(desc) > 0 {
		d = desc[0]
	}
	return &ai.Tool{
		Name:        name,
		Description: d,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
	}
}

func replayStrPtr(s string) *string { return &s }

func buildReplayTranscript() *ai.TranscriptContext {
	s1 := omap.NewMap[string, *string]()
	s1.Set("a", replayStrPtr("<a>1</a>"))
	s1.Set("b", replayStrPtr("<b>1</b>"))

	s2 := omap.NewMap[string, *string]()
	s2.Set("a", replayStrPtr("<a>2</a>"))
	s2.Set("b", nil)
	s2.Set("c", replayStrPtr("<c>1</c>"))

	return ai.NormalizeContext(&ai.Context{
		Messages: ai.MessageList{
			&ai.SystemMessage{
				Role:       "system",
				Content:    ai.SystemMessageContent{Text: replayStrPtr("base")},
				Sections:   s1,
				ToolsAdded: []*ai.Tool{replayTool("first")},
				Timestamp:  10,
			},
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: replayStrPtr("hello")},
				Timestamp: 11,
			},
			&ai.SystemMessage{
				Role:      "system",
				Content:   ai.SystemMessageContent{Text: replayStrPtr("also do this")},
				Timestamp: 12,
			},
			&ai.AssistantMessage{
				Role:      "assistant",
				Content:   ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
				Timestamp: 13,
			},
			&ai.SystemMessage{
				Role:         "system",
				Content:      ai.SystemMessageContent{Text: replayStrPtr("")},
				Sections:     s2,
				ToolsRemoved: []ai.ToolReference{{Name: "first"}},
				ToolsAdded:   []*ai.Tool{replayTool("second")},
				Timestamp:    14,
			},
		},
	})
}

func TestSystemMessageReplay(t *testing.T) {
	transcript := buildReplayTranscript()

	t.Run("replays content, sections, and tools into one leading message", func(t *testing.T) {
		current := ai.GetCurrentSystemMessage(transcript.Messages)
		if current == nil {
			t.Fatal("expected non-nil SystemMessage")
		}
		if current.Role != "system" || ai.ContentText(current.Content) != "base\n\nalso do this" || current.Timestamp != 10 {
			t.Fatalf("unexpected head: role=%q content=%q ts=%d", current.Role, ai.ContentText(current.Content), current.Timestamp)
		}
		if current.Sections == nil || current.Sections.Len() != 2 {
			t.Fatalf("unexpected sections: %+v", current.Sections)
		}
		va, _ := current.Sections.Get("a")
		vc, _ := current.Sections.Get("c")
		if va == nil || *va != "<a>2</a>" || vc == nil || *vc != "<c>1</c>" {
			t.Fatalf("unexpected sections a=%v c=%v", va, vc)
		}
		if len(current.ToolsAdded) != 1 || current.ToolsAdded[0].Name != "second" {
			t.Fatalf("unexpected toolsAdded: %+v", current.ToolsAdded)
		}
		if got := ai.GetCurrentSystemPrompt(transcript.Messages); got != "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>" {
			t.Fatalf("got prompt %q", got)
		}
	})

	t.Run("collapse keeps only non-system messages after the replayed head", func(t *testing.T) {
		collapsed := ai.CollapseSystemMessages(transcript)
		var roles []string
		for _, m := range collapsed.Messages {
			roles = append(roles, m.GetRole())
		}
		if !slices.Equal(roles, []string{"system", "user", "assistant"}) {
			t.Fatalf("roles = %v, want [system user assistant]", roles)
		}
		collapsedAgain := ai.CollapseSystemMessages(collapsed)
		if len(collapsedAgain.Messages) != len(collapsed.Messages) ||
			ai.GetCurrentSystemPrompt(collapsedAgain.Messages) != ai.GetCurrentSystemPrompt(collapsed.Messages) {
			t.Fatalf("expected idempotent collapse")
		}
	})

	t.Run("replay of a transcript without system messages is empty", func(t *testing.T) {
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: replayStrPtr("hi")}, Timestamp: 1},
			},
		})
		if sm := ai.GetCurrentSystemMessage(ctx.Messages); sm != nil {
			t.Fatalf("expected nil, got %+v", sm)
		}
		if got := ai.GetCurrentSystemPrompt(ctx.Messages); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if collapsed := ai.CollapseSystemMessages(ctx); len(collapsed.Messages) != len(ctx.Messages) {
			t.Fatalf("expected unchanged messages")
		}
	})

	t.Run("a late full patch on a transcript without a leading message replays as the prompt", func(t *testing.T) {
		secs := omap.NewMap[string, *string]()
		secs.Set("preamble", replayStrPtr("You are pi."))
		ctx := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: replayStrPtr("old session")}, Timestamp: 1},
				&ai.SystemMessage{
					Role:       "system",
					Content:    ai.SystemMessageContent{Text: replayStrPtr("")},
					Sections:   secs,
					ToolsAdded: []*ai.Tool{replayTool("x")},
					Timestamp:  2,
				},
			},
		})
		if got := ai.GetCurrentSystemPrompt(ctx.Messages); got != "You are pi." {
			t.Fatalf("got %q, want You are pi.", got)
		}
		collapsed := ai.CollapseSystemMessages(ctx)
		head, ok := collapsed.Messages[0].(*ai.SystemMessage)
		if !ok || len(head.ToolsAdded) != 1 || head.ToolsAdded[0].Name != "x" {
			t.Fatalf("unexpected collapsed head: %#v", collapsed.Messages[0])
		}
	})

	t.Run("renders complete prompts and framed updates", func(t *testing.T) {
		leading := transcript.Messages[0].(*ai.SystemMessage)
		update := transcript.Messages[4].(*ai.SystemMessage)
		if got := ai.GetSystemMessageText(leading); got != "base\n\n<a>1</a>\n\n<b>1</b>" {
			t.Fatalf("GetSystemMessageText = %q", got)
		}
		wantUpdate := "Updated system prompt section \"a\":\n\n<a>2</a>\n\nRemoved system prompt section \"b\".\n\nUpdated system prompt section \"c\":\n\n<c>1</c>"
		if got := ai.RenderSystemMessageUpdate(update); got != wantUpdate {
			t.Fatalf("RenderSystemMessageUpdate = %q, want %q", got, wantUpdate)
		}
	})

	t.Run("normalizes the legacy prompt and tool fields into a leading system message", func(t *testing.T) {
		msgs := ai.MessageList{
			&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: replayStrPtr("hi")}, Timestamp: 1},
		}
		if norm := ai.NormalizeContext(&ai.Context{Messages: msgs}); len(norm.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(norm.Messages))
		}
		if norm := ai.NormalizeContext(&ai.Context{SystemPrompt: replayStrPtr(""), Tools: []*ai.Tool{}, Messages: msgs}); len(norm.Messages) != 1 {
			t.Fatalf("expected 1 message for empty prompt/tools, got %d", len(norm.Messages))
		}
		norm := ai.NormalizeContext(&ai.Context{
			SystemPrompt: replayStrPtr("be brief"),
			Tools:        []*ai.Tool{replayTool("a")},
			Messages:     msgs,
		})
		if len(norm.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(norm.Messages))
		}
		head, ok := norm.Messages[0].(*ai.SystemMessage)
		if !ok || ai.ContentText(head.Content) != "be brief" || len(head.ToolsAdded) != 1 || head.ToolsAdded[0].Name != "a" || head.Timestamp != 0 {
			t.Fatalf("unexpected normalized head: %+v", head)
		}
	})

	t.Run("compares tool declarations with optional fields", func(t *testing.T) {
		a := replayTool("a")
		aFalse := replayTool("a")
		aFalse.ConstrainedSampling = ai.ToolConstrainedSamplingValue{V: ai.ToolConstrainedSamplingFalse{}}
		if !ai.DeclarationsEqual(a, replayTool("a")) {
			t.Fatal("expected equal declarations")
		}
		if ai.DeclarationsEqual(a, replayTool("a", "changed")) {
			t.Fatal("expected different description declarations to differ")
		}
		if ai.DeclarationsEqual(a, aFalse) {
			t.Fatal("expected different constrainedSampling declarations to differ")
		}
	})

	t.Run("tool state changes treat changed definitions as removal plus addition", func(t *testing.T) {
		changes := ai.GetToolStateChanges([]*ai.Tool{replayTool("a"), replayTool("b")}, []*ai.Tool{replayTool("b", "changed"), replayTool("c")})
		if len(changes.ToolsAdded) != 2 || changes.ToolsAdded[0].Name != "b" || changes.ToolsAdded[0].Description != "changed" || changes.ToolsAdded[1].Name != "c" {
			t.Fatalf("unexpected ToolsAdded: %+v", changes.ToolsAdded)
		}
		if len(changes.ToolsRemoved) != 2 || changes.ToolsRemoved[0].Name != "a" || changes.ToolsRemoved[1].Name != "b" {
			t.Fatalf("unexpected ToolsRemoved: %+v", changes.ToolsRemoved)
		}
		same := ai.GetToolStateChanges([]*ai.Tool{replayTool("a")}, []*ai.Tool{replayTool("a")})
		if len(same.ToolsAdded) != 0 || len(same.ToolsRemoved) != 0 {
			t.Fatalf("expected empty changes, got %+v", same)
		}
	})

	t.Run("detects non-additive tool history and redefinitions", func(t *testing.T) {
		if !ai.HasNonAdditiveToolChanges(transcript.Messages) {
			t.Fatal("expected HasNonAdditiveToolChanges = true")
		}
		if ai.HasToolRedefinitions(transcript.Messages) {
			t.Fatal("expected HasToolRedefinitions = false")
		}
		additive := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: replayStrPtr("")}, ToolsAdded: []*ai.Tool{replayTool("a")}, Timestamp: 1},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: replayStrPtr("")}, ToolsAdded: []*ai.Tool{replayTool("b")}, Timestamp: 2},
			},
		})
		if ai.HasNonAdditiveToolChanges(additive.Messages) {
			t.Fatal("expected HasNonAdditiveToolChanges = false for additive")
		}
		redeclared := ai.NormalizeContext(&ai.Context{
			Messages: ai.MessageList{
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: replayStrPtr("")}, ToolsAdded: []*ai.Tool{replayTool("a")}, Timestamp: 1},
				&ai.SystemMessage{Role: "system", Content: ai.SystemMessageContent{Text: replayStrPtr("")}, ToolsAdded: []*ai.Tool{replayTool("a", "changed")}, Timestamp: 2},
			},
		})
		if !ai.HasNonAdditiveToolChanges(redeclared.Messages) {
			t.Fatal("expected HasNonAdditiveToolChanges = true")
		}
		if !ai.HasToolRedefinitions(redeclared.Messages) {
			t.Fatal("expected HasToolRedefinitions = true")
		}
	})
}

func TestSystemMessageWithToolsAdded_JSONRoundTrip(t *testing.T) {
	props := omap.NewMap[string, *typebox.Schema]()
	props.Set("path", typebox.String())
	props.Set("limit", typebox.Optional(typebox.Integer()))
	tool := &ai.Tool{
		Name:        "read",
		Description: "Read a file",
		Parameters:  typebox.Object(props),
	}

	rawTool, err := jsonx.Marshal(tool)
	if err != nil {
		t.Fatalf("Marshal tool failed: %v", err)
	}
	var decodedTool ai.Tool
	if err := jsonx.Unmarshal(rawTool, &decodedTool); err != nil {
		t.Fatalf("Unmarshal tool failed: %v", err)
	}
	if decodedTool.Parameters == nil || !ai.DeclarationsEqual(tool, &decodedTool) {
		t.Fatalf("tool roundtrip mismatch: got %+v want %+v", decodedTool, tool)
	}

	msg := &ai.SystemMessage{
		Role:       "system",
		Content:    ai.SystemMessageContent{Text: replayStrPtr("system")},
		ToolsAdded: []*ai.Tool{tool},
		Timestamp:  1234,
	}
	rawMsg, err := jsonx.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal SystemMessage failed: %v", err)
	}
	decodedMsgAny, err := ai.UnmarshalMessage(rawMsg)
	if err != nil {
		t.Fatalf("UnmarshalMessage failed: %v", err)
	}
	decodedMsg, ok := decodedMsgAny.(*ai.SystemMessage)
	if !ok || len(decodedMsg.ToolsAdded) != 1 {
		t.Fatalf("unexpected decoded message: %T %+v", decodedMsgAny, decodedMsgAny)
	}
	if !ai.DeclarationsEqual(tool, decodedMsg.ToolsAdded[0]) {
		t.Fatalf("SystemMessage ToolsAdded roundtrip mismatch")
	}
}
