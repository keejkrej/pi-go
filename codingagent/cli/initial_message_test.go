// Ported from packages/coding-agent/test/initial-message.test.ts (pi v1.5.2).

package cli

import (
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

func timStrPtr(s string) *string { return &s }

func TestBuildInitialMessageCombinesAndConsumes(t *testing.T) {
	parsed := &core.Args{Messages: []string{"first", "second"}}
	img := &ai.ImageContent{Type: "image", MimeType: "image/png", Data: "abc"}

	res := BuildInitialMessage(InitialMessageInput{
		Parsed:       parsed,
		StdinContent: timStrPtr("stdin\n"),
		FileText:     timStrPtr("<file/>\n"),
		FileImages:   []*ai.ImageContent{img},
	})

	if res.InitialMessage == nil || *res.InitialMessage != "stdin\n<file/>\nfirst" {
		t.Fatalf("InitialMessage = %v, want %q", res.InitialMessage, "stdin\n<file/>\nfirst")
	}
	if len(res.InitialImages) != 1 || res.InitialImages[0] != img {
		t.Fatalf("InitialImages = %+v", res.InitialImages)
	}
	if len(parsed.Messages) != 1 || parsed.Messages[0] != "second" {
		t.Fatalf("parsed.Messages after BuildInitialMessage = %v, want [second]", parsed.Messages)
	}

	empty := BuildInitialMessage(InitialMessageInput{
		Parsed:   &core.Args{},
		FileText: timStrPtr(""),
	})
	if empty.InitialMessage != nil || empty.InitialImages != nil {
		t.Fatalf("expected nil InitialMessage/InitialImages for empty inputs, got %+v", empty)
	}
}
