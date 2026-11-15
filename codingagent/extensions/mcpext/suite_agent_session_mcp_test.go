// Ported from packages/coding-agent/test/suite/agent-session-mcp.test.ts (pi v1.5.2).
// Uses lightweight in-test codemode / tool_search mock extensions so mcpext + AgentSession tests stay fast and self-contained.

package mcpext

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/mcp"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

const tsasmTinyPngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
const tsasmToolSearchDescription = "Search deferred tools and load them for use."

func tsasmStrPtr(s string) *string       { return &s }
func tsasmBoolPtr(b bool) *bool          { return &b }
func tsasmInt64Ptr(v int64) *int64       { return &v }
func tsasmFloat64Ptr(v float64) *float64 { return &v }

func tsasmServerTools() []mcpprotocol.Tool {
	return []mcpprotocol.Tool{
		{
			Name:        "search",
			Description: tsasmStrPtr("Search the docs."),
			InputSchema: jsonx.ObjectOf(
				"type", "object",
				"properties", jsonx.ObjectOf("query", jsonx.ObjectOf("type", "string")),
				"required", []any{"query"},
			),
			OutputSchema: jsonx.ObjectOf(
				"type", "object",
				"properties", jsonx.ObjectOf("hits", jsonx.ObjectOf("type", "array", "items", jsonx.ObjectOf("type", "string"))),
				"required", []any{"hits"},
			),
		},
		{
			Name:        "fail",
			Description: tsasmStrPtr("Always fails."),
			InputSchema: jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject()),
			Annotations: &mcpprotocol.ToolAnnotations{
				Title:           tsasmStrPtr("Fail"),
				DestructiveHint: tsasmBoolPtr(true),
				ReadOnlyHint:    tsasmBoolPtr(false),
			},
		},
		{
			Name:        "shot",
			Description: tsasmStrPtr("Returns an image."),
			InputSchema: jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject()),
		},
	}
}

type tsasmFakeServerOpts struct {
	listTools         func() []mcpprotocol.Tool
	resources         bool
	instructions      string
	initializeDelayMs float64
}

func tsasmStartFakeServer(t *testing.T, calls *[]string, mu *sync.Mutex, opts tsasmFakeServerOpts) *mcp.InMemoryTransportPair {
	t.Helper()
	pair := mcp.CreateInMemoryTransportPair()
	t.Cleanup(func() {
		_ = pair.Server.Close()
	})
	listFn := opts.listTools
	if listFn == nil {
		listFn = tsasmServerTools
	}
	pair.Server.OnMessage(func(msg mcpprotocol.JsonRpcMessage) {
		req, ok := msg.(*mcpprotocol.JsonRpcRequest)
		if !ok {
			return
		}
		var resp mcpprotocol.JsonRpcMessage
		buildResp := func() mcpprotocol.JsonRpcMessage {
			switch req.Method {
			case "initialize":
				caps := jsonx.ObjectOf("tools", jsonx.NewObject())
				if opts.resources {
					caps.Set("resources", jsonx.NewObject())
				}
				res := jsonx.ObjectOf(
					"protocolVersion", mcpprotocol.LatestProtocolVersion,
					"capabilities", caps,
					"serverInfo", jsonx.ObjectOf("name", "docs", "version", "1.0.0"),
				)
				if opts.instructions != "" {
					res.Set("instructions", opts.instructions)
				}
				return &mcpprotocol.JsonRpcSuccessResponse{Jsonrpc: "2.0", Id: req.Id, Result: res}
			case "tools/list":
				tools := listFn()
				return &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result:  jsonx.ObjectOf("tools", tools),
				}
			case "resources/list":
				return &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf("resources", []any{
						jsonx.ObjectOf("uri", "docs://intro", "name", "intro", "mimeType", "text/markdown", "_meta", jsonx.ObjectOf("x", float64(1))),
						jsonx.ObjectOf("uri", "ui://docs/viewer", "name", "viewer", "mimeType", "text/html;profile=mcp-app"),
					}),
				}
			case "resources/templates/list":
				return &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf("resourceTemplates", []any{
						jsonx.ObjectOf(
							"uriTemplate", "docs://pages/{slug}",
							"name", "page",
							"icons", []any{jsonx.ObjectOf("src", "data:image/png;base64,AAAA")},
						),
					}),
				}
			case "resources/read":
				paramsObj, _ := req.Params.(*jsonx.Object)
				uri, _ := paramsObj.GetString("uri")
				mu.Lock()
				*calls = append(*calls, "read:"+uri)
				mu.Unlock()
				return &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result: jsonx.ObjectOf("contents", []any{
						jsonx.ObjectOf("uri", uri, "mimeType", "text/markdown", "text", "# "+uri),
					}),
				}
			case "tools/call":
				paramsObj, _ := req.Params.(*jsonx.Object)
				tName, _ := paramsObj.GetString("name")
				argsRaw, hasArgs := paramsObj.Get("arguments")
				argsObj, _ := argsRaw.(*jsonx.Object)
				if !hasArgs || argsObj == nil {
					argsObj = jsonx.NewObject()
				}
				encodedArgs, _ := jsonx.Stringify(argsObj)
				mu.Lock()
				*calls = append(*calls, tName+":"+encodedArgs)
				mu.Unlock()
				switch tName {
				case "search":
					q, _ := argsObj.GetString("query")
					hits := []string{q + " guide", q + " faq"}
					return &mcpprotocol.JsonRpcSuccessResponse{
						Jsonrpc: "2.0",
						Id:      req.Id,
						Result: jsonx.ObjectOf(
							"content", []any{jsonx.ObjectOf("type", "text", "text", strings.Join(hits, "\n"))},
							"structuredContent", jsonx.ObjectOf("hits", hits),
						),
					}
				case "shot":
					return &mcpprotocol.JsonRpcSuccessResponse{
						Jsonrpc: "2.0",
						Id:      req.Id,
						Result: jsonx.ObjectOf(
							"content", []any{jsonx.ObjectOf("type", "image", "data", tsasmTinyPngBase64, "mimeType", "image/png")},
						),
					}
				case "read-file", "read_file":
					return &mcpprotocol.JsonRpcSuccessResponse{
						Jsonrpc: "2.0",
						Id:      req.Id,
						Result: jsonx.ObjectOf(
							"content", []any{jsonx.ObjectOf("type", "text", "text", tName)},
						),
					}
				default:
					return &mcpprotocol.JsonRpcSuccessResponse{
						Jsonrpc: "2.0",
						Id:      req.Id,
						Result: jsonx.ObjectOf(
							"content", []any{jsonx.ObjectOf("type", "text", "text", "server exploded")},
							"isError", true,
						),
					}
				}
			default:
				return &mcpprotocol.JsonRpcSuccessResponse{Jsonrpc: "2.0", Id: req.Id, Result: jsonx.NewObject()}
			}
		}

		if req.Method == "initialize" && opts.initializeDelayMs > 0 {
			if math.IsInf(opts.initializeDelayMs, 1) {
				return
			}
			go func() {
				time.Sleep(time.Duration(opts.initializeDelayMs * float64(time.Millisecond)))
				_ = pair.Server.Send(buildResp())
			}()
			return
		}
		resp = buildResp()
		go func() {
			_ = pair.Server.Send(resp)
		}()
	})
	if err := pair.Server.Start(); err != nil {
		t.Fatalf("pair.Server.Start: %v", err)
	}
	return pair
}

var tsasmMcpTokenRegex = regexp.MustCompile(`mcp__[A-Za-z0-9_-]+`)

func tsasmMockCodemodeExtension() core.ExtensionFactory {
	return func(pi core.ExtensionAPI) error {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("code", typebox.Optional(typebox.String()))
		props.Set("calls", typebox.Optional(typebox.Any()))
		params := typebox.Object(props)
		pi.RegisterTool(core.ToolDefinition{
			Name:          "codemode",
			Label:         "codemode",
			Description:   "Run codemode script against callable tools.",
			Parameters:    params,
			Exposure:      core.ToolExposureModelOnly,
			DefaultActive: tsasmBoolPtr(false),
			ReachesTools: func(input *jsonx.Object) core.ToolReachesResult {
				if input == nil {
					return core.ToolReachesResult{}
				}
				code, _ := input.GetString("code")
				matches := tsasmMcpTokenRegex.FindAllString(code, -1)
				if callsRaw, ok := input.Get("calls"); ok {
					if arr, ok := callsRaw.([]any); ok {
						for _, item := range arr {
							if obj, ok := item.(*jsonx.Object); ok && obj != nil {
								if n, ok := obj.GetString("name"); ok && n != "" {
									matches = append(matches, n)
								}
							}
						}
					}
				}
				return core.ToolReachesResult{Tools: matches}
			},
			Execute: func(ctx context.Context, _ string, input *jsonx.Object, _ agent.AgentToolUpdateCallback, ectx core.ExtensionToolContext) (agent.AgentToolResult, error) {
				code, _ := input.GetString("code")
				if code == "return 1 + 1;" {
					return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "2"}}}, nil
				}
				if callsRaw, ok := input.Get("calls"); ok {
					if arr, ok := callsRaw.([]any); ok {
						out := jsonx.NewObject()
						var results []any
						for _, item := range arr {
							callObj, ok := item.(*jsonx.Object)
							if !ok || callObj == nil {
								continue
							}
							tName, _ := callObj.GetString("name")
							if descSub, hasDescSub := callObj.GetString("byDescription"); hasDescSub && descSub != "" {
								for _, tl := range ectx.Tools() {
									if strings.Contains(tl.Description, descSub) {
										tName = tl.Name
										break
									}
								}
							}
							argsRaw, _ := callObj.Get("arguments")
							if argsRaw == nil {
								argsRaw = jsonx.NewObject()
							}
							outcome, err := ectx.ExecuteTool(ctx, tName, argsRaw)
							if err != nil {
								return agent.AgentToolResult{}, err
							}
							var texts []string
							var imgs int
							for _, blk := range outcome.Result.Content {
								switch v := blk.(type) {
								case *ai.TextContent:
									texts = append(texts, v.Text)
								case *ai.ImageContent:
									imgs++
								}
							}
							entry := jsonx.ObjectOf("name", tName, "isError", outcome.IsError, "text", strings.Join(texts, "\n"), "images", imgs)
							if outcome.Result.StructuredContent != nil {
								entry.Set("structuredContent", outcome.Result.StructuredContent)
							}
							results = append(results, entry)
						}
						out.Set("results", results)
						encoded, _ := jsonx.Stringify(out)
						return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: encoded}}}, nil
					}
				}
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}}}, nil
			},
		})
		return nil
	}
}

