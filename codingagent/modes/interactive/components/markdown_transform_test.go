package components

import (
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
)

func TestCreateMarkdownTransformChainAndPanicRecovery(t *testing.T) {
	if fn := CreateMarkdownTransform(core.MarkdownMessageTypeUser, nil, nil, nil, nil); fn != nil {
		t.Fatalf("expected nil transform when transformers slice is empty")
	}

	msgId := "msg-1"
	ts := "2026-01-01T00:00:00Z"
	streaming := true

	var captured core.MarkdownTransformContext
	t1 := func(md string, rctx core.MarkdownTransformContext) string {
		captured = rctx
		return md + " [t1]"
	}
	tPanic := func(string, core.MarkdownTransformContext) string {
		panic("boom")
	}
	t2 := func(md string, rctx core.MarkdownTransformContext) string {
		return md + " [t2]"
	}

	fn := CreateMarkdownTransform(
		core.MarkdownMessageTypeAssistant,
		func() bool { return streaming },
		[]core.MarkdownTransformer{nil, t1, tPanic, t2},
		&msgId,
		&ts,
	)
	if fn == nil {
		t.Fatalf("expected non-nil composed transform")
	}

	got := fn("hello", 72)
	if got != "hello [t1] [t2]" {
		t.Fatalf("got %q, want %q", got, "hello [t1] [t2]")
	}
	if captured.MessageType != core.MarkdownMessageTypeAssistant || !captured.IsStreaming || captured.AvailableWidth != 72 {
		t.Fatalf("unexpected context: %+v", captured)
	}
	if captured.MessageId == nil || *captured.MessageId != "msg-1" || captured.Timestamp == nil || *captured.Timestamp != ts {
		t.Fatalf("expected messageId/timestamp propagated, got %+v", captured)
	}
}
