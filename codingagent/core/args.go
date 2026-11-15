// Ported from packages/coding-agent/src/cli/args.ts (pi v1.5.2).

package core

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/tui"
)

// Mode selects the coding-agent CLI output/execution mode.
type Mode string

const (
	ModeText Mode = "text"
	ModeJson Mode = "json"
	ModeRpc  Mode = "rpc"
	ModeGui  Mode = "gui"
)

// ArgsDiagnostic reports a warning or error discovered while parsing CLI arguments.
type ArgsDiagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ListModelsOption models the --list-models value (either true or a search pattern string).
type ListModelsOption struct {
	All    bool
	Search string
}

// UnknownFlagValue stores either a boolean presence or string value for an unrecognized `--flag`.
type UnknownFlagValue struct {
	IsString bool
	Bool     bool
	String   string
}

// Args holds parsed coding-agent CLI flags, positionals, `@file` references, and extension flags.
type Args struct {
	Provider             *string
	Model                *string
	ApiKey               *string
	SystemPrompt         *string
	AppendSystemPrompt   []string
	Thinking             *agent.ThinkingLevel
	Continue             bool
	Resume               bool
	Help                 bool
	Version              bool
	Mode                 *Mode
	Gui                  bool
	Name                 *string
	NoSession            bool
	Session              *string
	SessionId            *string
	Fork                 *string
	SessionDir           *string
	Models               []string
	Tools                []string
	ExcludeTools         []string
	NoTools              bool
	NoBuiltinTools       bool
	Extensions           []string
	NoExtensions         bool
	NoMcp                bool
	Print                bool
	Export               *string
	NoSkills             bool
	Skills               []string
	PromptTemplates      []string
	NoPromptTemplates    bool
	Themes               []string
	UseTheme             *string
	NoThemes             bool
	NoContextFiles       bool
	ListModels           *ListModelsOption
	Offline              bool
	TuiMode              *TuiMode
	Verbose              bool
	ProjectTrustOverride *bool
	Messages             []string
	FileArgs             []string
	UnknownFlags         *omap.Map[string, UnknownFlagValue]
	Diagnostics          []ArgsDiagnostic
}

var argsValidThinkingLevels = []agent.ThinkingLevel{
	"off",
	"minimal",
	"low",
	"medium",
	"high",
	"xhigh",
	"max",
}

// IsValidThinkingLevel reports whether level is a recognized ThinkingLevel string.
func IsValidThinkingLevel(level string) bool {
	return slices.Contains(argsValidThinkingLevels, agent.ThinkingLevel(level))
}

// NormalizeSessionName trims surrounding whitespace and returns nil when empty.
func NormalizeSessionName(value string) *string {
	name := strings.TrimSpace(value)
	if name == "" {
		return nil
	}
	return &name
}

