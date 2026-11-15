// Ported from packages/coding-agent/test/mcp-extension.test.ts (pi v1.5.2).

package mcpext

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codemode"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/mcp"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
)

const tmeTokenHeader = "Bearer ${TOKEN}"

func tmeSetupConfigDirs(t *testing.T, globalCfg any, projectCfg any) (agentDir, cwd string) {
	t.Helper()
	root := t.TempDir()
	agentDir = filepath.Join(root, "agent")
	cwd = filepath.Join(root, "project")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".pi"), 0o755); err != nil {
		t.Fatalf("mkdir .pi: %v", err)
	}
	gBytes, err := jsonx.Marshal(globalCfg)
	if err != nil {
		t.Fatalf("marshal global: %v", err)
	}
	pBytes, err := jsonx.Marshal(projectCfg)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), gBytes, 0o600); err != nil {
		t.Fatalf("write global mcp.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".pi", "mcp.json"), pBytes, 0o600); err != nil {
		t.Fatalf("write project mcp.json: %v", err)
	}
	return agentDir, cwd
}

func tmeContainsAll(t *testing.T, items []string, wants []string) {
	t.Helper()
	if len(items) != len(wants) {
		t.Fatalf("got %d items %v, want %d items containing %v", len(items), items, len(wants), wants)
	}
	for i, w := range wants {
		if !strings.Contains(items[i], w) {
			t.Fatalf("item[%d] = %q, want substring %q", i, items[i], w)
		}
	}
}

