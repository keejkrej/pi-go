// Ported from packages/coding-agent/src/utils/terminal-question.ts (pi v1.5.2).

package utils

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
)

// QuestionTerminalOptions configures QuestionTerminal prompts.
type QuestionTerminalOptions struct {
	// Input reads user lines; nil defaults to os.Stdin.
	Input io.Reader
	// Output receives the prompt message.
	Output io.Writer
	// OnInterrupt is called on Ctrl+C or EOF, not when ctx cancels the prompt.
	OnInterrupt func()
}

var tqNotifyInterrupt = func(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt)
}

var tqStopInterrupt = func(ch chan<- os.Signal) {
	signal.Stop(ch)
}

type tqReadResult struct {
	line        string
	interrupted bool
	err         error
}

// QuestionTerminal reads a single line from Input (default os.Stdin), aborting on ctx cancellation,
// Ctrl+C, or EOF. On Ctrl+C or EOF it invokes options.OnInterrupt before returning an AbortError.
func QuestionTerminal(ctx context.Context, message string, options QuestionTerminalOptions) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", &AbortError{Message: "The operation was aborted"}
	}

	input := options.Input
	if input == nil {
		input = os.Stdin
	}

	if options.Output != nil && message != "" {
		if _, err := io.WriteString(options.Output, message); err != nil {
			return "", err
		}
	}

	sigCh := make(chan os.Signal, 1)
	tqNotifyInterrupt(sigCh)
	defer tqStopInterrupt(sigCh)

	resCh := make(chan tqReadResult, 1)
	go func() {
		reader := bufio.NewReader(input)
		var buf strings.Builder
		for {
			b, err := reader.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
					resCh <- tqReadResult{interrupted: true, err: &AbortError{Message: "The operation was aborted"}}
					return
				}
				resCh <- tqReadResult{err: err}
				return
			}
			if b == 0x03 {
				resCh <- tqReadResult{interrupted: true, err: &AbortError{Message: "The operation was aborted"}}
				return
			}
			if b == '\n' {
				line := strings.TrimSuffix(buf.String(), "\r")
				resCh <- tqReadResult{line: line}
				return
			}
			buf.WriteByte(b)
		}
	}()

	var doneCh <-chan struct{}
	if ctx != nil {
		doneCh = ctx.Done()
	}

	select {
	case <-doneCh:
		return "", &AbortError{Message: "The operation was aborted"}
	case <-sigCh:
		if options.OnInterrupt != nil {
			options.OnInterrupt()
		}
		return "", &AbortError{Message: "The operation was aborted"}
	case res := <-resCh:
		if res.interrupted {
			if options.OnInterrupt != nil {
				options.OnInterrupt()
			}
			return "", res.err
		}
		return res.line, res.err
	}
}
