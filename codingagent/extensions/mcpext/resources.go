// Ported from packages/coding-agent/src/extensions/mcp/resources.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/mcp"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

const (
	ListMcpResourcesTool         = core.ListMcpResourcesTool
	ListMcpResourceTemplatesTool = core.ListMcpResourceTemplatesTool
)

// McpResourceServer represents a connected MCP server that exposes resources.
type McpResourceServer interface {
	Name() string
	TimeoutMs() int64
	ResourcesPage(ctx context.Context, cursor *string, options *mcp.McpRequestOptions) (*mcpprotocol.ListResourcesResult, error)
	ResourceTemplatesPage(ctx context.Context, cursor *string, options *mcp.McpRequestOptions) (*mcpprotocol.ListResourceTemplatesResult, error)
	AllResources(ctx context.Context, options *mcp.McpRequestOptions) ([]mcpprotocol.Resource, error)
	AllResourceTemplates(ctx context.Context, options *mcp.McpRequestOptions) ([]mcpprotocol.ResourceTemplate, error)
	ReadResource(ctx context.Context, uri string, options *mcp.McpRequestOptions) (*mcpprotocol.ReadResourceResult, error)
}

var resoMcpAppMimePattern = regexp.MustCompile(`(?i);\s*profile\s*=\s*"?mcp-app"?`)

func resoIsMcpAppResource(uri string, mimeType *string) bool {
	if strings.HasPrefix(uri, "ui://") {
		return true
	}
	if mimeType == nil {
		return false
	}
	return resoMcpAppMimePattern.MatchString(*mimeType)
}

func resoListed(server string, item any) *jsonx.Object {
	out := jsonx.NewObject()
	out.Set("server", server)
	encoded, err := jsonx.Stringify(item)
	if err != nil {
		return out
	}
	parsed, err := jsonx.ParseString(encoded)
	if err != nil {
		return out
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return out
	}
	for k, v := range obj.All() {
		if k == "_meta" || k == "icons" {
			continue
		}
		out.Set(k, v)
	}
	return out
}

func resoStringProp(description string) *jsonx.Object {
	return jsonx.ObjectOf("type", "string", "description", description)
}

func resoListParameters() *typebox.Schema {
	props := jsonx.NewObject()
	props.Set("server", resoStringProp("MCP server name. Omit to list every server with resources."))
	props.Set("cursor", resoStringProp("Opaque cursor from a previous call with the same server; omit for the first page."))
	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("additionalProperties", false)
	return typebox.Unsafe(schema)
}

func resoReadParameters() *typebox.Schema {
	props := jsonx.NewObject()
	props.Set("server", resoStringProp("MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."))
	props.Set("uri", resoStringProp("Resource URI to read. Must be one of the URIs returned by list_mcp_resources."))
	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"server", "uri"})
	schema.Set("additionalProperties", false)
	return typebox.Unsafe(schema)
}

func resoListingErrorsSchema() *jsonx.Object {
	itemProps := jsonx.NewObject()
	itemProps.Set("server", jsonx.ObjectOf("type", "string"))
	itemProps.Set("error", jsonx.ObjectOf("type", "string"))
	itemSchema := jsonx.NewObject()
	itemSchema.Set("type", "object")
	itemSchema.Set("properties", itemProps)
	itemSchema.Set("required", []any{"server", "error"})

	schema := jsonx.NewObject()
	schema.Set("type", "array")
	schema.Set("description", "Servers that could not be listed")
	schema.Set("items", itemSchema)
	return schema
}

