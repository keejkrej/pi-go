package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/compat"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/mcp"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

func tmStrPtr(s string) *string { return &s }

type tmInMemoryMCPPair struct {
	client *mcp.McpClient
	server *mcp.InMemoryTransport
}

func tmStartInMemoryMCP(t *testing.T) *tmInMemoryMCPPair {
	t.Helper()
	pair := mcp.CreateInMemoryTransportPair()
	pair.Server.OnMessage(func(msg protocol.JsonRpcMessage) {
		req, isReq := msg.(*protocol.JsonRpcRequest)
		if !isReq || req == nil {
			return
		}
		go func() {
			var res any
			var rpcErr *protocol.McpError
			switch req.Method {
			case "initialize":
				res = jsonx.ObjectOf(
					"protocolVersion", protocol.LatestProtocolVersion,
					"capabilities", jsonx.ObjectOf("tools", jsonx.ObjectOf("listChanged", true)),
					"serverInfo", jsonx.ObjectOf("name", "mock-src-mcp", "version", "1.0.0"),
				)
			case "tools/list":
				res = jsonx.ObjectOf(
					"tools", []any{
						jsonx.ObjectOf(
							"name", "count_ts",
							"description", "Count TypeScript files in src/ and list largest files via MCP",
							"inputSchema", jsonx.ObjectOf(
								"type", "object",
								"properties", jsonx.ObjectOf("dir", jsonx.ObjectOf("type", "string")),
							),
						),
						jsonx.ObjectOf(
							"name", "read_meta",
							"description", "Deferred MCP metadata lookup tool",
							"inputSchema", jsonx.ObjectOf(
								"type", "object",
								"properties", jsonx.ObjectOf("file", jsonx.ObjectOf("type", "string")),
							),
						),
					},
				)
			case "tools/call":
				var name string
				if obj, ok := req.Params.(*jsonx.Object); ok && obj != nil {
					if n, ok := obj.Get("name"); ok {
						name, _ = n.(string)
					}
				}
				switch name {
				case "count_ts":
					res = jsonx.ObjectOf(
						"content", []any{jsonx.ObjectOf("type", "text", "text", "ts_count=42; top3=[a.ts,b.ts,c.ts]")},
					)
				case "read_meta":
					res = jsonx.ObjectOf(
						"content", []any{jsonx.ObjectOf("type", "text", "text", "meta:a.ts=900bytes")},
					)
				default:
					rpcErr = protocol.NewMcpError(-32601, "unknown tool "+name, nil)
				}
			default:
				res = jsonx.ObjectOf()
			}
			if rpcErr != nil {
				_ = pair.Server.Send(&protocol.JsonRpcErrorResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Error:   protocol.JsonRpcErrorObject{Code: rpcErr.Code, Message: rpcErr.Message, Data: rpcErr.Data},
				})
				return
			}
			_ = pair.Server.Send(&protocol.JsonRpcSuccessResponse{
				Jsonrpc: "2.0",
				Id:      req.Id,
				Result:  res,
			})
		}()
	})
	if err := pair.Server.Start(); err != nil {
		t.Fatalf("pair.Server.Start failed: %v", err)
	}
	client := mcp.NewMcpClient(&mcp.McpClientOptions{})
	if _, err := client.ConnectContext(context.Background(), pair.Client); err != nil {
		t.Fatalf("client.ConnectContext failed: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = pair.Server.Close()
	})
	return &tmInMemoryMCPPair{client: client, server: pair.Server}
}

func tmFauxSetup(t *testing.T) (*core.AuthStorage, *core.ModelRegistry, *core.ModelRuntime, *providers.FauxProviderRegistration) {
	t.Helper()
	ctx := context.Background()
	faux := compat.RegisterFauxProvider(nil)
	model := faux.GetModel()
	auth := core.AuthStorageInMemory()
	key := "faux-key"
	if _, err := auth.Modify(ctx, string(model.Provider), func(ai.Credential) (ai.Credential, error) {
		return &ai.ApiKeyCredential{Type: ai.AuthTypeApiKey, Key: &key}, nil
	}, nil); err != nil {
		if faux.Unregister != nil {
			faux.Unregister()
		}
		t.Fatalf("auth.Modify failed: %v", err)
	}
	reg, err := testkit.CreateInMemoryModelRegistry(ctx, auth)
	if err != nil {
		if faux.Unregister != nil {
			faux.Unregister()
		}
		t.Fatalf("CreateInMemoryModelRegistry failed: %v", err)
	}
	rt, err := testkit.GetModelRuntime(reg)
	if err != nil {
		if faux.Unregister != nil {
			faux.Unregister()
		}
		t.Fatalf("GetModelRuntime failed: %v", err)
	}
	apiStr := faux.Api
	baseUrl := model.BaseUrl
	if err := reg.RegisterProvider(string(model.Provider), core.ProviderConfigInput{
		BaseUrl: &baseUrl,
		ApiKey:  &key,
		Api:     &apiStr,
		Models: []core.ProviderModelConfig{
			&core.ProviderChatModelConfig{
				ProviderModelConfigBase: core.ProviderModelConfigBase{
					Id:          model.Id,
					Name:        model.Name,
					Api:         &apiStr,
					BaseUrl:     &baseUrl,
					Input:       model.Input,
					InputLimits: model.InputLimits,
					Cost:        model.Cost,
				},
				Reasoning:     model.Reasoning,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
			},
		},
	}); err != nil {
		rt.Close()
		if faux.Unregister != nil {
			faux.Unregister()
		}
		t.Fatalf("RegisterProvider failed: %v", err)
	}
	t.Cleanup(func() {
		rt.Close()
		if faux.Unregister != nil {
			faux.Unregister()
		}
	})
	return auth, reg, rt, faux
}

