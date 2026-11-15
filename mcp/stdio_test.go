// Ported from packages/mcp/test/stdio.test.ts (pi v1.5.2).

package mcp

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp/protocol"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("PI_MCP_STDIO_FIXTURE"); mode != "" {
		tstdioRunFixture(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func tstdioRunFixture(mode string) {
	switch mode {
	case "stdio":
		fmt.Fprintln(os.Stderr, "stdio fixture ready")
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				tstdioHandleLine(trimmed)
			}
			if err != nil {
				return
			}
		}
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		exe, _ := os.Executable()
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "PI_MCP_STDIO_FIXTURE=grandchild-ignore-term")
		_ = cmd.Start()
		if cmd.Process != nil {
			fmt.Fprintf(os.Stderr, "grandchild %d\n", cmd.Process.Pid)
		}
		go func() {
			reader := bufio.NewReader(os.Stdin)
			for {
				line, err := reader.ReadString('\n')
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					tstdioHandleInitOnly(trimmed)
				}
				if err != nil {
					return
				}
			}
		}()
		tstdioBlockForever()
	case "grandchild-ignore-term":
		signal.Ignore(syscall.SIGTERM)
		tstdioBlockForever()
	case "exiting-parent":
		exe, _ := os.Executable()
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "PI_MCP_STDIO_FIXTURE=helper-sleep")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Start()
		if cmd.Process != nil {
			fmt.Fprintf(os.Stderr, "helper %d\n", cmd.Process.Pid)
		}
		os.Exit(0)
	case "helper-sleep":
		tstdioBlockForever()
	}
}

func tstdioBlockForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func tstdioHandleLine(line string) {
	parsed, err := jsonx.ParseString(line)
	if err != nil {
		return
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return
	}
	idVal, hasId := obj.Get("id")
	if !hasId {
		return
	}
	methodVal, _ := obj.Get("method")
	method, _ := methodVal.(string)
	var out *jsonx.Object
	switch method {
	case "initialize":
		out = jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"protocolVersion", "2025-06-18",
				"capabilities", jsonx.ObjectOf("tools", jsonx.NewObject()),
				"serverInfo", jsonx.ObjectOf("name", "stdio-fixture", "version", "1.0.0"),
			),
		)
	case "tools/list":
		out = jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"tools", []any{
					jsonx.ObjectOf("name", "echo", "inputSchema", jsonx.ObjectOf("type", "object")),
				},
			),
		)
	case "tools/call":
		var textVal any
		if p, ok := obj.Get("params"); ok {
			if po, ok := p.(*jsonx.Object); ok {
				if args, ok := po.Get("arguments"); ok {
					if ao, ok := args.(*jsonx.Object); ok {
						textVal, _ = ao.Get("text")
					}
				}
			}
		}
		out = jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"result", jsonx.ObjectOf(
				"content", []any{jsonx.ObjectOf("type", "text", "text", fmt.Sprint(textVal))},
			),
		)
	case "ping":
		out = jsonx.ObjectOf("jsonrpc", "2.0", "id", idVal, "result", jsonx.NewObject())
	default:
		out = jsonx.ObjectOf(
			"jsonrpc", "2.0",
			"id", idVal,
			"error", jsonx.ObjectOf("code", -32601, "message", "not found"),
		)
	}
	b, _ := jsonx.Marshal(out)
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func tstdioHandleInitOnly(line string) {
	parsed, err := jsonx.ParseString(line)
	if err != nil {
		return
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil {
		return
	}
	idVal, hasId := obj.Get("id")
	methodVal, _ := obj.Get("method")
	if !hasId || methodVal != "initialize" {
		return
	}
	out := jsonx.ObjectOf(
		"jsonrpc", "2.0",
		"id", idVal,
		"result", jsonx.ObjectOf(
			"protocolVersion", "2025-06-18",
			"capabilities", jsonx.NewObject(),
			"serverInfo", jsonx.ObjectOf("name", "stubborn-fixture", "version", "1.0.0"),
		),
	)
	b, _ := jsonx.Marshal(out)
	_, _ = os.Stdout.Write(append(b, '\n'))
}

func tstdioExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func tstdioIsAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func tstdioWaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

func TestStdio_ConnectsAndCapturesStderr(t *testing.T) {
	var mu sync.Mutex
	var stderrChunks []string
	env := NewStdioTransport(&StdioTransportOptions{
		Command: tstdioExe(t),
		Env:     tstdioFixtureEnv("stdio"),
		OnStderr: func(chunk string) {
			mu.Lock()
			stderrChunks = append(stderrChunks, chunk)
			mu.Unlock()
		},
	})
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "stdio-test", Version: "1.0.0"},
	})
	if _, err := client.Connect(env); err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %#v", tools)
	}
	res, err := client.CallTool(t.Context(), "echo", jsonx.ObjectOf("text", "hello"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 || res.Content[0].(*protocol.TextContent).Text != "hello" {
		t.Fatalf("res = %#v", res)
	}
	if env.Pid() == nil || *env.Pid() <= 0 {
		t.Fatalf("expected valid pid, got %v", env.Pid())
	}
	if !tstdioWaitFor(2*time.Second, func() bool {
		return strings.Contains(env.Stderr(), "stdio fixture ready")
	}) {
		t.Fatalf("expected stderr to contain fixture ready banner, got %q", env.Stderr())
	}
	mu.Lock()
	joined := strings.Join(stderrChunks, "")
	mu.Unlock()
	if !strings.Contains(joined, "stdio fixture ready") {
		t.Fatalf("stderrChunks = %q", joined)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if client.ConnectionState() != ClientStateClosed {
		t.Fatalf("state = %q", client.ConnectionState())
	}
}

func TestStdio_KillsStubbornServerAndGrandchildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process group kill test")
	}
	closeTimeout := int64(100)
	tr := NewStdioTransport(&StdioTransportOptions{
		Command:        tstdioExe(t),
		Env:            tstdioFixtureEnv("stubborn"),
		CloseTimeoutMs: &closeTimeout,
	})
	client := NewMcpClient(&McpClientOptions{
		Implementation: protocol.Implementation{Name: "stdio-test", Version: "1.0.0"},
	})
	if _, err := client.Connect(tr); err != nil {
		t.Fatal(err)
	}

	var grandchild int
	re := regexp.MustCompile(`grandchild (\d+)`)
	if !tstdioWaitFor(2*time.Second, func() bool {
		m := re.FindStringSubmatch(tr.Stderr())
		if len(m) == 2 {
			grandchild, _ = strconv.Atoi(m[1])
			return true
		}
		return false
	}) {
		t.Fatalf("did not observe grandchild pid in stderr: %q", tr.Stderr())
	}
	defer func() {
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
	}()

	start := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) >= 5*time.Second {
		t.Fatalf("close took too long: %v", time.Since(start))
	}
	if !tstdioWaitFor(2*time.Second, func() bool { return !tstdioIsAlive(grandchild) }) {
		t.Fatalf("grandchild %d still alive after Close", grandchild)
	}
}