func TestMcpConfig(t *testing.T) {
	t.Run("merges global and trusted project servers and validates entries", func(t *testing.T) {
		globalServers := jsonx.NewObject()
		globalServers.Set("shared", jsonx.ObjectOf("command", "global-cmd"))
		globalServers.Set("remote", jsonx.ObjectOf(
			"url", "https://example.com/mcp",
			"headers", jsonx.ObjectOf("Authorization", tmeTokenHeader),
		))
		globalServers.Set("off", jsonx.ObjectOf("command", "x", "enabled", false))
		globalServers.Set("bad", jsonx.ObjectOf("args", []any{"no command"}))
		globalServers.Set("legacy", jsonx.ObjectOf("type", "sse", "url", "https://example.com/sse"))
		globalServers.Set("badUrl", jsonx.ObjectOf("url", "example.com/mcp"))
		globalServers.Set("bad name", jsonx.ObjectOf("command", "x"))

		agentDir, cwd := tmeSetupConfigDirs(t,
			jsonx.ObjectOf("mcpServers", globalServers),
			jsonx.ObjectOf("mcpServers", jsonx.ObjectOf("shared", jsonx.ObjectOf("command", "project-cmd", "exposure", "direct"))),
		)

		trusted := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if len(trusted.Servers) != 3 {
			t.Fatalf("expected 3 servers, got %d: %+v", len(trusted.Servers), trusted.Servers)
		}
		if trusted.Servers[0].Name != "shared" || trusted.Servers[0].Scope != "project" ||
			trusted.Servers[0].Config.Command == nil || *trusted.Servers[0].Config.Command != "project-cmd" ||
			trusted.Servers[0].Config.Exposure == nil || *trusted.Servers[0].Config.Exposure != core.McpExposureDirect {
			t.Fatalf("unexpected server[0]: %+v", trusted.Servers[0])
		}
		authVal, _ := trusted.Servers[1].Config.Headers.Get("Authorization")
		if trusted.Servers[1].Name != "remote" || trusted.Servers[1].Scope != "global" ||
			trusted.Servers[1].Config.Url == nil || *trusted.Servers[1].Config.Url != "https://example.com/mcp" ||
			authVal != tmeTokenHeader {
			t.Fatalf("unexpected server[1]: %+v", trusted.Servers[1])
		}
		if trusted.Servers[2].Name != "off" || trusted.Servers[2].Scope != "global" ||
			trusted.Servers[2].Config.Command == nil || *trusted.Servers[2].Config.Command != "x" ||
			trusted.Servers[2].Config.Enabled == nil || *trusted.Servers[2].Config.Enabled {
			t.Fatalf("unexpected server[2]: %+v", trusted.Servers[2])
		}
		tmeContainsAll(t, trusted.Errors, []string{
			`server "bad" needs either "command"`,
			"legacy SSE transport is not supported",
			`server "badUrl": url must be an http or https URL`,
			`invalid server name "bad name"`,
		})

		untrusted := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false})
		var untrustedShared *McpServerEntry
		for i := range untrusted.Servers {
			if untrusted.Servers[i].Name == "shared" {
				untrustedShared = &untrusted.Servers[i]
				break
			}
		}
		if untrustedShared == nil || untrustedShared.Config.Command == nil || *untrustedShared.Config.Command != "global-cmd" {
			t.Fatalf("expected global shared in untrusted project, got %+v", untrustedShared)
		}
	})

	t.Run("lets project entries override enabled and exposure of global servers", func(t *testing.T) {
		agentDir, cwd := tmeSetupConfigDirs(t,
			jsonx.ObjectOf("mcpServers", jsonx.ObjectOf("tools", jsonx.ObjectOf(
				"command", "x",
				"env", jsonx.ObjectOf("TOKEN", "secret"),
			))),
			jsonx.ObjectOf("mcpServers", jsonx.ObjectOf(
				"tools", jsonx.ObjectOf("enabled", false, "args", []any{"y"}),
				"missing", jsonx.ObjectOf("enabled", false),
			)),
		)
		projectPath := filepath.Join(cwd, ".pi", "mcp.json")
		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if len(loaded.Servers) != 1 || loaded.Servers[0].Name != "tools" || loaded.Servers[0].Override != nil {
			t.Fatalf("unexpected servers: %+v", loaded.Servers)
		}
		tmeContainsAll(t, loaded.Errors, []string{
			`server "tools": an override can only set enabled, exposure, toolExposure`,
			`server "missing" needs "command" or "url", or a global server to override`,
		})

		if err := os.WriteFile(projectPath, []byte(`{"mcpServers":{"tools":{"enabled":false}}}`), 0o600); err != nil {
			t.Fatalf("write override: %v", err)
		}
		loaded2 := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if len(loaded2.Servers) != 1 || loaded2.Servers[0].Override == nil || *loaded2.Servers[0].Override != projectPath ||
			loaded2.Servers[0].Config.Enabled == nil || *loaded2.Servers[0].Config.Enabled {
			t.Fatalf("expected override enabled=false, got %+v", loaded2.Servers)
		}

		enabledTrue := true
		if err := UpdateMcpServerConfig(projectPath, "tools", McpServerConfigPatch{Enabled: &enabledTrue}, UpdateMcpConfigOptions{Override: true}); err != nil {
			t.Fatalf("UpdateMcpServerConfig: %v", err)
		}
		raw, err := os.ReadFile(projectPath)
		if err != nil {
			t.Fatalf("read projectPath: %v", err)
		}
		parsed, _ := jsonx.Parse(raw)
		root := parsed.(*jsonx.Object)
		ms, _ := root.Get("mcpServers")
		toolsObj, _ := ms.(*jsonx.Object).Get("tools")
		tMap := toolsObj.(*jsonx.Object)
		en, hasEn := tMap.Get("enabled")
		if tMap.Len() != 1 || !hasEn || en != true {
			t.Fatalf("expected override to keep enabled:true, got %+v", tMap.Keys())
		}
	})

	t.Run("reads and edits mcp.json files that start with a UTF-8 BOM", func(t *testing.T) {
		agentDir, cwd := tmeSetupConfigDirs(t, jsonx.NewObject(), jsonx.NewObject())
		globalPath := filepath.Join(agentDir, "mcp.json")
		projectPath := filepath.Join(cwd, ".pi", "mcp.json")
		if err := os.WriteFile(globalPath, []byte("\uFEFF{\"mcpServers\":{\"demo\":{\"command\":\"echo\",\"enabled\":false}}}\n"), 0o600); err != nil {
			t.Fatalf("write global BOM: %v", err)
		}
		if err := os.WriteFile(projectPath, []byte("\uFEFF{\"mcpServers\":{\"local\":{\"command\":\"local-cmd\"}}}\n"), 0o600); err != nil {
			t.Fatalf("write project BOM: %v", err)
		}

		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if len(loaded.Errors) != 0 {
			t.Fatalf("expected 0 errors, got %v", loaded.Errors)
		}
		if len(loaded.Servers) != 2 || loaded.Servers[0].Name != "demo" || loaded.Servers[0].Scope != "global" ||
			loaded.Servers[1].Name != "local" || loaded.Servers[1].Scope != "project" {
			t.Fatalf("unexpected servers: %+v", loaded.Servers)
		}

		otherCmd := "other-cmd"
		replaced, err := AddMcpServerConfig(globalPath, "other", McpServerConfig{Command: &otherCmd})
		if err != nil || replaced {
			t.Fatalf("AddMcpServerConfig: replaced=%v err=%v", replaced, err)
		}
		raw, _ := os.ReadFile(globalPath)
		parsed, err := jsonx.Parse(raw)
		if err != nil {
			t.Fatalf("parse updated global: %v", err)
		}
		msAny, _ := parsed.(*jsonx.Object).Get("mcpServers")
		ms := msAny.(*jsonx.Object)
		if keys := ms.Keys(); !slices.Equal(keys, []string{"demo", "other"}) {
			t.Fatalf("expected keys [demo other], got %v", keys)
		}
	})

	t.Run("rejects server names that differ only in - and _", func(t *testing.T) {
		srvMap := jsonx.NewObject()
		srvMap.Set("work-files", jsonx.ObjectOf("command", "a"))
		srvMap.Set("work_files", jsonx.ObjectOf("command", "b"))
		agentDir, cwd := tmeSetupConfigDirs(t, jsonx.ObjectOf("mcpServers", srvMap), jsonx.NewObject())
		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false})
		if len(loaded.Servers) != 1 || loaded.Servers[0].Name != "work-files" {
			t.Fatalf("expected only work-files, got %+v", loaded.Servers)
		}
		tmeContainsAll(t, loaded.Errors, []string{`server "work_files" conflicts with "work-files"`})
	})

	t.Run("validates exposure and reads autoEnableCodemode with project precedence", func(t *testing.T) {
		srvMap := jsonx.NewObject()
		srvMap.Set("later", jsonx.ObjectOf("command", "x", "exposure", "deferred"))
		srvMap.Set("scripts", jsonx.ObjectOf("command", "x", "exposure", "codemode-deferred", "toolExposure", jsonx.ObjectOf("a", "codemode-deferred")))
		srvMap.Set("off", jsonx.ObjectOf("command", "x", "exposure", "hidden"))
		srvMap.Set("wrong", jsonx.ObjectOf("command", "x", "exposure", "model-only"))
		srvMap.Set("described", jsonx.ObjectOf("command", "x", "description", "Docs search"))
		srvMap.Set("badDescription", jsonx.ObjectOf("command", "x", "description", 1))

		agentDir, cwd := tmeSetupConfigDirs(t,
			jsonx.ObjectOf("autoEnableCodemode", false, "mcpServers", srvMap),
			jsonx.ObjectOf("autoEnableCodemode", "yes", "mcpServers", jsonx.NewObject()),
		)

		untrusted := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false})
		if untrusted.AutoEnableCodemode == nil || *untrusted.AutoEnableCodemode {
			t.Fatalf("expected AutoEnableCodemode=false, got %v", untrusted.AutoEnableCodemode)
		}
		if len(untrusted.Servers) != 4 ||
			*untrusted.Servers[0].Config.Exposure != core.McpExposureDeferred ||
			*untrusted.Servers[1].Config.Exposure != core.McpExposureCodemode ||
			*untrusted.Servers[2].Config.Exposure != core.McpExposureHidden ||
			untrusted.Servers[3].Config.Exposure != nil {
			t.Fatalf("unexpected exposure parsing: %+v", untrusted.Servers)
		}
		expA, _ := untrusted.Servers[1].Config.ToolExposure.Get("a")
		if expA != core.McpExposureCodemode {
			t.Fatalf("expected codemode alias for toolExposure a, got %q", expA)
		}
		if untrusted.Servers[3].Config.Description == nil || *untrusted.Servers[3].Config.Description != "Docs search" {
			t.Fatalf("unexpected description: %v", untrusted.Servers[3].Config.Description)
		}
		tmeContainsAll(t, untrusted.Errors, []string{
			`server "wrong": exposure must be one of`,
			`server "badDescription": description must be a string`,
		})

		trusted := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if trusted.AutoEnableCodemode == nil || *trusted.AutoEnableCodemode {
			t.Fatalf("expected global false autoEnableCodemode fallback on invalid project, got %v", trusted.AutoEnableCodemode)
		}
		foundErr := false
		for _, e := range trusted.Errors {
			if strings.Contains(e, "autoEnableCodemode must be a boolean") {
				foundErr = true
			}
		}
		if !foundErr {
			t.Fatalf("expected autoEnableCodemode error in %v", trusted.Errors)
		}
	})

	t.Run("validates the OAuth callback URL, scope, and client name", func(t *testing.T) {
		srvMap := jsonx.NewObject()
		srvMap.Set("ok", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("callbackUrl", "http://localhost:8080/callback", "scope", "a b")))
		srvMap.Set("ipv6", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("callbackUrl", "http://[::1]/cb", "callbackPort", 9000)))
		srvMap.Set("same", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("callbackUrl", "http://127.0.0.1:2/cb", "callbackPort", 2)))
		srvMap.Set("remote", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("callbackUrl", "https://example.com/callback")))
		srvMap.Set("both", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("callbackUrl", "http://127.0.0.1:1/cb", "callbackPort", 2)))
		srvMap.Set("scope", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("scope", []any{"a"})))
		srvMap.Set("named", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientName", "Claude Code")))
		srvMap.Set("unnamed", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientName", " ")))
		srvMap.Set("metadata", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("authServerMetadataUrl", "https://idp.example/m")))
		srvMap.Set("plainMetadata", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("authServerMetadataUrl", "http://idp.example/m")))
		srvMap.Set("cimd", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientRegistration", "cimd", "callbackUrl", "http://localhost/callback")))
		srvMap.Set("auto", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientRegistration", "auto", "clientName", "x")))
		srvMap.Set("badRegistration", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientRegistration", "oidc")))
		srvMap.Set("cimdClient", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientRegistration", "cimd", "clientId", "x")))
		srvMap.Set("cimdPath", jsonx.ObjectOf("url", "https://a.example/mcp", "oauth", jsonx.ObjectOf("clientRegistration", "cimd", "callbackUrl", "http://127.0.0.1/cb")))

		agentDir, cwd := tmeSetupConfigDirs(t, jsonx.ObjectOf("mcpServers", srvMap), jsonx.NewObject())
		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false})
		gotNames := make([]string, len(loaded.Servers))
		for i, s := range loaded.Servers {
			gotNames[i] = s.Name
		}
		wantNames := []string{"ok", "ipv6", "same", "named", "metadata", "cimd", "auto"}
		if !slices.Equal(gotNames, wantNames) {
			t.Fatalf("got servers %v, want %v", gotNames, wantNames)
		}
		tmeContainsAll(t, loaded.Errors, []string{
			`server "remote": oauth.callbackUrl must be an http URI on localhost`,
			`server "both": oauth.callbackUrl and oauth.callbackPort name different ports`,
			`server "scope": oauth.scope must be a string`,
			`server "unnamed": oauth.clientName must be a non-empty string`,
			`server "plainMetadata": oauth.authServerMetadataUrl must be an https URL`,
			`server "badRegistration": oauth.clientRegistration must be "auto", "dcr", or "cimd"`,
			`server "cimdClient": oauth.clientRegistration "cimd" cannot be combined`,
			`server "cimdPath": oauth.clientRegistration "cimd" requires oauth.callbackUrl`,
		})
	})

	t.Run("resolves per-tool exposure from exact names, then patterns in order", func(t *testing.T) {
		srvMap := jsonx.NewObject()
		te := jsonx.NewObject()
		te.Set("get_*", "codemode")
		te.Set("get_me", "direct")
		te.Set("*delete*", "hidden")
		te.Set("get_file.*", "direct")
		srvMap.Set("gh", jsonx.ObjectOf("command", "x", "exposure", "deferred", "toolExposure", te))
		srvMap.Set("bad", jsonx.ObjectOf("command", "x", "toolExposure", jsonx.ObjectOf("a", "visible")))

		agentDir, cwd := tmeSetupConfigDirs(t, jsonx.ObjectOf("mcpServers", srvMap), jsonx.NewObject())
		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: false})
		tmeContainsAll(t, loaded.Errors, []string{`server "bad": toolExposure "a" must be one of`})
		cfg := loaded.Servers[0].Config
		if GetMcpToolExposure(cfg, "get_me") != core.McpExposureDirect ||
			GetMcpToolExposure(cfg, "get_issue") != core.McpExposureCodemode ||
			GetMcpToolExposure(cfg, "get_delete_hint") != core.McpExposureCodemode ||
			GetMcpToolExposure(cfg, "delete_repo") != core.McpExposureHidden ||
			GetMcpToolExposure(cfg, "list_issues") != core.McpExposureDeferred {
			t.Fatalf("unexpected GetMcpToolExposure resolutions")
		}
		cmdX := "x"
		literalDot, _ := ValidateMcpServerConfig("t", jsonx.ObjectOf("command", cmdX, "toolExposure", jsonx.ObjectOf("get_file.*", "direct")))
		if got := GetMcpToolExposure(*literalDot, "get_file_x"); got != core.McpExposureCodemode {
			t.Fatalf("expected literal dot not to match _, got %q", got)
		}
	})

	t.Run("validates provider auth and accepts it only in the global mcp.json", func(t *testing.T) {
		globalServers := jsonx.NewObject()
		globalServers.Set("radius", jsonx.ObjectOf("url", "https://radius.example/mcp", "auth", jsonx.ObjectOf("provider", "radius")))
		globalServers.Set("local", jsonx.ObjectOf("url", "http://localhost:8788/mcp", "auth", jsonx.ObjectOf("provider", "radius-dev")))
		globalServers.Set("plain", jsonx.ObjectOf("url", "http://radius.example/mcp", "auth", jsonx.ObjectOf("provider", "radius")))
		globalServers.Set("empty", jsonx.ObjectOf("url", "https://radius.example/mcp", "auth", jsonx.ObjectOf("provider", "")))

		projectServers := jsonx.NewObject()
		projectServers.Set("radius", jsonx.ObjectOf("url", "https://evil.example/mcp", "auth", jsonx.ObjectOf("provider", "radius")))

		agentDir, cwd := tmeSetupConfigDirs(t, jsonx.ObjectOf("mcpServers", globalServers), jsonx.ObjectOf("mcpServers", projectServers))
		loaded := LoadMcpConfig(LoadMcpConfigOptions{AgentDir: agentDir, Cwd: cwd, ProjectTrusted: true})
		if len(loaded.Servers) != 2 ||
			loaded.Servers[0].Name != "radius" || loaded.Servers[0].Scope != "global" || *loaded.Servers[0].Config.Url != "https://radius.example/mcp" ||
			loaded.Servers[1].Name != "local" || loaded.Servers[1].Scope != "global" || *loaded.Servers[1].Config.Url != "http://localhost:8788/mcp" {
			t.Fatalf("unexpected servers: %+v", loaded.Servers)
		}
		tmeContainsAll(t, loaded.Errors, []string{
			`server "plain": auth requires an https URL`,
			`server "empty": auth.provider must be a provider name`,
			`server "radius": auth is only allowed in the global mcp.json`,
		})
	})
}

