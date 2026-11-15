// Ported from packages/coding-agent/src/extensions/mcp/tools.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/mcp"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
	"github.com/keejkrej/pi-go/tui"
)

// ToToolExposure maps an MCP exposure to a registered tool exposure.
// `codemode` and `deferred` both register as `deferred`; they differ only in
// which helper tool the MCP extension activates to reach them.
func ToToolExposure(exposure McpExposure) core.ToolExposure {
	if exposure == core.McpExposureCodemode {
		return core.ToolExposureDeferred
	}
	return core.ToolExposure(exposure)
}

// toolsOutputPreviewLines is the number of visual wrapped lines shown before expansion.
const toolsOutputPreviewLines = 5

// ReadMcpResourceTool is the tool that reads resources named by resource links.
const ReadMcpResourceTool = core.ReadMcpResourceTool

// McpToolDetails records provenance and optional overflow file path for an MCP tool result.
type McpToolDetails struct {
	Server         string  `json:"server"`
	Tool           string  `json:"tool"`
	FullOutputPath *string `json:"fullOutputPath,omitzero"`
}

// SaveToTempFile saves truncated text or a binary resource to a temp file and returns its path.
func SaveToTempFile(data []byte, extension string) (string, error) {
	return utils.WriteOutputFile("pi-mcp", extension, data)
}

// McpToolCaller invokes a named tool on a connected MCP server.
type McpToolCaller interface {
	CallTool(ctx context.Context, name string, args *jsonx.Object, options *mcp.McpRequestOptions) (*mcpprotocol.CallToolResult, error)
}

func toolsTextOf(content ai.ContentList) string {
	var parts []string
	for _, block := range content {
		if tc, ok := block.(*ai.TextContent); ok && tc != nil {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func toolsContentBlockSchema() *jsonx.Object {
	props := jsonx.NewObject()
	props.Set("type", jsonx.ObjectOf("enum", []any{"text", "image", "audio", "resource_link", "resource"}))
	props.Set("text", jsonx.ObjectOf("type", "string"))
	props.Set("data", jsonx.ObjectOf("type", "string"))
	props.Set("mimeType", jsonx.ObjectOf("type", "string"))
	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"type"})
	schema.Set("additionalProperties", true)
	return schema
}

var toolsSchemaDataKeywords = map[string]bool{
	"const":    true,
	"enum":     true,
	"default":  true,
	"examples": true,
}

var toolsSchemaMapKeywords = map[string]bool{
	"properties":        true,
	"patternProperties": true,
	"dependentSchemas":  true,
	"dependencies":      true,
	"$defs":             true,
	"definitions":       true,
}

func toolsRelocateSchema(schema any, pointer string) any {
	if arr, ok := schema.([]any); ok {
		out := make([]any, len(arr))
		for i, item := range arr {
			out[i] = toolsRelocateSchema(item, pointer)
		}
		return out
	}
	obj, ok := schema.(*jsonx.Object)
	if !ok || obj == nil {
		return schema
	}
	relocated := jsonx.NewObject()
	for key, value := range obj.All() {
		if key == "$ref" {
			if ref, ok := value.(string); ok && (ref == "#" || strings.HasPrefix(ref, "#/")) {
				relocated.Set(key, pointer+ref[1:])
				continue
			}
		}
		if toolsSchemaDataKeywords[key] {
			relocated.Set(key, value)
			continue
		}
		if toolsSchemaMapKeywords[key] {
			if subMap, ok := value.(*jsonx.Object); ok && subMap != nil {
				mapped := jsonx.NewObject()
				for name, subschema := range subMap.All() {
					mapped.Set(name, toolsRelocateSchema(subschema, pointer))
				}
				relocated.Set(key, mapped)
				continue
			}
		}
		relocated.Set(key, toolsRelocateSchema(value, pointer))
	}
	return relocated
}

// CreateMcpResultSchema builds the output schema of an MCP tool, matching the CallToolResult
// shape exposed to codemode scripts (without `_meta`).
func CreateMcpResultSchema(structuredContentSchema *jsonx.Object) *typebox.Schema {
	var structured any = jsonx.NewObject()
	if structuredContentSchema != nil {
		structured = toolsRelocateSchema(structuredContentSchema, "#/properties/structuredContent")
	}
	contentProp := jsonx.NewObject()
	contentProp.Set("type", "array")
	contentProp.Set("items", toolsContentBlockSchema())

	props := jsonx.NewObject()
	props.Set("content", contentProp)
	props.Set("structuredContent", structured)
	props.Set("isError", jsonx.ObjectOf("type", "boolean"))

	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"content"})
	schema.Set("additionalProperties", true)
	return typebox.Unsafe(schema)
}

