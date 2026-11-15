package components

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

// ExternalEditorOptions specifies the editor command and initial buffer content.
type ExternalEditorOptions struct {
	Command string
	Content string
}

// ExternalEditorStatus reports whether editing completed cleanly or failed.
type ExternalEditorStatus string

const (
	// ExternalEditorStatusComplete indicates the external editor exited normally.
	ExternalEditorStatusComplete ExternalEditorStatus = "complete"
	// ExternalEditorStatusFailed indicates the command could not run or exited non-zero.
	ExternalEditorStatusFailed ExternalEditorStatus = "failed"
)

// ExternalEditorResult holds the outcome and edited content from EditInExternalEditor.
type ExternalEditorResult struct {
	Status  ExternalEditorStatus
	Content string
	Message string
}

var eedExec = func(name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// EditInExternalEditor launches an external editor on a temporary Markdown file
// inside a private 0700 directory, waits for exit, and returns the edited text
// with any leading UTF-8 BOM stripped.
func EditInExternalEditor(options ExternalEditorOptions) ExternalEditorResult {
	parts := eedParseCommand(options.Command)
	if len(parts) == 0 {
		return ExternalEditorResult{
			Status:  ExternalEditorStatusFailed,
			Content: options.Content,
			Message: "External editor command is empty",
		}
	}

	tmpDir, err := os.MkdirTemp("", "pi-editor-*")
	if err != nil {
		return ExternalEditorResult{
			Status:  ExternalEditorStatusFailed,
			Content: options.Content,
			Message: fmt.Sprintf("Failed to create temp directory: %v", err),
		}
	}
	defer os.RemoveAll(tmpDir)

	tmpFile := filepath.Join(tmpDir, "input.md")
	if err := os.WriteFile(tmpFile, []byte(options.Content), 0o600); err != nil {
		return ExternalEditorResult{
			Status:  ExternalEditorStatusFailed,
			Content: options.Content,
			Message: fmt.Sprintf("Failed to write temp file: %v", err),
		}
	}

	args := append(append([]string(nil), parts[1:]...), tmpFile)
	if err := eedExec(parts[0], args); err != nil {
		return ExternalEditorResult{
			Status:  ExternalEditorStatusFailed,
			Content: options.Content,
			Message: fmt.Sprintf("External editor failed: %v", err),
		}
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return ExternalEditorResult{
			Status:  ExternalEditorStatusFailed,
			Content: options.Content,
			Message: fmt.Sprintf("Failed to read edited file: %v", err),
		}
	}

	return ExternalEditorResult{
		Status:  ExternalEditorStatusComplete,
		Content: utils.StripBom(string(data)),
	}
}

func eedParseCommand(command string) []string {
	var args []string
	var cur strings.Builder
	inToken := false
	inSingle := false
	inDouble := false

	flush := func() {
		if inToken {
			args = append(args, cur.String())
			cur.Reset()
			inToken = false
		}
	}

	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(ch)
			}
			inToken = true
			continue
		}
		if inDouble {
			if ch == '"' {
				inDouble = false
			} else if ch == '\\' && i+1 < len(runes) {
				next := runes[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' {
					cur.WriteRune(next)
					i++
				} else {
					cur.WriteRune(ch)
				}
			} else {
				cur.WriteRune(ch)
			}
			inToken = true
			continue
		}

		switch {
		case ch == '\'':
			inSingle = true
			inToken = true
		case ch == '"':
			inDouble = true
			inToken = true
		case ch == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
			inToken = true
		case unicode.IsSpace(ch):
			flush()
		default:
			cur.WriteRune(ch)
			inToken = true
		}
	}
	flush()
	return args
}