func resoListOutputSchema() *typebox.Schema {
	optString := jsonx.ObjectOf("type", "string")

	itemProps := jsonx.NewObject()
	itemProps.Set("server", jsonx.ObjectOf("type", "string"))
	itemProps.Set("uri", jsonx.ObjectOf("type", "string"))
	itemProps.Set("name", jsonx.ObjectOf("type", "string"))
	itemProps.Set("title", optString)
	itemProps.Set("description", optString)
	itemProps.Set("mimeType", optString)
	itemProps.Set("size", jsonx.ObjectOf("type", "number"))
	itemSchema := jsonx.NewObject()
	itemSchema.Set("type", "object")
	itemSchema.Set("properties", itemProps)
	itemSchema.Set("required", []any{"server", "uri", "name"})

	props := jsonx.NewObject()
	props.Set("server", optString)
	props.Set("resources", jsonx.ObjectOf("type", "array", "items", itemSchema))
	props.Set("nextCursor", optString)
	props.Set("errors", resoListingErrorsSchema())

	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"resources"})
	return typebox.Unsafe(schema)
}

func resoListTemplatesOutputSchema() *typebox.Schema {
	optString := jsonx.ObjectOf("type", "string")

	itemProps := jsonx.NewObject()
	itemProps.Set("server", jsonx.ObjectOf("type", "string"))
	itemProps.Set("uriTemplate", jsonx.ObjectOf("type", "string", "description", "RFC 6570 URI template"))
	itemProps.Set("name", jsonx.ObjectOf("type", "string"))
	itemProps.Set("title", optString)
	itemProps.Set("description", optString)
	itemProps.Set("mimeType", optString)
	itemSchema := jsonx.NewObject()
	itemSchema.Set("type", "object")
	itemSchema.Set("properties", itemProps)
	itemSchema.Set("required", []any{"server", "uriTemplate", "name"})

	props := jsonx.NewObject()
	props.Set("server", optString)
	props.Set("resourceTemplates", jsonx.ObjectOf("type", "array", "items", itemSchema))
	props.Set("nextCursor", optString)
	props.Set("errors", resoListingErrorsSchema())

	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"resourceTemplates"})
	return typebox.Unsafe(schema)
}

func resoReadOutputSchema() *typebox.Schema {
	optString := jsonx.ObjectOf("type", "string")

	textProps := jsonx.NewObject()
	textProps.Set("uri", jsonx.ObjectOf("type", "string"))
	textProps.Set("mimeType", optString)
	textProps.Set("text", jsonx.ObjectOf("type", "string"))
	textSchema := jsonx.NewObject()
	textSchema.Set("type", "object")
	textSchema.Set("properties", textProps)
	textSchema.Set("required", []any{"uri", "text"})

	blobProps := jsonx.NewObject()
	blobProps.Set("uri", jsonx.ObjectOf("type", "string"))
	blobProps.Set("mimeType", optString)
	blobProps.Set("blob", jsonx.ObjectOf("type", "string", "description", "base64"))
	blobSchema := jsonx.NewObject()
	blobSchema.Set("type", "object")
	blobSchema.Set("properties", blobProps)
	blobSchema.Set("required", []any{"uri", "blob"})

	contentsSchema := jsonx.NewObject()
	contentsSchema.Set("type", "array")
	contentsSchema.Set("items", jsonx.ObjectOf("anyOf", []any{textSchema, blobSchema}))

	props := jsonx.NewObject()
	props.Set("server", jsonx.ObjectOf("type", "string"))
	props.Set("uri", jsonx.ObjectOf("type", "string"))
	props.Set("contents", contentsSchema)

	schema := jsonx.NewObject()
	schema.Set("type", "object")
	schema.Set("properties", props)
	schema.Set("required", []any{"server", "uri", "contents"})
	return typebox.Unsafe(schema)
}

func resoStringArgument(params *jsonx.Object, key string) (*string, error) {
	if params == nil {
		return nil, nil
	}
	val, ok := params.Get(key)
	if !ok || val == nil {
		return nil, nil
	}
	s, ok := val.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a string", key)
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, nil
	}
	return &trimmed, nil
}

