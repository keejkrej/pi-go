// Ported from packages/durable/src/tools/read.ts (pi v1.0.0).

package tools

import (
	"fmt"
	"math"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ReadToolInput defines arguments accepted by the built-in read tool.
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitzero"`
	Limit  *float64 `json:"limit,omitzero"`
}

// ReadTruncationDetails records how output from read was truncated (without repeating Content).
type ReadTruncationDetails struct {
	Truncated             bool              `json:"truncated"`
	TruncatedBy           jsonx.Opt[string] `json:"truncatedBy"`
	TotalLines            int               `json:"totalLines"`
	TotalBytes            int               `json:"totalBytes"`
	OutputLines           int               `json:"outputLines"`
	OutputBytes           int               `json:"outputBytes"`
	LastLinePartial       bool              `json:"lastLinePartial"`
	FirstLineExceedsLimit bool              `json:"firstLineExceedsLimit"`
	MaxLines              int               `json:"maxLines"`
	MaxBytes              int               `json:"maxBytes"`
}

// ReadToolDetails attaches truncation metadata to a read tool execution result.
type ReadToolDetails struct {
	Truncation *ReadTruncationDetails `json:"truncation,omitzero"`
}

func tredReadSchema() *typebox.Schema {
	return tidxObjectSchema(
		tidxProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to read (relative or absolute)"))},
		tidxProp{"offset", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Line number to start reading from (1-indexed)")))},
		tidxProp{"limit", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Maximum number of lines to read")))},
	)
}

func tredTruncationDetails(res durable.TruncationResult) ReadTruncationDetails {
	return ReadTruncationDetails{
		Truncated:             res.Truncated,
		TruncatedBy:           res.TruncatedBy,
		TotalLines:            res.TotalLines,
		TotalBytes:            res.TotalBytes,
		OutputLines:           res.OutputLines,
		OutputBytes:           res.OutputBytes,
		LastLinePartial:       res.LastLinePartial,
		FirstLineExceedsLimit: res.FirstLineExceedsLimit,
		MaxLines:              res.MaxLines,
		MaxBytes:              res.MaxBytes,
	}
}

// CreateReadTool constructs the durable "read" tool registration.
func CreateReadTool() *durable.ToolRegistration {
	return durable.DefineTool(&durable.ToolRegistration{
		Name: "read",
		Description: fmt.Sprintf(
			"Read the contents of a text file. Output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.",
			durable.DefaultMaxLines,
			durable.DefaultMaxBytes/1024,
		),
		Parameters: tredReadSchema(),
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			input, err := tidxDecodeArgs[ReadToolInput](args)
			if err != nil {
				return nil, err
			}
			executionEnv, err := RequireEnv(api, ctx)
			if err != nil {
				return nil, err
			}
			absolutePath, err := ResolveReadToolPath(executionEnv, input.Path, ctx)
			if err != nil {
				return nil, err
			}
			rawBytes, err := env.GetOrThrow(executionEnv.ReadBinaryFile(absolutePath, ctx))
			if err != nil {
				return nil, err
			}
			if mimeType := DetectSupportedImageMimeType(rawBytes); mimeType != "" {
				// Image content is not supported yet.
				code := "unsupported_image"
				return &durable.ToolExecutionResult{
					Content:    ai.ContentList{},
					HasContent: true,
					IsError:    true,
					Diagnostics: []durable.ToolDiagnostic{
						{
							Severity: durable.ToolDiagnosticError,
							Code:     &code,
							Message:  fmt.Sprintf("%s is an image (%s); reading images is not supported", input.Path, mimeType),
						},
					},
				}, nil
			}

			textContent := strings.ToValidUTF8(string(rawBytes), "\uFFFD")
			allLines := strings.Split(textContent, "\n")
			totalFileLines := len(allLines)

			startLine := 0
			if input.Offset != nil && !math.IsNaN(*input.Offset) && *input.Offset != 0 {
				candidate := int(*input.Offset) - 1
				if candidate > 0 {
					startLine = candidate
				}
			}
			startLineDisplay := startLine + 1
			if startLine >= len(allLines) {
				var offsetDisplay any
				if input.Offset != nil {
					offsetDisplay = *input.Offset
				}
				return nil, fmt.Errorf("Offset %v is beyond end of file (%d lines total)", offsetDisplay, len(allLines))
			}

			var selectedContent string
			var userLimitedLines *int
			if input.Limit != nil && !math.IsNaN(*input.Limit) {
				endLine := startLine + int(*input.Limit)
				if endLine > len(allLines) {
					endLine = len(allLines)
				}
				if endLine < startLine {
					endLine = startLine
				}
				selectedContent = strings.Join(allLines[startLine:endLine], "\n")
				n := endLine - startLine
				userLimitedLines = &n
			} else {
				selectedContent = strings.Join(allLines[startLine:], "\n")
			}

			truncation := durable.TruncateHead(selectedContent)
			var diagnostics []durable.ToolDiagnostic
			outputText := truncation.Content
			var details *ReadToolDetails

			if truncation.FirstLineExceedsLimit {
				lineBytes := []byte(allLines[startLine])
				end := durable.CharacterEnd(lineBytes, durable.DefaultMaxBytes)
				outputText = string(lineBytes[:end])
				code := "truncated"
				diagnostics = append(diagnostics, durable.ToolDiagnostic{
					Severity: durable.ToolDiagnosticWarn,
					Code:     &code,
					Message: fmt.Sprintf(
						"Line %d is %s, exceeds the %s limit; showing its first %s. Use bash: sed -n '%dp' %s | tail -c +%d",
						startLineDisplay,
						durable.FormatSize(len(lineBytes)),
						durable.FormatSize(durable.DefaultMaxBytes),
						durable.FormatSize(end),
						startLineDisplay,
						input.Path,
						end+1,
					),
				})
				truncDetails := tredTruncationDetails(truncation)
				truncDetails.OutputBytes = end
				truncDetails.OutputLines = 1
				details = &ReadToolDetails{Truncation: &truncDetails}
			} else if truncation.Truncated {
				endLineDisplay := startLineDisplay + truncation.OutputLines - 1
				nextOffset := endLineDisplay + 1
				limitText := ""
				truncBy, _ := truncation.TruncatedBy.Get()
				if truncBy != "lines" {
					limitText = fmt.Sprintf(" (%s limit)", durable.FormatSize(durable.DefaultMaxBytes))
				}
				code := "truncated"
				diagnostics = append(diagnostics, durable.ToolDiagnostic{
					Severity: durable.ToolDiagnosticInfo,
					Code:     &code,
					Message: fmt.Sprintf(
						"Showing lines %d-%d of %d%s. Use offset=%d to continue.",
						startLineDisplay,
						endLineDisplay,
						totalFileLines,
						limitText,
						nextOffset,
					),
				})
				truncDetails := tredTruncationDetails(truncation)
				details = &ReadToolDetails{Truncation: &truncDetails}
			} else if userLimitedLines != nil && startLine+*userLimitedLines < len(allLines) {
				remaining := len(allLines) - (startLine + *userLimitedLines)
				nextOffset := startLine + *userLimitedLines + 1
				diagnostics = append(diagnostics, durable.ToolDiagnostic{
					Severity: durable.ToolDiagnosticInfo,
					Message:  fmt.Sprintf("%d more lines in file. Use offset=%d to continue.", remaining, nextOffset),
				})
			}

			content := ai.ContentList{}
			if outputText != "" {
				content = ai.ContentList{&ai.TextContent{Type: "text", Text: outputText}}
			}
			result := &durable.ToolExecutionResult{
				Content:     content,
				HasContent:  true,
				Diagnostics: diagnostics,
			}
			if details != nil {
				result.Details = details
				result.HasDetails = true
			}
			return result, nil
		},
	})
}
