// Ported from packages/coding-agent/src/cli/file-processor.ts (pi v1.5.2).

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// ProcessedFiles holds combined `<file>` text blocks and image attachments
// produced from `@file` CLI arguments.
type ProcessedFiles struct {
	Text   string
	Images []*ai.ImageContent
}

// ProcessFileOptions configures ProcessFileArguments.
type ProcessFileOptions struct {
	// AutoResizeImages controls image resizing during processing. Nil defaults to true.
	AutoResizeImages *bool
	// Cwd overrides the working directory used to resolve relative paths. Empty uses os.Getwd().
	Cwd string
}

// ProcessFileArguments reads `@file` paths into XML-wrapped text blocks and image attachments.
func ProcessFileArguments(fileArgs []string, options ...*ProcessFileOptions) (ProcessedFiles, error) {
	autoResize := true
	var cwd string
	if len(options) > 0 && options[0] != nil {
		if options[0].AutoResizeImages != nil {
			autoResize = *options[0].AutoResizeImages
		}
		cwd = options[0].Cwd
	}
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return ProcessedFiles{}, err
		}
		cwd = wd
	}

	var textBuilder strings.Builder
	var images []*ai.ImageContent

	for _, fileArg := range fileArgs {
		resolved := core.ResolveReadPath(fileArg, cwd)
		absolutePath, err := filepath.Abs(resolved)
		if err != nil {
			absolutePath = resolved
		}

		info, err := os.Stat(absolutePath)
		if err != nil {
			return ProcessedFiles{}, fmt.Errorf("File not found: %s", absolutePath)
		}
		if info.Size() == 0 {
			continue
		}

		mimeType, _ := utils.DetectSupportedImageMimeTypeFromFile(absolutePath)
		if mimeType != "" {
			rawBytes, err := os.ReadFile(absolutePath)
			if err != nil {
				return ProcessedFiles{}, fmt.Errorf("Could not read file %s: %v", absolutePath, err)
			}
			processed := utils.ProcessImage(rawBytes, mimeType, &utils.ProcessImageOptions{
				AutoResizeImages: &autoResize,
			})
			if !processed.Ok {
				fmt.Fprintf(&textBuilder, `<file name="%s">%s</file>`+"\n", absolutePath, processed.Message)
				continue
			}

			images = append(images, &ai.ImageContent{
				Type:     "image",
				MimeType: processed.MimeType,
				Data:     processed.Data,
			})
			hints := strings.Join(processed.Hints, "\n")
			fmt.Fprintf(&textBuilder, `<file name="%s">%s</file>`+"\n", absolutePath, hints)
			continue
		}

		rawBytes, err := os.ReadFile(absolutePath)
		if err != nil {
			return ProcessedFiles{}, fmt.Errorf("Could not read file %s: %v", absolutePath, err)
		}
		content := utils.StripBom(string(rawBytes))
		fmt.Fprintf(&textBuilder, "<file name=%q>\n%s\n</file>\n", absolutePath, content)
	}

	return ProcessedFiles{
		Text:   textBuilder.String(),
		Images: images,
	}, nil
}
