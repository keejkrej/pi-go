package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestBashTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("executes command locally and captures structured output", func(t *testing.T) {
		tool := CreateBashTool(dir)
		res, err := tool.Execute(ctx, "b1", jsonx.ObjectOf("command", "echo hello-bash"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(ttrTestTextOutput(res)); got != "hello-bash" {
			t.Fatalf("got %q, want %q", got, "hello-bash")
		}
		outStruct, ok := res.StructuredContent.(BashToolOutput)
		if !ok {
			t.Fatalf("expected BashToolOutput, got %T", res.StructuredContent)
		}
		if outStruct.ExitCode != 0 || outStruct.Truncated || !strings.Contains(outStruct.Output, "hello-bash") {
			t.Fatalf("unexpected structured output: %+v", outStruct)
		}
	})

	t.Run("non-zero exit marks IsError with output and structured non-zero code", func(t *testing.T) {
		tool := CreateBashTool(dir)
		res, err := tool.Execute(ctx, "b2", jsonx.ObjectOf("command", "echo fail-msg >&2; exit 7"), nil)
		if err != nil {
			t.Fatalf("unexpected Execute error on non-zero exit (expected IsError result): %v", err)
		}
		if res.IsError == nil || !*res.IsError {
			t.Fatalf("expected IsError=true on non-zero exit, got %v", res.IsError)
		}
		outText := ttrTestTextOutput(res)
		if !strings.Contains(outText, "Command exited with code 7") || !strings.Contains(outText, "fail-msg") {
			t.Fatalf("unexpected non-zero output text: %q", outText)
		}
		outStruct, ok := res.StructuredContent.(BashToolOutput)
		if !ok || outStruct.ExitCode != 7 {
			t.Fatalf("expected ExitCode 7 in structured output, got %#v", res.StructuredContent)
		}
	})

	t.Run("applies CommandPrefix and SpawnHook", func(t *testing.T) {
		var sawCommand string
		var sawEnv []string
		tool := CreateBashTool(dir, &BashToolOptions{
			CommandPrefix: "FOO_PREFIX=applied",
			SpawnHook: func(sctx BashSpawnContext) BashSpawnContext {
				sctx.Env = append(sctx.Env, "CUSTOM_HOOK_ENV=yes")
				return sctx
			},
			Operations: BashOperationsFunc(func(_ context.Context, command, _ string, opts BashOperationsOptions) (BashOperationsResult, error) {
				sawCommand = command
				sawEnv = opts.Env
				if opts.OnData != nil {
					opts.OnData([]byte("ok\n"))
				}
				code := 0
				return BashOperationsResult{ExitCode: &code}, nil
			}),
		})
		res, err := tool.Execute(ctx, "b3", jsonx.ObjectOf("command", "echo $FOO_PREFIX"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if sawCommand != "FOO_PREFIX=applied\necho $FOO_PREFIX" {
			t.Fatalf("unexpected prefixed command: %q", sawCommand)
		}
		if !slices.Contains(sawEnv, "CUSTOM_HOOK_ENV=yes") {
			t.Fatalf("expected CUSTOM_HOOK_ENV=yes in env")
		}
		if strings.TrimSpace(ttrTestTextOutput(res)) != "ok" {
			t.Fatalf("got %q", ttrTestTextOutput(res))
		}
	})

	t.Run("writes full output temp file when truncated by line count and preserves structured output", func(t *testing.T) {
		totalLines := DefaultMaxLines + 50
		var big strings.Builder
		for i := 1; i <= totalLines; i++ {
			fmt.Fprintf(&big, "line-%04d\n", i)
		}
		payload := big.String()

		var mu sync.Mutex
		var updates []string
		tool := CreateBashTool(dir, &BashToolOptions{
			Operations: BashOperationsFunc(func(_ context.Context, _ string, _ string, opts BashOperationsOptions) (BashOperationsResult, error) {
				if opts.OnData != nil {
					for chunkStart := 0; chunkStart < len(payload); chunkStart += 256 {
						end := min(len(payload), chunkStart+256)
						opts.OnData([]byte(payload[chunkStart:end]))
					}
				}
				code := 0
				return BashOperationsResult{ExitCode: &code}, nil
			}),
		})

		res, err := tool.Execute(ctx, "b4", jsonx.ObjectOf("command", "emit-many-lines"), func(partial agent.AgentToolResult) {
			mu.Lock()
			updates = append(updates, ttrTestTextOutput(partial))
			mu.Unlock()
		})
		if err != nil {
			t.Fatal(err)
		}
		details, ok := res.Details.(*BashToolDetails)
		if !ok || details == nil || details.FullOutputPath == nil || details.Truncation == nil {
			t.Fatalf("expected truncation + fullOutputPath details, got %#v", res.Details)
		}
		t.Cleanup(func() { _ = os.Remove(*details.FullOutputPath) })

		fullBytes, readErr := os.ReadFile(*details.FullOutputPath)
		if readErr != nil {
			t.Fatalf("failed reading full output temp file %s: %v", *details.FullOutputPath, readErr)
		}
		if string(fullBytes) != payload {
			t.Fatalf("full output mismatch: len %d vs %d", len(fullBytes), len(payload))
		}
		if len(updates) == 0 {
			t.Fatalf("expected streaming updates")
		}
		outStruct, ok := res.StructuredContent.(BashToolOutput)
		if !ok || outStruct.Truncated || outStruct.FullOutputPath != nil || outStruct.Output != payload {
			t.Fatalf("expected untruncated 1MB structured output below 1MB cap, got Truncated=%v FullOutputPath=%v len=%d", outStruct.Truncated, outStruct.FullOutputPath, len(outStruct.Output))
		}
		if !strings.Contains(ttrTestTextOutput(res), "[Showing lines ") || !strings.Contains(ttrTestTextOutput(res), *details.FullOutputPath) {
			t.Fatalf("expected line-range notice with temp path in tool text")
		}
	})

	t.Run("errors cleanly when working directory does not exist", func(t *testing.T) {
		missingCwd := filepath.Join(dir, "missing-cwd")
		ops := CreateLocalBashOperations()
		_, err := ops.Exec(ctx, "echo hi", missingCwd, BashOperationsOptions{})
		if err == nil || !strings.Contains(err.Error(), "Working directory does not exist") {
			t.Fatalf("expected missing cwd error, got %v", err)
		}
	})

	t.Run("enforces timeout on hung subprocess", func(t *testing.T) {
		tool := CreateBashTool(dir)
		_, err := tool.Execute(ctx, "b-timeout", jsonx.ObjectOf("command", "sleep 5", "timeout", 0.15), nil)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})

	t.Run("CreateBashToolDefinition reflects ExposeSessionEnvironment in guidelines", func(t *testing.T) {
		showEnv := true
		hideEnv := false
		defShow := CreateBashToolDefinition(dir, &BashToolOptions{ExposeSessionEnvironment: &showEnv})
		defHide := CreateBashToolDefinition(dir, &BashToolOptions{ExposeSessionEnvironment: &hideEnv})
		if len(defShow.PromptGuidelines) == 0 {
			t.Fatalf("expected PI_* guideline when ExposeSessionEnvironment is true")
		}
		if len(defHide.PromptGuidelines) != 0 {
			t.Fatalf("expected no PI_* guideline when ExposeSessionEnvironment is false, got %v", defHide.PromptGuidelines)
		}
	})
}

func TestPowerShellTool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	t.Run("definition uses powershell metadata and ExposeSessionEnvironment toggles guidelines", func(t *testing.T) {
		def := CreatePowerShellToolDefinition(dir)
		if def.Name != "powershell" || def.Label != "powershell" || def.PromptSnippet == nil {
			t.Fatalf("unexpected powershell definition metadata: %+v", def)
		}
		if len(def.PromptGuidelines) != 1 || !strings.Contains(def.PromptGuidelines[0], "PI_*") {
			t.Fatalf("unexpected default guidelines: %v", def.PromptGuidelines)
		}

		hide := false
		defHidden := CreatePowerShellToolDefinition(dir, &PowerShellToolOptions{ExposeSessionEnvironment: &hide})
		if len(defHidden.PromptGuidelines) != 0 {
			t.Fatalf("expected empty guidelines when ExposeSessionEnvironment=false, got %v", defHidden.PromptGuidelines)
		}
	})

	t.Run("executes via custom PowerShellOperations and SpawnHook", func(t *testing.T) {
		var sawCmd, sawCwd string
		var sawEnv []string
		tool := CreatePowerShellTool(dir, &PowerShellToolOptions{
			SpawnHook: func(sctx PowerShellSpawnContext) PowerShellSpawnContext {
				sctx.Command = "$PS_HOOK = '1'; " + sctx.Command
				sctx.Env = append(sctx.Env, "PS_TEST_VAR=42")
				return sctx
			},
			Operations: BashOperationsFunc(func(_ context.Context, command, cwd string, opts BashOperationsOptions) (BashOperationsResult, error) {
				sawCmd = command
				sawCwd = cwd
				sawEnv = opts.Env
				if opts.OnData != nil {
					opts.OnData([]byte("ps-output\r\n"))
				}
				code := 0
				return BashOperationsResult{ExitCode: &code}, nil
			}),
		})

		res, err := tool.Execute(ctx, "ps-1", jsonx.ObjectOf("command", "Write-Output 'hi'"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if sawCwd != dir || !strings.HasPrefix(sawCmd, "$PS_HOOK = '1'; ") {
			t.Fatalf("unexpected hook result: cwd=%q cmd=%q", sawCwd, sawCmd)
		}
		if !slices.Contains(sawEnv, "PS_TEST_VAR=42") {
			t.Fatalf("expected PS_TEST_VAR=42 in env")
		}
		if strings.TrimSpace(ttrTestTextOutput(res)) != "ps-output" {
			t.Fatalf("got %q", ttrTestTextOutput(res))
		}
	})

	t.Run("tpWin32Basename handles Windows and POSIX separators", func(t *testing.T) {
		if got := tpWin32Basename(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`); got != "powershell.exe" {
			t.Fatalf("got %q", got)
		}
		if got := tpWin32Basename("/usr/local/bin/pwsh"); got != "pwsh" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("executes local pwsh when available", func(t *testing.T) {
		if _, err := utils.GetPowerShellConfig(); err != nil {
			if runtime.GOOS != "windows" {
				t.Skipf("pwsh not installed on %s: %v", runtime.GOOS, err)
			}
			t.Fatal(err)
		}
		tool := CreatePowerShellTool(dir)
		res, err := tool.Execute(ctx, "ps-local", jsonx.ObjectOf("command", "Write-Output 'hello from pwsh'"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ttrTestTextOutput(res), "hello from pwsh") {
			t.Fatalf("unexpected pwsh output: %q", ttrTestTextOutput(res))
		}
	})
}