// ConvertMcpResultOptions configures ConvertMcpResult and ToModelContent.
type ConvertMcpResultOptions struct {
	SaveOutput        mcp.McpOutputSaver
	ReadableResources bool
}

var toolsExtensionRegex = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)

func toolsExtensionOf(uri string) string {
	path := uri
	if parsed, err := url.Parse(uri); err == nil && parsed.Scheme != "" {
		path = parsed.Path
	}
	if match := toolsExtensionRegex.FindString(path); match != "" {
		return match
	}
	return ".bin"
}

func toolsIsTextMimeType(mimeType *string) bool {
	if mimeType == nil || *mimeType == "" {
		return false
	}
	raw := *mimeType
	if idx := strings.IndexByte(raw, ';'); idx >= 0 {
		raw = raw[:idx]
	}
	typ := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(typ, "text/") ||
		typ == "application/json" ||
		strings.HasSuffix(typ, "+json") ||
		strings.HasSuffix(typ, "+xml")
}

func toolsDecodeBase64(raw string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\r', '\n':
			return -1
		default:
			return r
		}
	}, raw)
	cleaned = strings.TrimRight(cleaned, "=")
	return base64.RawStdEncoding.DecodeString(cleaned)
}

func toolsFromLlmContent(items []mcpprotocol.LlmContent) ai.ContentList {
	out := make(ai.ContentList, 0, len(items))
	for _, item := range items {
		switch b := item.(type) {
		case *mcpprotocol.LlmTextContent:
			if b != nil {
				out = append(out, &ai.TextContent{Type: "text", Text: b.Text})
			}
		case *mcpprotocol.LlmImageContent:
			if b != nil {
				out = append(out, &ai.ImageContent{Type: "image", Data: b.Data, MimeType: b.MimeType})
			}
		case *mcpprotocol.UnknownLlmContent:
			if b != nil && b.Raw != nil {
				if encoded, err := jsonx.Stringify(b.Raw); err == nil {
					out = append(out, &ai.TextContent{Type: "text", Text: encoded})
				}
			}
		}
	}
	return out
}

func toolsToLlmContent(items ai.ContentList) []mcpprotocol.LlmContent {
	out := make([]mcpprotocol.LlmContent, 0, len(items))
	for _, item := range items {
		switch b := item.(type) {
		case *ai.TextContent:
			if b != nil {
				out = append(out, &mcpprotocol.LlmTextContent{Type: "text", Text: b.Text})
			}
		case *ai.ImageContent:
			if b != nil {
				out = append(out, &mcpprotocol.LlmImageContent{Type: "image", Data: b.Data, MimeType: b.MimeType})
			}
		}
	}
	return out
}

func toolsDefaultSaveOutput(saveOutput mcp.McpOutputSaver) mcp.McpOutputSaver {
	if saveOutput != nil {
		return saveOutput
	}
	return func(_ context.Context, data []byte, extension string) (string, error) {
		return SaveToTempFile(data, extension)
	}
}

