// Ported from packages/coding-agent/test/mcp-command.test.ts (pi v1.5.2).

package mcpext

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	tmcStdioFixtureArg = "pi-test-stdio-fixture"
	tmcStdioCwdArg     = "pi-test-stdio-cwd"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == tmcStdioFixtureArg || os.Args[1] == tmcStdioCwdArg) {
		tmcServeStdioFixture(os.Args[1])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func tmcServeStdioFixture(mode string) {
	_, _ = fmt.Fprintln(os.Stderr, "stdio fixture ready")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parsed, err := jsonx.ParseString(line)
		if err != nil {
			continue
		}
		obj, ok := parsed.(*jsonx.Object)
		if !ok || obj == nil {
			continue
		}
		idVal, hasID := obj.Get("id")
		if !hasID || idVal == nil {
			continue
		}
		methodAny, _ := obj.Get("method")
		method, _ := methodAny.(string)

		var resp any
		switch method {
		case "initialize":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      idVal,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "stdio-fixture", "version": "1.0.0"},
				},
			}
		case "tools/list":
			toolName := "echo"
			if mode == tmcStdioCwdArg {
				toolName = "cwd"
			}
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      idVal,
				"result": map[string]any{
					"tools": []any{
						map[string]any{
							"name":        toolName,
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			}
		case "tools/call":
			var text string
			if mode == tmcStdioCwdArg {
				wd, _ := os.Getwd()
				text = wd
				if len(os.Args) > 2 {
					text += "|" + strings.Join(os.Args[2:], "|")
				}
			} else if paramsRaw, ok := obj.Get("params"); ok {
				if paramsObj, ok := paramsRaw.(*jsonx.Object); ok && paramsObj != nil {
					if argsRaw, ok := paramsObj.Get("arguments"); ok {
						if argsObj, ok := argsRaw.(*jsonx.Object); ok && argsObj != nil {
							if v, ok := argsObj.Get("text"); ok {
								text = fmt.Sprint(v)
							}
						}
					}
				}
			}
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      idVal,
				"result": map[string]any{
					"content": []any{map[string]any{"type": "text", "text": text}},
				},
			}
		case "ping":
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      idVal,
				"result":  map[string]any{},
			}
		default:
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      idVal,
				"error": map[string]any{
					"code":    -32601,
					"message": "not found",
				},
			}
		}
		out, err := jsonx.Stringify(resp)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintln(os.Stdout, out)
	}
}

func tmcTestExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return exe
}