func argsSplitCSVTrimmed(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ParseArgs parses CLI arguments identically to TS parseArgs, adding --gui and --mode gui support.
func ParseArgs(args []string) Args {
	result := Args{
		Messages:     []string{},
		FileArgs:     []string{},
		UnknownFlags: omap.NewMap[string, UnknownFlagValue](),
		Diagnostics:  []ArgsDiagnostic{},
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			for _, positionalArg := range args[i+1:] {
				if strings.HasPrefix(positionalArg, "@") {
					result.FileArgs = append(result.FileArgs, positionalArg[1:])
				} else {
					result.Messages = append(result.Messages, positionalArg)
				}
			}
			break
		} else if arg == "--help" || arg == "-h" {
			result.Help = true
		} else if arg == "--version" || arg == "-v" {
			result.Version = true
		} else if arg == "--gui" {
			result.Gui = true
		} else if arg == "--mode" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: "--mode requires text, json, rpc, or gui",
				})
				continue
			}
			i++
			mode := Mode(args[i])
			if mode != ModeText && mode != ModeJson && mode != ModeRpc && mode != ModeGui {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: fmt.Sprintf(`Invalid mode "%s". Valid values: text, json, rpc, gui`, mode),
				})
				continue
			}
			result.Mode = &mode
		} else if arg == "--continue" || arg == "-c" {
			result.Continue = true
		} else if arg == "--resume" || arg == "-r" {
			result.Resume = true
		} else if arg == "--provider" && i+1 < len(args) {
			i++
			v := args[i]
			result.Provider = &v
		} else if arg == "--model" && i+1 < len(args) {
			i++
			v := args[i]
			result.Model = &v
		} else if arg == "--api-key" && i+1 < len(args) {
			i++
			v := args[i]
			result.ApiKey = &v
		} else if arg == "--system-prompt" && i+1 < len(args) {
			i++
			v := args[i]
			result.SystemPrompt = &v
		} else if arg == "--append-system-prompt" && i+1 < len(args) {
			i++
			result.AppendSystemPrompt = append(result.AppendSystemPrompt, args[i])
		} else if arg == "--name" || arg == "-n" {
			if i+1 < len(args) {
				i++
				v := args[i]
				result.Name = &v
			} else {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: "--name requires a value",
				})
			}
		} else if arg == "--no-session" {
			result.NoSession = true
		} else if arg == "--session" && i+1 < len(args) {
			i++
			v := args[i]
			result.Session = &v
		} else if arg == "--session-id" && i+1 < len(args) {
			i++
			v := args[i]
			result.SessionId = &v
		} else if arg == "--fork" && i+1 < len(args) {
			i++
			v := args[i]
			result.Fork = &v
		} else if arg == "--session-dir" && i+1 < len(args) {
			i++
			v := args[i]
			result.SessionDir = &v
		} else if arg == "--models" && i+1 < len(args) {
			i++
			result.Models = argsSplitCSVTrimmed(args[i])
		} else if arg == "--no-tools" || arg == "-nt" {
			result.NoTools = true
		} else if arg == "--no-builtin-tools" || arg == "-nbt" {
			result.NoBuiltinTools = true
		} else if (arg == "--tools" || arg == "-t") && i+1 < len(args) {
			i++
			tools := argsSplitCSVTrimmed(args[i])
			if err := GetToolListError(tools); err != nil {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: fmt.Sprintf("%s: %s", arg, err.Error()),
				})
			} else {
				result.Tools = tools
			}
		} else if (arg == "--exclude-tools" || arg == "-xt") && i+1 < len(args) {
			i++
			result.ExcludeTools = argsSplitCSVTrimmed(args[i])
		} else if arg == "--thinking" && i+1 < len(args) {
			i++
			level := args[i]
			if IsValidThinkingLevel(level) {
				tl := agent.ThinkingLevel(level)
				result.Thinking = &tl
			} else {
				strs := make([]string, len(argsValidThinkingLevels))
				for idx, l := range argsValidThinkingLevels {
					strs[idx] = string(l)
				}
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "warning",
					Message: fmt.Sprintf(`Invalid thinking level "%s". Valid values: %s`, level, strings.Join(strs, ", ")),
				})
			}
		} else if arg == "--print" || arg == "-p" {
			result.Print = true
			if i+1 < len(args) {
				next := args[i+1]
				if !strings.HasPrefix(next, "@") && (!strings.HasPrefix(next, "-") || strings.HasPrefix(next, "---")) {
					result.Messages = append(result.Messages, next)
					i++
				}
			}
		} else if arg == "--export" && i+1 < len(args) {
			i++
			v := args[i]
			result.Export = &v
		} else if (arg == "--extension" || arg == "-e") && i+1 < len(args) {
			i++
			result.Extensions = append(result.Extensions, args[i])
		} else if arg == "--no-extensions" || arg == "-ne" {
			result.NoExtensions = true
		} else if arg == "--no-mcp" {
			result.NoMcp = true
		} else if arg == "--skill" && i+1 < len(args) {
			i++
			result.Skills = append(result.Skills, args[i])
		} else if arg == "--prompt-template" && i+1 < len(args) {
			i++
			result.PromptTemplates = append(result.PromptTemplates, args[i])
		} else if arg == "--theme" && i+1 < len(args) {
			i++
			result.Themes = append(result.Themes, args[i])
		} else if arg == "--use-theme" {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: "--use-theme requires a theme name",
				})
			} else {
				i++
				v := args[i]
				result.UseTheme = &v
			}
		} else if arg == "--no-skills" || arg == "-ns" {
			result.NoSkills = true
		} else if arg == "--no-prompt-templates" || arg == "-np" {
			result.NoPromptTemplates = true
		} else if arg == "--no-themes" {
			result.NoThemes = true
		} else if arg == "--no-context-files" || arg == "-nc" {
			result.NoContextFiles = true
		} else if arg == "--list-models" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
				i++
				result.ListModels = &ListModelsOption{Search: args[i]}
			} else {
				result.ListModels = &ListModelsOption{All: true}
			}
		} else if arg == "--tui-mode" {
			if i+1 < len(args) && (TuiMode(args[i+1]) == tui.TuiModeRegular || TuiMode(args[i+1]) == tui.TuiModeFullscreen) {
				i++
				m := TuiMode(args[i])
				result.TuiMode = &m
			} else if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: "--tui-mode requires regular or fullscreen",
				})
			} else {
				i++
				result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
					Type:    "error",
					Message: fmt.Sprintf(`Invalid TUI mode "%s". Valid values: regular, fullscreen`, args[i]),
				})
			}
		} else if arg == "--verbose" {
			result.Verbose = true
		} else if arg == "--approve" || arg == "-a" {
			v := true
			result.ProjectTrustOverride = &v
		} else if arg == "--no-approve" || arg == "-na" {
			v := false
			result.ProjectTrustOverride = &v
		} else if arg == "--offline" {
			result.Offline = true
		} else if strings.HasPrefix(arg, "@") {
			result.FileArgs = append(result.FileArgs, arg[1:])
		} else if strings.HasPrefix(arg, "--") {
			if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
				result.UnknownFlags.Set(arg[2:eqIdx], UnknownFlagValue{IsString: true, String: arg[eqIdx+1:]})
			} else {
				flagName := arg[2:]
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.HasPrefix(args[i+1], "@") {
					i++
					result.UnknownFlags.Set(flagName, UnknownFlagValue{IsString: true, String: args[i]})
				} else {
					result.UnknownFlags.Set(flagName, UnknownFlagValue{Bool: true})
				}
			}
		} else if strings.HasPrefix(arg, "-") {
			result.Diagnostics = append(result.Diagnostics, ArgsDiagnostic{
				Type:    "error",
				Message: "Unknown option: " + arg,
			})
		} else {
			result.Messages = append(result.Messages, arg)
		}
	}

	return result
}