func toolsBlockToContent(ctx context.Context, server string, block mcpprotocol.ContentBlock, options ConvertMcpResultOptions) ai.ContentList {
	if link, ok := block.(*mcpprotocol.ResourceLinkContent); ok && link != nil {
		var details []string
		if link.MimeType != nil && *link.MimeType != "" {
			details = append(details, *link.MimeType)
		}
		if link.Size != nil {
			details = append(details, core.FormatSize(*link.Size))
		}
		readHint := ""
		if options.ReadableResources {
			readHint = fmt.Sprintf(". Read it with %s (server %q)", ReadMcpResourceTool, server)
		}
		description := ""
		if link.Description != nil && *link.Description != "" {
			description = ": " + *link.Description
		}
		title := link.Name
		if link.Title != nil && *link.Title != "" {
			title = *link.Title
		}
		detailsText := ""
		if len(details) > 0 {
			detailsText = " (" + strings.Join(details, ", ") + ")"
		}
		return ai.ContentList{
			&ai.TextContent{
				Type: "text",
				Text: fmt.Sprintf("[Resource %s %q%s%s%s]", link.Uri, title, detailsText, description, readHint),
			},
		}
	}
	if emb, ok := block.(*mcpprotocol.EmbeddedResourceContent); ok && emb != nil {
		if blobRes, ok := emb.Resource.(*mcpprotocol.BlobResourceContents); ok && blobRes != nil {
			isImg := blobRes.MimeType != nil && strings.HasPrefix(*blobRes.MimeType, "image/")
			if !isImg {
				data, err := toolsDecodeBase64(blobRes.Blob)
				if err == nil && toolsIsTextMimeType(blobRes.MimeType) {
					return ai.ContentList{&ai.TextContent{Type: "text", Text: string(data)}}
				}
				mimeLabel := "unknown type"
				if blobRes.MimeType != nil && *blobRes.MimeType != "" {
					mimeLabel = *blobRes.MimeType
				}
				kind := fmt.Sprintf("%s, %s", mimeLabel, core.FormatSize(len(data)))
				saveOutput := toolsDefaultSaveOutput(options.SaveOutput)
				if err != nil {
					return ai.ContentList{
						&ai.TextContent{
							Type: "text",
							Text: fmt.Sprintf("[Binary resource %s (%s) could not be saved: %s]", blobRes.Uri, kind, err.Error()),
						},
					}
				}
				savedPath, saveErr := saveOutput(ctx, data, toolsExtensionOf(blobRes.Uri))
				if saveErr != nil {
					return ai.ContentList{
						&ai.TextContent{
							Type: "text",
							Text: fmt.Sprintf("[Binary resource %s (%s) could not be saved: %s]", blobRes.Uri, kind, saveErr.Error()),
						},
					}
				}
				return ai.ContentList{
					&ai.TextContent{
						Type: "text",
						Text: fmt.Sprintf("[Binary resource %s (%s) saved to %s]", blobRes.Uri, kind, savedPath),
					},
				}
			}
		}
	}
	return toolsFromLlmContent(mcpprotocol.ToLlmContent(&mcpprotocol.CallToolResult{
		Content: mcpprotocol.ContentBlockList{block},
	}))
}

// ToModelContent converts MCP content blocks into model-facing text and image content before size limiting.
func ToModelContent(ctx context.Context, server string, blocks []mcpprotocol.ContentBlock, options ...ConvertMcpResultOptions) ai.ContentList {
	var opts ConvertMcpResultOptions
	if len(options) > 0 {
		opts = options[0]
	}
	var out ai.ContentList
	for _, block := range blocks {
		out = append(out, toolsBlockToContent(ctx, server, block, opts)...)
	}
	return out
}