func TestEx14CodemodeMCP_OfflineIntegration(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	agentDir := t.TempDir()

	pair := tmStartInMemoryMCP(t)
	mcpTools, err := pair.client.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(mcpTools) != 2 {
		t.Fatalf("expected 2 MCP tools, got %d", len(mcpTools))
	}

	// Build an inline MCP extension wrapping the live in-memory mcp.McpClient,
	// mirroring mcpext's codemode/deferred server tool exposures.
	mcpInline := core.InlineExtension{
		Name: "in-memory-mcp",
		Factory: func(pi core.ExtensionAPI) error {
			ns := &core.ToolNamespace{Name: "repo", Description: tmStrPtr("In-memory MCP repo server")}
			for _, mt := range mcpTools {
				mcpName := mt.Name
				desc := ""
				if mt.Description != nil {
					desc = *mt.Description
				}
				exposure := core.ToolExposureCodemode
				regName := "repo_" + mcpName
				if mcpName == "read_meta" {
					exposure = core.ToolExposureDeferred
				}
				props := omap.NewMap[string, *typebox.Schema]()
				props.Set("arg", typebox.Optional(typebox.String()))
				pi.RegisterTool(core.ToolDefinition{
					Name:        regName,
					Label:       regName,
					Description: desc,
					Exposure:    exposure,
					Namespace:   ns,
					Parameters:  typebox.Object(props),
					Execute: func(execCtx context.Context, _ string, params *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
						res, callErr := pair.client.CallTool(execCtx, mcpName, params, nil)
						if callErr != nil {
							return agent.AgentToolResult{}, callErr
						}
						if res.IsError != nil && *res.IsError {
							return agent.AgentToolResult{}, errors.New("mcp tool error")
						}
						var texts []string
						for _, blk := range res.Content {
							if tb, ok := blk.(*protocol.TextContent); ok && tb != nil {
								texts = append(texts, tb.Text)
							}
						}
						return agent.AgentToolResult{
							Content: ai.ContentList{&ai.TextContent{Type: "text", Text: strings.Join(texts, "\n")}},
						}, nil
					},
				})
			}
			return nil
		},
	}

	auth, reg, rt, faux := tmFauxSetup(t)
	stopTool := ai.StopReasonToolUse
	var sawCodemodeAndSearch bool
	faux.SetResponses([]providers.FauxResponseStep{
		// Step 1: discover deferred MCP tool with tool_search.
		providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			var hasCodemode, hasToolSearch bool
			for _, tool := range ai.GetCurrentTools(tr.Messages) {
				if tool.Name == "codemode" {
					hasCodemode = true
				}
				if tool.Name == "tool_search" {
					hasToolSearch = true
				}
			}
			sawCodemodeAndSearch = hasCodemode && hasToolSearch
			return providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{
					Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "read_meta metadata"), nil),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
			), nil
		}),
		// Step 2: call both the codemode-exposed and deferred MCP tools inside QuickJS codemode.
		providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlock{
					Block: providers.FauxToolCall("codemode", jsonx.ObjectOf(
						"code", `
							const c = await tools.repo_count_ts({ dir: "src" });
							const m = await tools.repo_read_meta({ file: "a.ts" });
							return c + " | " + m;
						`,
					), nil),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
			),
		},
		// Step 3: emit final text verifying both MCP outputs reached the model.
		providers.FauxResponseFactory(func(_ context.Context, tr *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			var gotToolResult string
			for _, m := range tr.Messages {
				if trMsg, ok := m.(*ai.ToolResultMessage); ok && trMsg.ToolName == "codemode" {
					for _, blk := range trMsg.Content {
						if tc, ok := blk.(*ai.TextContent); ok && tc != nil {
							gotToolResult += tc.Text
						}
					}
				}
			}
			if !strings.Contains(gotToolResult, "ts_count=42") || !strings.Contains(gotToolResult, "meta:a.ts=900bytes") {
				return nil, fmt.Errorf("missing expected MCP tool outputs in codemode result: %s", gotToolResult)
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantText("final-mcp-summary:42"), nil), nil
		}),
	})

	var out bytes.Buffer
	err = mainRun(ctx, mainOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		ExtraExtensions: []core.InlineExtension{mcpInline},
		AuthStorage:     auth,
		ModelRegistry:   reg,
		ModelRuntime:    rt,
		Model:           faux.GetModel(),
		SessionManager:  core.SessionManagerInMemory(),
		NoExtensions:    true,
	}, &out)
	if err != nil {
		t.Fatalf("mainRun failed: %v\noutput:\n%s", err, out.String())
	}
	if !sawCodemodeAndSearch {
		t.Fatalf("expected both codemode and tool_search declared to model, output:\n%s", out.String())
	}
	output := out.String()
	if !strings.Contains(output, "Active tools:") || !strings.Contains(output, "codemode") || !strings.Contains(output, "tool_search") {
		t.Fatalf("expected Active tools line with codemode & tool_search, got:\n%s", output)
	}
	if !strings.Contains(output, "final-mcp-summary:42") {
		t.Fatalf("expected streamed final summary in output, got:\n%s", output)
	}
}