func argsBold(s string) string {
	return "\x1b[1m" + s + "\x1b[22m"
}

func argsPadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// FormatHelp renders CLI usage, commands, options, extension flags, examples, and environment variables.
func FormatHelp(extensionFlags ...ExtensionFlag) string {
	var extText string
	if len(extensionFlags) > 0 {
		lines := make([]string, 0, len(extensionFlags))
		for _, flag := range extensionFlags {
			valSuffix := ""
			if flag.Type == "string" {
				valSuffix = " <value>"
			}
			desc := "Registered by " + flag.ExtensionPath
			if flag.Description != nil {
				desc = *flag.Description
			}
			lines = append(lines, argsPadRight("  --"+flag.Name+valSuffix, 30)+desc)
		}
		extText = "\n" + argsBold("Extension CLI Flags:") + "\n" + strings.Join(lines, "\n") + "\n"
	}

	app := utils.AppName
	cfgDir := utils.ConfigDirName
	return fmt.Sprintf(`%s - AI coding assistant with read, bash, edit, write tools

%s
  %s [options] [--] [@files...] [messages...]

%s
  %s install <source> [-l]     Install extension source and add to settings
  %s remove <source> [-l]      Remove extension source from settings
  %s uninstall <source> [-l]   Alias for remove
  %s update [source|self|pi]   Update pi, extensions, or model catalogs
  %s list                      List installed extensions from settings
  %s config [-l]               Open TUI to enable/disable package resources (Tab switches scope)
  %s auth <command>            Log in or out, print credentials, or check provider readiness
  %s mcp <command>             Manage MCP servers
  %s <command> --help          Show help for install/remove/uninstall/update/list/config/auth/mcp

%s
  --provider <name>              Provider to search for --model (requires --model)
  --model <pattern>              Model pattern or ID (supports "provider/id" and optional ":<thinking>")
  --api-key <key>                API key (defaults to env vars)
  --system-prompt <text>         System prompt (default: coding assistant prompt)
  --append-system-prompt <text>  Append text or file contents to the system prompt (can be used multiple times)
  --mode <mode>                  Output mode: text (default), json, rpc, or gui
  --gui                          Launch the native desktop GUI
  --print, -p                    Non-interactive mode: process prompt and exit
  --continue, -c                 Continue the most recent session for the current directory
  --resume, -r                   Pick a session to resume
  --session <path|id>            Open a session by file path or full/partial ID
  --session-id <id>              Use the current directory's existing session with the ID, creating it otherwise
  --fork <path|id>               Fork a session by file path or full/partial ID into the current directory
  --session-dir <dir>            Directory for session storage and lookup
  --no-session                   Don't save session (ephemeral)
  --name, -n <name>              Set session display name
  --models <patterns>            Comma-separated model patterns for Ctrl+P cycling
                                 Supports globs (anthropic/*, *sonnet*) and fuzzy matching
  --no-tools, -nt                Disable all tools by default (built-in and extension)
  --no-builtin-tools, -nbt       Disable built-in tools by default but keep extension/custom tools enabled
  --tools, -t <tools>            Comma-separated allowlist of tool names or patterns (*) to enable
                                 Keeps MCP tools unless an entry starts with mcp__
                                 Only +name/-name entries add to or remove from the defaults
  --exclude-tools, -xt <tools>   Comma-separated denylist of tool names or patterns (*) to disable
                                 Applies to all tools, MCP tools included
  --thinking <level>             Set thinking level: off, minimal, low, medium, high, xhigh, max
  --extension, -e <path>         Load an extension file or builtin:<name> (can be used multiple times)
  --no-extensions, -ne           Disable extension discovery and built-in extensions (explicit -e paths still work)
  --no-mcp                       Disable built-in MCP support: no servers connect and no MCP tools
  --skill <path>                 Load a skill file or directory (can be used multiple times)
  --no-skills, -ns               Disable skills discovery and loading
  --prompt-template <path>       Load a prompt template file or directory (can be used multiple times)
  --no-prompt-templates, -np     Disable prompt template discovery and loading
  --theme <path>                 Load a theme file or directory (can be used multiple times)
  --use-theme <name[/name]>      Set the initial interactive theme for this run
  --no-themes                    Disable theme discovery and loading
  --no-context-files, -nc        Disable AGENTS.md and CLAUDE.md discovery and loading
  --export <file> [output]       Export session file to HTML and exit
  --list-models [search]         List available models (with optional fuzzy search)
  --verbose                      Force verbose startup (overrides quietStartup setting)
  --tui-mode <mode>              TUI mode: fullscreen (default) or regular
  --approve, -a                  Trust project-local files for this run
  --no-approve, -na              Ignore project-local files for this run
  --offline                      Disable startup network operations (same as PI_OFFLINE=1)
  --                             End option parsing; treat remaining arguments as messages/files
  --help, -h                     Show this help
  --version, -v                  Show version number

Extensions can register additional flags (e.g., --plan from plan-mode extension).%s

%s
  # Print a provider API key for an external client
  %s auth print-api-key --provider openai

  # Print an OAuth bearer token for an external client (refreshes if expired)
  %s auth print-bearer-token --provider openai-codex

  # Interactive mode
  %s

  # Interactive mode with initial prompt
  %s "List all .ts files in src/"

  # Include files in initial message
  %s @prompt.md @image.png "What color is the sky?"

  # Non-interactive mode (process and exit)
  %s -p "List all .ts files in src/"

  # Prompt beginning with a dash
  %s -p -- "- Summarize these points"

  # Multiple messages (interactive)
  %s "Read package.json" "What dependencies do we have?"

  # Continue previous session
  %s --continue "What did we discuss?"

  # Start a named session
  %s --name "Refactor auth module"

  # Use different model
  %s --provider openai --model gpt-4o-mini "Help me refactor this code"

  # Use model with provider prefix (no --provider needed)
  %s --model openai/gpt-4o "Help me refactor this code"

  # Use model with thinking level shorthand
  %s --model sonnet:high "Solve this complex problem"

  # Limit model cycling to specific models
  %s --models claude-sonnet,claude-haiku,gpt-4o

  # Limit to a specific provider with glob pattern
  %s --models "github-copilot/*"

  # Cycle models with fixed thinking levels
  %s --models sonnet:high,haiku:low

  # Start with a specific thinking level
  %s --thinking high "Solve this complex problem"

  # Read-only mode (no file modifications possible)
  %s --tools read,grep,find,ls -p "Review the code in src/"

  # Add codemode to the default tools
  %s --tools +codemode

  # Codemode with only the tools of one MCP server
  %s --tools read,bash,codemode,'mcp__radius__*'

  # Disable one tool while keeping the rest available
  %s --exclude-tools ask_question

  # Export a session file to HTML
  %s --export ~/%s/agent/sessions/--path--/session.jsonl
  %s --export session.jsonl output.html

%s
  ANTHROPIC_AUTH_TOKEN             - Anthropic bearer auth token
  ANTHROPIC_API_KEY                - Anthropic Claude API key
  ANTHROPIC_OAUTH_TOKEN            - Anthropic OAuth token (alternative to API key)
  ANT_LING_API_KEY                 - Ant Ling API key
  OPENAI_API_KEY                   - OpenAI GPT API key
  AZURE_OPENAI_API_KEY             - Azure OpenAI API key
  AZURE_OPENAI_BASE_URL            - Azure OpenAI/Cognitive Services base URL (e.g. https://{resource}.openai.azure.com)
  AZURE_OPENAI_RESOURCE_NAME       - Azure OpenAI resource name (alternative to base URL)
  AZURE_OPENAI_API_VERSION         - Azure OpenAI API version (default: v1)
  AZURE_OPENAI_DEPLOYMENT_NAME_MAP - Azure OpenAI model=deployment map (comma-separated)
  DEEPSEEK_API_KEY                 - DeepSeek API key
  NVIDIA_API_KEY                   - NVIDIA NIM API key
  GEMINI_API_KEY                   - Google Gemini API key
  GROQ_API_KEY                     - Groq API key
  CEREBRAS_API_KEY                 - Cerebras API key
  XAI_API_KEY                      - xAI Grok API key
  FIREWORKS_API_KEY                - Fireworks API key
  TOGETHER_API_KEY                 - Together AI API key
  BASETEN_API_KEY                  - Baseten API key
  OPENROUTER_API_KEY               - OpenRouter API key
  AI_GATEWAY_API_KEY               - Vercel AI Gateway API key
  ZAI_API_KEY                      - ZAI Coding Plan API key (Global)
  ZAI_CODING_CN_API_KEY            - ZAI Coding Plan API key (China)
  MISTRAL_API_KEY                  - Mistral API key
  MINIMAX_API_KEY                  - MiniMax API key
  MOONSHOT_API_KEY                 - Moonshot AI API key
  OPENCODE_API_KEY                 - OpenCode Zen/OpenCode Go API key
  KIMI_API_KEY                     - Kimi For Coding API key
  META_API_KEY                     - Meta Model API key
  CLOUDFLARE_API_KEY               - Cloudflare API token (Workers AI and AI Gateway)
  CLOUDFLARE_ACCOUNT_ID            - Cloudflare account id (required for both)
  CLOUDFLARE_GATEWAY_ID            - Cloudflare AI Gateway slug (required for AI Gateway)
  QWEN_TOKEN_PLAN_TEAM_API_KEY     - Qwen Token Plan Team API key (international region)
  QWEN_TOKEN_PLAN_TEAM_CN_API_KEY  - Qwen Token Plan Team API key (China region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_API_KEY - Qwen Token Plan Individual API key (international region)
  QWEN_TOKEN_PLAN_INDIVIDUAL_CN_API_KEY - Qwen Token Plan Individual API key (China region)
  XIAOMI_API_KEY                   - Xiaomi MiMo API key (api.xiaomimimo.com billing)
  XIAOMI_TOKEN_PLAN_CN_API_KEY     - Xiaomi MiMo Token Plan API key (China region)
  XIAOMI_TOKEN_PLAN_AMS_API_KEY    - Xiaomi MiMo Token Plan API key (Amsterdam region)
  XIAOMI_TOKEN_PLAN_SGP_API_KEY    - Xiaomi MiMo Token Plan API key (Singapore region)
  AWS_PROFILE                      - AWS profile for Amazon Bedrock
  AWS_ACCESS_KEY_ID                - AWS access key for Amazon Bedrock
  AWS_SECRET_ACCESS_KEY            - AWS secret key for Amazon Bedrock
  AWS_BEARER_TOKEN_BEDROCK         - Bedrock API key (bearer token)
  AWS_REGION                       - AWS region for Amazon Bedrock (e.g., us-east-1)
  %s - Config directory (default: ~/%s/agent)
  %s - Session storage directory (overridden by --session-dir)
  PI_PACKAGE_DIR                   - Override package directory (for Nix/Guix store paths)
  PI_OFFLINE                       - Disable startup network operations when set to 1/true/yes
  PI_SELF_UPDATE_COMMAND           - Command pi update runs to update pi itself ({packageName}, {version}, {installSpec})
  PI_TELEMETRY                     - Override install telemetry when set to 1/true/yes or 0/false/no
  PI_SHARE_VIEWER_URL              - Base URL for /share command (default: https://pi.dev/session/)

%s
  read       - Read file contents
  bash       - Execute bash commands
  powershell - Execute PowerShell commands on Windows
  edit       - Edit files with find/replace
  write      - Write files (creates/overwrites)
  grep       - Search file contents (read-only, off by default)
  find       - Find files by glob pattern (read-only, off by default)
  ls         - List directory contents (read-only, off by default)
`,
		argsBold(app),
		argsBold("Usage:"), app,
		argsBold("Commands:"), app, app, app, app, app, app, app, app, app,
		argsBold("Options:"),
		extText,
		argsBold("Examples:"),
		app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, app, cfgDir, app,
		argsBold("Environment Variables:"),
		argsPadRight(utils.EnvAgentDir, 32), cfgDir,
		argsPadRight(utils.EnvSessionDir, 32),
		argsBold("Built-in Tool Names:"),
	)
}

var argsStdout io.Writer = os.Stdout

// PrintHelp writes formatted CLI help output (including registered extension flags) to stdout.
func PrintHelp(extensionFlags ...ExtensionFlag) {
	_, _ = fmt.Fprintln(argsStdout, FormatHelp(extensionFlags...))
}
