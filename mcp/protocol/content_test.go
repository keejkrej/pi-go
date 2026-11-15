// Ported from packages/mcp/test/content.test.ts (pi v1.5.2).

package protocol

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tcontPtr[T any](v T) *T { return &v }

func TestContent_ToLlmContentPassesTextAndImagesAndReplacesOthers(t *testing.T) {
	blocks := ContentBlockList{
		&TextContent{
			Type:        "text",
			Text:        "hello",
			Annotations: &ContentAnnotations{Priority: tcontPtr(1.0)},
		},
		&ImageContent{
			Type:     "image",
			Data:     "aW1n",
			MimeType: "image/png",
			Meta:     jsonx.ObjectOf("x", 1),
		},
		&AudioContent{
			Type:     "audio",
			Data:     "YXVk",
			MimeType: "audio/wav",
		},
		&ResourceLinkContent{
			Type: "resource_link",
			Uri:  "file:///a.txt",
			Name: "a.txt",
		},
		&EmbeddedResourceContent{
			Type: "resource",
			Resource: &TextResourceContents{
				Uri:  "file:///b.txt",
				Text: "inline",
			},
		},
		&EmbeddedResourceContent{
			Type: "resource",
			Resource: &BlobResourceContents{
				Uri:      "file:///c.png",
				MimeType: tcontPtr("image/png"),
				Blob:     "Yw==",
			},
		},
		&EmbeddedResourceContent{
			Type: "resource",
			Resource: &BlobResourceContents{
				Uri:  "file:///d.bin",
				Blob: "ZA==",
			},
		},
	}

	got := ToLlmContent(&CallToolResult{Content: blocks})
	want := []LlmContent{
		&LlmTextContent{Type: "text", Text: "hello"},
		&LlmImageContent{Type: "image", Data: "aW1n", MimeType: "image/png"},
		&LlmTextContent{Type: "text", Text: "[audio audio/wav omitted]"},
		&LlmTextContent{Type: "text", Text: "a.txt: file:///a.txt"},
		&LlmTextContent{Type: "text", Text: "inline"},
		&LlmImageContent{Type: "image", Data: "Yw==", MimeType: "image/png"},
		&LlmTextContent{Type: "text", Text: "[binary resource file:///d.bin (unknown type) omitted]"},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if !jsonx.DeepEqual(got[i], want[i]) {
			t.Fatalf("item %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestContent_ToLlmContentStructuredContentFallback(t *testing.T) {
	// #10416: structured content may be any JSON value.
	cases := []struct {
		name   string
		result *CallToolResult
		want   []LlmContent
	}{
		{
			name: "array",
			result: &CallToolResult{
				Content:           ContentBlockList{},
				StructuredContent: jsonx.Some[any]([]any{1, 2}),
			},
			want: []LlmContent{&LlmTextContent{Type: "text", Text: "[\n  1,\n  2\n]"}},
		},
		{
			name: "null",
			result: &CallToolResult{
				Content:           ContentBlockList{},
				StructuredContent: jsonx.Null[any](),
			},
			want: []LlmContent{&LlmTextContent{Type: "text", Text: "null"}},
		},
		{
			name: "object",
			result: &CallToolResult{
				Content:           ContentBlockList{},
				StructuredContent: jsonx.Some[any](jsonx.ObjectOf("n", 1)),
			},
			want: []LlmContent{&LlmTextContent{Type: "text", Text: "{\n  \"n\": 1\n}"}},
		},
		{
			name: "prefers content blocks over structured content",
			result: &CallToolResult{
				Content:           ContentBlockList{&TextContent{Type: "text", Text: "n=1"}},
				StructuredContent: jsonx.Some[any](jsonx.ObjectOf("n", 1)),
			},
			want: []LlmContent{&LlmTextContent{Type: "text", Text: "n=1"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToLlmContent(tc.result)
			if len(got) != len(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
			for i := range tc.want {
				if !jsonx.DeepEqual(got[i], tc.want[i]) {
					t.Fatalf("got[%d] = %#v, want %#v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