func TestMcpTools(t *testing.T) {
	t.Run("creates provider-safe tool names", func(t *testing.T) {
		if got := mcp.CreateMcpToolName("docs", "search", nil); got != "mcp__docs__search" {
			t.Fatalf("got %q", got)
		}
		if got := mcp.CreateMcpToolName("my-server", "get.item/v2", nil); got != "mcp__my_server__get_item_v2" {
			t.Fatalf("got %q", got)
		}
		long := mcp.CreateMcpToolName("server", strings.Repeat("x", 100), nil)
		if len(long) != 64 || !regexp.MustCompile(`^mcp__server__x+_[0-9a-f]{8}$`).MatchString(long) {
			t.Fatalf("unexpected long name: %q", long)
		}
		longY := mcp.CreateMcpToolName("server", strings.Repeat("x", 100)+"y", nil)
		if longY == long {
			t.Fatalf("expected distinct hash for distinct long name")
		}
		taken := mcp.CreateMcpToolName("s", "a_b", nil)
		second := mcp.CreateMcpToolName("s", "a-b", func(name string) bool { return name == taken })
		if !regexp.MustCompile(`^mcp__s__a_b_[0-9a-f]{8}$`).MatchString(second) {
			t.Fatalf("unexpected collision-suffixed name: %q", second)
		}
	})

	t.Run("declares the CallToolResult scripts receive, keeping references in the server's output schema", func(t *testing.T) {
		contentType := `content: Array<{ data?: string; mimeType?: string; text?: string; type: "text" | "image" | "audio" | "resource_link" | "resource"; [key: string]: unknown; }>; isError?: boolean;`
		emptyRendered := codemode.RenderToolOutputType(CreateMcpResultSchema(nil).Object)
		wantEmpty := fmt.Sprintf(`{ %s structuredContent?: unknown; [key: string]: unknown; }`, contentType)
		if emptyRendered != wantEmpty {
			t.Fatalf("got:\n  %s\nwant:\n  %s", emptyRendered, wantEmpty)
		}

		hit := jsonx.ObjectOf(
			"type", "object",
			"properties", jsonx.ObjectOf("id", jsonx.ObjectOf("type", "string")),
			"required", []any{"id"},
		)
		hitWithDefault := hit.Clone()
		hitWithDefault.Set("default", jsonx.ObjectOf("$ref", "#/stays"))

		props := jsonx.NewObject()
		props.Set("hits", jsonx.ObjectOf("type", "array", "items", jsonx.ObjectOf("$ref", "#/$defs/Hit~1v1")))
		props.Set("again", jsonx.ObjectOf("$ref", "#/properties/hits"))
		props.Set("children", jsonx.ObjectOf("type", "array", "items", jsonx.ObjectOf("$ref", "#")))
		props.Set("default", jsonx.ObjectOf("$ref", "#/definitions/Hit"))
		props.Set("remote", jsonx.ObjectOf("$ref", "https://example.com/schema.json"))

		inputSchema := jsonx.ObjectOf(
			"type", "object",
			"properties", props,
			"required", []any{"hits"},
			"$defs", jsonx.ObjectOf("Hit/v1", hitWithDefault),
			"definitions", jsonx.ObjectOf("Hit", hit),
		)
		schema := CreateMcpResultSchema(inputSchema)
		schemaProps, _ := schema.Object.Get("properties")
		scRaw, _ := schemaProps.(*jsonx.Object).Get("structuredContent")
		defsRaw, _ := scRaw.(*jsonx.Object).Get("$defs")
		hitV1Raw, _ := defsRaw.(*jsonx.Object).Get("Hit/v1")
		defData, _ := hitV1Raw.(*jsonx.Object).Get("default")
		refData, _ := defData.(*jsonx.Object).Get("$ref")
		if refData != "#/stays" {
			t.Fatalf("expected keyword data default.$ref untouched, got %v", refData)
		}

		rendered := codemode.RenderToolOutputType(schema.Object)
		wantComplex := fmt.Sprintf(
			`{ %s structuredContent?: { again?: Array<{ id: string; }>; children?: Array<{ again?: Array<{ id: string; }>; children?: Array<unknown>; default?: { id: string; }; hits: Array<{ id: string; }>; remote?: unknown; }>; default?: { id: string; }; hits: Array<{ id: string; }>; remote?: unknown; }; [key: string]: unknown; }`,
			contentType,
		)
		if rendered != wantComplex {
			t.Fatalf("got:\n  %s\nwant:\n  %s", rendered, wantComplex)
		}
	})

	t.Run("converts results, passing the CallToolResult to scripts and flagging errors", func(t *testing.T) {
		ctx := context.Background()
		nameA := "a"
		blocks := mcpprotocol.ContentBlockList{
			&mcpprotocol.ResourceLinkContent{Type: "resource_link", Uri: "file:///a", Name: nameA},
			&mcpprotocol.EmbeddedResourceContent{Type: "resource", Resource: &mcpprotocol.TextResourceContents{Uri: "file:///b", Text: "b text"}},
			&mcpprotocol.AudioContent{Type: "audio", Data: "", MimeType: "audio/wav"},
		}
		res1 := ConvertMcpResult(ctx, "docs", "t", &mcpprotocol.CallToolResult{
			Content:           blocks,
			StructuredContent: jsonx.Some[any](jsonx.ObjectOf("ok", true)),
			Meta:              jsonx.ObjectOf("trace", "x"),
		})
		if len(res1.Content) != 3 ||
			res1.Content[0].(*ai.TextContent).Text != `[Resource file:///a "a"]` ||
			res1.Content[1].(*ai.TextContent).Text != "b text" ||
			res1.Content[2].(*ai.TextContent).Text != "[audio audio/wav omitted]" {
			t.Fatalf("unexpected content1: %+v", res1.Content)
		}
		scStr, _ := jsonx.Stringify(res1.StructuredContent)
		if strings.Contains(scStr, "trace") || !strings.Contains(scStr, `"ok":true`) {
			t.Fatalf("unexpected structuredContent1: %s", scStr)
		}

		res2 := ConvertMcpResult(ctx, "docs", "t", &mcpprotocol.CallToolResult{
			Content:           mcpprotocol.ContentBlockList{},
			StructuredContent: jsonx.Some[any](jsonx.ObjectOf("n", 1)),
		})
		if len(res2.Content) != 1 || res2.Content[0].(*ai.TextContent).Text != "{\n  \"n\": 1\n}" {
			t.Fatalf("unexpected content2: %+v", res2.Content)
		}

		resArr := ConvertMcpResult(ctx, "docs", "t", &mcpprotocol.CallToolResult{
			Content:           mcpprotocol.ContentBlockList{},
			StructuredContent: jsonx.Some[any]([]any{jsonx.ObjectOf("id", 1)}),
		})
		if len(resArr.Content) != 1 || resArr.Content[0].(*ai.TextContent).Text != "[\n  {\n    \"id\": 1\n  }\n]" {
			t.Fatalf("unexpected array content: %+v", resArr.Content)
		}

		isErrTrue := true
		resErr := ConvertMcpResult(ctx, "docs", "t", &mcpprotocol.CallToolResult{
			Content: mcpprotocol.ContentBlockList{&mcpprotocol.TextContent{Type: "text", Text: "nope"}},
			IsError: &isErrTrue,
		})
		if resErr.IsError == nil || !*resErr.IsError || resErr.Content[0].(*ai.TextContent).Text != "nope" {
			t.Fatalf("unexpected error result: %+v", resErr)
		}

		resEmptyErr := ConvertMcpResult(ctx, "docs", "t", &mcpprotocol.CallToolResult{
			Content: mcpprotocol.ContentBlockList{},
			IsError: &isErrTrue,
		})
		if len(resEmptyErr.Content) != 1 || resEmptyErr.Content[0].(*ai.TextContent).Text != "MCP tool docs/t returned an error" {
			t.Fatalf("unexpected empty error result: %+v", resEmptyErr.Content)
		}
	})

	t.Run("points resource links to read_mcp_resource and saves binary resources", func(t *testing.T) {
		var saved [][]any
		saveOutput := func(_ context.Context, data []byte, ext string) (string, error) {
			saved = append(saved, []any{data, ext})
			return "/tmp/saved" + ext, nil
		}
		title := "The Guide"
		mimeMd := "text/markdown"
		sz := 2048
		desc := "How to use it"
		mimePdf := "application/pdf"
		mimePng := "image/png"

		converted := ConvertMcpResult(context.Background(), "docs", "t", &mcpprotocol.CallToolResult{
			Content: mcpprotocol.ContentBlockList{
				&mcpprotocol.ResourceLinkContent{
					Type:        "resource_link",
					Uri:         "docs://guide",
					Name:        "guide",
					Title:       &title,
					MimeType:    &mimeMd,
					Size:        &sz,
					Description: &desc,
				},
				&mcpprotocol.EmbeddedResourceContent{
					Type: "resource",
					Resource: &mcpprotocol.BlobResourceContents{
						Uri:      "file:///r/report.pdf",
						MimeType: &mimePdf,
						Blob:     base64.StdEncoding.EncodeToString([]byte("%PDF")),
					},
				},
				&mcpprotocol.EmbeddedResourceContent{
					Type: "resource",
					Resource: &mcpprotocol.BlobResourceContents{
						Uri:      "docs://logo",
						MimeType: &mimePng,
						Blob:     "AAAA",
					},
				},
			},
		}, ConvertMcpResultOptions{SaveOutput: saveOutput, ReadableResources: true})

		if len(converted.Content) != 3 ||
			converted.Content[0].(*ai.TextContent).Text != `[Resource docs://guide "The Guide" (text/markdown, 2.0KB): How to use it. Read it with read_mcp_resource (server "docs")]` ||
			converted.Content[1].(*ai.TextContent).Text != "[Binary resource file:///r/report.pdf (application/pdf, 4B) saved to /tmp/saved.pdf]" {
			t.Fatalf("unexpected converted content: %+v", converted.Content)
		}
		img, ok := converted.Content[2].(*ai.ImageContent)
		if !ok || img.Data != "AAAA" || img.MimeType != "image/png" {
			t.Fatalf("unexpected image content: %+v", converted.Content[2])
		}
		if len(saved) != 1 || !bytes.Equal(saved[0][0].([]byte), []byte("%PDF")) || saved[0][1].(string) != ".pdf" {
			t.Fatalf("unexpected saved binary: %+v", saved)
		}
	})

	t.Run("cuts the middle of model-facing text over 20KB and keeps the full result for scripts", func(t *testing.T) {
		var saved [][]byte
		saveOutput := func(_ context.Context, data []byte, _ string) (string, error) {
			saved = append(saved, data)
			return "/tmp/full.txt", nil
		}
		lines := make([]string, 3000)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		full := strings.Join(lines, "\n")
		input := &mcpprotocol.CallToolResult{
			Content: mcpprotocol.ContentBlockList{
				&mcpprotocol.TextContent{Type: "text", Text: full},
				&mcpprotocol.ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"},
			},
		}
		converted := ConvertMcpResult(context.Background(), "docs", "snapshot", input, ConvertMcpResultOptions{SaveOutput: saveOutput})
		if len(converted.Content) != 2 {
			t.Fatalf("expected 2 content items, got %d", len(converted.Content))
		}
		text := converted.Content[0].(*ai.TextContent).Text
		headerRe := regexp.MustCompile(fmt.Sprintf(`^Warning: truncated output \(original token count: %d\)\nTotal output lines: 3000\n\nline 1\nline 2\n`, (len(full)+3)/4))
		if !headerRe.MatchString(text) {
			t.Fatalf("unexpected truncated header: %.120q", text)
		}
		if !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
			t.Fatalf("missing truncation marker in text")
		}
		if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") {
			t.Fatalf("unexpected truncated suffix")
		}
		if len([]byte(text)) >= 21*1024 {
			t.Fatalf("truncated text too large: %d", len([]byte(text)))
		}
		details := converted.Details.(McpToolDetails)
		if details.Server != "docs" || details.Tool != "snapshot" || details.FullOutputPath == nil || *details.FullOutputPath != "/tmp/full.txt" {
			t.Fatalf("unexpected details: %+v", details)
		}
		if len(saved) != 1 || string(saved[0]) != full {
			t.Fatalf("expected full text saved once")
		}

		ConvertMcpResult(context.Background(), "docs", "small", &mcpprotocol.CallToolResult{
			Content: mcpprotocol.ContentBlockList{&mcpprotocol.TextContent{Type: "text", Text: "ok"}},
		}, ConvertMcpResultOptions{SaveOutput: saveOutput})
		if len(saved) != 1 {
			t.Fatalf("small output should not call saveOutput, saved=%d", len(saved))
		}
	})
}

