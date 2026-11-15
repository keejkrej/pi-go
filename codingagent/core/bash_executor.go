// Ported from packages/coding-agent/src/core/bash-executor.ts (pi v1.0.0).

package core

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/js"
)

// BashExecutorOptions configures streaming bash execution with cancellation.
type BashExecutorOptions struct {
	OnChunk func(chunk string)
	Signal  context.Context
}

// BashResult captures sanitized, possibly truncated command output and termination status.
type BashResult struct {
	Output         string  `json:"output"`
	ExitCode       *int    `json:"exitCode,omitzero"`
	Cancelled      bool    `json:"cancelled"`
	Truncated      bool    `json:"truncated"`
	FullOutputPath *string `json:"fullOutputPath,omitzero"`
}

// ExecuteBashWithOperations runs command in cwd via operations with rolling output buffering and tail truncation.
func ExecuteBashWithOperations(
	ctx context.Context,
	command string,
	cwd string,
	operations BashOperations,
	options ...*BashExecutorOptions,
) (BashResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt BashExecutorOptions
	if len(options) > 0 && options[0] != nil {
		opt = *options[0]
	}
	execCtx := ctx
	if opt.Signal != nil {
		execCtx = opt.Signal
	}

	var mu sync.Mutex
	var outputChunks []string
	outputBytes := 0
	discardedOutput := false
	maxOutputBytes := DefaultMaxBytes * 2

	var tempFilePath *string
	var tempFile *os.File
	totalBytes := 0

	ensureTempFile := func() {
		if tempFilePath != nil {
			return
		}
		p, f, err := utils.CreateOutputFileStream("pi-bash", ".log")
		if err != nil {
			return
		}
		tempFilePath = &p
		tempFile = f
		for _, chunk := range outputChunks {
			_, _ = tempFile.WriteString(chunk)
		}
	}

	decoder := js.NewUTF8StreamDecoder()
	pendingAnsi := ""

	appendText := func(rawText string) {
		text := strings.ReplaceAll(utils.SanitizeBinaryOutput(utils.StripAnsi(rawText)), "\r", "")
		if text == "" {
			return
		}

		if totalBytes > DefaultMaxBytes {
			ensureTempFile()
		}

		if tempFile != nil {
			_, _ = tempFile.WriteString(text)
		}

		outputChunks = append(outputChunks, text)
		outputBytes += len(text)
		for outputBytes > maxOutputBytes && len(outputChunks) > 1 {
			removed := outputChunks[0]
			outputChunks = outputChunks[1:]
			outputBytes -= len(removed)
			discardedOutput = true
		}

		if opt.OnChunk != nil {
			opt.OnChunk(text)
		}
	}

	onData := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()

		totalBytes += len(data)
		split := utils.SplitIncompleteAnsiSuffix(pendingAnsi + decoder.Decode(data))
		pendingAnsi = split.Pending
		appendText(split.Complete)
	}

	flushOutput := func() {
		mu.Lock()
		defer mu.Unlock()

		rest := pendingAnsi + decoder.Flush()
		pendingAnsi = ""
		appendText(rest)
	}

	execRes, err := operations.Exec(execCtx, command, cwd, BashOperationsOptions{
		OnData: onData,
		Signal: execCtx,
	})
	if err != nil {
		if execCtx.Err() == nil {
			mu.Lock()
			if tempFile != nil {
				_ = tempFile.Close()
			}
			mu.Unlock()
			return BashResult{}, err
		}
	}

	flushOutput()

	mu.Lock()
	fullOutput := strings.Join(outputChunks, "")
	truncationResult := TruncateTail(fullOutput)
	if truncationResult.Truncated {
		ensureTempFile()
	}
	if tempFile != nil {
		_ = tempFile.Close()
		tempFile = nil
	}
	outPath := tempFilePath
	truncated := discardedOutput || truncationResult.Truncated
	mu.Unlock()

	cancelled := execCtx.Err() != nil
	var exitCode *int
	if !cancelled {
		exitCode = execRes.ExitCode
	}
	output := fullOutput
	if truncationResult.Truncated {
		output = truncationResult.Content
	}

	return BashResult{
		Output:         output,
		ExitCode:       exitCode,
		Cancelled:      cancelled,
		Truncated:      truncated,
		FullOutputPath: outPath,
	}, nil
}
