// Ported from packages/mcp/src/protocol/content.ts (pi v1.0.0).

package protocol

import (
	"errors"
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ContentAnnotationsAudience is one entry of ContentAnnotations.audience.
type ContentAnnotationsAudience string

const (
	ContentAnnotationsAudienceUser      ContentAnnotationsAudience = "user"
	ContentAnnotationsAudienceAssistant ContentAnnotationsAudience = "assistant"
)

// ContentAnnotations is optional display metadata on a content block.
type ContentAnnotations struct {
	Audience     []ContentAnnotationsAudience `json:"audience,omitzero"`
	Priority     *float64                     `json:"priority,omitzero"`
	LastModified *string                      `json:"lastModified,omitzero"`
}

// ContentBlock is a sealed union: *TextContent, *ImageContent, *AudioContent,
// *ResourceLinkContent, *EmbeddedResourceContent, *UnknownContentBlock.
type ContentBlock interface{ isContentBlock() }

// TextContent is a text content block.
type TextContent struct {
	Type        string              `json:"type"`
	Text        string              `json:"text"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// ImageContent is a base64 image content block.
type ImageContent struct {
	Type        string              `json:"type"`
	Data        string              `json:"data"`
	MimeType    string              `json:"mimeType"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// AudioContent is a base64 audio content block.
type AudioContent struct {
	Type        string              `json:"type"`
	Data        string              `json:"data"`
	MimeType    string              `json:"mimeType"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// ResourceLinkContent points at a resource by URI.
type ResourceLinkContent struct {
	Type        string              `json:"type"`
	Uri         string              `json:"uri"`
	Name        string              `json:"name"`
	Title       *string             `json:"title,omitzero"`
	Description *string             `json:"description,omitzero"`
	MimeType    *string             `json:"mimeType,omitzero"`
	Size        *int                `json:"size,omitzero"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// TextResourceContents is an embedded resource with text.
type TextResourceContents struct {
	Uri      string        `json:"uri"`
	MimeType *string       `json:"mimeType,omitzero"`
	Text     string        `json:"text"`
	Meta     *jsonx.Object `json:"_meta,omitzero"`
}

// BlobResourceContents is an embedded resource with base64 bytes.
type BlobResourceContents struct {
	Uri      string        `json:"uri"`
	MimeType *string       `json:"mimeType,omitzero"`
	Blob     string        `json:"blob"`
	Meta     *jsonx.Object `json:"_meta,omitzero"`
}

// ResourceContents is a sealed union: *TextResourceContents, *BlobResourceContents,
// *UnknownResourceContents. Text is preferred when both text and blob are present.
type ResourceContents interface{ isResourceContents() }

// UnknownResourceContents keeps a resource object that is neither text nor blob.
type UnknownResourceContents struct {
	Raw *jsonx.Object
}

// ResourceContentsList is a JSON array of resource contents.
type ResourceContentsList []ResourceContents

// EmbeddedResourceContent inlines a resource.
type EmbeddedResourceContent struct {
	Type        string              `json:"type"`
	Resource    ResourceContents    `json:"resource"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// UnknownContentBlock keeps a content block whose type is not recognized.
type UnknownContentBlock struct {
	Raw *jsonx.Object
}

// ContentBlockList is a JSON array of content blocks.
type ContentBlockList []ContentBlock

// CallToolResult is the result of tools/call.
type CallToolResult struct {
	Content           ContentBlockList `json:"content"`
	StructuredContent jsonx.Opt[any]   `json:"structuredContent,omitzero"`
	IsError           *bool            `json:"isError,omitzero"`
	Meta              *jsonx.Object    `json:"_meta,omitzero"`
}

// LlmContent is a sealed union of model-facing content: *LlmTextContent,
// *LlmImageContent, *UnknownLlmContent. Text and images match pi-ai's text
// and image blocks.
type LlmContent interface{ isLlmContent() }

// LlmTextContent is model-facing text.
type LlmTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// LlmImageContent is a model-facing base64 image.
type LlmImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// UnknownLlmContent keeps an LLM content block this package does not produce.
type UnknownLlmContent struct {
	Raw *jsonx.Object
}

func (*TextContent) isContentBlock()             {}
func (*ImageContent) isContentBlock()            {}
func (*AudioContent) isContentBlock()            {}
func (*ResourceLinkContent) isContentBlock()     {}
func (*EmbeddedResourceContent) isContentBlock() {}
func (*UnknownContentBlock) isContentBlock()     {}

func (*TextResourceContents) isResourceContents()    {}
func (*BlobResourceContents) isResourceContents()    {}
func (*UnknownResourceContents) isResourceContents() {}

func (*LlmTextContent) isLlmContent()    {}
func (*LlmImageContent) isLlmContent()   {}
func (*UnknownLlmContent) isLlmContent() {}

func (u *UnknownContentBlock) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownResourceContents) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (u *UnknownLlmContent) MarshalJSON() ([]byte, error) {
	if u == nil || u.Raw == nil {
		return []byte("null"), nil
	}
	return jsonx.Marshal(u.Raw)
}

func (l *ContentBlockList) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if v == nil {
		*l = nil
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return errors.New("expected JSON array for ContentBlockList")
	}
	out := make(ContentBlockList, 0, len(arr))
	for _, elem := range arr {
		b, err := DecodeContentBlock(elem)
		if err != nil {
			return err
		}
		out = append(out, b)
	}
	*l = out
	return nil
}

func (l *ResourceContentsList) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	if v == nil {
		*l = nil
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return errors.New("expected JSON array for ResourceContentsList")
	}
	out := make(ResourceContentsList, 0, len(arr))
	for _, elem := range arr {
		r, err := DecodeResourceContents(elem)
		if err != nil {
			return err
		}
		out = append(out, r)
	}
	*l = out
	return nil
}

type contEmbeddedResourceRaw struct {
	Type        string              `json:"type"`
	Resource    any                 `json:"resource"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

func (c *EmbeddedResourceContent) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return err
	}
	if obj == nil {
		return errors.New("expected JSON object for EmbeddedResourceContent")
	}
	var raw contEmbeddedResourceRaw
	if err := jsonx.Decode(obj, &raw); err != nil {
		return err
	}
	res, err := DecodeResourceContents(raw.Resource)
	if err != nil {
		return err
	}
	*c = EmbeddedResourceContent{
		Type:        raw.Type,
		Resource:    res,
		Annotations: raw.Annotations,
		Meta:        raw.Meta,
	}
	return nil
}

// UnmarshalContentBlock decodes one content block. An unknown type becomes *UnknownContentBlock.
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeContentBlock(v)
}

// DecodeContentBlock decodes one content block from a jsonx value.
func DecodeContentBlock(v any) (ContentBlock, error) {
	switch b := v.(type) {
	case *TextContent, *ImageContent, *AudioContent, *ResourceLinkContent, *EmbeddedResourceContent, *UnknownContentBlock:
		return b.(ContentBlock), nil
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected JSON object for ContentBlock")
	}
	typVal, _ := obj.Get("type")
	typ, _ := typVal.(string)
	switch typ {
	case "text":
		var out TextContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "image":
		var out ImageContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "audio":
		var out AudioContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "resource_link":
		var out ResourceLinkContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "resource":
		var raw contEmbeddedResourceRaw
		if err := jsonx.Decode(obj, &raw); err != nil {
			return nil, err
		}
		res, err := DecodeResourceContents(raw.Resource)
		if err != nil {
			return nil, err
		}
		return &EmbeddedResourceContent{
			Type:        raw.Type,
			Resource:    res,
			Annotations: raw.Annotations,
			Meta:        raw.Meta,
		}, nil
	default:
		return &UnknownContentBlock{Raw: obj}, nil
	}
}

// UnmarshalResourceContents decodes one embedded resource. An unknown shape becomes *UnknownResourceContents.
func UnmarshalResourceContents(data []byte) (ResourceContents, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeResourceContents(v)
}

// DecodeResourceContents decodes one embedded resource from a jsonx value.
func DecodeResourceContents(v any) (ResourceContents, error) {
	switch r := v.(type) {
	case *TextResourceContents, *BlobResourceContents, *UnknownResourceContents:
		return r.(ResourceContents), nil
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected JSON object for ResourceContents")
	}
	if tv, hasText := obj.Get("text"); hasText {
		if _, ok := tv.(string); ok {
			var out TextResourceContents
			if err := jsonx.Decode(obj, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
	}
	if bv, hasBlob := obj.Get("blob"); hasBlob {
		if _, ok := bv.(string); ok {
			var out BlobResourceContents
			if err := jsonx.Decode(obj, &out); err != nil {
				return nil, err
			}
			return &out, nil
		}
	}
	return &UnknownResourceContents{Raw: obj}, nil
}

// UnmarshalLlmContent decodes one LLM content block. An unknown type becomes *UnknownLlmContent.
func UnmarshalLlmContent(data []byte) (LlmContent, error) {
	v, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeLlmContent(v)
}

// DecodeLlmContent decodes one LLM content block from a jsonx value.
func DecodeLlmContent(v any) (LlmContent, error) {
	switch c := v.(type) {
	case *LlmTextContent, *LlmImageContent, *UnknownLlmContent:
		return c.(LlmContent), nil
	}
	obj, err := jsonToObject(v)
	if err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("expected JSON object for LlmContent")
	}
	typVal, _ := obj.Get("type")
	typ, _ := typVal.(string)
	switch typ {
	case "text":
		var out LlmTextContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	case "image":
		var out LlmImageContent
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		return &UnknownLlmContent{Raw: obj}, nil
	}
}

// ToLlmContent converts a tool result to text and image content for a model.
// Text and images pass through, embedded text resources become text, embedded
// image resources become images, and other blocks become a short text placeholder.
// A result without content blocks but with structuredContent becomes its JSON.
func ToLlmContent(result *CallToolResult) []LlmContent {
	if result == nil {
		return []LlmContent{}
	}
	content := make([]LlmContent, 0, len(result.Content))
	for _, block := range result.Content {
		content = append(content, contBlockToLlmContent(block))
	}
	if len(content) == 0 && !result.StructuredContent.IsZero() {
		var val any
		if v, ok := result.StructuredContent.Get(); ok {
			val = v
		}
		if b, err := jsonx.MarshalIndent(val, "  "); err == nil && len(b) > 0 {
			content = append(content, &LlmTextContent{
				Type: "text",
				Text: string(b),
			})
		}
	}
	return content
}

func contBlockToLlmContent(block ContentBlock) LlmContent {
	switch b := block.(type) {
	case *TextContent:
		return &LlmTextContent{Type: "text", Text: b.Text}
	case *ImageContent:
		return &LlmImageContent{Type: "image", Data: b.Data, MimeType: b.MimeType}
	case *AudioContent:
		return &LlmTextContent{Type: "text", Text: fmt.Sprintf("[audio %s omitted]", b.MimeType)}
	case *ResourceLinkContent:
		return &LlmTextContent{Type: "text", Text: fmt.Sprintf("%s: %s", b.Name, b.Uri)}
	case *EmbeddedResourceContent:
		switch r := b.Resource.(type) {
		case *TextResourceContents:
			return &LlmTextContent{Type: "text", Text: r.Text}
		case *BlobResourceContents:
			if r.MimeType != nil && strings.HasPrefix(*r.MimeType, "image/") {
				return &LlmImageContent{Type: "image", Data: r.Blob, MimeType: *r.MimeType}
			}
			mimeType := "unknown type"
			if r.MimeType != nil {
				mimeType = *r.MimeType
			}
			return &LlmTextContent{
				Type: "text",
				Text: fmt.Sprintf("[binary resource %s (%s) omitted]", r.Uri, mimeType),
			}
		default:
			return &LlmTextContent{Type: "text", Text: "[unsupported MCP content resource]"}
		}
	case *UnknownContentBlock:
		typ := "undefined"
		if b != nil && b.Raw != nil {
			if tv, ok := b.Raw.Get("type"); ok {
				typ = jsonValueToString(tv)
			}
		}
		return &LlmTextContent{Type: "text", Text: fmt.Sprintf("[unsupported MCP content %s]", typ)}
	default:
		return &LlmTextContent{Type: "text", Text: "[unsupported MCP content undefined]"}
	}
}