type tmeTransportOptions struct {
	expireFirstCall bool
	methods         *[]string
	noTools         bool
	resources       bool
	customSend      func(msg mcpprotocol.JsonRpcMessage, inner mcp.McpTransport) error
}

type tmeWrappedTransport struct {
	*mcp.InMemoryTransport
	sendFn func(msg mcpprotocol.JsonRpcMessage, inner mcp.McpTransport) error
}

func (w *tmeWrappedTransport) Send(msg mcpprotocol.JsonRpcMessage) error {
	if w.sendFn != nil {
		return w.sendFn(msg, w.InMemoryTransport)
	}
	return w.InMemoryTransport.Send(msg)
}

func tmeNewInMemoryTransport(t *testing.T, opts tmeTransportOptions) (*tmeWrappedTransport, *mcp.InMemoryTransport) {
	t.Helper()
	pair := mcp.CreateInMemoryTransportPair()
	t.Cleanup(func() { _ = pair.Server.Close() })

	pair.Server.OnMessage(func(msg mcpprotocol.JsonRpcMessage) {
		req, ok := msg.(*mcpprotocol.JsonRpcRequest)
		if !ok {
			return
		}
		if opts.methods != nil {
			*opts.methods = append(*opts.methods, req.Method)
		}
		var resp mcpprotocol.JsonRpcMessage
		switch req.Method {
		case "initialize":
			caps := jsonx.NewObject()
			if opts.noTools {
				caps.Set("prompts", jsonx.NewObject())
			} else {
				caps.Set("tools", jsonx.NewObject())
			}
			if opts.resources {
				caps.Set("resources", jsonx.NewObject())
			}
			resp = &mcpprotocol.JsonRpcSuccessResponse{
				Jsonrpc: "2.0",
				Id:      req.Id,
				Result: jsonx.ObjectOf(
					"protocolVersion", mcpprotocol.LatestProtocolVersion,
					"capabilities", caps,
					"serverInfo", jsonx.ObjectOf("name", "fake", "version", "1.0.0"),
				),
			}
		case "tools/list":
			if opts.noTools {
				resp = &mcpprotocol.JsonRpcErrorResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Error:   mcpprotocol.JsonRpcErrorObject{Code: -32601, Message: "Method not found"},
				}
			} else {
				resp = &mcpprotocol.JsonRpcSuccessResponse{
					Jsonrpc: "2.0",
					Id:      req.Id,
					Result:  jsonx.ObjectOf("tools", []any{}),
				}
			}
		case "resources/read":
			resp = &mcpprotocol.JsonRpcSuccessResponse{
				Jsonrpc: "2.0",
				Id:      req.Id,
				Result:  jsonx.ObjectOf("contents", []any{jsonx.ObjectOf("uri", "docs://a", "text", "ok")}),
			}
		default:
			resp = &mcpprotocol.JsonRpcSuccessResponse{
				Jsonrpc: "2.0",
				Id:      req.Id,
				Result:  jsonx.ObjectOf("content", []any{jsonx.ObjectOf("type", "text", "text", "ok")}),
			}
		}
		go func() {
			_ = pair.Server.Send(resp)
		}()
	})
	if err := pair.Server.Start(); err != nil {
		t.Fatalf("pair.Server.Start: %v", err)
	}

	sendFn := opts.customSend
	if opts.expireFirstCall && sendFn == nil {
		sendFn = func(msg mcpprotocol.JsonRpcMessage, inner mcp.McpTransport) error {
			if req, ok := msg.(*mcpprotocol.JsonRpcRequest); ok && req.Method == "tools/call" {
				return mcp.NewMcpSessionExpiredError("gone")
			}
			return inner.Send(msg)
		}
	}
	return &tmeWrappedTransport{InMemoryTransport: pair.Client, sendFn: sendFn}, pair.Server
}

