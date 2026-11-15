// Ported from packages/coding-agent/src/core/tools/powershell.ts (pi v1.0.0).

package core

import (
	"context"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

const (
	tpUTF8OutputPrefix  = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"
	tpWindowsExitSuffix = "\n;\nif ($?) { exit 0 }\nif (($LASTEXITCODE -eq 0) -and (\"$($Error[0].FullyQualifiedErrorId)\" -like \"NativeCommandError*\")) { exit 0 }\nexit 1"
)

// PowerShellToolSystemPromptContribution holds the system prompt snippet and guidelines for powershell.
var PowerShellToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Execute PowerShell commands",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// PowerShellOperations is an alias for BashOperations.
type PowerShellOperations = BashOperations

// PowerShellSpawnContext is an alias for BashSpawnContext.
type PowerShellSpawnContext = BashSpawnContext

// PowerShellSpawnHook is an alias for BashSpawnHook.
type PowerShellSpawnHook = BashSpawnHook

// PowerShellToolDetails is an alias for BashToolDetails.
type PowerShellToolDetails = BashToolDetails

// PowerShellToolInput is an alias for BashToolInput.
type PowerShellToolInput = BashToolInput

// PowerShellToolOptions configures CreatePowerShellToolDefinition and CreatePowerShellTool.
type PowerShellToolOptions struct {
	Operations               PowerShellOperations
	ExposeSessionEnvironment *bool
	SpawnHook                PowerShellSpawnHook
}

func tpWin32Basename(p string) string {
	idx := strings.LastIndexAny(p, `/\`)
	if idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// CreateLocalPowerShellOperations creates PowerShellOperations backed by Windows PowerShell or PowerShell 7.
func CreateLocalPowerShellOperations() PowerShellOperations {
	return BashOperationsFunc(func(ctx context.Context, command, cwd string, options BashOperationsOptions) (BashOperationsResult, error) {
		shellConfig, err := utils.GetPowerShellConfig()
		if err != nil {
			return BashOperationsResult{}, err
		}
		suffix := ""
		if strings.EqualFold(tpWin32Basename(shellConfig.Shell), "powershell.exe") && !strings.HasSuffix(command, "`") {
			suffix = tpWindowsExitSuffix
		}
		wrappedCommand := tpUTF8OutputPrefix + command + suffix
		return CreateLocalShellOperations("PowerShell", func() (utils.ShellConfig, error) {
			return shellConfig, nil
		}).Exec(ctx, wrappedCommand, cwd, options)
	})
}

var tpPowerShellToolConfig = ShellToolConfig{
	Name:             "powershell",
	Label:            "powershell",
	ShellName:        "PowerShell",
	Prompt:           "PS>",
	PromptSnippet:    PowerShellToolSystemPromptContribution.Snippet,
	PromptGuidelines: PowerShellToolSystemPromptContribution.Guidelines,
	TempFilePrefix:   "pi-powershell",
}

// CreatePowerShellToolDefinition creates the ToolDefinition for the built-in powershell tool.
func CreatePowerShellToolDefinition(cwd string, options ...*PowerShellToolOptions) ToolDefinition {
	var bashOpts *BashToolOptions
	if len(options) > 0 && options[0] != nil {
		opt := options[0]
		ops := opt.Operations
		if ops == nil {
			ops = CreateLocalPowerShellOperations()
		}
		bashOpts = &BashToolOptions{
			Operations:               ops,
			ExposeSessionEnvironment: opt.ExposeSessionEnvironment,
			SpawnHook:                opt.SpawnHook,
		}
	} else {
		bashOpts = &BashToolOptions{
			Operations: CreateLocalPowerShellOperations(),
		}
	}
	return CreateShellToolDefinition(cwd, tpPowerShellToolConfig, bashOpts)
}

// CreatePowerShellTool wraps CreatePowerShellToolDefinition as an executable *agent.AgentTool.
func CreatePowerShellTool(cwd string, options ...*PowerShellToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreatePowerShellToolDefinition(cwd, options...))
}
