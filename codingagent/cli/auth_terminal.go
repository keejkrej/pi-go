// Ported from packages/coding-agent/src/cli/auth-terminal.ts (pi v1.5.2).

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"golang.org/x/term"
)

// AuthOutput is the line output contract shared by terminal auth commands.
type AuthOutput interface {
	Write(line string)
	Error(line string)
}

// LoginTerminal provides line-based terminal I/O for `pi auth login`.
// Prompts return an error when ctx cancels or the user interrupts.
type LoginTerminal interface {
	AuthOutput
	// Interactive reports whether stdin and stdout are attached to a terminal.
	Interactive() bool
	Question(ctx context.Context, message string) (string, error)
	// Secret reads a line without echoing characters to stdout.
	Secret(ctx context.Context, message string) (string, error)
	OpenUrl(targetUrl string)
}

type atProcessLoginTerminal struct {
	in          io.Reader
	out         io.Writer
	errOut      io.Writer
	interactive bool
	onInterrupt func()
	openUrlFn   func(string)
}

// NewProcessLoginTerminal creates a LoginTerminal backed by os.Stdin and os.Stdout.
// onInterrupt runs on Ctrl+C or EOF in Question, or Ctrl+C in Secret.
func NewProcessLoginTerminal(onInterrupt func()) LoginTerminal {
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	return atNewProcessLoginTerminalWithIO(
		os.Stdin,
		os.Stdout,
		os.Stderr,
		interactive,
		utils.OpenBrowser,
		onInterrupt,
	)
}

func atNewProcessLoginTerminalWithIO(
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
	interactive bool,
	openUrlFn func(string),
	onInterrupt func(),
) LoginTerminal {
	return &atProcessLoginTerminal{
		in:          in,
		out:         out,
		errOut:      errOut,
		interactive: interactive,
		onInterrupt: onInterrupt,
		openUrlFn:   openUrlFn,
	}
}

func (t *atProcessLoginTerminal) Interactive() bool {
	return t.interactive
}

func (t *atProcessLoginTerminal) Write(line string) {
	if t.out != nil {
		_, _ = fmt.Fprintln(t.out, line)
	}
}

func (t *atProcessLoginTerminal) Error(line string) {
	if t.errOut != nil {
		_, _ = fmt.Fprintln(t.errOut, line)
	}
}

func (t *atProcessLoginTerminal) Question(ctx context.Context, message string) (string, error) {
	return utils.QuestionTerminal(ctx, message, utils.QuestionTerminalOptions{
		Input:       t.in,
		Output:      t.out,
		OnInterrupt: t.onInterrupt,
	})
}

func (t *atProcessLoginTerminal) Secret(ctx context.Context, message string) (string, error) {
	return atReadSecret(ctx, t.in, t.out, message, t.onInterrupt)
}

func (t *atProcessLoginTerminal) OpenUrl(targetUrl string) {
	if t.openUrlFn != nil {
		t.openUrlFn(targetUrl)
	}
}

type atSecretResult struct {
	value       string
	interrupted bool
	err         error
}

func atPopLastRune(s string) string {
	if s == "" {
		return ""
	}
	_, size := utf8.DecodeLastRuneInString(s)
	if size <= 0 {
		return ""
	}
	return s[:len(s)-size]
}

func atReadSecret(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
	message string,
	onInterrupt func(),
) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", &utils.AbortError{Message: "Login cancelled"}
	}
	if in == nil {
		in = os.Stdin
	}
	if out != nil && message != "" {
		if _, err := io.WriteString(out, message); err != nil {
			return "", err
		}
	}

	var restoreRaw func()
	if f, ok := in.(*os.File); ok {
		fd := int(f.Fd())
		if term.IsTerminal(fd) {
			if state, err := term.MakeRaw(fd); err == nil {
				restoreRaw = func() {
					_ = term.Restore(fd, state)
				}
			}
		}
	}
	finish := func() {
		if restoreRaw != nil {
			restoreRaw()
			restoreRaw = nil
		}
		if out != nil {
			_, _ = io.WriteString(out, "\n")
		}
	}

	resCh := make(chan atSecretResult, 1)
	go func() {
		var value string
		buf := make([]byte, 256)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				// Ignore escape sequences such as arrow keys.
				if strings.HasPrefix(chunk, "\x1b") {
					if err != nil {
						resCh <- atSecretResult{err: err}
						return
					}
					continue
				}
				for _, ch := range chunk {
					if ch == '\r' || ch == '\n' {
						resCh <- atSecretResult{value: value}
						return
					}
					if ch == '\x03' {
						resCh <- atSecretResult{interrupted: true, err: &utils.AbortError{Message: "Login cancelled"}}
						return
					}
					if ch == '\x7f' || ch == '\b' {
						value = atPopLastRune(value)
					} else if ch >= ' ' {
						value += string(ch)
					}
				}
			}
			if err != nil {
				resCh <- atSecretResult{interrupted: true, err: &utils.AbortError{Message: "Login cancelled"}}
				return
			}
		}
	}()

	var doneCh <-chan struct{}
	if ctx != nil {
		doneCh = ctx.Done()
	}

	select {
	case <-doneCh:
		finish()
		return "", &utils.AbortError{Message: "Login cancelled"}
	case res := <-resCh:
		finish()
		if res.interrupted {
			if onInterrupt != nil {
				onInterrupt()
			}
			return "", res.err
		}
		if res.err != nil {
			return "", res.err
		}
		return res.value, nil
	}
}
