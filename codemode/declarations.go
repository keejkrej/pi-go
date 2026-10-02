// Ported from packages/codemode/src/declarations.ts (pi v1.0.0).

package codemode

import "regexp"

// declIdentifier matches a JavaScript identifier.
var declIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

const declIndent = "  "

// DefaultInputSchemaMaxChars is the largest rendered input type, in characters, before it becomes unknown.
const DefaultInputSchemaMaxChars = 16_000

// declMaxRefExpansions is the local $ref expansions per rendered schema.
const declMaxRefExpansions = 32

// McpTypescriptPreamble is the TypeScript types for MCP results, from the MCP CallToolResult
// schema, so CallToolResult<T> declarations can refer to them.
const McpTypescriptPreamble = `type Role = "user" | "assistant";
type MetaObject = Record<string, unknown>;
type Annotations = {
  audience?: Role[];
  priority?: number;
  lastModified?: string;
};
type Icon = {
  src: string;
  mimeType?: string;
  sizes?: string[];
  theme?: "light" | "dark";
};
type TextResourceContents = {
  uri: string;
  mimeType?: string;
  _meta?: MetaObject;
  text: string;
};
type BlobResourceContents = {
  uri: string;
  mimeType?: string;
  _meta?: MetaObject;
  blob: string;
};
type TextContent = {
  type: "text";
  text: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ImageContent = {
  type: "image";
  data: string;
  mimeType: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type AudioContent = {
  type: "audio";
  data: string;
  mimeType: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ResourceLink = {
  icons?: Icon[];
  name: string;
  title?: string;
  uri: string;
  description?: string;
  mimeType?: string;
  annotations?: Annotations;
  size?: number;
  _meta?: MetaObject;
  type: "resource_link";
};
type EmbeddedResource = {
  type: "resource";
  resource: TextResourceContents | BlobResourceContents;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ContentBlock =
  | TextContent
  | ImageContent
  | AudioContent
  | ResourceLink
  | EmbeddedResource;
type CallToolResult<TStructured = { [key: string]: unknown }> = {
  _meta?: MetaObject;
  content: ContentBlock[];
  isError?: boolean;
  structuredContent?: TStructured;
  [key: string]: unknown;
};`

// RenderDeclarationsOptions selects the tools and globals to declare.
// Nil means no tools and no globals.
type RenderDeclarationsOptions struct {
	Tools   []*CodemodeTool `json:"tools,omitzero"`
	Globals []*CodemodeTool `json:"globals,omitzero"`
}

// RenderDeclarations renders TypeScript declarations for the script-visible API.
// Tools become members of `declare const tools`, globals become `declare function`
// statements, and ns.member globals become members of `declare const ns`.
// Descriptions become doc comments; schemas become types.
func RenderDeclarations(options *RenderDeclarationsOptions) string {
	panic("unported: RenderDeclarations")
}

// RenderToolOptions bounds rendered input types.
// Nil means the defaults (DefaultInputSchemaMaxChars).
type RenderToolOptions struct {
	InputMaxChars *int `json:"inputMaxChars,omitzero"`
}

// RenderToolSignature renders one tool as a member of the tools object:
// `name(args: T): Promise<R>;`, with the name as the identifier scripts use.
// Input types longer than InputMaxChars render as unknown.
// Tools whose output schema is an MCP CallToolResult render as Promise<CallToolResult<T>>,
// which needs McpTypescriptPreamble.
func RenderToolSignature(tool *CodemodeTool, options *RenderToolOptions) string {
	panic("unported: RenderToolSignature")
}

// RenderToolSample is a tool's description followed by the tool's declaration.
// Used for tool listings and ALL_TOOLS entries.
func RenderToolSample(tool *CodemodeTool, options *RenderToolOptions) string {
	panic("unported: RenderToolSample")
}

// McpStructuredContentSchema returns the structuredContent schema of an MCP CallToolResult
// output schema (detected by a content array of objects, boolean isError, and object _meta).
// The result is the boolean true when the schema declares no structuredContent.
// Nil means the schema is not a CallToolResult.
func McpStructuredContentSchema(schema CodemodeJsonSchema) CodemodeJsonSchema {
	panic("unported: McpStructuredContentSchema")
}

// RenderToolOutputType is the type a tool call resolves to: CallToolResult<T> for MCP
// output schemas (needs McpTypescriptPreamble), the schema's type otherwise, and unknown
// without a schema.
func RenderToolOutputType(schema CodemodeJsonSchema) string {
	panic("unported: RenderToolOutputType")
}

// SchemaToTypeOptions bounds a rendered type expression.
// Nil means no length limit.
type SchemaToTypeOptions struct {
	MaxChars *int `json:"maxChars,omitzero"`
}

// SchemaToType converts a JSON Schema to a TypeScript type expression.
// Objects are one line (`{ a: string; b?: number; }`) with properties sorted by name,
// or one property per line with // comments when a property has a description.
// Arrays render as Array<T>. Local references (#/$defs/..., #/definitions/...) resolve
// against schema; recursive and remote references render as unknown.
// A result longer than MaxChars renders as unknown.
func SchemaToType(schema CodemodeJsonSchema, options *SchemaToTypeOptions) string {
	panic("unported: SchemaToType")
}
