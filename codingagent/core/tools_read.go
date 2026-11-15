// Ported from packages/coding-agent/src/core/tools/read.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

// ReadToolInput defines parameters for the read tool.
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitzero"`
	Limit  *float64 `json:"limit,omitzero"`
}

// ReadToolImageOutput is the structured image value returned to programmatic callers.
type ReadToolImageOutput struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	Note     string `json:"note"`
}

// ReadToolSystemPromptContribution holds the system prompt snippet and guidelines for read.
var ReadToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Read file contents",
	Guidelines: []string{"Use read to examine files instead of cat or sed."},
}

// ReadToolDetails provides truncation and model-capability metadata for rendered read results.
type ReadToolDetails struct {
	Truncation              *TruncationResult `json:"truncation,omitzero"`
	ImageUnsupportedByModel *string           `json:"imageUnsupportedByModel,omitzero"`
}

// ReadOperations configures filesystem/stream hooks for the read tool.
type ReadOperations struct {
	ReadFile            func(ctx context.Context, absolutePath string) ([]byte, error)
	Access              func(ctx context.Context, absolutePath string) error
	DetectImageMimeType func(ctx context.Context, absolutePath string) (*string, error)
	CreateReadStream    func(ctx context.Context, absolutePath string) (io.ReadCloser, error)
}

// ReadToolOptions configures CreateReadToolDefinition and CreateReadTool.
type ReadToolOptions struct {
	AutoResizeImages *bool
	ResizeOptions    *ai.ModelImageResizeOptions
	Operations       *ReadOperations
}

type trLimitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *trLimitedReadCloser) Read(p []byte) (int, error) {
	return l.r.Read(p)
}

func (l *trLimitedReadCloser) Close() error {
	return l.c.Close()
}

func trDefaultReadOperations() ReadOperations {
	return ReadOperations{
		ReadFile: func(_ context.Context, absolutePath string) ([]byte, error) {
			return os.ReadFile(absolutePath)
		},
		Access: func(_ context.Context, absolutePath string) error {
			f, err := os.Open(absolutePath)
			if err != nil {
				return err
			}
			return f.Close()
		},
		DetectImageMimeType: func(_ context.Context, absolutePath string) (*string, error) {
			mime, err := utils.DetectSupportedImageMimeTypeFromFile(absolutePath)
			if err != nil {
				return nil, err
			}
			if mime == "" {
				return nil, nil
			}
			return &mime, nil
		},
		CreateReadStream: trReadLocalFileChunks,
	}
}

func trReadLocalFileChunks(ctx context.Context, absolutePath string) (io.ReadCloser, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, errors.New("Operation aborted")
	}
	f, err := os.Open(absolutePath)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.Mode().IsRegular() && info.Size() > 0 {
		return &trLimitedReadCloser{
			r: io.LimitReader(f, info.Size()),
			c: f,
		}, nil
	}
	return f, nil
}

func trReadSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"path", typebox.String(jsonx.ObjectOf("description", "Path to the file to read (relative or absolute)"))},
		tiProp{"offset", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Line number to start reading from (1-indexed)")))},
		tiProp{"limit", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Maximum number of lines to read")))},
	)
}

func trReadOutputSchema() *typebox.Schema {
	return typebox.Union([]*typebox.Schema{
		typebox.String(),
		tiObjectSchema(
			tiProp{"type", typebox.Literal("image")},
			tiProp{"data", typebox.String()},
			tiProp{"mimeType", typebox.String()},
			tiProp{"note", typebox.String()},
		),
	})
}

func trToReadOutput(content ai.ContentList) any {
	var text string
	var img *ai.ImageContent
	for _, block := range content {
		switch b := block.(type) {
		case *ai.TextContent:
			if text == "" && b != nil {
				text = b.Text
			}
		case *ai.ImageContent:
			if img == nil && b != nil {
				img = b
			}
		}
	}
	if img != nil {
		return ReadToolImageOutput{
			Type:     "image",
			Data:     img.Data,
			MimeType: img.MimeType,
			Note:     text,
		}
	}
	return text
}

type trSelectedLines struct {
	totalFileLines int
	truncation     TruncationResult
	firstLineBytes int
}

func trIsInteger(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v == math.Trunc(v)
}

func trIsStreamableRange(offset, limit *float64) bool {
	if offset != nil && !trIsInteger(*offset) {
		return false
	}
	if limit != nil && (!trIsInteger(*limit) || *limit < 0) {
		return false
	}
	return true
}