func tmeConnect(
	entry McpServerEntry,
	transports []func() mcp.McpTransport,
	log *McpServerLog,
) (*McpServerConnection, func() int) {
	var mu sync.Mutex
	opened := 0
	conn := NewMcpServerConnection(McpServerConnectionOptions{
		Entry: entry,
		Cwd:   ".",
		CreateTransport: func(McpServerEntry, string, mcp.AuthProvider) (mcp.McpTransport, error) {
			mu.Lock()
			idx := opened
			opened++
			mu.Unlock()
			return transports[idx](), nil
		},
		Credentials: NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()),
		Log:         log,
	})
	return conn, func() int {
		mu.Lock()
		defer mu.Unlock()
		return opened
	}
}

func TestMcpConnections(t *testing.T) {
	cmdUnused := "unused"
	urlUnused := "http://unused.invalid"

	t.Run("starts a new session and retries once when the session expired", func(t *testing.T) {
		conn, opened := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{expireFirstCall: true})
					return tr
				},
				func() mcp.McpTransport { tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{}); return tr },
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		var wg sync.WaitGroup
		results := make([]*mcpprotocol.CallToolResult, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = conn.CallTool(context.Background(), "echo", jsonx.NewObject(), nil)
			}(i)
		}
		wg.Wait()
		for i := range 2 {
			if errs[i] != nil {
				t.Fatalf("call %d failed: %v", i, errs[i])
			}
			if len(results[i].Content) != 1 || results[i].Content[0].(*mcpprotocol.TextContent).Text != "ok" {
				t.Fatalf("call %d unexpected result: %+v", i, results[i])
			}
		}
		if opened() != 2 {
			t.Fatalf("expected opened() == 2, got %d", opened())
		}
	})

	t.Run("expands ~ in the command, arguments, and cwd of stdio servers", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		workDir := filepath.Join(home, "work")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatalf("mkdir work: %v", err)
		}
		exe := tmcTestExecutable(t)
		symlinkPath := filepath.Join(home, "mcp-fixture-bin")
		if err := os.Symlink(exe, symlinkPath); err != nil {
			t.Fatalf("symlink executable: %v", err)
		}
		cmd := "~/mcp-fixture-bin"
		subCwd := "~/work"
		conn := NewMcpServerConnection(McpServerConnectionOptions{
			Entry: McpServerEntry{
				Name:   "home",
				Config: McpServerConfig{Command: &cmd, Args: []string{tmcStdioCwdArg, "~/subarg"}, Cwd: &subCwd},
				Source: "test",
			},
			Cwd: os.TempDir(),
			CreateTransport: func(e McpServerEntry, cwd string, ap mcp.AuthProvider) (mcp.McpTransport, error) {
				return CreateDefaultTransport(e, cwd, ap, nil)
			},
			Credentials: NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()),
		})
		defer func() { _ = conn.Close() }()

		res, err := conn.CallTool(context.Background(), "cwd", jsonx.NewObject(), nil)
		if err != nil {
			t.Fatalf("call cwd failed: %v", err)
		}
		parts := strings.Split(res.Content[0].(*mcpprotocol.TextContent).Text, "|")
		wantWork, _ := filepath.EvalSymlinks(workDir)
		gotWork, _ := filepath.EvalSymlinks(parts[0])
		if gotWork != wantWork {
			t.Fatalf("got cwd %q, want %q", gotWork, wantWork)
		}
		wantArg := filepath.Join(home, "subarg")
		if len(parts) < 2 || parts[1] != wantArg {
			t.Fatalf("expected expanded arg %q, got %v", wantArg, parts)
		}
	})

	t.Run("connects to servers without the tools capability without listing tools", func(t *testing.T) {
		var methods []string
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{noTools: true, methods: &methods})
					return tr
				},
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		if _, err := conn.GetClient(context.Background()); err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		if conn.State() != ServerStateConnected || len(conn.Tools()) != 0 || !slices.Equal(methods, []string{"initialize"}) {
			t.Fatalf("state=%s tools=%v methods=%v", conn.State(), conn.Tools(), methods)
		}
	})

	t.Run("marks a dropped connection and reconnects on the next call", func(t *testing.T) {
		var firstServer *mcp.InMemoryTransport
		conn, opened := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, srv := tmeNewInMemoryTransport(t, tmeTransportOptions{})
					firstServer = srv
					return tr
				},
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{})
					return tr
				},
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		if _, err := conn.GetClient(context.Background()); err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		_ = firstServer.Close()
		deadline := time.Now().Add(time.Second)
		for conn.State() != ServerStateDisconnected && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if conn.State() != ServerStateDisconnected {
			t.Fatalf("expected disconnected, got %s", conn.State())
		}
		if conn.Error() == nil || *conn.Error() != "Connection closed" {
			t.Fatalf("expected Connection closed, got %v", conn.Error())
		}
		res, err := conn.CallTool(context.Background(), "echo", jsonx.NewObject(), nil)
		if err != nil || res.Content[0].(*mcpprotocol.TextContent).Text != "ok" {
			t.Fatalf("reconnect CallTool failed: %v %+v", err, res)
		}
		if conn.State() != ServerStateConnected || opened() != 2 {
			t.Fatalf("state=%s opened=%d", conn.State(), opened())
		}
	})

	t.Run("retries HTTP connections that fail with a transient error", func(t *testing.T) {
		hdrs := jsonx.ObjectOf("Authorization", "x")
		var hdrMap = FromCoreMcpServerConfig(core.McpServerConfig{Url: &urlUnused}).Headers
		_ = hdrs
		_ = hdrMap
		httpEntry := McpServerEntry{
			Name:   "fake",
			Config: McpServerConfig{Url: &urlUnused},
			Source: "test",
		}
		// Add Authorization header via validation so IsHttp & !OAuth.
		validated, _ := ValidateMcpServerConfig("fake", jsonx.ObjectOf("url", urlUnused, "headers", jsonx.ObjectOf("Authorization", "x")))
		httpEntry.Config = *validated

		conn, opened := tmeConnect(httpEntry, []func() mcp.McpTransport{
			func() mcp.McpTransport {
				tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{
					customSend: func(mcpprotocol.JsonRpcMessage, mcp.McpTransport) error {
						return mcp.NewMcpHttpError(503, "MCP HTTP request failed with status 503", "")
					},
				})
				return tr
			},
			func() mcp.McpTransport {
				tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{})
				return tr
			},
		}, nil)
		defer func() { _ = conn.Close() }()

		if _, err := conn.GetClient(context.Background()); err != nil {
			t.Fatalf("expected transient 503 retry to succeed, got %v", err)
		}
		if conn.State() != ServerStateConnected || opened() != 2 {
			t.Fatalf("state=%s opened=%d", conn.State(), opened())
		}

		failing, failingOpened := tmeConnect(httpEntry, []func() mcp.McpTransport{
			func() mcp.McpTransport {
				tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{
					customSend: func(mcpprotocol.JsonRpcMessage, mcp.McpTransport) error {
						return mcp.NewMcpHttpError(400, "MCP HTTP request failed with status 400: bad", "")
					},
				})
				return tr
			},
		}, nil)
		defer func() { _ = failing.Close() }()
		if _, err := failing.GetClient(context.Background()); err == nil || !strings.Contains(err.Error(), "status 400: bad") {
			t.Fatalf("expected status 400 error, got %v", err)
		}
		if failing.State() != ServerStateFailed || failingOpened() != 1 {
			t.Fatalf("failing state=%s opened=%d", failing.State(), failingOpened())
		}
	})

	t.Run("closes a connection that is still initializing before close returns", func(t *testing.T) {
		pair := mcp.CreateInMemoryTransportPair()
		initializing := make(chan struct{}, 1)
		pair.Server.OnMessage(func(mcpprotocol.JsonRpcMessage) {
			select {
			case initializing <- struct{}{}:
			default:
			}
		})
		_ = pair.Server.Start()
		defer func() { _ = pair.Server.Close() }()

		var mu sync.Mutex
		transportClosed := false
		pair.Client.OnClose(func() {
			mu.Lock()
			transportClosed = true
			mu.Unlock()
		})

		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{func() mcp.McpTransport { return pair.Client }},
			nil,
		)
		errCh := make(chan error, 1)
		go func() {
			_, err := conn.GetClient(context.Background())
			errCh <- err
		}()
		<-initializing
		_ = conn.Close()
		err := <-errCh
		mu.Lock()
		closed := transportClosed
		mu.Unlock()
		if !closed {
			t.Fatalf("expected transportClosed == true")
		}
		if err == nil || !strings.Contains(err.Error(), "failed to connect") {
			t.Fatalf("expected failed to connect error, got %v", err)
		}
	})

	t.Run("stops waiting to retry a connection when closed", func(t *testing.T) {
		validated, _ := ValidateMcpServerConfig("fake", jsonx.ObjectOf("url", urlUnused, "headers", jsonx.ObjectOf("Authorization", "x")))
		firstAttemptFailed := make(chan struct{}, 1)
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: *validated, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{
						customSend: func(mcpprotocol.JsonRpcMessage, mcp.McpTransport) error {
							select {
							case firstAttemptFailed <- struct{}{}:
							default:
							}
							return mcp.NewMcpHttpError(503, "MCP HTTP request failed with status 503", "")
						},
					})
					return tr
				},
			},
			nil,
		)
		errCh := make(chan error, 1)
		go func() {
			_, err := conn.GetClient(context.Background())
			errCh <- err
		}()
		<-firstAttemptFailed
		time.Sleep(10 * time.Millisecond)
		_ = conn.Close()
		err := <-errCh
		if err == nil || !strings.Contains(err.Error(), "status 503") {
			t.Fatalf("expected status 503 error, got %v", err)
		}
	})

	t.Run("retries resource reads, but not tool calls, after a transient HTTP error", func(t *testing.T) {
		var mu sync.Mutex
		failed := make(map[string]bool)
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{
						customSend: func(msg mcpprotocol.JsonRpcMessage, inner mcp.McpTransport) error {
							if req, ok := msg.(*mcpprotocol.JsonRpcRequest); ok &&
								(req.Method == "resources/read" || req.Method == "tools/call") {
								mu.Lock()
								already := failed[req.Method]
								failed[req.Method] = true
								mu.Unlock()
								if !already {
									return mcp.NewMcpHttpError(502, "MCP HTTP request failed with status 502", "")
								}
							}
							return inner.Send(msg)
						},
					})
					return tr
				},
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		readRes, err := conn.ReadResource(context.Background(), "docs://a", nil)
		if err != nil || len(readRes.Contents) != 1 {
			t.Fatalf("expected resource read retry to succeed, err=%v res=%+v", err, readRes)
		}
		if _, err := conn.CallTool(context.Background(), "echo", jsonx.NewObject(), nil); err == nil || !strings.Contains(err.Error(), "status 502") {
			t.Fatalf("expected tool call NOT to retry 502, got %v", err)
		}
	})

	t.Run("asks OAuth servers that keep rejecting requests for a new sign-in", func(t *testing.T) {
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Url: &urlUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{
						customSend: func(mcpprotocol.JsonRpcMessage, mcp.McpTransport) error {
							return mcp.NewMcpAuthRequiredError(&http.Response{StatusCode: 401}, "")
						},
					})
					return tr
				},
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		_, err := conn.GetClient(context.Background())
		want := `MCP server "fake" requires sign-in. Run /mcp to sign in.`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want %q", err, want)
		}
		if conn.State() != ServerStateNeedsAuth {
			t.Fatalf("expected needs-auth, got %s", conn.State())
		}
	})

	t.Run("sends the provider token and asks for the provider login when the server rejects it", func(t *testing.T) {
		var mu sync.Mutex
		var authorizations []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			authorizations = append(authorizations, r.Header.Get("Authorization"))
			mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		srvURL := srv.URL + "/mcp"
		conn := NewMcpServerConnection(McpServerConnectionOptions{
			Entry: McpServerEntry{
				Name:   "radius",
				Config: McpServerConfig{Url: &srvURL, Auth: &McpAuthConfig{Provider: "radius"}},
				Source: "test",
			},
			Cwd:        ".",
			HTTPClient: srv.Client(),
			CreateTransport: func(e McpServerEntry, cwd string, ap mcp.AuthProvider) (mcp.McpTransport, error) {
				return CreateDefaultTransport(e, cwd, ap, srv.Client())
			},
			Credentials: NewMcpOAuthCredentialStore(core.NewInMemoryAuthStorageBackend()),
			ProviderToken: func(_ context.Context, provider string) (*string, error) {
				if provider == "radius" {
					tok := "tok"
					return &tok, nil
				}
				return nil, nil
			},
		})
		defer func() { _ = conn.Close() }()

		if conn.OAuthUrl() != nil {
			t.Fatalf("expected OAuthUrl() == nil when auth.provider is set")
		}
		_, err := conn.GetClient(context.Background())
		want := `MCP server "radius" requires sign-in. Run /login radius to sign in.`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("got %v, want %q", err, want)
		}
		if conn.State() != ServerStateNeedsAuth {
			t.Fatalf("expected needs-auth, got %s", conn.State())
		}
		mu.Lock()
		gotAuths := slices.Clone(authorizations)
		mu.Unlock()
		if !slices.Equal(gotAuths, []string{"Bearer tok"}) {
			t.Fatalf("expected [Bearer tok], got %v", gotAuths)
		}
	})

	t.Run("appends server log messages to the log file", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "mcp.log")
		var serverTr *mcp.InMemoryTransport
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, srv := tmeNewInMemoryTransport(t, tmeTransportOptions{})
					serverTr = srv
					return tr
				},
			},
			NewMcpServerLog(logPath),
		)
		defer func() { _ = conn.Close() }()

		if _, err := conn.GetClient(context.Background()); err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		_ = serverTr.Send(&mcpprotocol.JsonRpcNotification{
			Jsonrpc: "2.0",
			Method:  "notifications/message",
			Params:  jsonx.ObjectOf("level", "warning", "logger", "db", "data", "slow\nquery"),
		})
		_ = serverTr.Send(&mcpprotocol.JsonRpcNotification{
			Jsonrpc: "2.0",
			Method:  "notifications/message",
			Params:  jsonx.ObjectOf("level", "error", "data", jsonx.ObjectOf("code", 7)),
		})
		tsPrefixRe := regexp.MustCompile(`(?m)^\S+ `)
		want := "[fake] warning db: slow\n    query\n[fake] error {\"code\":7}\n"
		var stripped string
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(logPath)
			stripped = tsPrefixRe.ReplaceAllString(string(data), "")
			if stripped == want {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if stripped != want {
			t.Fatalf("got log:\n%q\nwant:\n%q", stripped, want)
		}
	})

	t.Run("resolves the OAuth client secret lazily", func(t *testing.T) {
		conn, _ := tmeConnect(
			McpServerEntry{
				Name: "fake",
				Config: McpServerConfig{
					Url:   &urlUnused,
					OAuth: &McpOAuthConfig{ClientSecret: "!exit 1"},
				},
				Source: "test",
			},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport { tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{}); return tr },
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		res, err := conn.CallTool(context.Background(), "echo", jsonx.NewObject(), nil)
		if err != nil || res.Content[0].(*mcpprotocol.TextContent).Text != "ok" {
			t.Fatalf("CallTool should not resolve oauth.clientSecret eagerly: err=%v res=%+v", err, res)
		}
		if _, err := conn.OAuthSettings(); err == nil || !strings.Contains(err.Error(), "oauth.clientSecret") {
			t.Fatalf("expected OAuthSettings to fail on !exit 1, got %v", err)
		}
	})

	t.Run("connects to servers with resources without listing them", func(t *testing.T) {
		var methods []string
		conn, _ := tmeConnect(
			McpServerEntry{Name: "fake", Config: McpServerConfig{Command: &cmdUnused}, Source: "test"},
			[]func() mcp.McpTransport{
				func() mcp.McpTransport {
					tr, _ := tmeNewInMemoryTransport(t, tmeTransportOptions{resources: true, methods: &methods})
					return tr
				},
			},
			nil,
		)
		defer func() { _ = conn.Close() }()

		if _, err := conn.GetClient(context.Background()); err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		if conn.State() != ServerStateConnected || !conn.HasResources() || !slices.Equal(methods, []string{"initialize", "tools/list"}) {
			t.Fatalf("state=%s hasResources=%v methods=%v", conn.State(), conn.HasResources(), methods)
		}
	})
}