func tsasmMockToolSearchExtension() core.ExtensionFactory {
	return func(pi core.ExtensionAPI) error {
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("query", typebox.String())
		props.Set("limit", typebox.Optional(typebox.Number()))
		params := typebox.Object(props)
		pi.RegisterTool(core.ToolDefinition{
			Name:          "tool_search",
			Label:         "tool_search",
			Description:   tsasmToolSearchDescription,
			Parameters:    params,
			Exposure:      core.ToolExposureModelOnly,
			DefaultActive: tsasmBoolPtr(false),
			ReachesTools: func(*jsonx.Object) core.ToolReachesResult {
				return core.ToolReachesResult{All: true}
			},
			Execute: func(_ context.Context, _ string, input *jsonx.Object, _ agent.AgentToolUpdateCallback, _ core.ExtensionToolContext) (agent.AgentToolResult, error) {
				query, _ := input.GetString("query")
				limit := 0
				if l, ok := input.GetNumber("limit"); ok && l > 0 {
					limit = int(l)
				}
				active := pi.GetActiveTools()
				activeSet := make(map[string]bool, len(active))
				for _, a := range active {
					activeSet[a] = true
				}
				type scoredTool struct {
					info  core.ToolInfo
					score int
				}
				var candidates []scoredTool
				words := strings.Fields(strings.ToLower(query))
				for _, tl := range pi.GetAllTools() {
					if activeSet[tl.Name] || tl.Name == "tool_search" || tl.Name == "codemode" {
						continue
					}
					score := 1
					if len(words) > 0 {
						haystack := strings.ToLower(tl.Name + " " + tl.Description)
						score = 0
						for _, word := range words {
							if strings.Contains(haystack, word) {
								score++
							}
						}
						if score == 0 {
							continue
						}
					}
					candidates = append(candidates, scoredTool{info: tl, score: score})
				}
				for i := 0; i < len(candidates); i++ {
					for j := i + 1; j < len(candidates); j++ {
						if candidates[j].score > candidates[i].score {
							candidates[i], candidates[j] = candidates[j], candidates[i]
						}
					}
				}
				var loaded []core.ToolInfo
				for _, cand := range candidates {
					loaded = append(loaded, cand.info)
					if limit > 0 && len(loaded) >= limit {
						break
					}
				}
				if len(loaded) == 0 {
					return agent.AgentToolResult{
						Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "No matching tools found."}},
					}, nil
				}
				next := append([]string(nil), active...)
				var lines []string
				for _, tl := range loaded {
					next = append(next, tl.Name)
					lines = append(lines, fmt.Sprintf("- %s: %s", tl.Name, tl.Description))
				}
				pi.SetActiveTools(next)
				text := fmt.Sprintf("Loaded %d tool. They are available from your next call:\n%s", len(loaded), strings.Join(lines, "\n"))
				return agent.AgentToolResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: text}}}, nil
			},
		})
		return nil
	}
}

type tsasmSetupOptions struct {
	autoEnableCodemode *bool
	builtInTools       []string
	allowedToolNames   []string
	excludedToolNames  []string
	sessionManager     *core.SessionManager
	waitForTools       *bool
	extensionFactories []core.ExtensionFactory
	toolExposure       map[string]core.McpExposure
	resources          bool
	withoutToolSearch  bool
	withoutCodemode    bool
	description        *string
	instructions       string
}

type tsasmSetupResult struct {
	harness       *testkit.SuiteHarness
	calls         *[]string
	callsMu       *sync.Mutex
	servers       *[]*mcp.InMemoryTransport
	serversMu     *sync.Mutex
	notifications *[]string
	notifyMu      *sync.Mutex
}

func tsasmWaitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func tsasmSetup(t *testing.T, exposure core.McpExposure, listTools func() []mcpprotocol.Tool, opts tsasmSetupOptions) *tsasmSetupResult {
	t.Helper()
	ctx := context.Background()
	var calls []string
	var callsMu sync.Mutex
	var notifications []string
	var notifyMu sync.Mutex
	var servers []*mcp.InMemoryTransport
	var serversMu sync.Mutex

	expVal := exposure
	cfg := McpServerConfig{
		Url:         tsasmStrPtr("http://unused.invalid"),
		Exposure:    &expVal,
		Description: opts.description,
	}
	if len(opts.toolExposure) > 0 {
		// Build ordered ToolExposure map in deterministic key order.
		parsedObj := jsonx.NewObject()
		parsedObj.Set("url", "http://unused.invalid")
		parsedObj.Set("exposure", string(expVal))
		if opts.description != nil {
			parsedObj.Set("description", *opts.description)
		}
		teObj := jsonx.NewObject()
		for k, v := range opts.toolExposure {
			teObj.Set(k, string(v))
		}
		parsedObj.Set("toolExposure", teObj)
		validated, valErr := ValidateMcpServerConfig("docs", parsedObj)
		if valErr != nil || validated == nil {
			t.Fatalf("ValidateMcpServerConfig: %v", valErr)
		}
		cfg = *validated
	}
	entry := McpServerEntry{
		Name:   "docs",
		Config: cfg,
		Source: "test",
	}

	var factories []core.InlineExtension
	for i, fn := range opts.extensionFactories {
		factories = append(factories, core.InlineExtension{Name: fmt.Sprintf("custom-%d", i), Factory: fn})
	}
	if !opts.withoutCodemode {
		factories = append(factories, core.InlineExtension{Name: "codemode", Factory: tsasmMockCodemodeExtension()})
	}
	if !opts.withoutToolSearch {
		factories = append(factories, core.InlineExtension{Name: "tool-search", Factory: tsasmMockToolSearchExtension()})
	}
	factories = append(factories, core.InlineExtension{
		Name: "mcp",
		Factory: CreateMcpExtension(McpExtensionOptions{
			LoadConfig: func(core.ExtensionContext) LoadedMcpConfig {
				return LoadedMcpConfig{
					Servers:            []McpServerEntry{entry},
					AutoEnableCodemode: opts.autoEnableCodemode,
				}
			},
			CreateTransport: func(McpServerEntry, string, mcp.AuthProvider) (mcp.McpTransport, error) {
				pair := tsasmStartFakeServer(t, &calls, &callsMu, tsasmFakeServerOpts{
					listTools:    listTools,
					resources:    opts.resources,
					instructions: opts.instructions,
				})
				serversMu.Lock()
				servers = append(servers, pair.Server)
				serversMu.Unlock()
				return pair.Client, nil
			},
		}),
	})

	extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
		Cwd:       t.TempDir(),
		Factories: factories,
	})
	if len(extRes.Errors) > 0 {
		t.Fatalf("LoadExtensionsWithOptions errors: %+v", extRes.Errors)
	}

	initialActive := []string{}
	if opts.allowedToolNames != nil {
		initialActive = opts.allowedToolNames
	} else if opts.builtInTools != nil {
		initialActive = opts.builtInTools
	}
	harness, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
		InitialActiveToolNames: initialActive,
		AllowedToolNames:       opts.allowedToolNames,
		ExcludedToolNames:      opts.excludedToolNames,
		SessionManager:         opts.sessionManager,
		ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
			ExtensionsResult: &extRes,
		}),
	})
	if err != nil {
		t.Fatalf("CreateSuiteHarness: %v", err)
	}
	t.Cleanup(harness.Cleanup)

	uiCtx := testkit.CreateTestUiContext(&testkit.TestUiContextOverrides{
		Notify: func(message string, _ ...string) {
			notifyMu.Lock()
			notifications = append(notifications, message)
			notifyMu.Unlock()
		},
	})
	if err := harness.Session.BindExtensions(ctx, core.ExtensionBindings{
		UIContext: uiCtx,
	}); err != nil {
		t.Fatalf("BindExtensions: %v", err)
	}

	wait := true
	if opts.waitForTools != nil {
		wait = *opts.waitForTools
	}
	if wait {
		tsasmWaitFor(t, 5*time.Second, func() bool {
			for _, tl := range harness.Session.GetAllTools() {
				if tl.Name == "mcp__docs__search" {
					return true
				}
			}
			return false
		})
	}

	return &tsasmSetupResult{
		harness:       harness,
		calls:         &calls,
		callsMu:       &callsMu,
		servers:       &servers,
		serversMu:     &serversMu,
		notifications: &notifications,
		notifyMu:      &notifyMu,
	}
}

func tsasmDeclaredToolNames(harness *testkit.SuiteHarness) []string {
	var names []string
	for _, msg := range harness.Session.Messages() {
		if sys, ok := msg.(*ai.SystemMessage); ok && sys != nil {
			for _, tl := range sys.ToolsAdded {
				names = append(names, tl.Name)
			}
		}
	}
	return names
}

func tsasmServersSection(harness *testkit.SuiteHarness) (string, bool) {
	var found bool
	var val string
	for _, msg := range harness.Session.Messages() {
		if sys, ok := msg.(*ai.SystemMessage); ok && sys != nil && sys.Sections != nil {
			if raw, exists := sys.Sections.Get(McpServersSection); exists {
				if raw == nil {
					found = false
					val = ""
				} else {
					found = true
					val = *raw
				}
			}
		}
	}
	return val, found
}

func tsasmAllToolNames(harness *testkit.SuiteHarness) []string {
	all := harness.Session.GetAllTools()
	out := make([]string, len(all))
	for i, tl := range all {
		out[i] = tl.Name
	}
	return out
}

