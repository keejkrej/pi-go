// Ported from packages/coding-agent/src/core/tools/index.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ToolName identifies one of pi's built-in tools.
type ToolName string

const (
	ToolNameRead       ToolName = "read"
	ToolNameBash       ToolName = "bash"
	ToolNamePowershell ToolName = "powershell"
	ToolNameEdit       ToolName = "edit"
	ToolNameWrite      ToolName = "write"
	ToolNameGrep       ToolName = "grep"
	ToolNameFind       ToolName = "find"
	ToolNameLs         ToolName = "ls"
)

// AllToolNames lists all built-in tool names in canonical order.
var AllToolNames = []ToolName{
	ToolNameRead,
	ToolNameBash,
	ToolNamePowershell,
	ToolNameEdit,
	ToolNameWrite,
	ToolNameGrep,
	ToolNameFind,
	ToolNameLs,
}

// AllToolNamesSet reports membership among pi's built-in tools.
var AllToolNamesSet = map[ToolName]bool{
	ToolNameRead:       true,
	ToolNameBash:       true,
	ToolNamePowershell: true,
	ToolNameEdit:       true,
	ToolNameWrite:      true,
	ToolNameGrep:       true,
	ToolNameFind:       true,
	ToolNameLs:         true,
}

// ToolSystemPromptContribution holds a built-in tool's system prompt snippet and guidelines.
type ToolSystemPromptContribution struct {
	Snippet    string
	Guidelines []string
}

// BuiltinToolSystemPromptContributions maps each built-in tool name to its system prompt contribution.
var BuiltinToolSystemPromptContributions = map[ToolName]ToolSystemPromptContribution{
	ToolNameRead:       ReadToolSystemPromptContribution,
	ToolNameBash:       BashToolSystemPromptContribution,
	ToolNamePowershell: PowerShellToolSystemPromptContribution,
	ToolNameEdit:       EditToolSystemPromptContribution,
	ToolNameWrite:      WriteToolSystemPromptContribution,
	ToolNameGrep:       GrepToolSystemPromptContribution,
	ToolNameFind:       FindToolSystemPromptContribution,
	ToolNameLs:         LsToolSystemPromptContribution,
}

// ToolsOptions holds per-tool options passed to tool factory functions.
type ToolsOptions struct {
	Read       *ReadToolOptions
	Bash       *BashToolOptions
	Powershell *PowerShellToolOptions
	Write      *WriteToolOptions
	Edit       *EditToolOptions
	Grep       *GrepToolOptions
	Find       *FindToolOptions
	Ls         *LsToolOptions
}

type tiProp struct {
	key    string
	schema *typebox.Schema
}

func tiObjectSchema(props ...tiProp) *typebox.Schema {
	m := omap.NewMap[string, *typebox.Schema]()
	for _, p := range props {
		m.Set(p.key, p.schema)
	}
	return typebox.Object(m)
}

func tiStrictPreferJsonSchema() ai.ToolConstrainedSamplingValue {
	return ai.ToolConstrainedSamplingValue{
		V: &ai.ConstrainedSamplingJsonSchema{
			Type:   "json_schema",
			Strict: ai.ConstrainedSamplingStrictPrefer,
		},
	}
}

func tiResolveToolCwd(ectx ExtensionToolContext, fallback string) string {
	if ectx != nil {
		if c := ectx.Cwd(); c != "" {
			return c
		}
	}
	return fallback
}

func tiCheckAborted(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return errors.New("Operation aborted")
	}
	return nil
}

func tiApplyToolRenderers(def *ToolDefinition, r ToolRenderers) {
	if def == nil {
		return
	}
	if r.RenderShell != "" && def.RenderShell == "" {
		def.RenderShell = r.RenderShell
	}
	if r.RenderCall != nil {
		def.RenderCall = r.RenderCall
	}
	if r.RenderResult != nil {
		def.RenderResult = r.RenderResult
	}
}

func tiUnpackOptions(options []*ToolsOptions) ToolsOptions {
	if len(options) > 0 && options[0] != nil {
		return *options[0]
	}
	return ToolsOptions{}
}

