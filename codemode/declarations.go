// Ported from packages/codemode/src/declarations.ts (pi v1.0.0).

package codemode

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

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
	var sections []string
	var tools []*CodemodeTool
	var globals []*CodemodeTool
	if options != nil {
		tools = options.Tools
		globals = options.Globals
	}

	if len(tools) > 0 {
		members := make([]string, len(tools))
		for i, tool := range tools {
			var doc string
			if tool.Description != nil {
				doc = declDocComment(*tool.Description, declIndent)
			}
			members[i] = doc + declIndent + RenderToolSignature(tool, nil)
		}
		sections = append(sections, "declare const tools: {\n"+strings.Join(members, "\n")+"\n};")
	}

	namespaces := omap.NewMap[string, []string]()
	for _, global := range globals {
		dot := strings.IndexByte(global.Name, '.')
		if dot == -1 {
			sections = append(sections, declRenderGlobal("declare function "+global.Name, global, ""))
			continue
		}
		ns := global.Name[:dot]
		memberName := global.Name[dot+1:]
		existing, _ := namespaces.Get(ns)
		existing = append(existing, declRenderGlobal(memberName, global, declIndent))
		namespaces.Set(ns, existing)
	}
	for _, ns := range namespaces.Keys() {
		members, _ := namespaces.Get(ns)
		sections = append(sections, "declare const "+ns+": {\n"+strings.Join(members, "\n")+"\n};")
	}
	return strings.Join(sections, "\n\n")
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
	maxChars := DefaultInputSchemaMaxChars
	if options != nil && options.InputMaxChars != nil {
		maxChars = *options.InputMaxChars
	}
	input := "unknown"
	if tool.InputSchema != nil {
		input = SchemaToType(tool.InputSchema, &SchemaToTypeOptions{MaxChars: &maxChars})
	}
	return fmt.Sprintf("%s(args: %s): Promise<%s>;", ToCodemodeIdentifier(tool.Name), input, RenderToolOutputType(tool.OutputSchema))
}

// RenderToolSample is a tool's description followed by the tool's declaration.
// Used for tool listings and ALL_TOOLS entries.
func RenderToolSample(tool *CodemodeTool, options *RenderToolOptions) string {
	desc := ""
	if tool.Description != nil {
		desc = js.Trim(*tool.Description)
	}
	declaration := fmt.Sprintf("declare const tools: { %s };", RenderToolSignature(tool, options))
	return desc + "\n\ncodemode tool declaration:\n```ts\n" + declaration + "\n```"
}

// McpStructuredContentSchema returns the structuredContent schema of an MCP CallToolResult
// output schema (detected by a content array of objects, boolean isError, and object _meta).
// The result is the boolean true when the schema declares no structuredContent.
// Nil means the schema is not a CallToolResult.
func McpStructuredContentSchema(schema CodemodeJsonSchema) CodemodeJsonSchema {
	obj, ok := declAsObject(schema)
	if !ok {
		return nil
	}
	rawProps, _ := obj.get("properties")
	props, ok := declAsObject(rawProps)
	if !ok {
		return nil
	}
	rawContent, _ := props.get("content")
	content, ok := declAsObject(rawContent)
	if !ok || declStringProp(content, "type") != "array" {
		return nil
	}
	rawItems, _ := content.get("items")
	items, ok := declAsObject(rawItems)
	if !ok || declStringProp(items, "type") != "object" {
		return nil
	}
	rawIsError, _ := props.get("isError")
	isError, ok := declAsObject(rawIsError)
	if !ok || declStringProp(isError, "type") != "boolean" {
		return nil
	}
	rawMeta, _ := props.get("_meta")
	meta, ok := declAsObject(rawMeta)
	if !ok || declStringProp(meta, "type") != "object" {
		return nil
	}
	if sc, has := props.get("structuredContent"); has {
		if _, isObj := declAsObject(sc); isObj {
			return sc
		}
		if b, isBool := sc.(bool); isBool {
			return b
		}
	}
	return true
}