func tsasmToolResultsByName(harness *testkit.SuiteHarness, name string) []*ai.ToolResultMessage {
	var out []*ai.ToolResultMessage
	for _, msg := range harness.Session.Messages() {
		if tr, ok := msg.(*ai.ToolResultMessage); ok && tr != nil && tr.ToolName == name {
			out = append(out, tr)
		}
	}
	return out
}

func tsasmFindFirstEntryID(t *testing.T, sm *core.SessionManager, role string) string {
	t.Helper()
	for _, entry := range sm.GetBranch() {
		if me, ok := entry.(*core.SessionMessageEntry); ok && me.Message != nil && me.Message.GetRole() == role {
			return me.Id
		}
	}
	t.Fatalf("no %s entry in branch", role)
	return ""
}

type tsasmReloadableLoader struct {
	core.ResourceLoader
	mu   sync.Mutex
	exts core.LoadExtensionsResult
	load func() core.LoadExtensionsResult
}

func (l *tsasmReloadableLoader) GetExtensions() core.LoadExtensionsResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.exts
}

func (l *tsasmReloadableLoader) Reload(context.Context, ...*core.ResourceLoaderReloadOptions) error {
	next := l.load()
	l.mu.Lock()
	l.exts = next
	l.mu.Unlock()
	return nil
}

func TestAgentSessionMcpIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("keeps MCP tools when --tools names no MCP tool", func(t *testing.T) {
		for _, exp := range []core.McpExposure{core.McpExposureCodemode, core.McpExposureDeferred} {
			res := tsasmSetup(t, exp, nil, tsasmSetupOptions{
				allowedToolNames: []string{"read", "codemode"},
				resources:        true,
			})
			if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"read", "codemode"}) {
				t.Fatalf("exposure %s: active tools = %v, want [read codemode]", exp, got)
			}
			callable := res.harness.Session.GetCallableToolNames()
			for _, want := range []string{"mcp__docs__search", "mcp__docs__fail", "list_mcp_resources", "read_mcp_resource"} {
				if !slices.Contains(callable, want) {
					t.Fatalf("exposure %s: callable tools %v missing %q", exp, callable, want)
				}
			}
			if all := tsasmAllToolNames(res.harness); slices.Contains(all, "bash") {
				t.Fatalf("exposure %s: expected bash excluded, got %v", exp, all)
			}
		}

		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			allowedToolNames: []string{"codemode"},
			toolExposure:     map[string]core.McpExposure{"fail": core.McpExposureDirect},
		})
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(tsasmAllToolNames(res.harness), "mcp__docs__fail")
		})
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("expected active [codemode], got %v", got)
		}
	})

	t.Run("removes MCP tools with --no-tools", func(t *testing.T) {
		waitFalse := false
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{
			allowedToolNames: []string{},
			waitForTools:     &waitFalse,
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		res.serversMu.Lock()
		srvCount := len(*res.servers)
		res.serversMu.Unlock()
		if srvCount != 1 {
			t.Fatalf("expected 1 connected server, got %d", srvCount)
		}
		if len(res.harness.Session.GetAllTools()) != 0 {
			t.Fatalf("expected 0 registered tools, got %v", tsasmAllToolNames(res.harness))
		}
		if len(res.harness.Session.GetActiveToolNames()) != 0 {
			t.Fatalf("expected 0 active tools, got %v", res.harness.Session.GetActiveToolNames())
		}
	})

	t.Run("does not declare unnamed MCP tools restored from the transcript", func(t *testing.T) {
		first := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{})
		first.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil)},
		})
		if err := first.harness.Session.Prompt(ctx, "first"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if err := first.harness.Session.Prompt(ctx, "second"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		if !slices.Contains(tsasmDeclaredToolNames(first.harness), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search declared in first session")
		}

		second := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{
			allowedToolNames: []string{"read", "codemode"},
			sessionManager:   first.harness.SessionManager,
		})
		firstAssistantID := tsasmFindFirstEntryID(t, second.harness.SessionManager, "assistant")
		if _, err := second.harness.Session.NavigateTree(ctx, firstAssistantID, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if active := second.harness.Session.GetActiveToolNames(); slices.Contains(active, "mcp__docs__search") {
			t.Fatalf("expected restored direct MCP tool filtered out of active tools, got %v", active)
		}
		if !slices.Contains(tsasmAllToolNames(second.harness), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search still registered")
		}
	})

	t.Run("lets tool_search declare unnamed MCP tools when --tools names it", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDeferred, nil, tsasmSetupOptions{
			allowedToolNames: []string{"tool_search"},
		})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "search the docs", "limit", 1), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("loaded"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "load"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"tool_search", "mcp__docs__search"}) {
			t.Fatalf("expected [tool_search mcp__docs__search], got %v", got)
		}
	})

	t.Run("filters MCP tools by the mcp__ entries of --tools", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			allowedToolNames: []string{"codemode", "mcp__docs__s*"},
			resources:        true,
			toolExposure:     map[string]core.McpExposure{"shot": core.McpExposureDirect},
		})
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Equal(res.harness.Session.GetActiveToolNames(), []string{"codemode", "mcp__docs__shot"})
		})
		registered := tsasmAllToolNames(res.harness)
		if !slices.Contains(registered, "mcp__docs__search") || !slices.Contains(registered, "mcp__docs__shot") {
			t.Fatalf("expected search and shot registered, got %v", registered)
		}
		if slices.Contains(registered, "mcp__docs__fail") || slices.Contains(registered, "list_mcp_resources") {
			t.Fatalf("expected fail and list_mcp_resources filtered out, got %v", registered)
		}
	})

	t.Run("removes MCP tools matching --exclude-tools patterns", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			allowedToolNames:  []string{"codemode"},
			excludedToolNames: []string{"mcp__docs__f*"},
		})
		registered := tsasmAllToolNames(res.harness)
		if !slices.Contains(registered, "mcp__docs__search") || slices.Contains(registered, "mcp__docs__fail") {
			t.Fatalf("unexpected registered tools with exclude pattern: %v", registered)
		}
	})

	t.Run("exposes codemode-only MCP tools through codemode and hides them from the model", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{})
		stopTool := ai.StopReasonToolUse
		callsArg := jsonx.ObjectOf("calls", []any{
			jsonx.ObjectOf("name", "mcp__docs__search", "arguments", jsonx.ObjectOf("query", "mcp")),
			jsonx.ObjectOf("name", "mcp__docs__search", "arguments", jsonx.ObjectOf("query", "pi")),
			jsonx.ObjectOf("name", "mcp__docs__fail", "arguments", jsonx.NewObject()),
			jsonx.ObjectOf("name", "mcp__docs__shot", "arguments", jsonx.NewObject()),
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", callsArg, nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "search the docs"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("active tools = %v, want [codemode]", got)
		}
		if got := tsasmDeclaredToolNames(res.harness); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("declared tools = %v, want [codemode]", got)
		}
		if got := res.harness.Session.GetCallableToolNames(); !slices.Equal(got, []string{"mcp__docs__fail", "mcp__docs__search", "mcp__docs__shot"}) {
			t.Fatalf("callable tools = %v", got)
		}
		tr, err := testkit.GetToolResult(res.harness, "codemode")
		if err != nil || tr.IsError {
			t.Fatalf("GetToolResult(codemode): tr=%+v err=%v", tr, err)
		}
		res.callsMu.Lock()
		gotCalls := slices.Clone(*res.calls)
		res.callsMu.Unlock()
		wantCalls := []string{`search:{"query":"mcp"}`, `search:{"query":"pi"}`, "fail:{}", "shot:{}"}
		if !slices.Equal(gotCalls, wantCalls) {
			t.Fatalf("calls = %v, want %v", gotCalls, wantCalls)
		}
		parsed, err := jsonx.Parse([]byte(testkit.GetMessageText(tr)))
		if err != nil {
			t.Fatalf("Parse codemode result: %v", err)
		}
		resultsRaw, _ := parsed.(*jsonx.Object).Get("results")
		resultsArr := resultsRaw.([]any)
		if isErr, _ := resultsArr[2].(*jsonx.Object).GetBool("isError"); !isErr {
			t.Fatalf("expected mcp__docs__fail nested call isError=true")
		}
		if imgs, _ := resultsArr[3].(*jsonx.Object).GetNumber("images"); imgs != 1 {
			t.Fatalf("expected mcp__docs__shot nested call 1 image, got %v", imgs)
		}
	})

	t.Run("keeps codemode-only MCP tools callable across tree navigation", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "first"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		if err := res.harness.Session.Prompt(ctx, "second"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		firstAssistantID := tsasmFindFirstEntryID(t, res.harness.SessionManager, "assistant")
		if _, err := res.harness.Session.NavigateTree(ctx, firstAssistantID, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("active tools = %v, want [codemode]", got)
		}
		if !slices.Contains(res.harness.Session.GetCallableToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search callable after tree navigation")
		}
	})

	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("routes tools whose names differ only in - and _ (reverse: %v)", reverse), func(t *testing.T) {
			dashUndTools := []mcpprotocol.Tool{
				{Name: "read-file", Description: tsasmStrPtr("dashed"), InputSchema: jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject())},
				{Name: "read_file", Description: tsasmStrPtr("underscored"), InputSchema: jsonx.ObjectOf("type", "object", "properties", jsonx.NewObject())},
			}
			if reverse {
				slices.Reverse(dashUndTools)
			}
			allList := append(append([]mcpprotocol.Tool{}, dashUndTools...), tsasmServerTools()...)
			res := tsasmSetup(t, core.McpExposureCodemode, func() []mcpprotocol.Tool { return allList }, tsasmSetupOptions{})
			stopTool := ai.StopReasonToolUse
			callsArg := jsonx.ObjectOf("calls", []any{
				jsonx.ObjectOf("byDescription", "dashed", "arguments", jsonx.NewObject()),
				jsonx.ObjectOf("byDescription", "underscored", "arguments", jsonx.NewObject()),
			})
			res.harness.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{
					Message: providers.FauxAssistantMessage(
						providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", callsArg, nil)},
						&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
					),
				},
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
			})
			if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			res.callsMu.Lock()
			gotCalls := slices.Clone(*res.calls)
			res.callsMu.Unlock()
			if !slices.Equal(gotCalls, []string{"read-file:{}", "read_file:{}"}) {
				t.Fatalf("calls = %v, want [read-file:{} read_file:{}]", gotCalls)
			}
		})
	}

	t.Run("rejects direct model calls to codemode-only MCP tools", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__docs__search", jsonx.ObjectOf("query", "x"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, err := testkit.GetToolResult(res.harness, "mcp__docs__search")
		if err != nil || !tr.IsError {
			t.Fatalf("expected error tool result, got tr=%+v err=%v", tr, err)
		}
		if got := testkit.GetMessageText(tr); got != "Tool mcp__docs__search not found" {
			t.Fatalf("error text = %q, want Tool mcp__docs__search not found", got)
		}
		res.callsMu.Lock()
		nCalls := len(*res.calls)
		res.callsMu.Unlock()
		if nCalls != 0 {
			t.Fatalf("expected 0 server calls, got %d", nCalls)
		}
	})

	t.Run("declares directly exposed MCP tools to the model", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__docs__search", jsonx.ObjectOf("query", "direct"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := tsasmDeclaredToolNames(res.harness); !slices.Equal(got, []string{"mcp__docs__search", "mcp__docs__fail", "mcp__docs__shot"}) {
			t.Fatalf("declared = %v", got)
		}
		if slices.Contains(res.harness.Session.GetActiveToolNames(), "codemode") {
			t.Fatalf("expected codemode inactive")
		}
		annByTool := make(map[string]*core.ToolAnnotations)
		for _, info := range res.harness.Session.GetAllTools() {
			annByTool[info.Name] = info.Annotations
		}
		failAnn := annByTool["mcp__docs__fail"]
		if failAnn == nil || failAnn.DestructiveHint == nil || !*failAnn.DestructiveHint || failAnn.ReadOnlyHint == nil || *failAnn.ReadOnlyHint || failAnn.IdempotentHint != nil {
			t.Fatalf("unexpected annotations on fail: %+v", failAnn)
		}
		if annByTool["mcp__docs__search"] != nil {
			t.Fatalf("expected nil annotations on search, got %+v", annByTool["mcp__docs__search"])
		}
		tr, err := testkit.GetToolResult(res.harness, "mcp__docs__search")
		if err != nil || testkit.GetMessageText(tr) != "direct guide\ndirect faq" {
			t.Fatalf("unexpected result: tr=%+v err=%v", tr, err)
		}
	})

	for _, exp := range []core.McpExposure{core.McpExposureDirect, core.McpExposureCodemode} {
		t.Run(fmt.Sprintf("withdraws and restores %s MCP tools the server changes", exp), func(t *testing.T) {
			var toolsMu sync.Mutex
			currentTools := tsasmServerTools()
			res := tsasmSetup(t, exp, func() []mcpprotocol.Tool {
				toolsMu.Lock()
				defer toolsMu.Unlock()
				return slices.Clone(currentTools)
			}, tsasmSetupOptions{})
			res.harness.SetResponses([]providers.FauxResponseStep{
				providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
			})
			if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
				t.Fatalf("Prompt: %v", err)
			}
			reachable := func() []string {
				if exp == core.McpExposureDirect {
					return res.harness.Session.GetActiveToolNames()
				}
				return res.harness.Session.GetCallableToolNames()
			}
			if !slices.Contains(reachable(), "mcp__docs__fail") {
				t.Fatalf("expected mcp__docs__fail initially reachable: %v", reachable())
			}

			toolsMu.Lock()
			currentTools = []mcpprotocol.Tool{tsasmServerTools()[0], tsasmServerTools()[2]}
			toolsMu.Unlock()
			res.serversMu.Lock()
			srv := (*res.servers)[0]
			res.serversMu.Unlock()
			if err := srv.Send(&mcpprotocol.JsonRpcNotification{Jsonrpc: "2.0", Method: "notifications/tools/list_changed"}); err != nil {
				t.Fatalf("Send list_changed: %v", err)
			}
			tsasmWaitFor(t, 5*time.Second, func() bool {
				r := reachable()
				return !slices.Contains(r, "mcp__docs__fail") && slices.Contains(r, "mcp__docs__search")
			})

			toolsMu.Lock()
			currentTools = tsasmServerTools()
			toolsMu.Unlock()
			if err := srv.Send(&mcpprotocol.JsonRpcNotification{Jsonrpc: "2.0", Method: "notifications/tools/list_changed"}); err != nil {
				t.Fatalf("Send restore list_changed: %v", err)
			}
			tsasmWaitFor(t, 5*time.Second, func() bool {
				return slices.Contains(reachable(), "mcp__docs__fail")
			})
		})
	}

	t.Run("lists and reads resources with resource tools", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{resources: true})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlocks{
						providers.FauxToolCall("list_mcp_resources", jsonx.NewObject(), nil),
						providers.FauxToolCall("list_mcp_resource_templates", jsonx.ObjectOf("server", "docs"), nil),
						providers.FauxToolCall("read_mcp_resource", jsonx.ObjectOf("server", "docs", "uri", "docs://pages/setup"), nil),
					},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("read_mcp_resource", jsonx.ObjectOf("server", "nope", "uri", "docs://x"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "read"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		active := res.harness.Session.GetActiveToolNames()
		for _, rn := range []string{"list_mcp_resources", "list_mcp_resource_templates", "read_mcp_resource"} {
			if !slices.Contains(active, rn) {
				t.Fatalf("expected active resource tool %q, got %v", rn, active)
			}
		}
		listTr, _ := testkit.GetToolResult(res.harness, "list_mcp_resources")
		if txt := testkit.GetMessageText(listTr); !strings.Contains(txt, "docs://intro") || strings.Contains(txt, "ui://docs/viewer") {
			t.Fatalf("unexpected list_mcp_resources output: %s", txt)
		}
		tmplTr, _ := testkit.GetToolResult(res.harness, "list_mcp_resource_templates")
		if txt := testkit.GetMessageText(tmplTr); !strings.Contains(txt, "docs://pages/{slug}") {
			t.Fatalf("unexpected list_mcp_resource_templates output: %s", txt)
		}
		reads := tsasmToolResultsByName(res.harness, "read_mcp_resource")
		if len(reads) != 2 || reads[0].IsError || testkit.GetMessageText(reads[0]) != "# docs://pages/setup" {
			t.Fatalf("unexpected first read_mcp_resource result: %+v", reads)
		}
		if !reads[1].IsError || testkit.GetMessageText(reads[1]) != `MCP server "nope" has no resources. Servers with resources: docs` {
			t.Fatalf("unexpected missing server read_mcp_resource result: isErr=%v text=%q", reads[1].IsError, testkit.GetMessageText(reads[1]))
		}
		for _, info := range res.harness.Session.GetAllTools() {
			if info.Name == "read_mcp_resource" {
				if info.Annotations == nil || info.Annotations.ReadOnlyHint == nil || !*info.Annotations.ReadOnlyHint {
					t.Fatalf("expected readOnlyHint=true on read_mcp_resource, got %+v", info.Annotations)
				}
			}
		}
	})

	t.Run("makes resource tools callable from codemode for codemode servers", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{resources: true})
		stopTool := ai.StopReasonToolUse
		callsArg := jsonx.ObjectOf("calls", []any{
			jsonx.ObjectOf("name", "list_mcp_resources", "arguments", jsonx.ObjectOf("server", "docs")),
			jsonx.ObjectOf("name", "read_mcp_resource", "arguments", jsonx.ObjectOf("server", "docs", "uri", "docs://intro")),
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", callsArg, nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "read"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("active tools = %v, want [codemode]", got)
		}
		tr, _ := testkit.GetToolResult(res.harness, "codemode")
		if tr.IsError || !strings.Contains(testkit.GetMessageText(tr), "# docs://intro") {
			t.Fatalf("unexpected codemode resource call result: %s", testkit.GetMessageText(tr))
		}
		res.callsMu.Lock()
		gotCalls := slices.Clone(*res.calls)
		res.callsMu.Unlock()
		if !slices.Equal(gotCalls, []string{"read:docs://intro"}) {
			t.Fatalf("calls = %v, want [read:docs://intro]", gotCalls)
		}
	})

	t.Run("applies per-tool exposure overrides", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureHidden, nil, tsasmSetupOptions{
			toolExposure: map[string]core.McpExposure{
				"search": core.McpExposureDirect,
				"s*":     core.McpExposureCodemode,
			},
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := tsasmDeclaredToolNames(res.harness); !slices.Equal(got, []string{"codemode", "mcp__docs__search"}) {
			t.Fatalf("declared = %v, want [codemode mcp__docs__search]", got)
		}
		if got := res.harness.Session.GetCallableToolNames(); !slices.Equal(got, []string{"mcp__docs__search", "mcp__docs__shot"}) {
			t.Fatalf("callable = %v, want [mcp__docs__search mcp__docs__shot]", got)
		}
	})

	t.Run("describes server description and namespace instructions without leaking to descriptions", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			description:  tsasmStrPtr("Search the product docs"),
			instructions: "Always search before reading.",
			builtInTools: []string{"tool_search"},
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		for _, tl := range res.harness.Session.Agent.Tools() {
			if strings.Contains(tl.Description, "Always search") {
				t.Fatalf("tool %s leaked instructions into description", tl.Name)
			}
			if tl.Name == "codemode" && strings.Contains(tl.Description, "mcp__docs") {
				t.Fatalf("codemode leaked server name into description")
			}
		}
		searchDef := res.harness.Session.GetToolDefinition("mcp__docs__search")
		if searchDef == nil || searchDef.Namespace == nil {
			t.Fatalf("expected namespace metadata on mcp__docs__search")
		}
		if searchDef.Namespace.Name != "mcp__docs" ||
			searchDef.Namespace.Description == nil || *searchDef.Namespace.Description != "Search the product docs" ||
			searchDef.Namespace.Instructions == nil || *searchDef.Namespace.Instructions != "Always search before reading." {
			t.Fatalf("unexpected namespace metadata: %+v", searchDef.Namespace)
		}
		sec, ok := tsasmServersSection(res.harness)
		if !ok || !strings.Contains(sec, "- mcp__docs (codemode): Search the product docs") {
			t.Fatalf("unexpected mcp_servers section (ok=%v): %q", ok, sec)
		}
	})

	t.Run("lists servers by the first line of their instructions without a configured description", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDeferred, nil, tsasmSetupOptions{
			instructions: "Docs search.\nLong guidance.",
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		sec, ok := tsasmServersSection(res.harness)
		if !ok || !strings.Contains(sec, "- mcp__docs (tool_search): Docs search.") || strings.Contains(sec, "Long guidance") {
			t.Fatalf("unexpected mcp_servers section (ok=%v): %q", ok, sec)
		}
	})

	t.Run("does not activate codemode when autoEnableCodemode is false", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			autoEnableCodemode: tsasmBoolPtr(false),
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if len(res.harness.Session.GetActiveToolNames()) != 0 {
			t.Fatalf("expected 0 active tools, got %v", res.harness.Session.GetActiveToolNames())
		}
		if !slices.Contains(res.harness.Session.GetCallableToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search still callable")
		}
		res.notifyMu.Lock()
		notes := slices.Clone(*res.notifications)
		res.notifyMu.Unlock()
		want := []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active (autoEnableCodemode is false); they cannot be called."}
		if !slices.Equal(notes, want) {
			t.Fatalf("notifications = %v, want %v", notes, want)
		}
	})

	t.Run("treats codemode MCP tools as reachable through an active tool_search", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			autoEnableCodemode: tsasmBoolPtr(false),
			builtInTools:       []string{"tool_search"},
		})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "search the docs", "limit", 1), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__docs__search", jsonx.ObjectOf("query", "loaded"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"tool_search", "mcp__docs__search"}) {
			t.Fatalf("active tools = %v", got)
		}
		tr, _ := testkit.GetToolResult(res.harness, "mcp__docs__search")
		if got := testkit.GetMessageText(tr); got != "loaded guide\nloaded faq" {
			t.Fatalf("search result = %q", got)
		}
		res.notifyMu.Lock()
		notes := slices.Clone(*res.notifications)
		res.notifyMu.Unlock()
		if len(notes) != 0 {
			t.Fatalf("expected 0 notifications, got %v", notes)
		}
	})

	t.Run("reaches deferred MCP tools through codemode when tool_search is not available", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDeferred, nil, tsasmSetupOptions{
			builtInTools:      []string{"codemode"},
			withoutToolSearch: true,
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("active = %v, want [codemode]", got)
		}
		if !slices.Contains(res.harness.Session.GetCallableToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search callable")
		}
		res.notifyMu.Lock()
		notes := slices.Clone(*res.notifications)
		res.notifyMu.Unlock()
		if len(notes) != 0 {
			t.Fatalf("expected 0 notifications, got %v", notes)
		}
	})

	t.Run("warns when deferred MCP tools have neither tool_search nor codemode", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDeferred, nil, tsasmSetupOptions{withoutToolSearch: true})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if len(res.harness.Session.GetActiveToolNames()) != 0 {
			t.Fatalf("expected 0 active tools, got %v", res.harness.Session.GetActiveToolNames())
		}
		res.notifyMu.Lock()
		notes := slices.Clone(*res.notifications)
		res.notifyMu.Unlock()
		want := []string{"MCP tools are only reachable from the codemode or tool_search tool, but neither is active; they cannot be called."}
		if !slices.Equal(notes, want) {
			t.Fatalf("notifications = %v, want %v", notes, want)
		}
	})

	t.Run("attaches MCP result outputSchema for codemode output typing", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{})
		searchDef := res.harness.Session.GetToolDefinition("mcp__docs__search")
		failDef := res.harness.Session.GetToolDefinition("mcp__docs__fail")
		if searchDef == nil || searchDef.OutputSchema == nil || searchDef.OutputSchema.Object == nil {
			t.Fatalf("expected OutputSchema on search tool")
		}
		if failDef == nil || failDef.OutputSchema == nil || failDef.OutputSchema.Object == nil {
			t.Fatalf("expected OutputSchema on fail tool")
		}
		wantSearch := `{ content: Array<{ data?: string; mimeType?: string; text?: string; type: "text" | "image" | "audio" | "resource_link" | "resource"; [key: string]: unknown; }>; isError?: boolean; structuredContent?: { hits: Array<string>; }; [key: string]: unknown; }`
		if got := codemode.RenderToolOutputType(searchDef.OutputSchema.Object); got != wantSearch {
			t.Fatalf("rendered search output type = %q, want %q", got, wantSearch)
		}
		wantFail := `{ content: Array<{ data?: string; mimeType?: string; text?: string; type: "text" | "image" | "audio" | "resource_link" | "resource"; [key: string]: unknown; }>; isError?: boolean; structuredContent?: unknown; [key: string]: unknown; }`
		if got := codemode.RenderToolOutputType(failDef.OutputSchema.Object); got != wantFail {
			t.Fatalf("rendered fail output type = %q, want %q", got, wantFail)
		}
	})

	t.Run("activates another extension codemode tool when it reaches other tools", func(t *testing.T) {
		otherExec := func(pi core.ExtensionAPI) error {
			pi.RegisterTool(core.ToolDefinition{
				Name:          "codemode",
				Label:         "codemode",
				Description:   "Another extension's codemode tool.",
				Parameters:    typebox.Object(omap.NewMap[string, *typebox.Schema]()),
				DefaultActive: tsasmBoolPtr(false),
				ReachesTools:  func(*jsonx.Object) core.ToolReachesResult { return core.ToolReachesResult{All: true} },
				Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
					return agent.AgentToolResult{}, nil
				},
			})
			return nil
		}
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			withoutCodemode:    true,
			extensionFactories: []core.ExtensionFactory{otherExec},
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("active = %v, want [codemode]", got)
		}
	})

	t.Run("does not activate another extension tool named codemode without ReachesTools", func(t *testing.T) {
		otherExec := func(pi core.ExtensionAPI) error {
			pi.RegisterTool(core.ToolDefinition{
				Name:          "codemode",
				Label:         "codemode",
				Description:   "Another extension's codemode tool.",
				Parameters:    typebox.Object(omap.NewMap[string, *typebox.Schema]()),
				DefaultActive: tsasmBoolPtr(false),
				Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
					return agent.AgentToolResult{}, nil
				},
			})
			return nil
		}
		res := tsasmSetup(t, core.McpExposureCodemode, nil, tsasmSetupOptions{
			withoutCodemode:    true,
			extensionFactories: []core.ExtensionFactory{otherExec},
		})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if len(res.harness.Session.GetActiveToolNames()) != 0 {
			t.Fatalf("expected 0 active tools, got %v", res.harness.Session.GetActiveToolNames())
		}
	})

	type slowCfg struct {
		name              string
		exposure          *core.McpExposure
		toolExposure      map[string]core.McpExposure
		instructions      string
		initializeDelayMs float64
		startupWaitMs     *int64
	}
	setupSlow := func(t *testing.T, sc slowCfg) (*testkit.SuiteHarness, *[]string, *sync.Mutex, *[]string, *sync.Mutex) {
		t.Helper()
		name := sc.name
		if name == "" {
			name = "slow"
		}
		var calls []string
		var callsMu sync.Mutex
		var notes []string
		var notesMu sync.Mutex
		cfg := McpServerConfig{Url: tsasmStrPtr("http://unused.invalid"), Exposure: sc.exposure}
		if len(sc.toolExposure) > 0 {
			rawObj := jsonx.ObjectOf("url", "http://unused.invalid")
			if sc.exposure != nil {
				rawObj.Set("exposure", string(*sc.exposure))
			}
			teObj := jsonx.NewObject()
			for k, v := range sc.toolExposure {
				teObj.Set(k, string(v))
			}
			rawObj.Set("toolExposure", teObj)
			v, err := ValidateMcpServerConfig(name, rawObj)
			if err != nil || v == nil {
				t.Fatalf("ValidateMcpServerConfig: %v", err)
			}
			cfg = *v
		}
		entry := McpServerEntry{Name: name, Config: cfg, Source: "test"}
		extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
			Cwd: t.TempDir(),
			Factories: []core.InlineExtension{
				{Name: "codemode", Factory: tsasmMockCodemodeExtension()},
				{Name: "tool-search", Factory: tsasmMockToolSearchExtension()},
				{
					Name: "mcp",
					Factory: CreateMcpExtension(McpExtensionOptions{
						LoadConfig: func(core.ExtensionContext) LoadedMcpConfig {
							return LoadedMcpConfig{Servers: []McpServerEntry{entry}}
						},
						CreateTransport: func(McpServerEntry, string, mcp.AuthProvider) (mcp.McpTransport, error) {
							pair := tsasmStartFakeServer(t, &calls, &callsMu, tsasmFakeServerOpts{
								instructions:      sc.instructions,
								initializeDelayMs: sc.initializeDelayMs,
							})
							return pair.Client, nil
						},
						StartupWaitMs: sc.startupWaitMs,
					}),
				},
			},
		})
		if len(extRes.Errors) > 0 {
			t.Fatalf("LoadExtensionsWithOptions: %+v", extRes.Errors)
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			InitialActiveToolNames: []string{},
			ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
				ExtensionsResult: &extRes,
			}),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		t.Cleanup(h.Cleanup)
		if err := h.Session.BindExtensions(ctx, core.ExtensionBindings{
			UIContext: testkit.CreateTestUiContext(&testkit.TestUiContextOverrides{
				Notify: func(message string, _ ...string) {
					notesMu.Lock()
					notes = append(notes, message)
					notesMu.Unlock()
				},
			}),
		}); err != nil {
			t.Fatalf("BindExtensions: %v", err)
		}
		return h, &calls, &callsMu, &notes, &notesMu
	}

	t.Run("does not hold the first prompt for codemode servers that are still connecting", func(t *testing.T) {
		h, _, _, _, _ := setupSlow(t, slowCfg{initializeDelayMs: math.Inf(1)})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := testkit.GetAssistantTexts(h); !slices.Equal(got, []string{"ready"}) {
			t.Fatalf("assistant texts = %v", got)
		}
		if got := tsasmDeclaredToolNames(h); !slices.Equal(got, []string{"codemode"}) {
			t.Fatalf("declared = %v", got)
		}
	})

	t.Run("lists a server before it connects and appends its summary with the next prompt", func(t *testing.T) {
		h, _, _, _, _ := setupSlow(t, slowCfg{instructions: "Slow docs.\nMore.", initializeDelayMs: 30})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("one"), nil)},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("two"), nil)},
		})
		if err := h.Session.Prompt(ctx, "first"); err != nil {
			t.Fatalf("Prompt 1: %v", err)
		}
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(h.Session.GetCallableToolNames(), "mcp__slow__search")
		})
		if err := h.Session.Prompt(ctx, "second"); err != nil {
			t.Fatalf("Prompt 2: %v", err)
		}
		var sysMsgs []*ai.SystemMessage
		sysIdxs := []int{}
		firstAsstIdx := -1
		for idx, msg := range h.Session.Messages() {
			if sm, ok := msg.(*ai.SystemMessage); ok && sm != nil {
				sysMsgs = append(sysMsgs, sm)
				sysIdxs = append(sysIdxs, idx)
			}
			if firstAsstIdx < 0 {
				if _, ok := msg.(*ai.AssistantMessage); ok {
					firstAsstIdx = idx
				}
			}
		}
		if len(sysMsgs) != 2 {
			t.Fatalf("expected 2 system messages, got %d", len(sysMsgs))
		}
		s0, _ := sysMsgs[0].Sections.Get(McpServersSection)
		if s0 == nil || !strings.Contains(*s0, "- mcp__slow (codemode)\n") {
			t.Fatalf("unexpected first mcp_servers section: %v", s0)
		}
		s1, _ := sysMsgs[1].Sections.Get(McpServersSection)
		if s1 == nil || !strings.Contains(*s1, "- mcp__slow (codemode): Slow docs.") {
			t.Fatalf("unexpected appended mcp_servers patch: %v", s1)
		}
		if sysIdxs[1] <= firstAsstIdx {
			t.Fatalf("expected appended system message after first assistant message (sys1=%d, asst=%d)", sysIdxs[1], firstAsstIdx)
		}
	})

	t.Run("waits for servers with direct tools before listing the servers", func(t *testing.T) {
		expDirect := core.McpExposureDirect
		h, _, _, _, _ := setupSlow(t, slowCfg{
			exposure:          &expDirect,
			toolExposure:      map[string]core.McpExposure{"shot": core.McpExposureCodemode},
			instructions:      "Slow docs.",
			initializeDelayMs: 30,
		})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		sec, ok := tsasmServersSection(h)
		if !ok || !strings.Contains(sec, "- mcp__slow (codemode): Slow docs.") {
			t.Fatalf("unexpected mcp_servers section (ok=%v): %q", ok, sec)
		}
	})

	t.Run("leaves servers with only direct tools out of the servers section", func(t *testing.T) {
		expDirect := core.McpExposureDirect
		h, _, _, _, _ := setupSlow(t, slowCfg{exposure: &expDirect, initializeDelayMs: 0})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if _, ok := tsasmServersSection(h); ok {
			t.Fatalf("expected no mcp_servers section when all tools are direct")
		}
	})

	t.Run("waits for the servers a codemode script names", func(t *testing.T) {
		h, calls, callsMu, _, _ := setupSlow(t, slowCfg{initializeDelayMs: 30})
		stopTool := ai.StopReasonToolUse
		arg := jsonx.ObjectOf(
			"code", `return (await tools.mcp__slow__search({ query: "q" })).structuredContent;`,
			"calls", []any{jsonx.ObjectOf("name", "mcp__slow__search", "arguments", jsonx.ObjectOf("query", "q"))},
		)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", arg, nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := h.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, err := testkit.GetToolResult(h, "codemode")
		if err != nil || tr.IsError {
			t.Fatalf("GetToolResult(codemode): tr=%+v err=%v", tr, err)
		}
		callsMu.Lock()
		gotCalls := slices.Clone(*calls)
		callsMu.Unlock()
		if !slices.Equal(gotCalls, []string{`search:{"query":"q"}`}) {
			t.Fatalf("calls = %v", gotCalls)
		}
	})

	t.Run("waits for servers whose script identifiers differ from their names", func(t *testing.T) {
		h, calls, callsMu, _, _ := setupSlow(t, slowCfg{name: "slow-docs", initializeDelayMs: 30})
		stopTool := ai.StopReasonToolUse
		arg := jsonx.ObjectOf(
			"code", `await tools.mcp__slow_docs__search({ query: "q" });`,
			"calls", []any{jsonx.ObjectOf("name", "mcp__slow_docs__search", "arguments", jsonx.ObjectOf("query", "q"))},
		)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", arg, nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := h.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, err := testkit.GetToolResult(h, "codemode")
		if err != nil || tr.IsError {
			t.Fatalf("GetToolResult(codemode): tr=%+v err=%v", tr, err)
		}
		callsMu.Lock()
		gotCalls := slices.Clone(*calls)
		callsMu.Unlock()
		if !slices.Equal(gotCalls, []string{`search:{"query":"q"}`}) {
			t.Fatalf("calls = %v", gotCalls)
		}
	})

	t.Run("does not wait for servers a codemode script does not name", func(t *testing.T) {
		h, _, _, _, _ := setupSlow(t, slowCfg{initializeDelayMs: math.Inf(1)})
		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("codemode", jsonx.ObjectOf("code", "return 1 + 1;"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := h.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, _ := testkit.GetToolResult(h, "codemode")
		if testkit.GetMessageText(tr) != "2" {
			t.Fatalf("codemode result = %q, want 2", testkit.GetMessageText(tr))
		}
	})

	t.Run("waits for servers before tool_search searches", func(t *testing.T) {
		expDef := core.McpExposureDeferred
		h, _, _, _, _ := setupSlow(t, slowCfg{exposure: &expDef, initializeDelayMs: 30})
		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "search the docs", "limit", 1), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__slow__search", jsonx.ObjectOf("query", "late"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := h.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, _ := testkit.GetToolResult(h, "mcp__slow__search")
		if got := testkit.GetMessageText(tr); got != "late guide\nlate faq" {
			t.Fatalf("result = %q", got)
		}
	})

	t.Run("holds the first prompt for servers with direct tools", func(t *testing.T) {
		expDirect := core.McpExposureDirect
		h, _, _, _, _ := setupSlow(t, slowCfg{exposure: &expDirect, initializeDelayMs: 30})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !slices.Contains(tsasmDeclaredToolNames(h), "mcp__slow__search") {
			t.Fatalf("expected mcp__slow__search declared")
		}
	})

	t.Run("holds the first prompt for servers with direct tools only up to startupWaitMs", func(t *testing.T) {
		expDirect := core.McpExposureDirect
		h, _, _, notes, notesMu := setupSlow(t, slowCfg{
			exposure:          &expDirect,
			initializeDelayMs: math.Inf(1),
			startupWaitMs:     tsasmInt64Ptr(20),
		})
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := testkit.GetAssistantTexts(h); !slices.Equal(got, []string{"ready"}) {
			t.Fatalf("assistant texts = %v", got)
		}
		if len(h.Session.GetActiveToolNames()) != 0 {
			t.Fatalf("expected 0 active tools, got %v", h.Session.GetActiveToolNames())
		}
		notesMu.Lock()
		gotNotes := slices.Clone(*notes)
		notesMu.Unlock()
		want := []string{"MCP servers are still connecting; their tools become available once connected."}
		if !slices.Equal(gotNotes, want) {
			t.Fatalf("notifications = %v, want %v", gotNotes, want)
		}
	})

	t.Run("does not let codemode call itself or inactive direct tools", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{})
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		res.harness.Session.SetActiveToolsByName([]string{"codemode", "mcp__docs__search"})
		if got := res.harness.Session.GetCallableToolNames(); !slices.Equal(got, []string{"mcp__docs__search"}) {
			t.Fatalf("callable tools = %v, want [mcp__docs__search]", got)
		}
	})

	t.Run("activates tool_search for deferred MCP tools and keeps loaded tools declared on the branch", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDeferred, nil, tsasmSetupOptions{})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "search the docs", "limit", 1), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__docs__search", jsonx.ObjectOf("query", "loaded"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "find a docs tool"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := res.harness.Session.GetActiveToolNames(); !slices.Equal(got, []string{"tool_search", "mcp__docs__search"}) {
			t.Fatalf("active tools = %v", got)
		}
		var tsDesc string
		for _, tl := range res.harness.Session.Agent.Tools() {
			if tl.Name == "tool_search" {
				tsDesc = tl.Description
			}
		}
		if tsDesc != tsasmToolSearchDescription {
			t.Fatalf("tool_search description = %q", tsDesc)
		}
		searchTr, _ := testkit.GetToolResult(res.harness, "tool_search")
		wantSearch := "Loaded 1 tool. They are available from your next call:\n- mcp__docs__search: Search the docs."
		if got := testkit.GetMessageText(searchTr); got != wantSearch {
			t.Fatalf("tool_search output = %q, want %q", got, wantSearch)
		}
		var loadMsgs []*ai.SystemMessage
		for _, msg := range res.harness.Session.Messages() {
			if sys, ok := msg.(*ai.SystemMessage); ok && sys != nil {
				for _, ta := range sys.ToolsAdded {
					if ta.Name == "mcp__docs__search" {
						loadMsgs = append(loadMsgs, sys)
					}
				}
			}
		}
		if len(loadMsgs) != 1 || len(loadMsgs[0].ToolsAdded) != 1 || loadMsgs[0].ToolsAdded[0].Name != "mcp__docs__search" {
			t.Fatalf("unexpected load system messages: %+v", loadMsgs)
		}
		mcpTr, _ := testkit.GetToolResult(res.harness, "mcp__docs__search")
		if got := testkit.GetMessageText(mcpTr); got != "loaded guide\nloaded faq" {
			t.Fatalf("search output = %q", got)
		}

		branch := res.harness.SessionManager.GetBranch()
		firstUserID := tsasmFindFirstEntryID(t, res.harness.SessionManager, "user")
		lastID := branch[len(branch)-1].GetId()
		if _, err := res.harness.Session.NavigateTree(ctx, firstUserID, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree(firstUser): %v", err)
		}
		if slices.Contains(res.harness.Session.GetActiveToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search dropped before load")
		}
		if _, err := res.harness.Session.NavigateTree(ctx, lastID, &core.NavigateTreeOptions{Summarize: false}); err != nil {
			t.Fatalf("NavigateTree(last): %v", err)
		}
		if !slices.Contains(res.harness.Session.GetActiveToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search restored on branch")
		}
	})

	t.Run("finds nothing to load when every matching tool is already declared", func(t *testing.T) {
		res := tsasmSetup(t, core.McpExposureDirect, nil, tsasmSetupOptions{builtInTools: []string{"tool_search"}})
		stopTool := ai.StopReasonToolUse
		res.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "docs"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := res.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tr, _ := testkit.GetToolResult(res.harness, "tool_search")
		if got := testkit.GetMessageText(tr); got != "No matching tools found." {
			t.Fatalf("tool_search output = %q", got)
		}
	})
}