func tmcRun(
	t *testing.T,
	args []string,
	servers any,
	dir string,
	providerToken func(ctx context.Context, provider string) (*string, error),
	httpClient *http.Client,
) (exitCode int, output string, agentDir string) {
	t.Helper()
	agentDir = dir
	if agentDir == "" {
		agentDir = t.TempDir()
	}
	if servers != nil {
		raw, err := jsonx.Stringify(map[string]any{"mcpServers": servers})
		if err != nil {
			t.Fatalf("stringify mcpServers: %v", err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(raw), 0o600); err != nil {
			t.Fatalf("write mcp.json: %v", err)
		}
	}
	var lines []string
	code := RunMcpCommand(args, McpCommandOptions{
		Cwd:           agentDir,
		AgentDir:      agentDir,
		ProviderToken: providerToken,
		HTTPClient:    httpClient,
		Log:           func(line string) { lines = append(lines, line) },
		Error:         func(line string) { lines = append(lines, line) },
	})
	return code, strings.Join(lines, "\n"), agentDir
}

func tmcReadConfig(t *testing.T, path string) *jsonx.Object {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	parsed, err := jsonx.Parse(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		t.Fatalf("expected object in %s, got %T", path, parsed)
	}
	return obj
}

func tmcRequireServerObj(t *testing.T, root *jsonx.Object, name string) *jsonx.Object {
	t.Helper()
	msRaw, ok := root.Get("mcpServers")
	if !ok {
		t.Fatalf("missing mcpServers key in %+v", root.Keys())
	}
	msObj, ok := msRaw.(*jsonx.Object)
	if !ok || msObj == nil {
		t.Fatalf("expected mcpServers object, got %T", msRaw)
	}
	srvRaw, ok := msObj.Get(name)
	if !ok {
		t.Fatalf("missing server %q in mcpServers %v", name, msObj.Keys())
	}
	srvObj, ok := srvRaw.(*jsonx.Object)
	if !ok || srvObj == nil {
		t.Fatalf("expected server %q object, got %T", name, srvRaw)
	}
	return srvObj
}

func TestPiMcpCli(t *testing.T) {
	exe := tmcTestExecutable(t)
	fixtureCfg := map[string]any{"command": exe, "args": []string{tmcStdioFixtureArg}}
	parkedCfg := map[string]any{"command": exe, "args": []string{tmcStdioFixtureArg}, "enabled": false}
	servers := jsonx.NewObject()
	servers.Set("fixture", fixtureCfg)
	servers.Set("broken", map[string]any{"command": "pi-test-missing-mcp-server"})
	servers.Set("parked", parkedCfg)
	servers.Set("bad", map[string]any{"args": []string{"no command"}})

	t.Run("lists servers with their state, tools, and errors, and fails while anything is wrong", func(t *testing.T) {
		code, output, _ := tmcRun(t, []string{"list"}, servers, "", nil, nil)
		if code != 1 {
			t.Fatalf("expected exitCode 1, got %d; output:\n%s", code, output)
		}
		for _, want := range []string{
			"fixture: connected, 1 tool (codemode, global)\n",
			"  tools: echo",
			"broken: failed (codemode, global)\n  pi-test-missing-mcp-server\n  ",
			"parked: disabled (codemode, global)",
			"config error: ",
			`server "bad" needs either "command"`,
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("expected output to contain %q, got:\n%s", want, output)
			}
		}

		okCode, okOut, _ := tmcRun(t, []string{"list"}, map[string]any{"fixture": fixtureCfg}, "", nil, nil)
		if okCode != 0 {
			t.Fatalf("expected exitCode 0 for healthy fixture, got %d; output:\n%s", okCode, okOut)
		}
	})

	t.Run("prints JSON for scripts", func(t *testing.T) {
		pair := jsonx.NewObject()
		pair.Set("fixture", fixtureCfg)
		pair.Set("parked", parkedCfg)
		code, output, _ := tmcRun(t, []string{"list", "--json"}, pair, "", nil, nil)
		if code != 0 {
			t.Fatalf("expected exitCode 0, got %d; output:\n%s", code, output)
		}
		var parsed struct {
			Servers []struct {
				Name  string   `json:"name"`
				State string   `json:"state"`
				Tools []string `json:"tools"`
			} `json:"servers"`
		}
		if err := jsonx.Unmarshal([]byte(output), &parsed); err != nil {
			t.Fatalf("unmarshal list --json output: %v\n%s", err, output)
		}
		if len(parsed.Servers) != 2 ||
			parsed.Servers[0].Name != "fixture" || parsed.Servers[0].State != "connected" || len(parsed.Servers[0].Tools) != 1 || parsed.Servers[0].Tools[0] != "echo" ||
			parsed.Servers[1].Name != "parked" || parsed.Servers[1].State != "disabled" || len(parsed.Servers[1].Tools) != 0 {
			t.Fatalf("unexpected JSON servers: %+v", parsed.Servers)
		}
	})

	t.Run("rejects unknown servers and servers without OAuth for login and logout", func(t *testing.T) {
		code, output, _ := tmcRun(t, []string{"login", "nope"}, servers, "", nil, nil)
		if code != 1 || output != `No MCP server named "nope". Configured: fixture, broken, parked.` {
			t.Fatalf("unexpected login nope: code=%d output=%q", code, output)
		}
		code, output, _ = tmcRun(t, []string{"logout", "fixture"}, servers, "", nil, nil)
		if code != 1 || output != `MCP server "fixture" does not use OAuth. Only HTTP servers without an Authorization header do.` {
			t.Fatalf("unexpected logout fixture: code=%d output=%q", code, output)
		}
		code, _, _ = tmcRun(t, []string{"frobnicate"}, servers, "", nil, nil)
		if code != 1 {
			t.Fatalf("expected exitCode 1 for unknown subcommand, got %d", code)
		}
	})

	t.Run("suggests a configured server for a mistyped name", func(t *testing.T) {
		_, output, _ := tmcRun(t, []string{"login", "fixtures"}, servers, "", nil, nil)
		want := `No MCP server named "fixtures". Did you mean "fixture"? Configured: fixture, broken, parked.`
		if output != want {
			t.Fatalf("got %q, want %q", output, want)
		}
	})

	t.Run("sends the token of the provider named by auth.provider", func(t *testing.T) {
		srv := tmoStartOAuthMcpServer(t)
		radius := map[string]any{
			"radius": map[string]any{
				"url":  srv.URL,
				"auth": map[string]any{"provider": "radius"},
			},
		}
		token := srv.IssueAccessToken()
		var requested []string
		code, out, _ := tmcRun(t, []string{"list"}, radius, "", func(_ context.Context, provider string) (*string, error) {
			requested = append(requested, provider)
			return &token, nil
		}, srv.httpServer.Client())
		if code != 0 || !strings.Contains(out, "radius: connected, 1 tool") {
			t.Fatalf("expected connected radius, code=%d out=%q", code, out)
		}
		if len(requested) == 0 || requested[0] != "radius" {
			t.Fatalf("expected requested provider 'radius', got %v", requested)
		}

		code, out, _ = tmcRun(t, []string{"list"}, radius, "", func(context.Context, string) (*string, error) {
			return nil, nil
		}, srv.httpServer.Client())
		if code != 1 || !strings.Contains(out, "radius: needs sign-in (codemode, global)") || !strings.Contains(out, "  sign in with: /login radius in pi") {
			t.Fatalf("expected needs sign-in hint, code=%d out=%q", code, out)
		}

		for _, cmd := range []string{"login", "logout"} {
			c, o, _ := tmcRun(t, []string{cmd, "radius"}, radius, "", nil, srv.httpServer.Client())
			want := fmt.Sprintf(`MCP server "radius" uses the credentials of provider "radius". Run "pi auth %s radius".`, cmd)
			if c != 1 || o != want {
				t.Fatalf("%s radius: code=%d out=%q want=%q", cmd, c, o, want)
			}
		}
	})

	t.Run("reports options as typed", func(t *testing.T) {
		_, output, _ := tmcRun(t, []string{"list", "-l"}, servers, "", nil, nil)
		if !strings.Contains(output, "Unknown option -l.") {
			t.Fatalf("expected Unknown option -l., got:\n%s", output)
		}
	})

	t.Run("restricts login and logout to project servers with --local", func(t *testing.T) {
		_, _, agentDir := tmcRun(t, []string{"add", "-l", "local", "--", "node", "server.js"}, servers, "", nil, nil)
		trusted := true
		if err := core.NewProjectTrustStore(agentDir).Set(agentDir, &trusted); err != nil {
			t.Fatalf("trust project: %v", err)
		}

		code, out, _ := tmcRun(t, []string{"logout", "-l", "local"}, nil, agentDir, nil, nil)
		if code != 1 || out != `MCP server "local" does not use OAuth. Only HTTP servers without an Authorization header do.` {
			t.Fatalf("unexpected local logout: code=%d out=%q", code, out)
		}
		code, out, _ = tmcRun(t, []string{"login", "-l", "fixture"}, nil, agentDir, nil, nil)
		want := fmt.Sprintf(`MCP server "fixture" is defined in %s, not in the project; omit --local.`, filepath.Join(agentDir, "mcp.json"))
		if code != 1 || out != want {
			t.Fatalf("unexpected local login fixture: code=%d out=%q want=%q", code, out, want)
		}
	})

	t.Run("adds stdio servers and passes options after the command through", func(t *testing.T) {
		code, out, agentDir := tmcRun(t, []string{"add", "--env", "A=1", "--env", "B=x=y", "files", "--", "npx", "-y", "server", "--root", "."}, nil, "", nil, nil)
		if code != 0 || !strings.Contains(out, `Added global MCP server "files"`) {
			t.Fatalf("add failed: code=%d out=%q", code, out)
		}
		cfg := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		files := tmcRequireServerObj(t, cfg, "files")
		cmd, _ := files.Get("command")
		if cmd != "npx" {
			t.Fatalf("expected command npx, got %v", cmd)
		}
		if gotArgs, _ := files.Get("args"); fmt.Sprint(gotArgs) != "[-y server --root .]" {
			t.Fatalf("unexpected args: %v", gotArgs)
		}
		envRaw, _ := files.Get("env")
		envObj, _ := envRaw.(*jsonx.Object)
		if envObj == nil {
			t.Fatalf("expected env object, got %T", envRaw)
		}
		if v, _ := envObj.Get("A"); v != "1" {
			t.Fatalf("expected A=1, got %v", v)
		}
		if v, _ := envObj.Get("B"); v != "x=y" {
			t.Fatalf("expected B=x=y, got %v", v)
		}

		_, repOut, _ := tmcRun(t, []string{"add", "files", "node", "server.js", "--port", "1"}, nil, agentDir, nil, nil)
		if !strings.Contains(repOut, `Replaced global MCP server "files"`) {
			t.Fatalf("expected Replaced message, got %q", repOut)
		}
		cfg2 := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		files2 := tmcRequireServerObj(t, cfg2, "files")
		if cmd2, _ := files2.Get("command"); cmd2 != "node" {
			t.Fatalf("expected command node, got %v", cmd2)
		}
		if args2, _ := files2.Get("args"); fmt.Sprint(args2) != "[server.js --port 1]" {
			t.Fatalf("expected [server.js --port 1], got %v", args2)
		}
		if files2.Has("env") {
			t.Fatalf("expected env cleared on replace, got keys %v", files2.Keys())
		}
	})

	t.Run("adds HTTP servers and keeps other content of the file", func(t *testing.T) {
		code, out, agentDir := tmcRun(t, []string{
			"add", "docs",
			"--url", "https://example.com/mcp",
			"--bearer-token-env-var", "DOCS_TOKEN",
			"--header", "X-Team=core",
			"--exposure", "direct",
			"--description", "Product docs",
		}, map[string]any{"fixture": fixtureCfg}, "", nil, nil)
		if code != 0 || strings.Contains(out, "mcp login") {
			t.Fatalf("unexpected add HTTP result: code=%d out=%q", code, out)
		}
		cfg := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		_ = tmcRequireServerObj(t, cfg, "fixture")
		docs := tmcRequireServerObj(t, cfg, "docs")
		if u, _ := docs.Get("url"); u != "https://example.com/mcp" {
			t.Fatalf("unexpected url: %v", u)
		}
		if exp, _ := docs.Get("exposure"); exp != "direct" {
			t.Fatalf("unexpected exposure: %v", exp)
		}
		if desc, _ := docs.Get("description"); desc != "Product docs" {
			t.Fatalf("unexpected description: %v", desc)
		}
		hdrRaw, _ := docs.Get("headers")
		hdrObj, _ := hdrRaw.(*jsonx.Object)
		if hdrObj == nil {
			t.Fatalf("expected headers object, got %T", hdrRaw)
		}
		if v, _ := hdrObj.Get("X-Team"); v != "core" {
			t.Fatalf("expected X-Team=core, got %v", v)
		}
		if v, _ := hdrObj.Get("Authorization"); v != "Bearer ${DOCS_TOKEN}" {
			t.Fatalf("expected Bearer ${DOCS_TOKEN}, got %v", v)
		}

		_, oauthOut, _ := tmcRun(t, []string{
			"add", "sentry",
			"--url", "https://mcp.sentry.dev/mcp",
			"--oauth-client-id", "pi",
			"--oauth-client-name", "Claude Code",
		}, nil, agentDir, nil, nil)
		if !strings.Contains(oauthOut, "If it requires sign-in: pi mcp login sentry") {
			t.Fatalf("expected login hint, got %q", oauthOut)
		}
		cfgSentry := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		sentry := tmcRequireServerObj(t, cfgSentry, "sentry")
		oauthRaw, _ := sentry.Get("oauth")
		oauthObj, _ := oauthRaw.(*jsonx.Object)
		if oauthObj == nil {
			t.Fatalf("expected oauth object")
		}
		if v, _ := oauthObj.Get("clientId"); v != "pi" {
			t.Fatalf("expected clientId pi, got %v", v)
		}
		if v, _ := oauthObj.Get("clientName"); v != "Claude Code" {
			t.Fatalf("expected clientName Claude Code, got %v", v)
		}

		tmcRun(t, []string{"add", "linear", "--url", "https://mcp.linear.app/mcp", "--oauth-client-registration", "dcr"}, nil, agentDir, nil, nil)
		cfgLinear := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		linear := tmcRequireServerObj(t, cfgLinear, "linear")
		loRaw, _ := linear.Get("oauth")
		loObj, _ := loRaw.(*jsonx.Object)
		if loObj == nil {
			t.Fatalf("expected linear oauth object")
		}
		if v, _ := loObj.Get("clientRegistration"); v != "dcr" {
			t.Fatalf("expected clientRegistration dcr, got %v", v)
		}
	})

	t.Run("rejects invalid add invocations without writing", func(t *testing.T) {
		cases := [][]string{
			{"add", "x"},
			{"add", "x", "--url", "https://example.com", "--", "cmd"},
			{"add", "bad name", "--", "cmd"},
			{"add", "x", "--url", "ftp://example.com"},
			{"add", "x", "--env", "A=1", "--url", "https://example.com"},
			{"add", "x", "--header", "A=1", "--", "cmd"},
			{"add", "x", "--env", "NOVALUE", "--", "cmd"},
			{"add", "x", "--exposure", "loud", "--", "cmd"},
			{"add", "x", "--url", "https://example.com", "--oauth-client-registration", "maybe"},
		}
		for _, tc := range cases {
			code, _, agentDir := tmcRun(t, tc, nil, "", nil, nil)
			if code != 1 {
				t.Fatalf("expected exitCode 1 for %v, got %d", tc, code)
			}
			if _, err := os.Stat(filepath.Join(agentDir, "mcp.json")); !os.IsNotExist(err) {
				t.Fatalf("expected no mcp.json written for invalid %v", tc)
			}
		}
	})

	t.Run("adds and removes project servers", func(t *testing.T) {
		code, out, agentDir := tmcRun(t, []string{"add", "-l", "local", "--", "node", "server.js"}, nil, "", nil, nil)
		if code != 0 || !strings.Contains(out, "The project is not trusted") {
			t.Fatalf("expected untrusted project note, code=%d out=%q", code, out)
		}
		projectConfig := filepath.Join(agentDir, ".pi", "mcp.json")
		cfg := tmcReadConfig(t, projectConfig)
		localObj := tmcRequireServerObj(t, cfg, "local")
		if cmd, _ := localObj.Get("command"); cmd != "node" {
			t.Fatalf("expected command node, got %v", cmd)
		}

		wrongCode, wrongOut, _ := tmcRun(t, []string{"remove", "local"}, nil, agentDir, nil, nil)
		if wrongCode != 1 || !strings.Contains(wrongOut, fmt.Sprintf("It is defined in %s; use --local.", projectConfig)) {
			t.Fatalf("unexpected wrong scope remove: code=%d out=%q", wrongCode, wrongOut)
		}

		remCode, remOut, _ := tmcRun(t, []string{"remove", "local", "--local"}, nil, agentDir, nil, nil)
		if remCode != 0 || !strings.Contains(remOut, `Removed project MCP server "local"`) {
			t.Fatalf("unexpected local remove: code=%d out=%q", remCode, remOut)
		}
		cfgAfter := tmcReadConfig(t, projectConfig)
		msAfter, _ := cfgAfter.Get("mcpServers")
		msObjAfter, _ := msAfter.(*jsonx.Object)
		if msObjAfter == nil || msObjAfter.Len() != 0 {
			t.Fatalf("expected empty mcpServers after remove, got %v", msAfter)
		}
	})

	t.Run("removes global servers", func(t *testing.T) {
		code, _, agentDir := tmcRun(t, []string{"remove", "broken"}, servers, "", nil, nil)
		if code != 0 {
			t.Fatalf("remove broken failed: code=%d", code)
		}
		cfg := tmcReadConfig(t, filepath.Join(agentDir, "mcp.json"))
		msRaw, _ := cfg.Get("mcpServers")
		msObj, _ := msRaw.(*jsonx.Object)
		gotKeys := msObj.Keys()
		wantKeys := []string{"fixture", "parked", "bad"}
		if len(gotKeys) != len(wantKeys) {
			t.Fatalf("got keys %v, want %v", gotKeys, wantKeys)
		}
		for i, k := range wantKeys {
			if gotKeys[i] != k {
				t.Fatalf("got keys %v, want %v", gotKeys, wantKeys)
			}
		}

		missCode, missOut, _ := tmcRun(t, []string{"remove", "broken"}, nil, agentDir, nil, nil)
		if missCode != 1 || !strings.Contains(missOut, `No global MCP server named "broken"`) {
			t.Fatalf("unexpected missing remove: code=%d out=%q", missCode, missOut)
		}
	})
}