// ConvertMcpResult converts a CallToolResult to an AgentToolResult while preserving
// an untruncated structured result (without `_meta`) for codemode scripts.
func ConvertMcpResult(
	ctx context.Context,
	server string,
	tool string,
	result *mcpprotocol.CallToolResult,
	options ...ConvertMcpResultOptions,
) agent.AgentToolResult {
	var opts ConvertMcpResultOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if result == nil {
		result = &mcpprotocol.CallToolResult{}
	}

	var converted ai.ContentList
	if len(result.Content) > 0 {
		converted = ToModelContent(ctx, server, result.Content, opts)
	} else {
		converted = toolsFromLlmContent(mcpprotocol.ToLlmContent(result))
	}
	if result.IsError != nil && *result.IsError && toolsTextOf(converted) == "" {
		converted = append(converted, &ai.TextContent{
			Type: "text",
			Text: fmt.Sprintf("MCP tool %s/%s returned an error", server, tool),
		})
	}

	saveOutput := toolsDefaultSaveOutput(opts.SaveOutput)
	limited := mcp.LimitMcpContent(ctx, toolsToLlmContent(converted), saveOutput)

	scriptResult := jsonx.NewObject()
	contentList := result.Content
	if contentList == nil {
		contentList = mcpprotocol.ContentBlockList{}
	}
	scriptResult.Set("content", contentList)
	if !result.StructuredContent.IsZero() {
		sc, _ := result.StructuredContent.Get()
		scriptResult.Set("structuredContent", sc)
	}
	if result.IsError != nil {
		scriptResult.Set("isError", *result.IsError)
	}

	out := agent.AgentToolResult{
		Content: toolsFromLlmContent(limited.Content),
		Details: McpToolDetails{
			Server:         server,
			Tool:           tool,
			FullOutputPath: limited.FullOutputPath,
		},
		StructuredContent: scriptResult,
	}
	if result.IsError != nil && *result.IsError {
		isErr := true
		out.IsError = &isErr
	}
	return out
}

func toolsToToolAnnotations(tool mcpprotocol.Tool) *core.ToolAnnotations {
	if tool.Annotations == nil {
		return nil
	}
	ann := &core.ToolAnnotations{
		ReadOnlyHint:    tool.Annotations.ReadOnlyHint,
		DestructiveHint: tool.Annotations.DestructiveHint,
		IdempotentHint:  tool.Annotations.IdempotentHint,
		OpenWorldHint:   tool.Annotations.OpenWorldHint,
	}
	if ann.ReadOnlyHint == nil && ann.DestructiveHint == nil && ann.IdempotentHint == nil && ann.OpenWorldHint == nil {
		return nil
	}
	return ann
}

// CreateMcpToolDefinitionOptions configures CreateMcpToolDefinition.
type CreateMcpToolDefinitionOptions struct {
	Server            string
	Tool              mcpprotocol.Tool
	Name              string
	Exposure          McpExposure
	Namespace         core.ToolNamespace
	TimeoutMs         int64
	GetClient         func(ctx context.Context) (McpToolCaller, error)
	ReadableResources func() bool
}