// CreateToolDefinition constructs the ToolDefinition for a named built-in tool.
func CreateToolDefinition(toolName ToolName, cwd string, options ...*ToolsOptions) (ToolDefinition, error) {
	opts := tiUnpackOptions(options)
	switch toolName {
	case ToolNameRead:
		return CreateReadToolDefinition(cwd, opts.Read), nil
	case ToolNameBash:
		return CreateBashToolDefinition(cwd, opts.Bash), nil
	case ToolNamePowershell:
		return CreatePowerShellToolDefinition(cwd, opts.Powershell), nil
	case ToolNameEdit:
		return CreateEditToolDefinition(cwd, opts.Edit), nil
	case ToolNameWrite:
		return CreateWriteToolDefinition(cwd, opts.Write), nil
	case ToolNameGrep:
		return CreateGrepToolDefinition(cwd, opts.Grep), nil
	case ToolNameFind:
		return CreateFindToolDefinition(cwd, opts.Find), nil
	case ToolNameLs:
		return CreateLsToolDefinition(cwd, opts.Ls), nil
	default:
		return ToolDefinition{}, fmt.Errorf("Unknown tool name: %s", toolName)
	}
}

// CreateTool constructs the wrapped *agent.AgentTool for a named built-in tool.
func CreateTool(toolName ToolName, cwd string, options ...*ToolsOptions) (*agent.AgentTool, error) {
	def, err := CreateToolDefinition(toolName, cwd, options...)
	if err != nil {
		return nil, err
	}
	return WrapToolDefinition(def), nil
}

// CreateCodingToolDefinitions returns definitions for read, bash, edit, and write.
func CreateCodingToolDefinitions(cwd string, options ...*ToolsOptions) []ToolDefinition {
	opts := tiUnpackOptions(options)
	return []ToolDefinition{
		CreateReadToolDefinition(cwd, opts.Read),
		CreateBashToolDefinition(cwd, opts.Bash),
		CreateEditToolDefinition(cwd, opts.Edit),
		CreateWriteToolDefinition(cwd, opts.Write),
	}
}

// CreateReadOnlyToolDefinitions returns definitions for read, grep, find, and ls.
func CreateReadOnlyToolDefinitions(cwd string, options ...*ToolsOptions) []ToolDefinition {
	opts := tiUnpackOptions(options)
	return []ToolDefinition{
		CreateReadToolDefinition(cwd, opts.Read),
		CreateGrepToolDefinition(cwd, opts.Grep),
		CreateFindToolDefinition(cwd, opts.Find),
		CreateLsToolDefinition(cwd, opts.Ls),
	}
}

// CreateAllToolDefinitions returns a map of all 8 built-in tool definitions keyed by ToolName.
func CreateAllToolDefinitions(cwd string, options ...*ToolsOptions) map[ToolName]ToolDefinition {
	opts := tiUnpackOptions(options)
	return map[ToolName]ToolDefinition{
		ToolNameRead:       CreateReadToolDefinition(cwd, opts.Read),
		ToolNameBash:       CreateBashToolDefinition(cwd, opts.Bash),
		ToolNamePowershell: CreatePowerShellToolDefinition(cwd, opts.Powershell),
		ToolNameEdit:       CreateEditToolDefinition(cwd, opts.Edit),
		ToolNameWrite:      CreateWriteToolDefinition(cwd, opts.Write),
		ToolNameGrep:       CreateGrepToolDefinition(cwd, opts.Grep),
		ToolNameFind:       CreateFindToolDefinition(cwd, opts.Find),
		ToolNameLs:         CreateLsToolDefinition(cwd, opts.Ls),
	}
}

// CreateCodingTools returns wrapped executable tools for read, bash, edit, and write.
func CreateCodingTools(cwd string, options ...*ToolsOptions) []*agent.AgentTool {
	defs := CreateCodingToolDefinitions(cwd, options...)
	return WrapToolDefinitions(defs)
}

// CreateReadOnlyTools returns wrapped executable tools for read, grep, find, and ls.
func CreateReadOnlyTools(cwd string, options ...*ToolsOptions) []*agent.AgentTool {
	defs := CreateReadOnlyToolDefinitions(cwd, options...)
	return WrapToolDefinitions(defs)
}

// CreateAllTools returns wrapped executable tools for all 8 built-in tools keyed by ToolName.
func CreateAllTools(cwd string, options ...*ToolsOptions) map[ToolName]*agent.AgentTool {
	opts := tiUnpackOptions(options)
	return map[ToolName]*agent.AgentTool{
		ToolNameRead:       CreateReadTool(cwd, opts.Read),
		ToolNameBash:       CreateBashTool(cwd, opts.Bash),
		ToolNamePowershell: CreatePowerShellTool(cwd, opts.Powershell),
		ToolNameEdit:       CreateEditTool(cwd, opts.Edit),
		ToolNameWrite:      CreateWriteTool(cwd, opts.Write),
		ToolNameGrep:       CreateGrepTool(cwd, opts.Grep),
		ToolNameFind:       CreateFindTool(cwd, opts.Find),
		ToolNameLs:         CreateLsTool(cwd, opts.Ls),
	}
}

var _ = (*jsonx.Object)(nil)