func trSelectLinesFromBuffer(buffer []byte, startLine float64, limit *float64) trSelectedLines {
	dec := js.NewTextDecoder(&js.TextDecoderOptions{IgnoreBOM: true})
	decoded := dec.DecodeFinal(buffer)
	allLines := strings.Split(decoded, "\n")

	startIdx := int(math.Trunc(startLine))
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx > len(allLines) {
		startIdx = len(allLines)
	}

	endIdx := len(allLines)
	if limit != nil {
		rawEnd := startLine + *limit
		if math.IsNaN(rawEnd) {
			endIdx = 0
		} else if !math.IsInf(rawEnd, 1) {
			if math.IsInf(rawEnd, -1) {
				endIdx = 0
			} else {
				e := int(math.Trunc(rawEnd))
				if e < 0 {
					endIdx = max(0, len(allLines)+e)
				} else {
					endIdx = min(e, len(allLines))
				}
			}
		}
	}
	if endIdx < startIdx {
		endIdx = startIdx
	}

	selected := allLines[startIdx:endIdx]
	truncation := TruncateHead(strings.Join(selected, "\n"))
	firstLineBytes := 0
	if truncation.FirstLineExceedsLimit && startIdx < len(allLines) {
		firstLineBytes = len(allLines[startIdx])
	}
	return trSelectedLines{
		totalFileLines: len(allLines),
		truncation:     truncation,
		firstLineBytes: firstLineBytes,
	}
}

func trSelectLinesFromStream(
	ctx context.Context,
	chunks io.ReadCloser,
	startLine int,
	limit *int,
) (trSelectedLines, error) {
	defer chunks.Close()

	hasEndLine := limit != nil
	endLine := 0
	if limit != nil {
		endLine = startLine + *limit
	}

	decoder := js.NewTextDecoder(&js.TextDecoderOptions{IgnoreBOM: true})
	var storedLines []string
	storedBytes := 0
	storedPastCaps := false
	line := 0
	selectedLines := 0
	selectedBytes := 0
	firstLineBytes := 0
	lastLineEmpty := true

	pieces := []string{}
	lineBytes := 0
	lineHasText := false

	appendToLine := func(text string, start, end int) {
		lineHasText = true
		if pieces == nil {
			return
		}
		piece := text[start:end]
		if lineBytes <= DefaultMaxBytes {
			pieces = append(pieces, piece)
		}
		lineBytes += len(piece)
	}

	finishLine := func() {
		if pieces != nil {
			if selectedLines == 0 {
				firstLineBytes = lineBytes
			}
			if len(storedLines) > DefaultMaxLines || storedBytes > DefaultMaxBytes {
				storedPastCaps = true
			}
			text := strings.Join(pieces, "")
			if len(storedLines) > 0 {
				storedBytes += 1
			}
			storedBytes += len(text)
			storedLines = append(storedLines, text)
		}
		selectedLines++
		lastLineEmpty = !lineHasText
		if storedPastCaps {
			pieces = nil
		} else {
			pieces = []string{}
		}
		lineBytes = 0
		lineHasText = false
	}

	selectText := func(text string) {
		position := 0
		selectionStart := -1
		for {
			selected := line >= startLine && (!hasEndLine || line < endLine)
			if selected && selectionStart == -1 {
				selectionStart = position
			}
			newlineRel := strings.IndexByte(text[position:], '\n')
			var end int
			if newlineRel == -1 {
				end = len(text)
			} else {
				end = position + newlineRel
			}
			if selected && end > position {
				appendToLine(text, position, end)
			}
			if newlineRel == -1 {
				break
			}
			if selected {
				finishLine()
				if hasEndLine && line+1 == endLine {
					selectedBytes += end - selectionStart
					selectionStart = -1
				}
			}
			line++
			position = end + 1
		}
		if selectionStart != -1 {
			selectedBytes += len(text) - selectionStart
		}
	}

	buf := make([]byte, 32*1024)
	for {
		if ctx != nil && ctx.Err() != nil {
			return trSelectedLines{}, errors.New("Operation aborted")
		}
		n, err := chunks.Read(buf)
		if ctx != nil && ctx.Err() != nil {
			return trSelectedLines{}, errors.New("Operation aborted")
		}
		if n > 0 {
			selectText(decoder.Decode(buf[:n]))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return trSelectedLines{}, err
		}
	}

	selectText(decoder.Flush())
	if line >= startLine && (!hasEndLine || line < endLine) {
		finishLine()
	}

	truncation := TruncateHead(strings.Join(storedLines, "\n"))
	totalLines := 0
	if selectedBytes != 0 {
		totalLines = selectedLines
		if lastLineEmpty {
			totalLines--
		}
	}
	truncation.TotalLines = totalLines
	truncation.TotalBytes = selectedBytes

	return trSelectedLines{
		totalFileLines: line + 1,
		truncation:     truncation,
		firstLineBytes: firstLineBytes,
	}, nil
}

