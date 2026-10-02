// Ported from packages/mcp/src/protocol/types.ts (pi v1.0.0).

package protocol

import "github.com/keejkrej/pi-go/internal/jsonx"

// LatestProtocolVersion is the protocol version the client requests.
const LatestProtocolVersion = "2025-11-25"

// SupportedProtocolVersion is a protocol version the client accepts from a server.
type SupportedProtocolVersion string

const (
	SupportedProtocolVersion20251125 SupportedProtocolVersion = "2025-11-25"
	SupportedProtocolVersion20250618 SupportedProtocolVersion = "2025-06-18"
	SupportedProtocolVersion20250326 SupportedProtocolVersion = "2025-03-26"
	SupportedProtocolVersion20241105 SupportedProtocolVersion = "2024-11-05"
)

// SupportedProtocolVersions is the versions the client accepts from a server.
// Servers that do not support the requested version answer with their own latest one,
// so older versions stay accepted for servers built on older SDKs.
var SupportedProtocolVersions = []SupportedProtocolVersion{
	SupportedProtocolVersion20251125,
	SupportedProtocolVersion20250618,
	SupportedProtocolVersion20250326,
	SupportedProtocolVersion20241105,
}

// Implementation identifies a client or server.
type Implementation struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Title   *string `json:"title,omitzero"`
}

// Root is one workspace root advertised by the client.
type Root struct {
	Uri  string  `json:"uri"`
	Name *string `json:"name,omitzero"`
}

// ClientCapabilitiesRoots is ClientCapabilities.roots.
type ClientCapabilitiesRoots struct {
	ListChanged *bool `json:"listChanged,omitzero"`
}

// ClientCapabilities is sent in initialize.
type ClientCapabilities struct {
	Experimental *jsonx.Object            `json:"experimental,omitzero"`
	Roots        *ClientCapabilitiesRoots `json:"roots,omitzero"`
	Sampling     *jsonx.Object            `json:"sampling,omitzero"`
	Elicitation  *jsonx.Object            `json:"elicitation,omitzero"`
}

// ServerCapabilitiesPrompts is ServerCapabilities.prompts.
type ServerCapabilitiesPrompts struct {
	ListChanged *bool `json:"listChanged,omitzero"`
}

// ServerCapabilitiesResources is ServerCapabilities.resources.
type ServerCapabilitiesResources struct {
	Subscribe   *bool `json:"subscribe,omitzero"`
	ListChanged *bool `json:"listChanged,omitzero"`
}

// ServerCapabilitiesTools is ServerCapabilities.tools.
type ServerCapabilitiesTools struct {
	ListChanged *bool `json:"listChanged,omitzero"`
}

// ServerCapabilities is returned by initialize.
type ServerCapabilities struct {
	Experimental *jsonx.Object                `json:"experimental,omitzero"`
	Logging      *jsonx.Object                `json:"logging,omitzero"`
	Prompts      *ServerCapabilitiesPrompts   `json:"prompts,omitzero"`
	Resources    *ServerCapabilitiesResources `json:"resources,omitzero"`
	Tools        *ServerCapabilitiesTools     `json:"tools,omitzero"`
	Completions  *jsonx.Object                `json:"completions,omitzero"`
}

// InitializeParams is the params object of initialize.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// InitializeResult is the result of initialize.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    *string            `json:"instructions,omitzero"`
}

// ProgressNotification is the params object of notifications/progress.
type ProgressNotification struct {
	ProgressToken JsonRpcId `json:"progressToken"`
	Progress      float64   `json:"progress"`
	Total         *float64  `json:"total,omitzero"`
	Message       *string   `json:"message,omitzero"`
}

// CancelledNotification is the params object of notifications/cancelled.
type CancelledNotification struct {
	RequestId JsonRpcId `json:"requestId"`
	Reason    *string   `json:"reason,omitzero"`
}

// ToolAnnotations is optional tool UI hints.
type ToolAnnotations struct {
	Title           *string `json:"title,omitzero"`
	ReadOnlyHint    *bool   `json:"readOnlyHint,omitzero"`
	DestructiveHint *bool   `json:"destructiveHint,omitzero"`
	IdempotentHint  *bool   `json:"idempotentHint,omitzero"`
	OpenWorldHint   *bool   `json:"openWorldHint,omitzero"`
}

// ToolExecutionTaskSupport is ToolExecution.taskSupport.
type ToolExecutionTaskSupport string

const (
	ToolExecutionTaskSupportForbidden ToolExecutionTaskSupport = "forbidden"
	ToolExecutionTaskSupportOptional  ToolExecutionTaskSupport = "optional"
	ToolExecutionTaskSupportRequired  ToolExecutionTaskSupport = "required"
)

// ToolExecution is optional task-support metadata on a tool.
type ToolExecution struct {
	TaskSupport *ToolExecutionTaskSupport `json:"taskSupport,omitzero"`
}

// Tool is one entry of tools/list.
type Tool struct {
	Name         string           `json:"name"`
	Title        *string          `json:"title,omitzero"`
	Description  *string          `json:"description,omitzero"`
	InputSchema  *jsonx.Object    `json:"inputSchema"`
	OutputSchema *jsonx.Object    `json:"outputSchema,omitzero"`
	Annotations  *ToolAnnotations `json:"annotations,omitzero"`
	Execution    *ToolExecution   `json:"execution,omitzero"`
	Meta         *jsonx.Object    `json:"_meta,omitzero"`
}

// ListToolsResult is one page of tools/list.
type ListToolsResult struct {
	Tools      []Tool        `json:"tools"`
	NextCursor *string       `json:"nextCursor,omitzero"`
	Meta       *jsonx.Object `json:"_meta,omitzero"`
}

// Resource is a resource a server lists in resources/list.
type Resource struct {
	Uri         string              `json:"uri"`
	Name        string              `json:"name"`
	Title       *string             `json:"title,omitzero"`
	Description *string             `json:"description,omitzero"`
	MimeType    *string             `json:"mimeType,omitzero"`
	Size        *int                `json:"size,omitzero"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// ResourceTemplate is a family of resources, addressed by an RFC 6570 URI template,
// from resources/templates/list.
type ResourceTemplate struct {
	UriTemplate string              `json:"uriTemplate"`
	Name        string              `json:"name"`
	Title       *string             `json:"title,omitzero"`
	Description *string             `json:"description,omitzero"`
	MimeType    *string             `json:"mimeType,omitzero"`
	Annotations *ContentAnnotations `json:"annotations,omitzero"`
	Meta        *jsonx.Object       `json:"_meta,omitzero"`
}

// ListResourcesResult is one page of resources/list.
type ListResourcesResult struct {
	Resources  []Resource    `json:"resources"`
	NextCursor *string       `json:"nextCursor,omitzero"`
	Meta       *jsonx.Object `json:"_meta,omitzero"`
}

// ListResourceTemplatesResult is one page of resources/templates/list.
type ListResourceTemplatesResult struct {
	ResourceTemplates []ResourceTemplate `json:"resourceTemplates"`
	NextCursor        *string            `json:"nextCursor,omitzero"`
	Meta              *jsonx.Object      `json:"_meta,omitzero"`
}

// ReadResourceResult is the result of resources/read.
type ReadResourceResult struct {
	Contents ResourceContentsList `json:"contents"`
	Meta     *jsonx.Object        `json:"_meta,omitzero"`
}