func resoJsonResult(ctx context.Context, tool string, server *string, payload *jsonx.Object) agent.AgentToolResult {
	encoded, err := jsonx.Stringify(payload)
	if err != nil {
		encoded = "{}"
	}
	limited := mcp.LimitMcpContent(
		ctx,
		[]mcpprotocol.LlmContent{&mcpprotocol.LlmTextContent{Type: "text", Text: encoded}},
		func(_ context.Context, data []byte, ext string) (string, error) {
			return SaveToTempFile(data, ext)
		},
	)
	serverName := ""
	if server != nil {
		serverName = *server
	}
	return agent.AgentToolResult{
		Content: toolsFromLlmContent(limited.Content),
		Details: McpToolDetails{
			Server:         serverName,
			Tool:           tool,
			FullOutputPath: limited.FullOutputPath,
		},
		StructuredContent: payload,
	}
}

// CreateMcpResourceToolDefinitionsOptions configures CreateMcpResourceToolDefinitions.
type CreateMcpResourceToolDefinitionsOptions struct {
	Exposure McpExposure
	Servers  func() []McpResourceServer
}

type resoServerError struct {
	server string
	err    error
}

// CreateMcpResourceToolDefinitions returns the `list_mcp_resources`, `list_mcp_resource_templates`,
// and `read_mcp_resource` tool definitions.
func CreateMcpResourceToolDefinitions(options CreateMcpResourceToolDefinitionsOptions) []core.ToolDefinition {
	boolTrue := true
	readOnly := &core.ToolAnnotations{ReadOnlyHint: &boolTrue}
	exposure := ToToolExposure(options.Exposure)

	findServer := func(name string) (McpResourceServer, error) {
		var servers []McpResourceServer
		if options.Servers != nil {
			servers = options.Servers()
		}
		for _, candidate := range servers {
			if candidate != nil && candidate.Name() == name {
				return candidate, nil
			}
		}
		names := make([]string, 0, len(servers))
		for _, candidate := range servers {
			if candidate != nil {
				names = append(names, candidate.Name())
			}
		}
		available := strings.Join(names, ", ")
		hint := ""
		if available != "" {
			hint = ". Servers with resources: " + available
		}
		return nil, fmt.Errorf("MCP server %q has no resources%s", name, hint)
	}

	listResources := core.ToolDefinition{
		Name:         ListMcpResourcesTool,
		Label:        ListMcpResourcesTool,
		Description:  "Lists resources provided by MCP servers. Resources allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resources over web search when possible.",
		Parameters:   resoListParameters(),
		OutputSchema: resoListOutputSchema(),
		Exposure:     exposure,
		Annotations:  readOnly,
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			_ core.ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			serverName, err := resoStringArgument(params, "server")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			cursor, err := resoStringArgument(params, "cursor")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if serverName != nil {
				server, err := findServer(*serverName)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				timeoutMs := server.TimeoutMs()
				res, err := server.ResourcesPage(ctx, cursor, &mcp.McpRequestOptions{TimeoutMs: &timeoutMs})
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				items := make([]any, 0, len(res.Resources))
				for _, r := range res.Resources {
					if resoIsMcpAppResource(r.Uri, r.MimeType) {
						continue
					}
					items = append(items, resoListed(server.Name(), r))
				}
				payload := jsonx.NewObject()
				payload.Set("server", server.Name())
				payload.Set("resources", items)
				if res.NextCursor != nil {
					payload.Set("nextCursor", *res.NextCursor)
				}
				return resoJsonResult(ctx, ListMcpResourcesTool, serverName, payload), nil
			}
			if cursor != nil {
				return agent.AgentToolResult{}, errors.New("cursor can only be used when a server is specified")
			}

			var servers []McpResourceServer
			if options.Servers != nil {
				servers = append(servers, options.Servers()...)
			}
			sort.Slice(servers, func(i, j int) bool {
				return servers[i].Name() < servers[j].Name()
			})

			type serverRes struct {
				items []mcpprotocol.Resource
				err   error
			}
			results := make([]serverRes, len(servers))
			var wg sync.WaitGroup
			for i, srv := range servers {
				wg.Add(1)
				go func(idx int, s McpResourceServer) {
					defer wg.Done()
					timeoutMs := s.TimeoutMs()
					all, err := s.AllResources(ctx, &mcp.McpRequestOptions{TimeoutMs: &timeoutMs})
					results[idx] = serverRes{items: all, err: err}
				}(i, srv)
			}
			wg.Wait()

			items := make([]any, 0)
			errs := make([]any, 0)
			for i, srv := range servers {
				res := results[i]
				if res.err != nil {
					errs = append(errs, jsonx.ObjectOf("server", srv.Name(), "error", res.err.Error()))
					continue
				}
				for _, r := range res.items {
					if resoIsMcpAppResource(r.Uri, r.MimeType) {
						continue
					}
					items = append(items, resoListed(srv.Name(), r))
				}
			}
			payload := jsonx.NewObject()
			payload.Set("resources", items)
			if len(errs) > 0 {
				payload.Set("errors", errs)
			}
			return resoJsonResult(ctx, ListMcpResourcesTool, nil, payload), nil
		},
	}

	listTemplates := core.ToolDefinition{
		Name:         ListMcpResourceTemplatesTool,
		Label:        ListMcpResourceTemplatesTool,
		Description:  "Lists resource templates provided by MCP servers. Parameterized resource templates allow servers to share data that takes parameters and provides context to language models, such as files, database schemas, or application-specific information. Prefer resource templates over web search when possible.",
		Parameters:   resoListParameters(),
		OutputSchema: resoListTemplatesOutputSchema(),
		Exposure:     exposure,
		Annotations:  readOnly,
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			_ core.ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			serverName, err := resoStringArgument(params, "server")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			cursor, err := resoStringArgument(params, "cursor")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if serverName != nil {
				server, err := findServer(*serverName)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				timeoutMs := server.TimeoutMs()
				res, err := server.ResourceTemplatesPage(ctx, cursor, &mcp.McpRequestOptions{TimeoutMs: &timeoutMs})
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				items := make([]any, 0, len(res.ResourceTemplates))
				for _, r := range res.ResourceTemplates {
					if resoIsMcpAppResource(r.UriTemplate, r.MimeType) {
						continue
					}
					items = append(items, resoListed(server.Name(), r))
				}
				payload := jsonx.NewObject()
				payload.Set("server", server.Name())
				payload.Set("resourceTemplates", items)
				if res.NextCursor != nil {
					payload.Set("nextCursor", *res.NextCursor)
				}
				return resoJsonResult(ctx, ListMcpResourceTemplatesTool, serverName, payload), nil
			}
			if cursor != nil {
				return agent.AgentToolResult{}, errors.New("cursor can only be used when a server is specified")
			}

			var servers []McpResourceServer
			if options.Servers != nil {
				servers = append(servers, options.Servers()...)
			}
			sort.Slice(servers, func(i, j int) bool {
				return servers[i].Name() < servers[j].Name()
			})

			type serverRes struct {
				items []mcpprotocol.ResourceTemplate
				err   error
			}
			results := make([]serverRes, len(servers))
			var wg sync.WaitGroup
			for i, srv := range servers {
				wg.Add(1)
				go func(idx int, s McpResourceServer) {
					defer wg.Done()
					timeoutMs := s.TimeoutMs()
					all, err := s.AllResourceTemplates(ctx, &mcp.McpRequestOptions{TimeoutMs: &timeoutMs})
					results[idx] = serverRes{items: all, err: err}
				}(i, srv)
			}
			wg.Wait()

			items := make([]any, 0)
			errs := make([]any, 0)
			for i, srv := range servers {
				res := results[i]
				if res.err != nil {
					errs = append(errs, jsonx.ObjectOf("server", srv.Name(), "error", res.err.Error()))
					continue
				}
				for _, r := range res.items {
					if resoIsMcpAppResource(r.UriTemplate, r.MimeType) {
						continue
					}
					items = append(items, resoListed(srv.Name(), r))
				}
			}
			payload := jsonx.NewObject()
			payload.Set("resourceTemplates", items)
			if len(errs) > 0 {
				payload.Set("errors", errs)
			}
			return resoJsonResult(ctx, ListMcpResourceTemplatesTool, nil, payload), nil
		},
	}

	readResource := core.ToolDefinition{
		Name:         ReadMcpResourceTool,
		Label:        ReadMcpResourceTool,
		Description:  "Read a specific resource from an MCP server given the server name and resource URI.",
		Parameters:   resoReadParameters(),
		OutputSchema: resoReadOutputSchema(),
		Exposure:     exposure,
		Annotations:  readOnly,
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			_ core.ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			serverName, err := resoStringArgument(params, "server")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			uri, err := resoStringArgument(params, "uri")
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			if serverName == nil {
				return agent.AgentToolResult{}, errors.New("server must be provided")
			}
			if uri == nil {
				return agent.AgentToolResult{}, errors.New("uri must be provided")
			}
			server, err := findServer(*serverName)
			if err != nil {
				return agent.AgentToolResult{}, err
			}
			timeoutMs := server.TimeoutMs()
			res, err := server.ReadResource(ctx, *uri, &mcp.McpRequestOptions{TimeoutMs: &timeoutMs})
			if err != nil {
				return agent.AgentToolResult{}, err
			}

			var blocks []mcpprotocol.ContentBlock
			contentsOut := make([]any, 0, len(res.Contents))
			for _, contentItem := range res.Contents {
				var itemUri string
				switch c := contentItem.(type) {
				case *mcpprotocol.TextResourceContents:
					itemUri = c.Uri
					stripped := jsonx.ObjectOf("uri", c.Uri, "text", c.Text)
					if c.MimeType != nil {
						stripped.Set("mimeType", *c.MimeType)
					}
					contentsOut = append(contentsOut, stripped)
				case *mcpprotocol.BlobResourceContents:
					itemUri = c.Uri
					stripped := jsonx.ObjectOf("uri", c.Uri, "blob", c.Blob)
					if c.MimeType != nil {
						stripped.Set("mimeType", *c.MimeType)
					}
					contentsOut = append(contentsOut, stripped)
				case *mcpprotocol.UnknownResourceContents:
					if c != nil && c.Raw != nil {
						if u, ok := c.Raw.GetString("uri"); ok {
							itemUri = u
						}
						stripped := jsonx.NewObject()
						for k, v := range c.Raw.All() {
							if k == "_meta" {
								continue
							}
							stripped.Set(k, v)
						}
						contentsOut = append(contentsOut, stripped)
					}
				}
				if len(res.Contents) > 1 && itemUri != "" {
					blocks = append(blocks, &mcpprotocol.TextContent{Type: "text", Text: itemUri + ":"})
				}
				blocks = append(blocks, &mcpprotocol.EmbeddedResourceContent{
					Type:     "resource",
					Resource: contentItem,
				})
			}

			converted := ToModelContent(ctx, server.Name(), blocks)
			if len(converted) == 0 {
				converted = ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("Resource %s is empty.", *uri)}}
			}
			limited := mcp.LimitMcpContent(
				ctx,
				toolsToLlmContent(converted),
				func(_ context.Context, data []byte, ext string) (string, error) {
					return SaveToTempFile(data, ext)
				},
			)
			structured := jsonx.NewObject()
			structured.Set("server", server.Name())
			structured.Set("uri", *uri)
			structured.Set("contents", contentsOut)

			return agent.AgentToolResult{
				Content: toolsFromLlmContent(limited.Content),
				Details: McpToolDetails{
					Server:         server.Name(),
					Tool:           ReadMcpResourceTool,
					FullOutputPath: limited.FullOutputPath,
				},
				StructuredContent: structured,
			}, nil
		},
	}

	return []core.ToolDefinition{listResources, listTemplates, readResource}
}