// CreateMcpToolDefinition adapts an MCP server tool to a pi ToolDefinition.
func CreateMcpToolDefinition(options CreateMcpToolDefinitionOptions) core.ToolDefinition {
	server := options.Server
	tool := options.Tool
	var title string
	if tool.Title != nil && *tool.Title != "" {
		title = *tool.Title
	} else if tool.Annotations != nil && tool.Annotations.Title != nil && *tool.Annotations.Title != "" {
		title = *tool.Annotations.Title
	}
	description := ""
	if tool.Description != nil {
		description = strings.TrimSpace(*tool.Description)
	}
	if description == "" {
		description = title
	}
	if description == "" {
		description = fmt.Sprintf("MCP tool %s from server %s", tool.Name, server)
	}

	label := server + "/" + tool.Name
	ns := options.Namespace
	renderers := CreateMcpToolRenderers(label)
	annotations := toolsToToolAnnotations(tool)

	return core.ToolDefinition{
		Name:         options.Name,
		Label:        label,
		Description:  description,
		Parameters:   typebox.Unsafe(mcp.NormalizeMcpInputSchema(tool.InputSchema)),
		OutputSchema: CreateMcpResultSchema(tool.OutputSchema),
		Exposure:     ToToolExposure(options.Exposure),
		Namespace:    &ns,
		Annotations:  annotations,
		RenderCall:   renderers.RenderCall,
		RenderResult: renderers.RenderResult,
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			onUpdate agent.AgentToolUpdateCallback,
			_ core.ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			client, err := options.GetClient(ctx)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			callArgs := params
			if callArgs == nil {
				callArgs = jsonx.NewObject()
			}
			timeoutMs := options.TimeoutMs
			reqOpts := &mcp.McpRequestOptions{
				TimeoutMs: &timeoutMs,
				OnProgress: func(progress *mcpprotocol.ProgressNotification) {
					if onUpdate == nil || progress == nil {
						return
					}
					total := ""
					if progress.Total != nil {
						total = "/" + strconv.FormatFloat(*progress.Total, 'f', -1, 64)
					}
					text := fmt.Sprintf("Progress %s%s", strconv.FormatFloat(progress.Progress, 'f', -1, 64), total)
					if progress.Message != nil {
						text = *progress.Message
					}
					onUpdate(agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
						Details: McpToolDetails{Server: server, Tool: tool.Name},
					})
				},
			}
			res, err := client.CallTool(ctx, tool.Name, callArgs, reqOpts)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			readable := false
			if options.ReadableResources != nil {
				readable = options.ReadableResources()
			}
			return ConvertMcpResult(ctx, server, tool.Name, res, ConvertMcpResultOptions{
				ReadableResources: readable,
			}), nil
		},
	}
}

// CreateMcpToolRenderers returns renderers for calls to an MCP tool labeled `server/tool`.
func CreateMcpToolRenderers(label string) core.ToolRenderers {
	return core.ToolRenderers{
		RenderCall: func(args *jsonx.Object, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
			comp, ok := rctx.LastComponent.(*tui.Text)
			if !ok || comp == nil {
				comp = tui.NewText("", 0, 0, nil)
			}
			var callArgs any = args
			if args == nil {
				callArgs = jsonx.NewObject()
			}
			comp.SetText(core.FormatToolCallWithArgs(label, callArgs, th, rctx.Expanded))
			return comp
		},
		RenderResult: func(result agent.AgentToolResult, options core.ToolRenderResultOptions, th *theme.Theme, rctx core.ToolRenderContext) tui.Component {
			comp, ok := rctx.LastComponent.(*tui.Container)
			if !ok || comp == nil {
				comp = tui.NewContainer()
			}
			comp.Clear()
			output := strings.TrimSpace(core.GetTextOutput(&result, rctx.ShowImages))
			if output == "" {
				return comp
			}
			color := theme.FgToolOutput
			if rctx.IsError {
				color = theme.FgError
			}
			rawLines := strings.Split(core.ReplaceTabs(output), "\n")
			styledLines := make([]string, len(rawLines))
			for i, line := range rawLines {
				styledLines[i] = th.Fg(color, line)
			}
			styled := strings.Join(styledLines, "\n")
			comp.AddChild(tui.NewSpacer(1))
			if options.Expanded {
				comp.AddChild(tui.NewText(styled, 0, 0, nil))
			} else {
				comp.AddChild(theme.NewVisualLinePreview(theme.VisualLinePreviewOptions{
					Text:           styled,
					MaxVisualLines: toolsOutputPreviewLines,
					Keep:           "start",
					FormatHint: func(hidden int) string {
						return fmt.Sprintf(
							"%s %s%s",
							th.Fg(theme.FgMuted, fmt.Sprintf("... (%d more lines,", hidden)),
							theme.AppKeyHint(th, core.KeybindingAppToolsExpand, "to expand"),
							th.Fg(theme.FgMuted, ")"),
						)
					},
				}))
				if details, ok := result.Details.(McpToolDetails); ok && details.FullOutputPath != nil && *details.FullOutputPath != "" {
					comp.AddChild(tui.NewText(th.Fg(theme.FgMuted, "Full output: "+*details.FullOutputPath), 0, 0, nil))
				}
			}
			return comp
		},
	}
}