// RenderToolOutputType is the type a tool call resolves to: CallToolResult<T> for MCP
// output schemas (needs McpTypescriptPreamble), the schema's type otherwise, and unknown
// without a schema.
func RenderToolOutputType(schema CodemodeJsonSchema) string {
	structured := McpStructuredContentSchema(schema)
	if structured != nil {
		t := SchemaToType(structured, nil)
		if t == "unknown" {
			return "CallToolResult"
		}
		return fmt.Sprintf("CallToolResult<%s>", t)
	}
	if schema == nil {
		return "unknown"
	}
	return SchemaToType(schema, nil)
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
	ctx := &declSchemaContext{
		root:      schema,
		resolving: make(map[string]bool),
	}
	t := declToType(schema, ctx)
	if options != nil && options.MaxChars != nil && declUTF16Len(t) > *options.MaxChars {
		return "unknown"
	}
	return t
}

type declSchemaObject struct {
	keys []string
	get  func(string) (any, bool)
}

type declSchemaContext struct {
	root       CodemodeJsonSchema
	resolving  map[string]bool
	expansions int
}

var declLineBreakRe = regexp.MustCompile(`\r?\n`)

func declAsObject(v any) (declSchemaObject, bool) {
	switch o := v.(type) {
	case *jsonx.Object:
		if o == nil {
			return declSchemaObject{}, false
		}
		return declSchemaObject{keys: o.Keys(), get: o.Get}, true
	case map[string]any:
		if o == nil {
			return declSchemaObject{}, false
		}
		keys := make([]string, 0, len(o))
		for k := range o {
			keys = append(keys, k)
		}
		return declSchemaObject{
			keys: keys,
			get: func(k string) (any, bool) {
				val, ok := o[k]
				return val, ok
			},
		}, true
	default:
		return declSchemaObject{}, false
	}
}