func TestMcpServersSection(t *testing.T) {
	makeServer := func(name string, description *string, exposure *McpExposure, instructions *string) McpServerListing {
		cmd := "x"
		return McpServerListing{
			Entry: McpServerEntry{
				Name:   name,
				Config: McpServerConfig{Command: &cmd, Description: description, Exposure: exposure},
				Source: "test",
			},
			Instructions: instructions,
		}
	}

	t.Run("lists servers with how their tools are reached and the first line of their description", func(t *testing.T) {
		docsDesc := "Docs search.\nMore."
		deferred := core.McpExposureDeferred
		direct := core.McpExposureDirect
		directDesc := "Declared."
		inst := "From instructions."

		section := RenderServersSection([]McpServerListing{
			makeServer("docs", &docsDesc, nil, nil),
			makeServer("later", nil, &deferred, nil),
			makeServer("direct", &directDesc, &direct, nil),
			makeServer("plain", nil, nil, &inst),
		})
		if section == nil {
			t.Fatalf("expected non-nil section")
		}
		lines := strings.Split(*section, "\n")
		wantTail := []string{
			"- mcp__docs (codemode): Docs search.",
			"- mcp__later (tool_search)",
			"- mcp__plain (codemode): From instructions.",
		}
		if len(lines) < 2 || !slices.Equal(lines[1:], wantTail) {
			t.Fatalf("got lines %v, want tail %v", lines, wantTail)
		}
		if onlyDirect := RenderServersSection([]McpServerListing{makeServer("direct", &directDesc, &direct, nil)}); onlyDirect != nil {
			t.Fatalf("expected nil for direct-only servers, got %q", *onlyDirect)
		}
	})

	t.Run("shortens descriptions to fit the size limit", func(t *testing.T) {
		longDesc := strings.Repeat("x", 400)
		servers := make([]McpServerListing, 40)
		for i := range servers {
			servers[i] = makeServer(fmt.Sprintf("server%d", i), &longDesc, nil, nil)
		}
		section := RenderServersSection(servers)
		if section == nil {
			t.Fatalf("expected non-nil section")
		}
		if len([]rune(*section)) > MaxServersSectionChars {
			t.Fatalf("section exceeds %d chars: %d", MaxServersSectionChars, len([]rune(*section)))
		}
		lines := strings.Split(*section, "\n")
		if len(lines) != 41 {
			t.Fatalf("expected 41 lines, got %d", len(lines))
		}
		if !strings.Contains(*section, "- mcp__server39 (codemode): x") {
			t.Fatalf("missing server39 line in section")
		}
	})

	t.Run("leaves out the last servers when their names alone do not fit", func(t *testing.T) {
		desc := "desc"
		servers := make([]McpServerListing, 200)
		for i := range servers {
			servers[i] = makeServer(fmt.Sprintf("server-with-a-long-name-%d", i), &desc, nil, nil)
		}
		section := RenderServersSection(servers)
		if section == nil {
			t.Fatalf("expected non-nil section")
		}
		if len([]rune(*section)) > MaxServersSectionChars {
			t.Fatalf("section exceeds %d chars: %d", MaxServersSectionChars, len([]rune(*section)))
		}
		lines := strings.Split(*section, "\n")
		last := lines[len(lines)-1]
		re := regexp.MustCompile(`^- … (\d+) more servers; find their tools with searchTools\(\)$`)
		m := re.FindStringSubmatch(last)
		if len(m) != 2 {
			t.Fatalf("unexpected last line: %q", last)
		}
		var omitted int
		_, _ = fmt.Sscanf(m[1], "%d", &omitted)
		if len(lines)-2+omitted != 200 {
			t.Fatalf("lines=%d omitted=%d, expected lines-2+omitted == 200", len(lines), omitted)
		}
	})
}
