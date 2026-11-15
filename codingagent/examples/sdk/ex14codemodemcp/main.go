// Ported from packages/coding-agent/examples/sdk/14-codemode-mcp.ts (pi v1.0.0).

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/extensions/codemodeext"
	"github.com/keejkrej/pi-go/codingagent/extensions/toolsearchext"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

const mainDefaultPrompt = "Use codemode to count the TypeScript files in src/ and list the three largest."

type mainOptions struct {
	Cwd             string
	AgentDir        string
	Prompt          string
	ExtraExtensions []core.InlineExtension
	AuthStorage     *core.AuthStorage
	ModelRegistry   *core.ModelRegistry
	ModelRuntime    *core.ModelRuntime
	Model           *ai.Model
	SettingsManager *core.SettingsManager
	SessionManager  *core.SessionManager
	NoExtensions    bool
}

func mainRun(ctx context.Context, opts mainOptions, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if out == nil {
		out = io.Discard
	}

	cwd := opts.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd = wd
	}
	agentDir := opts.AgentDir
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}

	modeOn := core.CodemodeModeOn
	extensionFactories := []core.InlineExtension{
		{
			Name:    "codemode",
			Factory: codemodeext.CreateCodemodeExtension(&codemodeext.CodemodeExtensionOptions{Mode: &modeOn}),
		},
		{
			Name:    "tool-search",
			Factory: toolsearchext.Extension,
		},
	}
	// Built-in MCP extension (mcpext) is discovered automatically via
	// codingagent/extensions.BuiltInExtensions once integrated, or callers may
	// inject custom/MCP inline extensions through opts.ExtraExtensions.
	extensionFactories = append(extensionFactories, opts.ExtraExtensions...)

	settingsManager := opts.SettingsManager
	if settingsManager == nil {
		settingsManager = core.SettingsManagerCreate(cwd, agentDir)
	}
	// `+name` adds to configured default tools instead of replacing them.
	settingsManager.ApplyOverrides(&core.Settings{
		DefaultTools: []string{"+codemode", "+tool_search"},
	})

	disabledBuiltins := []string{"codemode", "tool-search"}
	resourceLoader := core.NewDefaultResourceLoader(core.DefaultResourceLoaderOptions{
		Cwd:                       cwd,
		AgentDir:                  agentDir,
		SettingsManager:           settingsManager,
		NoExtensions:              opts.NoExtensions,
		DisabledBuiltinExtensions: disabledBuiltins,
		ExtensionFactories:        extensionFactories,
	})
	if err := resourceLoader.Reload(ctx); err != nil {
		return err
	}

	sessionManager := opts.SessionManager
	if sessionManager == nil {
		sessionManager = core.SessionManagerInMemory()
	}

	res, err := core.CreateAgentSession(ctx, &core.CreateAgentSessionOptions{
		Cwd:                       cwd,
		AgentDir:                  agentDir,
		AuthStorage:               opts.AuthStorage,
		ModelRegistry:             opts.ModelRegistry,
		ModelRuntime:              opts.ModelRuntime,
		Model:                     opts.Model,
		SettingsManager:           settingsManager,
		ResourceLoader:            resourceLoader,
		SessionManager:            sessionManager,
		DisabledBuiltinExtensions: disabledBuiltins,
	})
	if err != nil {
		return err
	}
	defer res.Session.Dispose()

	if err := res.Session.BindExtensions(ctx, core.ExtensionBindings{}); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Active tools: %s\n", strings.Join(res.Session.GetActiveToolNames(), ", ")); err != nil {
		return err
	}

	unsub := res.Session.Subscribe(func(ev core.AgentSessionEvent) {
		upd, ok := ev.(*core.AgentSessionEventMessageUpdate)
		if !ok || upd == nil {
			return
		}
		if delta, ok := upd.AssistantMessageEvent.(*ai.AssistantMessageEventTextDelta); ok && delta != nil {
			_, _ = io.WriteString(out, delta.Delta)
		}
	})
	if unsub != nil {
		defer unsub()
	}

	prompt := opts.Prompt
	if prompt == "" {
		prompt = mainDefaultPrompt
	}
	if err := res.Session.Prompt(ctx, prompt); err != nil {
		return err
	}
	_, err = io.WriteString(out, "\n")
	return err
}

func main() {
	if err := mainRun(context.Background(), mainOptions{}, os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