func TestAgentSessionMcpExtensionRegisteredServers(t *testing.T) {
	ctx := context.Background()

	setupRegistered := func(t *testing.T, plugins []core.ExtensionFactory, configured []McpServerEntry) (*testkit.SuiteHarness, *[]McpServerEntry, *sync.Mutex) {
		t.Helper()
		var connected []McpServerEntry
		var connectedMu sync.Mutex
		var calls []string
		var callsMu sync.Mutex

		var factories []core.InlineExtension
		for i, p := range plugins {
			factories = append(factories, core.InlineExtension{Name: fmt.Sprintf("plugin-%d", i), Factory: p})
		}
		factories = append(factories, core.InlineExtension{Name: "codemode", Factory: tsasmMockCodemodeExtension()})
		factories = append(factories, core.InlineExtension{
			Name: "mcp",
			Factory: CreateMcpExtension(McpExtensionOptions{
				LoadConfig: func(core.ExtensionContext) LoadedMcpConfig {
					return LoadedMcpConfig{Servers: configured}
				},
				CreateTransport: func(entry McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.McpTransport, error) {
					connectedMu.Lock()
					connected = append(connected, entry)
					connectedMu.Unlock()
					pair := tsasmStartFakeServer(t, &calls, &callsMu, tsasmFakeServerOpts{})
					return pair.Client, nil
				},
			}),
		})

		extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
			Cwd:       t.TempDir(),
			Factories: factories,
		})
		if len(extRes.Errors) > 0 {
			t.Fatalf("LoadExtensionsWithOptions errors: %+v", extRes.Errors)
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			InitialActiveToolNames: []string{},
			ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
				ExtensionsResult: &extRes,
			}),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		t.Cleanup(h.Cleanup)
		if err := h.Session.BindExtensions(ctx, core.ExtensionBindings{
			UIContext: testkit.CreateTestUiContext(),
		}); err != nil {
			t.Fatalf("BindExtensions: %v", err)
		}
		return h, &connected, &connectedMu
	}

	t.Run("connects servers registered while extensions load", func(t *testing.T) {
		expDirect := core.McpExposureDirect
		h, connected, connectedMu := setupRegistered(t, []core.ExtensionFactory{
			func(pi core.ExtensionAPI) error {
				pi.RegisterMcpServer("plugin", core.McpServerConfig{
					Url:      tsasmStrPtr("http://plugin.invalid"),
					Exposure: &expDirect,
				})
				return nil
			},
		}, nil)
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("ready"), nil)},
		})
		if err := h.Session.Prompt(ctx, "start"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		connectedMu.Lock()
		gotConn := slices.Clone(*connected)
		connectedMu.Unlock()
		if len(gotConn) != 1 || gotConn[0].Name != "plugin" || gotConn[0].Scope != "extension" {
			t.Fatalf("unexpected connected entries: %+v", gotConn)
		}
		if !slices.Contains(h.Session.GetActiveToolNames(), "mcp__plugin__search") {
			t.Fatalf("expected mcp__plugin__search active, got %v", h.Session.GetActiveToolNames())
		}
	})

	t.Run("connects and disconnects servers registered during the session", func(t *testing.T) {
		var api core.ExtensionAPI
		h, connected, connectedMu := setupRegistered(t, []core.ExtensionFactory{
			func(pi core.ExtensionAPI) error {
				api = pi
				return nil
			},
		}, nil)
		api.RegisterMcpServer("late", core.McpServerConfig{Url: tsasmStrPtr("http://late.invalid")})
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(h.Session.GetCallableToolNames(), "mcp__late__search")
		})
		connectedMu.Lock()
		gotConn := slices.Clone(*connected)
		connectedMu.Unlock()
		if len(gotConn) != 1 || gotConn[0].Name != "late" {
			t.Fatalf("unexpected connected entries: %+v", gotConn)
		}
		if !slices.Contains(h.Session.GetActiveToolNames(), "codemode") {
			t.Fatalf("expected codemode activated, got %v", h.Session.GetActiveToolNames())
		}

		api.UnregisterMcpServer("late")
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return !slices.Contains(h.Session.GetCallableToolNames(), "mcp__late__search")
		})
	})

	for _, name := range []string{"my-docs", "my_docs"} {
		t.Run("prefers the mcp.json server over a registered "+name, func(t *testing.T) {
			configured := McpServerEntry{
				Name:   "my-docs",
				Config: McpServerConfig{Url: tsasmStrPtr("http://config.invalid")},
				Source: "mcp.json",
			}
			_, connected, connectedMu := setupRegistered(t, []core.ExtensionFactory{
				func(pi core.ExtensionAPI) error {
					pi.RegisterMcpServer(name, core.McpServerConfig{Url: tsasmStrPtr("http://plugin.invalid")})
					return nil
				},
			}, []McpServerEntry{configured})
			tsasmWaitFor(t, 5*time.Second, func() bool {
				connectedMu.Lock()
				defer connectedMu.Unlock()
				return len(*connected) >= 1
			})
			time.Sleep(20 * time.Millisecond)
			connectedMu.Lock()
			gotConn := slices.Clone(*connected)
			connectedMu.Unlock()
			if len(gotConn) != 1 || gotConn[0].Name != "my-docs" || gotConn[0].Source != "mcp.json" {
				t.Fatalf("expected only mcp.json server connected, got %+v", gotConn)
			}
		})
	}

	t.Run("rejects names another extension registered", func(t *testing.T) {
		var caught []string
		setupRegistered(t, []core.ExtensionFactory{
			func(pi core.ExtensionAPI) error {
				pi.RegisterMcpServer("taken", core.McpServerConfig{Url: tsasmStrPtr("http://x.invalid")})
				pi.RegisterMcpServer("taken", core.McpServerConfig{Url: tsasmStrPtr("http://y.invalid")})
				pi.RegisterMcpServer("my-server", core.McpServerConfig{Url: tsasmStrPtr("http://x.invalid")})
				return nil
			},
			func(pi core.ExtensionAPI) error {
				for _, name := range []string{"taken", "my_server"} {
					func() {
						defer func() {
							if r := recover(); r != nil {
								caught = append(caught, fmt.Sprint(r))
							}
						}()
						pi.RegisterMcpServer(name, core.McpServerConfig{Url: tsasmStrPtr("http://z.invalid")})
					}()
				}
				return nil
			},
		}, nil)
		if len(caught) != 2 ||
			!strings.Contains(caught[0], `MCP server "taken" is already registered by extension`) ||
			caught[1] != `MCP server "my_server" conflicts with registered server "my-server"` {
			t.Fatalf("unexpected conflict errors: %v", caught)
		}
	})

	t.Run("reports registered servers when no extension connects them", func(t *testing.T) {
		extRes := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
			Cwd: t.TempDir(),
			Factories: []core.InlineExtension{
				{
					Name: "orphan-ext",
					Factory: func(pi core.ExtensionAPI) error {
						pi.RegisterMcpServer("orphan", core.McpServerConfig{Url: tsasmStrPtr("http://orphan.invalid")})
						return nil
					},
				},
			},
		})
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			ResourceLoader: testkit.CreateTestResourceLoader(&testkit.CreateTestResourceLoaderOptions{
				ExtensionsResult: &extRes,
			}),
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		t.Cleanup(h.Cleanup)
		var errors []string
		var errMu sync.Mutex
		if err := h.Session.BindExtensions(ctx, core.ExtensionBindings{
			OnError: func(e core.ExtensionError) {
				errMu.Lock()
				errors = append(errors, e.Error)
				errMu.Unlock()
			},
		}); err != nil {
			t.Fatalf("BindExtensions: %v", err)
		}
		errMu.Lock()
		gotErrs := slices.Clone(errors)
		errMu.Unlock()
		if len(gotErrs) != 1 || !strings.Contains(gotErrs[0], `MCP server "orphan" is registered, but no loaded extension`) {
			t.Fatalf("unexpected unhandled MCP server errors: %v", gotErrs)
		}
	})
}