// CreateReadToolDefinition creates the ToolDefinition for the built-in read tool.
func CreateReadToolDefinition(cwd string, options ...*ReadToolOptions) ToolDefinition {
	autoResizeImages := true
	var fallbackResizeOptions *utils.ImageResizeOptions
	ops := trDefaultReadOperations()

	if len(options) > 0 && options[0] != nil {
		opt := options[0]
		if opt.AutoResizeImages != nil {
			autoResizeImages = *opt.AutoResizeImages
		}
		fallbackResizeOptions = opt.ResizeOptions
		if opt.Operations != nil {
			ops = *opt.Operations
		}
	}

	snippet := ReadToolSystemPromptContribution.Snippet
	def := ToolDefinition{
		Name:                "read",
		Label:               "read",
		Description:         fmt.Sprintf("Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.", DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet:       &snippet,
		PromptGuidelines:    slices.Clone(ReadToolSystemPromptContribution.Guidelines),
		Parameters:          trReadSchema(),
		OutputSchema:        trReadOutputSchema(),
		ConstrainedSampling: tiStrictPreferJsonSchema(),
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			_ agent.AgentToolUpdateCallback,
			ectx ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}
			var input ReadToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			effectiveCwd := tiResolveToolCwd(ectx, cwd)
			absolutePath := ResolveReadPathAsync(ctx, input.Path, effectiveCwd)
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			if ops.Access != nil {
				if err := ops.Access(ctx, absolutePath); err != nil {
					return agent.AgentToolResult{}, err
				}
			}
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			var mimeType *string
			if ops.DetectImageMimeType != nil {
				detected, err := ops.DetectImageMimeType(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				mimeType = detected
			}
			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			var content ai.ContentList
			var details *ReadToolDetails

			if mimeType != nil && *mimeType != "" {
				if ops.ReadFile == nil {
					return agent.AgentToolResult{}, errors.New("readFile operation is not configured")
				}
				buffer, err := ops.ReadFile(ctx, absolutePath)
				if err != nil {
					return agent.AgentToolResult{}, err
				}
				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				resizeOpts := fallbackResizeOptions
				var model *ai.Model
				if ectx != nil {
					model = ectx.Model()
					if model != nil && model.InputLimits != nil && model.InputLimits.Images != nil && model.InputLimits.Images.Resize != nil {
						resizeOpts = model.InputLimits.Images.Resize
					}
				}
				processed := utils.ProcessImage(buffer, *mimeType, &utils.ProcessImageOptions{
					AutoResizeImages: &autoResizeImages,
					Resize:           resizeOpts,
				})
				if !processed.Ok {
					textNote := fmt.Sprintf("Read image file [%s]\n%s", *mimeType, processed.Message)
					content = ai.ContentList{&ai.TextContent{Type: "text", Text: textNote}}
				} else {
					textNote := fmt.Sprintf("Read image file [%s]", processed.MimeType)
					if len(processed.Hints) > 0 {
						textNote += "\n" + strings.Join(processed.Hints, "\n")
					}
					content = ai.ContentList{
						&ai.TextContent{Type: "text", Text: textNote},
						&ai.ImageContent{Type: "image", Data: processed.Data, MimeType: processed.MimeType},
					}
					if model != nil && !slices.Contains(model.Input, ai.ModelModalityImage) {
						modelId := model.Id
						details = &ReadToolDetails{ImageUnsupportedByModel: &modelId}
					}
				}
			} else {
				startLine := 0.0
				if input.Offset != nil && *input.Offset != 0 {
					startLine = math.Max(0, *input.Offset-1)
				}
				startLineDisplay := startLine + 1

				var selected trSelectedLines
				if ops.CreateReadStream != nil && trIsStreamableRange(input.Offset, input.Limit) {
					rc, err := ops.CreateReadStream(ctx, absolutePath)
					if err != nil {
						return agent.AgentToolResult{}, err
					}
					var limitInt *int
					if input.Limit != nil {
						v := int(*input.Limit)
						limitInt = &v
					}
					selected, err = trSelectLinesFromStream(ctx, rc, int(startLine), limitInt)
					if err != nil {
						return agent.AgentToolResult{}, err
					}
				} else {
					if ops.ReadFile == nil {
						return agent.AgentToolResult{}, errors.New("readFile operation is not configured")
					}
					buffer, err := ops.ReadFile(ctx, absolutePath)
					if err != nil {
						return agent.AgentToolResult{}, err
					}
					selected = trSelectLinesFromBuffer(buffer, startLine, input.Limit)
				}

				if err := tiCheckAborted(ctx); err != nil {
					return agent.AgentToolResult{}, err
				}

				if startLine >= float64(selected.totalFileLines) {
					return agent.AgentToolResult{}, fmt.Errorf(
						"Offset %s is beyond end of file (%d lines total)",
						trFormatNumber(input.Offset),
						selected.totalFileLines,
					)
				}

				var userLimitedLines *float64
				if input.Limit != nil {
					v := math.Min(startLine+*input.Limit, float64(selected.totalFileLines)) - startLine
					userLimitedLines = &v
				}

				var outputText string
				trunc := selected.truncation
				if trunc.FirstLineExceedsLimit {
					firstLineSize := FormatSize(selected.firstLineBytes)
					outputText = fmt.Sprintf(
						"[Line %s is %s, exceeds %s limit. Use bash: sed -n '%sp' %s | head -c %d]",
						trFormatFloat(startLineDisplay),
						firstLineSize,
						FormatSize(DefaultMaxBytes),
						trFormatFloat(startLineDisplay),
						input.Path,
						DefaultMaxBytes,
					)
					details = &ReadToolDetails{Truncation: &trunc}
				} else if trunc.Truncated {
					endLineDisplay := startLineDisplay + float64(trunc.OutputLines) - 1
					nextOffset := endLineDisplay + 1
					outputText = trunc.Content
					if trunc.TruncatedBy.OrElse("") == "lines" {
						outputText += fmt.Sprintf(
							"\n\n[Showing lines %s-%s of %d. Use offset=%s to continue.]",
							trFormatFloat(startLineDisplay),
							trFormatFloat(endLineDisplay),
							selected.totalFileLines,
							trFormatFloat(nextOffset),
						)
					} else {
						outputText += fmt.Sprintf(
							"\n\n[Showing lines %s-%s of %d (%s limit). Use offset=%s to continue.]",
							trFormatFloat(startLineDisplay),
							trFormatFloat(endLineDisplay),
							selected.totalFileLines,
							FormatSize(DefaultMaxBytes),
							trFormatFloat(nextOffset),
						)
					}
					details = &ReadToolDetails{Truncation: &trunc}
				} else if userLimitedLines != nil && startLine+*userLimitedLines < float64(selected.totalFileLines) {
					remaining := float64(selected.totalFileLines) - (startLine + *userLimitedLines)
					nextOffset := startLine + *userLimitedLines + 1
					outputText = fmt.Sprintf(
						"%s\n\n[%s more lines in file. Use offset=%s to continue.]",
						trunc.Content,
						trFormatFloat(remaining),
						trFormatFloat(nextOffset),
					)
				} else {
					outputText = trunc.Content
				}
				content = ai.ContentList{&ai.TextContent{Type: "text", Text: outputText}}
			}

			if err := tiCheckAborted(ctx); err != nil {
				return agent.AgentToolResult{}, err
			}

			var detailsVal any
			if details != nil {
				detailsVal = details
			}
			return agent.AgentToolResult{
				Content:           content,
				Details:           detailsVal,
				StructuredContent: trToReadOutput(content),
			}, nil
		},
	}
	tiApplyToolRenderers(&def, ReadToolRenderers())
	return def
}

func trFormatNumber(v *float64) string {
	if v == nil {
		return "undefined"
	}
	return trFormatFloat(*v)
}

func trFormatFloat(v float64) string {
	return jsonx.FormatNumber(v)
}

// CreateReadTool wraps CreateReadToolDefinition as an executable *agent.AgentTool.
func CreateReadTool(cwd string, options ...*ReadToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateReadToolDefinition(cwd, options...))
}
