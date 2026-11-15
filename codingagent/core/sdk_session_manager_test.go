// Ported from packages/coding-agent/test/sdk-session-manager.test.ts (pi v1.5.2).

package core_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tssmExtractText(res agent.AgentToolResult) string {
	var b strings.Builder
	for _, item := range res.Content {
		if tc, ok := item.(*ai.TextContent); ok && tc != nil {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func tssmFindTool(tools []*agent.AgentTool, name string) *agent.AgentTool {
	for _, tool := range tools {
		if tool != nil && tool.Name == name {
			return tool
		}
	}
	return nil
}

func TestSdkSessionManagerDefaults(t *testing.T) {
	ctx := context.Background()
	model := providers.GetBuiltinModel(ai.KnownProviderAnthropic, "claude-sonnet-4-5")
	if model == nil {
		t.Fatal("claude-sonnet-4-5 model not found")
	}

	t.Run("uses agentDir for the default persisted session path", func(t *testing.T) {
		root := t.TempDir()
		cwd := filepath.Join(root, "project")
		agentDir := filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}

		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:      cwd,
			AgentDir: agentDir,
			Model:    model,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		stripped := strings.TrimLeft(cwd, `/\`)
		safePath := "--" + regexp.MustCompile(`[/\\:]`).ReplaceAllString(stripped, "-") + "--"
		expectedSessionDir := filepath.Join(agentDir, "sessions", safePath)

		sessionDir := res.Session.SessionManager.GetSessionDir()
		sessionFile := res.Session.SessionManager.GetSessionFile()
		if sessionDir != expectedSessionDir {
			t.Fatalf("SessionDir = %q, want %q", sessionDir, expectedSessionDir)
		}
		if sessionFile == nil || !strings.HasPrefix(*sessionFile, expectedSessionDir+string(filepath.Separator)) {
			t.Fatalf("SessionFile = %v, want prefix %q", sessionFile, expectedSessionDir)
		}
	})

	t.Run("keeps an explicit sessionManager override", func(t *testing.T) {
		root := t.TempDir()
		cwd := filepath.Join(root, "project")
		agentDir := filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}

		sm, err := core.SessionManagerInMemoryWith(cwd, nil, nil)
		if err != nil {
			t.Fatalf("SessionManagerInMemoryWith: %v", err)
		}
		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:            cwd,
			AgentDir:       agentDir,
			Model:          model,
			SessionManager: sm,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		if res.Session.SessionManager != sm {
			t.Fatal("expected Session.SessionManager to preserve explicit override")
		}
		if res.Session.SessionManager.IsPersisted() {
			t.Fatal("expected in-memory SessionManager.IsPersisted() == false")
		}
	})

	t.Run("derives cwd from an explicit sessionManager when cwd is omitted", func(t *testing.T) {
		root := t.TempDir()
		sessionCwd := filepath.Join(root, "session-project")
		agentDir := filepath.Join(root, "agent")
		if err := os.MkdirAll(sessionCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}

		sm, err := core.SessionManagerInMemoryWith(sessionCwd, nil, nil)
		if err != nil {
			t.Fatalf("SessionManagerInMemoryWith: %v", err)
		}
		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			AgentDir:       agentDir,
			Model:          model,
			SessionManager: sm,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		if res.Session.SessionManager != sm {
			t.Fatal("expected Session.SessionManager to match override")
		}
		wantPromptFragment := "<cwd>\n" + sessionCwd + "\n</cwd>"
		if !strings.Contains(res.Session.SystemPrompt(), wantPromptFragment) {
			t.Fatalf("SystemPrompt missing %q", wantPromptFragment)
		}

		bashTool := tssmFindTool(res.Session.Agent.Tools(), "bash")
		if bashTool == nil {
			t.Fatal("bash tool not registered on agent")
		}
		outRes, err := bashTool.Execute(ctx, "test", jsonx.ObjectOf("command", "pwd"), nil)
		if err != nil {
			t.Fatalf("bashTool.Execute: %v", err)
		}
		gotPath := strings.TrimSpace(tssmExtractText(outRes))
		gotReal, err1 := filepath.EvalSymlinks(gotPath)
		wantReal, err2 := filepath.EvalSymlinks(sessionCwd)
		if err1 != nil || err2 != nil || gotReal != wantReal {
			t.Fatalf("bash pwd realpath = %q (err=%v), want %q (err=%v)", gotReal, err1, wantReal, err2)
		}
	})

	t.Run("exposes current session state to the built-in bash tool", func(t *testing.T) {
		root := t.TempDir()
		cwd := filepath.Join(root, "project")
		agentDir := filepath.Join(root, "agent")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}

		res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
			Cwd:           cwd,
			AgentDir:      agentDir,
			Model:         model,
			ThinkingLevel: agent.ThinkingLevelHigh,
		})
		if err != nil {
			t.Fatalf("CreateAgentSession: %v", err)
		}
		defer res.Session.Dispose()

		if res.Session.SessionFile() == nil {
			t.Fatal("expected persisted SessionFile")
		}
		if !strings.Contains(res.Session.SystemPrompt(), "You can inspect PI_* environment variables for current model and session details.") {
			t.Fatal("SystemPrompt missing PI_* env vars note")
		}

		bashTool := tssmFindTool(res.Session.Agent.Tools(), "bash")
		if bashTool == nil {
			t.Fatal("bash tool not registered on agent")
		}
		outRes, err := bashTool.Execute(ctx, "test", jsonx.ObjectOf(
			"command", `printf '%s\n' "$PI_SESSION_ID" "$PI_SESSION_FILE" "$PI_PROVIDER" "$PI_MODEL" "$PI_REASONING_LEVEL"`,
		), nil)
		if err != nil {
			t.Fatalf("bashTool.Execute: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(tssmExtractText(outRes)), "\n")
		want := []string{
			res.Session.SessionId(),
			*res.Session.SessionFile(),
			string(model.Provider),
			model.Id,
			string(res.Session.ThinkingLevel()),
		}
		if len(lines) != len(want) {
			t.Fatalf("got %d lines %#v, want %#v", len(lines), lines, want)
		}
		for i := range want {
			if lines[i] != want[i] {
				t.Fatalf("line[%d] = %q, want %q", i, lines[i], want[i])
			}
		}
	})
}
