// Ported from packages/mcp/src/protocol/content.ts (pi v1.0.0).

package protocol

import "github.com/keejkrej/pi-go/internal/jsonx"

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
	StructuredContent *jsonx.Object    `json:"structuredContent,omitzero"`
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
	panic("unported: UnknownContentBlock.MarshalJSON")
}

func (u *UnknownResourceContents) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownResourceContents.MarshalJSON")
}

func (u *UnknownLlmContent) MarshalJSON() ([]byte, error) {
	panic("unported: UnknownLlmContent.MarshalJSON")
}

func (l *ContentBlockList) UnmarshalJSON(data []byte) error {
	panic("unported: ContentBlockList.UnmarshalJSON")
}

func (l *ResourceContentsList) UnmarshalJSON(data []byte) error {
	panic("unported: ResourceContentsList.UnmarshalJSON")
}

func (c *EmbeddedResourceContent) UnmarshalJSON(data []byte) error {
	panic("unported: EmbeddedResourceContent.UnmarshalJSON")
}

// UnmarshalContentBlock decodes one content block. An unknown type becomes *UnknownContentBlock.
func UnmarshalContentBlock(data []byte) (ContentBlock, error) {
	panic("unported: UnmarshalContentBlock")
}

// DecodeContentBlock decodes one content block from a jsonx value.
func DecodeContentBlock(v any) (ContentBlock, error) {
	panic("unported: DecodeContentBlock")
}

// UnmarshalResourceContents decodes one embedded resource. An unknown shape becomes *UnknownResourceContents.
func UnmarshalResourceContents(data []byte) (ResourceContents, error) {
	panic("unported: UnmarshalResourceContents")
}

// DecodeResourceContents decodes one embedded resource from a jsonx value.
func DecodeResourceContents(v any) (ResourceContents, error) {
	panic("unported: DecodeResourceContents")
}

// UnmarshalLlmContent decodes one LLM content block. An unknown type becomes *UnknownLlmContent.
func UnmarshalLlmContent(data []byte) (LlmContent, error) {
	panic("unported: UnmarshalLlmContent")
}

// DecodeLlmContent decodes one LLM content block from a jsonx value.
func DecodeLlmContent(v any) (LlmContent, error) {
	panic("unported: DecodeLlmContent")
}

// ToLlmContent converts a tool result to text and image content for a model.
// Text and images pass through, embedded text resources become text, embedded
// image resources become images, and other blocks become a short text placeholder.
// A result without content blocks but with structuredContent becomes its JSON.
func ToLlmContent(result *CallToolResult) []LlmContent {
	panic("unported: ToLlmContent")
}

func contBlockToLlmContent(block ContentBlock) LlmContent {
	panic("unported: contBlockToLlmContent")
}
