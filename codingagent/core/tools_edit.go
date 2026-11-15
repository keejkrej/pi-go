// Ported from packages/coding-agent/src/core/tools/edit.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// EditToolInput defines parameters for the edit tool.
type EditToolInput struct {
	Path  string            `json:"path"`
	Edits []EditReplacement `json:"edits"`
}

// EditToolDetails captures the formatted diff, unified patch, and first changed line.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine *int   `json:"firstChangedLine,omitzero"`
}

// EditToolSystemPromptContribution holds the system prompt snippet and guidelines for edit.
var EditToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
	Guidelines: []string{
		"Use edit for targeted changes. Batch changes to one file in a single call.",
		"Each edits[].oldText must exactly and uniquely match the original file. Keep matches short and non-overlapping; merge nearby changes without including large unchanged regions.",
	},
}

// EditOperations configures filesystem hooks for the edit tool.
type EditOperations struct {
	ReadFile  func(ctx context.Context, absolutePath string) ([]byte, error)
	WriteFile func(ctx context.Context, absolutePath, content string) error
	Access    func(ctx context.Context, absolutePath string) error
}

// EditToolOptions configures CreateEditToolDefinition and CreateEditTool.
type EditToolOptions struct {
	Operations *EditOperations
}

func teDefaultEditOperations() EditOperations {
	return EditOperations{
		ReadFile: func(_ context.Context, absolutePath string) ([]byte, error) {
			return os.ReadFile(absolutePath)
		},
		WriteFile: func(_ context.Context, absolutePath, content string) error {
			return os.WriteFile(absolutePath, []byte(content), 0o666)
		},
		Access: func(_ context.Context, absolutePath string) error {
			f, err := os.OpenFile(absolutePath, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			return f.Close()
		},
	}
}

func teEditSchema() *typebox.Schema {
	replaceEditSchema := tiObjectSchema(
		tiProp{"oldText", typebox.String(jsonx.ObjectOf("description", "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."))},
		tiProp{"newText", typebox.String(jsonx.ObjectOf("description", "Replacement text for this targeted edit."))},
	)
	return tiObjectSchema(
		tiProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to edit (relative or absolute)"))},
		tiProp{"edits", typebox.Array(replaceEditSchema, jsonx.ObjectOf("description", "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."))},
	)
}

func teIsSingleEditObject(v any) bool {
	switch o := v.(type) {
	case *jsonx.Object:
		if o == nil {
			return false
		}
		oldVal, hasOld := o.Get("oldText")
		newVal, hasNew := o.Get("newText")
		_, oldStr := oldVal.(string)
		_, newStr := newVal.(string)
		return hasOld && hasNew && oldStr && newStr
	case map[string]any:
		_, oldStr := o["oldText"].(string)
		_, newStr := o["newText"].(string)
		return oldStr && newStr
	default:
		return false
	}
}

func tePrepareEditArguments(input any) any {
	obj, ok := input.(*jsonx.Object)
	if !ok || obj == nil {
		m, isMap := input.(map[string]any)
		if !isMap || m == nil {
			return input
		}
		obj = jsonx.NewObject()
		for k, v := range m {
			obj.Set(k, v)
		}
	}

	out := jsonx.NewObject()
	for _, k := range obj.Keys() {
		v, _ := obj.Get(k)
		out.Set(k, v)
	}

	if editsVal, hasEdits := out.Get("edits"); hasEdits {
		switch ev := editsVal.(type) {
		case string:
			if parsed, err := jsonx.ParseString(ev); err == nil {
				if arr, isArr := parsed.([]any); isArr {
					out.Set("edits", arr)
				} else if teIsSingleEditObject(parsed) {
					out.Set("edits", []any{parsed})
				}
			}
		default:
			if teIsSingleEditObject(ev) {
				out.Set("edits", []any{ev})
			}
		}
	}

	oldVal, hasOld := out.Get("oldText")
	newVal, hasNew := out.Get("newText")
	oldStr, isOldStr := oldVal.(string)
	newStr, isNewStr := newVal.(string)
	if !hasOld || !hasNew || !isOldStr || !isNewStr {
		return out
	}

	var edits []any
	if curEdits, ok := out.Get("edits"); ok {
		if arr, isArr := curEdits.([]any); isArr {
			edits = slices.Clone(arr)
		}
	}
	edits = append(edits, jsonx.ObjectOf("oldText", oldStr, "newText", newStr))
	out.Delete("oldText")
	out.Delete("newText")
	out.Set("edits", edits)
	return out
}

func teValidateEditInput(input EditToolInput) error {
	if len(input.Edits) == 0 {
		return errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	return nil
}

// CreateEditToolDefinition creates the ToolDefinition for the built-in edit tool.
func CreateEditToolDefinition(cwd string, options ...*EditToolOptions) ToolDefinition {
	ops := teDefaultEditOperations()
	if len(options) > 0 && options[0] != nil && options[0].Operations != nil {
		ops = *options[0].Operations
	}

	snippet := EditToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:                "edit",
		Label:               "edit",
		Description:         "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		PromptSnippet:       &snippet,
		PromptGuidelines:    slices.Clone(EditToolSystemPromptContribution.Guidelines),
		Parameters:          teEditSchema(),
		ConstrainedSampling: tiStrictPreferJsonSchema(),
		RenderShell:         ToolRenderShellSelf,
		PrepareArguments:    tePrepareEditArguments,
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			ectx ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			var input EditToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}
			if err := teValidateEditInput(input); err != nil {
				return agent.AgentToolResult{}, err
			}

			effectiveCwd := tiResolveToolCwd(ectx, cwd)
			absolutePath := ResolveToCwd(input.Path, effectiveCwd)

			return WithFileMutationQueue(ctx, absolutePath, func() (agent.AgentToolResult, error) {
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				if ops.Access != nil {
					if err := ops.Access(ctx, absolutePath); err != nil {
						if abortErr := tiCheckAborted(ctx); abortErr != nil {
							return agent.AgentToolResult{}, abortErr
						}
						return agent.AgentToolResult{}, fmt.Errorf("Could not edit file: %s. %s.", input.Path, tedFormatAccessErrorCode(err))
					}
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				if ops.ReadFile == nil {
					return agent.AgentToolResult{}, errors.New("readFile operation is not configured")
				}
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				dec := js.NewTextDecoder(&js.TextDecoderOptions{IgnoreBOM: true})
				rawContent := dec.DecodeFinal(buffer)
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				bomSplit := utils.SplitBom(rawContent)
				normalizedContent := NormalizeToLF(bomSplit.Text)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, input.Edits, input.Path)
				if err != nil {
					return agent.AgentToolResult{}, err
				}

				editDiff := GenerateEditDiff(input.Path, applied.BaseContent, applied.NewContent, applied.Replacements)
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				var body string
				if bomSplit.Text == normalizedContent {
					body = applied.NewContent
				} else {
					body = ApplyReplacementsToOriginal(bomSplit.Text, applied.Replacements)
				}
				finalContent := bomSplit.Bom + body

				if ops.WriteFile == nil {
					return agent.AgentToolResult{}, errors.New("writeFile operation is not configured")
				}
				if err := ops.WriteFile(ctx, absolutePath, finalContent); err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				return agent.AgentToolResult{
					Content: ai.ContentList{
						&ai.TextContent{
							Type: "text",
							Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(input.Edits), input.Path),
						},
					},
					Details: &EditToolDetails{
						Diff:             editDiff.Diff,
						Patch:            editDiff.Patch,
						FirstChangedLine: editDiff.FirstChangedLine,
					},
				}, nil
			})
		},
	}
	tiApplyToolRenderers(&def, EditToolRenderers())
	return def
}

// CreateEditTool wraps CreateEditToolDefinition as an executable *agent.AgentTool.
func CreateEditTool(cwd string, options ...*EditToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateEditToolDefinition(cwd, options...))
}
