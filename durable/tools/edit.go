// Ported from packages/durable/src/tools/edit.ts (pi v1.0.0).

package tools

import (
	"errors"
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// EditToolInput defines arguments accepted by the built-in edit tool.
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails carries diff and unified patch details produced by the edit tool.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine *int   `json:"firstChangedLine,omitzero"`
}

type editAccessError struct {
	path  string
	cause *env.FileError
}

func (e *editAccessError) Error() string {
	var code env.FileErrorCode
	if e.cause != nil {
		code = e.cause.Code
	}
	return fmt.Sprintf("Could not edit file: %s. Error code: %s.", e.path, code)
}

func (e *editAccessError) Unwrap() error {
	if e.cause == nil {
		return nil
	}
	return e.cause
}

func editEditSchema() *typebox.Schema {
	replaceEditSchema := indexObjectSchema(
		indexProp{"oldText", typebox.String(jsonx.ObjectOf("description", "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."))},
		indexProp{"newText", typebox.String(jsonx.ObjectOf("description", "Replacement text for this targeted edit."))},
	)
	return indexObjectSchema(
		indexProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to edit (relative or absolute)"))},
		indexProp{"edits", typebox.Array(replaceEditSchema, jsonx.ObjectOf("description", "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."))},
	)
}

func editIsSingleEditObject(value any) bool {
	switch o := value.(type) {
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

func editPrepareEditArguments(input any) any {
	obj, ok := input.(*jsonx.Object)
	if !ok || obj == nil {
		if m, isMap := input.(map[string]any); isMap && m != nil {
			obj = jsonx.NewObject()
			for k, v := range m {
				obj.Set(k, v)
			}
		} else {
			return input
		}
	}

	out := obj.Clone()
	if editsVal, hasEdits := out.Get("edits"); hasEdits {
		switch ev := editsVal.(type) {
		case string:
			if parsed, err := jsonx.ParseString(ev); err == nil {
				if arr, isArr := parsed.([]any); isArr {
					out.Set("edits", arr)
				} else if editIsSingleEditObject(parsed) {
					out.Set("edits", []any{parsed})
				}
			}
		default:
			if editIsSingleEditObject(ev) {
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

func editValidateEditInput(input EditToolInput) (string, []Edit, error) {
	if len(input.Edits) == 0 {
		return "", nil, errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	}
	return input.Path, input.Edits, nil
}

// CreateEditTool constructs the durable "edit" tool registration.
func CreateEditTool() *durable.ToolRegistration {
	return durable.DefineTool(&durable.ToolRegistration{
		Name:             "edit",
		Description:      "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		Parameters:       editEditSchema(),
		PrepareArguments: editPrepareEditArguments,
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			decoded, err := indexDecodeArgs[EditToolInput](args)
			if err != nil {
				return nil, err
			}
			path, edits, err := editValidateEditInput(decoded)
			if err != nil {
				return nil, err
			}
			executionEnv, err := RequireEnv(api, ctx)
			if err != nil {
				return nil, err
			}
			absolutePath, err := ResolveToolPath(executionEnv, path, ctx)
			if err != nil {
				return nil, err
			}

			return WithFileMutationQueue(executionEnv, absolutePath, func() (*durable.ToolExecutionResult, error) {
				if indexIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}
				info := executionEnv.FileInfo(absolutePath, ctx)
				if !info.Ok {
					return nil, &editAccessError{path: path, cause: info.Error}
				}
				if info.Value.Kind != env.FileKindFile && info.Value.Kind != env.FileKindSymlink {
					return nil, fmt.Errorf("Could not edit file: %s. Path is not a file.", path)
				}

				readResult := executionEnv.ReadTextFile(absolutePath, ctx)
				if !readResult.Ok {
					return nil, &editAccessError{path: path, cause: readResult.Error}
				}
				if indexIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}

				bom, content := StripBom(readResult.Value)
				originalEnding := DetectLineEnding(content)
				normalizedContent := NormalizeToLF(content)
				applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
				if err != nil {
					return nil, err
				}
				if indexIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}

				finalContent := bom + RestoreLineEndings(applied.NewContent, originalEnding)
				writeResult := executionEnv.WriteFile(absolutePath, []byte(finalContent), ctx)
				if !writeResult.Ok {
					return nil, &editAccessError{path: path, cause: writeResult.Error}
				}
				if indexIsAborted(ctx) {
					return nil, errors.New("Operation aborted")
				}

				diffResult := GenerateDiffString(applied.BaseContent, applied.NewContent)
				details := &EditToolDetails{
					Diff:             diffResult.Diff,
					Patch:            GenerateUnifiedPatch(path, applied.BaseContent, applied.NewContent),
					FirstChangedLine: diffResult.FirstChangedLine,
				}
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)}},
					HasContent: true,
					Details:    details,
					HasDetails: true,
				}, nil
			}, ctx)
		},
	})
}