func TestStdio_KillsChildrenHoldingPipesAfterParentExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process group kill test")
	}
	closeTimeout := int64(100)
	tr := NewStdioTransport(&StdioTransportOptions{
		Command:        tstdioExe(t),
		Env:            tstdioFixtureEnv("exiting-parent"),
		CloseTimeoutMs: &closeTimeout,
	})
	var mu sync.Mutex
	closeEvents := 0
	tr.OnClose(func() {
		mu.Lock()
		closeEvents++
		mu.Unlock()
	})
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	pid := tr.Pid()
	if pid == nil {
		t.Fatal("expected pid")
	}
	serverPid := *pid

	var helper int
	re := regexp.MustCompile(`helper (\d+)`)
	if !tstdioWaitFor(2*time.Second, func() bool {
		m := re.FindStringSubmatch(tr.Stderr())
		if len(m) == 2 {
			helper, _ = strconv.Atoi(m[1])
			return true
		}
		return false
	}) {
		t.Fatalf("did not observe helper pid in stderr: %q", tr.Stderr())
	}
	defer func() {
		_ = syscall.Kill(helper, syscall.SIGKILL)
	}()

	if !tstdioWaitFor(2*time.Second, func() bool { return !tstdioIsAlive(serverPid) }) {
		t.Fatalf("parent %d did not exit cleanly", serverPid)
	}
	if !tstdioIsAlive(helper) {
		t.Fatalf("expected helper %d to remain alive before Close", helper)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if !tstdioWaitFor(2*time.Second, func() bool { return !tstdioIsAlive(helper) }) {
		t.Fatalf("helper %d still alive after Close", helper)
	}
	mu.Lock()
	gotCloses := closeEvents
	mu.Unlock()
	if gotCloses != 1 {
		t.Fatalf("closeEvents = %d, want 1", gotCloses)
	}
}

func tstdioFixtureEnv(mode string) *omap.Map[string, string] {
	// InheritEnv defaults to nil (inherit) and merges Env entries on top.
	m := omap.NewMap[string, string]()
	m.Set("PI_MCP_STDIO_FIXTURE", mode)
	return m
}