func declStringProp(obj declSchemaObject, key string) string {
	v, ok := obj.get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func declAsSlice(v any) ([]any, bool) {
	switch a := v.(type) {
	case []any:
		if a == nil {
			return nil, false
		}
		return a, true
	case []string:
		if a == nil {
			return nil, false
		}
		out := make([]any, len(a))
		for i, s := range a {
			out[i] = s
		}
		return out, true
	default:
		return nil, false
	}
}

func declCloneWithType(obj declSchemaObject, entry any) *jsonx.Object {
	cloned := jsonx.NewObject()
	replaced := false
	for _, k := range obj.keys {
		if k == "type" {
			cloned.Set("type", entry)
			replaced = true
			continue
		}
		v, _ := obj.get(k)
		cloned.Set(k, v)
	}
	if !replaced {
		cloned.Set("type", entry)
	}
	return cloned
}

func declRenderGlobal(head string, global *CodemodeTool, indent string) string {
	desc := ""
	if global.Description != nil {
		desc = declDocComment(*global.Description, indent)
	}
	if global.Signature != nil {
		return fmt.Sprintf("%s%s%s%s;", desc, indent, head, *global.Signature)
	}
	input := "unknown"
	if global.InputSchema != nil {
		input = SchemaToType(global.InputSchema, nil)
	}
	output := "unknown"
	if global.OutputSchema != nil {
		output = SchemaToType(global.OutputSchema, nil)
	}
	return fmt.Sprintf("%s%s%s(args: %s): Promise<%s>;", desc, indent, head, input, output)
}

func declDocComment(description string, indent string) string {
	text := js.Trim(description)
	if text == "" {
		return ""
	}
	escaped := strings.ReplaceAll(text, "*/", `*\`+`/`)
	lines := declLineBreakRe.Split(escaped, -1)
	if len(lines) == 1 {
		return fmt.Sprintf("%s/** %s */\n", indent, lines[0])
	}
	var b strings.Builder
	b.WriteString(indent)
	b.WriteString("/**\n")
	for _, line := range lines {
		b.WriteString(indent)
		if line != "" {
			b.WriteString(" * ")
			b.WriteString(line)
		} else {
			b.WriteString(" *")
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent)
	b.WriteString(" */\n")
	return b.String()
}

func declPropertyKey(name string) string {
	if declIdentifier.MatchString(name) {
		return name
	}
	return declStringifyLiteral(name)
}

func declStringifyLiteral(v any) string {
	s, err := jsonx.Stringify(v)
	if err != nil || s == "" {
		return "unknown"
	}
	return s
}

func declUnion(types []string) string {
	seen := make(map[string]bool, len(types))
	var unique []string
	for _, t := range types {
		if t == "unknown" {
			return "unknown"
		}
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	if len(unique) == 0 {
		return "never"
	}
	return strings.Join(unique, " | ")
}

func declResolveRef(ref string, root CodemodeJsonSchema) (CodemodeJsonSchema, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var current any = root
	rest := ""
	if len(ref) > 2 {
		rest = ref[2:]
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" {
			continue
		}
		decoded, err := js.DecodeURIComponent(segment)
		if err != nil {
			return nil, false
		}
		key := strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
		obj, ok := declAsObject(current)
		if !ok {
			return nil, false
		}
		next, exists := obj.get(key)
		if !exists {
			return nil, false
		}
		current = next
	}
	if b, ok := current.(bool); ok {
		return b, true
	}
	if _, ok := declAsObject(current); ok {
		return current, true
	}
	return nil, false
}

func declToType(schema CodemodeJsonSchema, ctx *declSchemaContext) string {
	if b, ok := schema.(bool); ok {
		if b {
			return "unknown"
		}
		return "never"
	}
	obj, ok := declAsObject(schema)
	if !ok {
		return "unknown"
	}

	if rawRef, hasRef := obj.get("$ref"); hasRef {
		if ref, isStr := rawRef.(string); isStr {
			if ctx.resolving[ref] || ctx.expansions >= declMaxRefExpansions {
				return "unknown"
			}
			target, found := declResolveRef(ref, ctx.root)
			if !found {
				return "unknown"
			}
			ctx.expansions++
			ctx.resolving[ref] = true
			defer delete(ctx.resolving, ref)
			return declToType(target, ctx)
		}
	}

	if c, hasConst := obj.get("const"); hasConst {
		return declStringifyLiteral(c)
	}
	if rawEnum, hasEnum := obj.get("enum"); hasEnum {
		if arr, isArr := declAsSlice(rawEnum); isArr {
			vals := make([]string, len(arr))
			for i, v := range arr {
				vals[i] = declStringifyLiteral(v)
			}
			return declUnion(vals)
		}
	}

	var variants []any
	if rawAnyOf, hasAnyOf := obj.get("anyOf"); hasAnyOf {
		variants, _ = declAsSlice(rawAnyOf)
	}
	if variants == nil {
		if rawOneOf, hasOneOf := obj.get("oneOf"); hasOneOf {
			variants, _ = declAsSlice(rawOneOf)
		}
	}
	if variants != nil {
		rendered := make([]string, len(variants))
		for i, v := range variants {
			rendered[i] = declToType(v, ctx)
		}
		return declUnion(rendered)
	}

	if rawAllOf, hasAllOf := obj.get("allOf"); hasAllOf {
		if arr, isArr := declAsSlice(rawAllOf); isArr {
			var parts []string
			for _, part := range arr {
				t := declToType(part, ctx)
				if t != "unknown" {
					if strings.Contains(t, " | ") {
						t = "(" + t + ")"
					}
					parts = append(parts, t)
				}
			}
			if len(parts) == 0 {
				return "unknown"
			}
			return strings.Join(parts, " & ")
		}
	}

	rawType, hasType := obj.get("type")
	if arr, isArr := declAsSlice(rawType); hasType && isArr {
		rendered := make([]string, len(arr))
		for i, entry := range arr {
			rendered[i] = declToType(declCloneWithType(obj, entry), ctx)
		}
		return declUnion(rendered)
	}

	if !hasType || rawType == nil {
		_, hasProps := obj.get("properties")
		_, hasAdd := obj.get("additionalProperties")
		_, hasReq := obj.get("required")
		if hasProps || hasAdd || hasReq {
			return declToObjectType(obj, ctx)
		}
		_, hasItems := obj.get("items")
		_, hasPrefix := obj.get("prefixItems")
		if hasItems || hasPrefix {
			return declToArrayType(obj, ctx)
		}
		return "unknown"
	}

	typeStr, _ := rawType.(string)
	switch typeStr {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return declToArrayType(obj, ctx)
	case "object":
		return declToObjectType(obj, ctx)
	default:
		return "unknown"
	}
}

func declToArrayType(obj declSchemaObject, ctx *declSchemaContext) string {
	if rawItems, hasItems := obj.get("items"); hasItems {
		if _, isArr := declAsSlice(rawItems); !isArr {
			return fmt.Sprintf("Array<%s>", declToType(rawItems, ctx))
		}
	}
	var tuple []any
	if rawPrefix, hasPrefix := obj.get("prefixItems"); hasPrefix {
		tuple, _ = declAsSlice(rawPrefix)
	}
	if tuple == nil {
		if rawItems, hasItems := obj.get("items"); hasItems {
			tuple, _ = declAsSlice(rawItems)
		}
	}
	if len(tuple) > 0 {
		items := make([]string, len(tuple))
		for i, item := range tuple {
			items[i] = declToType(item, ctx)
		}
		return "[" + strings.Join(items, ", ") + "]"
	}
	return "unknown[]"
}

func declDescriptionOf(prop any) string {
	obj, ok := declAsObject(prop)
	if !ok {
		return ""
	}
	raw, _ := obj.get("description")
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return js.Trim(s)
}

func declToObjectType(obj declSchemaObject, ctx *declSchemaContext) string {
	var hasProps bool
	rawProps, _ := obj.get("properties")
	props, hasProps := declAsObject(rawProps)

	reqSet := make(map[string]bool)
	if rawReq, hasReq := obj.get("required"); hasReq {
		if arr, isArr := declAsSlice(rawReq); isArr {
			for _, item := range arr {
				if s, ok := item.(string); ok {
					reqSet[s] = true
				}
			}
		}
	}

	var names []string
	if hasProps {
		names = append([]string(nil), props.keys...)
		slices.SortFunc(names, js.CompareUTF16)
	}

	members := make([]string, 0, len(names)+1)
	for _, name := range names {
		propSchema, _ := props.get(name)
		opt := ""
		if !reqSet[name] {
			opt = "?"
		}
		members = append(members, fmt.Sprintf("%s%s: %s;", declPropertyKey(name), opt, declToType(propSchema, ctx)))
	}

	additional, hasAdditional := obj.get("additionalProperties")
	if hasAdditional && additional != false {
		t := "unknown"
		if additional != true {
			t = declToType(additional, ctx)
		}
		members = append(members, fmt.Sprintf("[key: string]: %s;", t))
	} else if !hasAdditional && len(names) == 0 {
		members = append(members, "[key: string]: unknown;")
	}

	if len(members) == 0 {
		return "{}"
	}

	hasDesc := false
	for _, name := range names {
		propSchema, _ := props.get(name)
		if declDescriptionOf(propSchema) != "" {
			hasDesc = true
			break
		}
	}
	if !hasDesc {
		return "{ " + strings.Join(members, " ") + " }"
	}

	lines := []string{"{"}
	for i, name := range names {
		propSchema, _ := props.get(name)
		if desc := declDescriptionOf(propSchema); desc != "" {
			for _, rawLine := range declLineBreakRe.Split(desc, -1) {
				trimmed := js.Trim(rawLine)
				if trimmed != "" {
					lines = append(lines, declIndent+"// "+trimmed)
				}
			}
		}
		lines = append(lines, declIndent+strings.ReplaceAll(members[i], "\n", "\n"+declIndent))
	}
	for _, member := range members[len(names):] {
		lines = append(lines, declIndent+member)
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

func declUTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}