func TestAgentSessionMcpToolsAfterResumeAndReload(t *testing.T) {
	ctx := context.Background()

	type resumeSetup struct {
		harness     *testkit.SuiteHarness
		connected   *[]string
		connectedMu *sync.Mutex
	}
	setupResume := func(t *testing.T, sessionManager *core.SessionManager, extra []core.ExtensionFactory, delayMs float64) *resumeSetup {
		t.Helper()
		var connected []string
		var connectedMu sync.Mutex
		var calls []string
		var callsMu sync.Mutex
		expDef := core.McpExposureDeferred
		srvEntry := McpServerEntry{
			Name:   "docs",
			Config: McpServerConfig{Url: tsasmStrPtr("http://unused.invalid"), Exposure: &expDef},
			Source: "test",
		}
		tmpCwd := t.TempDir()
		buildExts := func() core.LoadExtensionsResult {
			var factories []core.InlineExtension
			for i, ef := range extra {
				factories = append(factories, core.InlineExtension{Name: fmt.Sprintf("extra-%d", i), Factory: ef})
			}
			factories = append(factories,
				core.InlineExtension{Name: "tool-search", Factory: tsasmMockToolSearchExtension()},
				core.InlineExtension{
					Name: "mcp",
					Factory: CreateMcpExtension(McpExtensionOptions{
						LoadConfig: func(core.ExtensionContext) LoadedMcpConfig {
							return LoadedMcpConfig{Servers: []McpServerEntry{srvEntry}}
						},
						CreateTransport: func(entry McpServerEntry, _ string, _ mcp.AuthProvider) (mcp.McpTransport, error) {
							connectedMu.Lock()
							connected = append(connected, entry.Name)
							connectedMu.Unlock()
							pair := tsasmStartFakeServer(t, &calls, &callsMu, tsasmFakeServerOpts{initializeDelayMs: delayMs})
							return pair.Client, nil
						},
					}),
				},
			)
			res := core.LoadExtensionsWithOptions(ctx, nil, &core.LoadExtensionsOptions{
				Cwd:       tmpCwd,
				Factories: factories,
			})
			if len(res.Errors) > 0 {
				t.Fatalf("LoadExtensionsWithOptions: %+v", res.Errors)
			}
			return res
		}
		baseLoader := testkit.CreateTestResourceLoader()
		loader := &tsasmReloadableLoader{
			ResourceLoader: baseLoader,
			exts:           buildExts(),
			load:           buildExts,
		}
		h, err := testkit.CreateSuiteHarness(ctx, &testkit.SuiteHarnessOptions{
			Cwd:            tmpCwd,
			ResourceLoader: loader,
			SessionManager: sessionManager,
		})
		if err != nil {
			t.Fatalf("CreateSuiteHarness: %v", err)
		}
		t.Cleanup(h.Cleanup)
		if err := h.Session.BindExtensions(ctx, core.ExtensionBindings{
			UIContext: testkit.CreateTestUiContext(),
		}); err != nil {
			t.Fatalf("BindExtensions: %v", err)
		}
		return &resumeSetup{harness: h, connected: &connected, connectedMu: &connectedMu}
	}

	loadDocsSearch := func(t *testing.T, h *testkit.SuiteHarness) {
		t.Helper()
		stopTool := ai.StopReasonToolUse
		h.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("tool_search", jsonx.ObjectOf("query", "search the docs", "limit", 1), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("loaded"), nil)},
		})
		if err := h.Session.Prompt(ctx, "load"); err != nil {
			t.Fatalf("Prompt(load): %v", err)
		}
		if !slices.Contains(h.Session.GetActiveToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search active after loadDocsSearch, got %v", h.Session.GetActiveToolNames())
		}
	}

	t.Run("declares tools tool_search loaded again on resume once their server connects", func(t *testing.T) {
		first := setupResume(t, nil, nil, 0)
		loadDocsSearch(t, first.harness)

		second := setupResume(t, first.harness.SessionManager, nil, 0)
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(second.harness.Session.GetActiveToolNames(), "mcp__docs__search")
		})
		stopTool := ai.StopReasonToolUse
		second.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{
				Message: providers.FauxAssistantMessage(
					providers.FauxAssistantBlock{Block: providers.FauxToolCall("mcp__docs__search", jsonx.ObjectOf("query", "again"), nil)},
					&providers.FauxAssistantMessageOptions{StopReason: &stopTool},
				),
			},
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := second.harness.Session.Prompt(ctx, "use it"); err != nil {
			t.Fatalf("Prompt(use it): %v", err)
		}
		tr, _ := testkit.GetToolResult(second.harness, "mcp__docs__search")
		if got := testkit.GetMessageText(tr); got != "again guide\nagain faq" {
			t.Fatalf("result = %q", got)
		}
		for _, msg := range second.harness.Session.Messages() {
			if sys, ok := msg.(*ai.SystemMessage); ok && sys != nil && len(sys.ToolsRemoved) > 0 {
				t.Fatalf("expected no tool removals, got %+v", sys.ToolsRemoved)
			}
		}
	})

	for _, tc := range []struct {
		label   string
		loadout []string
		kept    bool
	}{
		{label: "drops", loadout: []string{"read"}, kept: false},
		{label: "keeps", loadout: nil, kept: true},
	} {
		t.Run(tc.label+" restored tools when an extension sets the loadout before they register", func(t *testing.T) {
			first := setupResume(t, nil, nil, 0)
			loadDocsSearch(t, first.harness)

			setLoadout := func(pi core.ExtensionAPI) error {
				pi.OnSessionStart(func(context.Context, *core.SessionStartEvent, core.ExtensionContext) (*core.NoResult, error) {
					if tc.loadout != nil {
						pi.SetActiveTools(slices.Clone(tc.loadout))
					} else {
						pi.SetActiveTools(append(pi.GetActiveTools(), "read"))
					}
					return nil, nil
				})
				return nil
			}
			second := setupResume(t, first.harness.SessionManager, []core.ExtensionFactory{setLoadout}, 0)
			tsasmWaitFor(t, 5*time.Second, func() bool {
				return slices.Contains(tsasmAllToolNames(second.harness), "mcp__docs__search")
			})
			if got := slices.Contains(second.harness.Session.GetActiveToolNames(), "mcp__docs__search"); got != tc.kept {
				t.Fatalf("kept = %v, want %v (active=%v)", got, tc.kept, second.harness.Session.GetActiveToolNames())
			}
		})
	}

	dropLoader := func(pi core.ExtensionAPI) error {
		pi.RegisterTool(core.ToolDefinition{
			Name:        "loader",
			Label:       "loader",
			Description: "Loads the extension's tools.",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(context.Context, string, *jsonx.Object, agent.AgentToolUpdateCallback, core.ExtensionToolContext) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{}, nil
			},
		})
		pi.OnSessionStart(func(context.Context, *core.SessionStartEvent, core.ExtensionContext) (*core.NoResult, error) {
			var filtered []string
			for _, n := range pi.GetActiveTools() {
				if n != "loader" {
					filtered = append(filtered, n)
				}
			}
			pi.SetActiveTools(filtered)
			return nil, nil
		})
		return nil
	}

	t.Run("keeps restored tools when an extension deactivates only its own tool before they register", func(t *testing.T) {
		first := setupResume(t, nil, []core.ExtensionFactory{dropLoader}, 0)
		loadDocsSearch(t, first.harness)
		second := setupResume(t, first.harness.SessionManager, []core.ExtensionFactory{dropLoader}, 0)
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(tsasmAllToolNames(second.harness), "mcp__docs__search")
		})
		active := second.harness.Session.GetActiveToolNames()
		if !slices.Contains(active, "mcp__docs__search") || slices.Contains(active, "loader") {
			t.Fatalf("unexpected active tools: %v", active)
		}
	})

	t.Run("keeps tools tool_search loaded after /reload when an extension deactivates only its own tool", func(t *testing.T) {
		res := setupResume(t, nil, []core.ExtensionFactory{dropLoader}, 0)
		loadDocsSearch(t, res.harness)
		if err := res.harness.Session.Reload(ctx); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		tsasmWaitFor(t, 5*time.Second, func() bool {
			res.connectedMu.Lock()
			defer res.connectedMu.Unlock()
			return slices.Equal(*res.connected, []string{"docs", "docs"})
		})
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(res.harness.Session.GetActiveToolNames(), "mcp__docs__search")
		})
		if slices.Contains(res.harness.Session.GetActiveToolNames(), "loader") {
			t.Fatalf("expected loader inactive, got %v", res.harness.Session.GetActiveToolNames())
		}
	})

	t.Run("does not activate restored tools that register after the next prompt starts", func(t *testing.T) {
		first := setupResume(t, nil, nil, 0)
		loadDocsSearch(t, first.harness)

		second := setupResume(t, first.harness.SessionManager, nil, 200)
		second.harness.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("done"), nil)},
		})
		if err := second.harness.Session.Prompt(ctx, "go"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(tsasmAllToolNames(second.harness), "mcp__docs__search")
		})
		if slices.Contains(second.harness.Session.GetActiveToolNames(), "mcp__docs__search") {
			t.Fatalf("expected mcp__docs__search NOT activated after registering mid/post-prompt, got %v", second.harness.Session.GetActiveToolNames())
		}
	})

	t.Run("declares tools tool_search loaded again after /reload", func(t *testing.T) {
		res := setupResume(t, nil, nil, 0)
		loadDocsSearch(t, res.harness)
		if err := res.harness.Session.Reload(ctx); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		tsasmWaitFor(t, 5*time.Second, func() bool {
			res.connectedMu.Lock()
			defer res.connectedMu.Unlock()
			return slices.Equal(*res.connected, []string{"docs", "docs"})
		})
		tsasmWaitFor(t, 5*time.Second, func() bool {
			return slices.Contains(res.harness.Session.GetActiveToolNames(), "mcp__docs__search")
		})
	})
}
