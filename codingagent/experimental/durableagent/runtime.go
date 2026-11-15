// Ported from packages/coding-agent/src/experimental/durable/runtime.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
)

// DurableAgentCliOptions holds parsed CLI flags for the standalone durable agent.
type DurableAgentCliOptions struct {
	Cwd               string
	AgentDir          string
	Session           string
	SessionDir        string
	Continue          bool
	Resume            bool
	NoSession         bool
	Provider          *string
	Model             *string
	Thinking          *agent.ThinkingLevel
	Instructions      *string
	Prompt            string
	PrintMode         bool
	AllowModelNetwork bool
	Help              bool
}

// ParseDurableAgentArgs parses command-line arguments for durableagent.
func ParseDurableAgentArgs(args []string) (DurableAgentCliOptions, error) {
	opts := DurableAgentCliOptions{
		AllowModelNetwork: true,
	}
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			return args[i], nil
		}
		switch {
		case arg == "-h" || arg == "--help":
			opts.Help = true
		case arg == "-p" || arg == "--print":
			opts.PrintMode = true
		case arg == "-c" || arg == "--continue":
			opts.Continue = true
		case arg == "-r" || arg == "--resume":
			opts.Resume = true
		case arg == "--no-session":
			opts.NoSession = true
		case arg == "--no-network":
			opts.AllowModelNetwork = false
		case arg == "--cwd" || arg == "-C":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Cwd = v
		case strings.HasPrefix(arg, "--cwd="):
			opts.Cwd = strings.TrimPrefix(arg, "--cwd=")
		case arg == "--session":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Session = v
		case strings.HasPrefix(arg, "--session="):
			opts.Session = strings.TrimPrefix(arg, "--session=")
		case arg == "--session-dir":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.SessionDir = v
		case strings.HasPrefix(arg, "--session-dir="):
			opts.SessionDir = strings.TrimPrefix(arg, "--session-dir=")
		case arg == "--agent-dir":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.AgentDir = v
		case arg == "--provider":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Provider = &v
		case strings.HasPrefix(arg, "--provider="):
			v := strings.TrimPrefix(arg, "--provider=")
			opts.Provider = &v
		case arg == "--model":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Model = &v
		case strings.HasPrefix(arg, "--model="):
			v := strings.TrimPrefix(arg, "--model=")
			opts.Model = &v
		case arg == "--thinking":
			v, err := next()
			if err != nil {
				return opts, err
			}
			lvl := agent.ThinkingLevel(v)
			opts.Thinking = &lvl
		case strings.HasPrefix(arg, "--thinking="):
			lvl := agent.ThinkingLevel(strings.TrimPrefix(arg, "--thinking="))
			opts.Thinking = &lvl
		case arg == "--append-system-prompt" || arg == "--instructions":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Instructions = &v
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag: %s", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) > 0 {
		opts.Prompt = strings.Join(positional, " ")
	}
	if opts.Cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			opts.Cwd = wd
		} else {
			opts.Cwd = "."
		}
	}
	return opts, nil
}

// DurableAgentRuntime manages a live durable agent harness, session file lock, and root conversation.
type DurableAgentRuntime struct {
	mu          sync.Mutex
	Bundle      *DurableHarnessBundle
	SessionPath string
	SessionDir  string
	releaseLock func() error
	closed      bool
}

// StartDurableAgentRuntime opens or creates the session database, resumes tasks, and returns a runtime.
func StartDurableAgentRuntime(ctx context.Context, opts DurableAgentCliOptions, extraExtensions ...*durable.Extension) (*DurableAgentRuntime, error) {
	sessionDir := opts.SessionDir
	if sessionDir == "" {
		sessionDir = GetDurableSessionsDir(opts.AgentDir)
	}

	var dbStorage durable.Storage
	var release func() error
	var sessionPath string

	if opts.NoSession {
		dbStorage = storage.NewMemoryStorage()
	} else {
		sessionPath = strings.TrimSpace(opts.Session)
		if sessionPath == "" && opts.Continue {
			list, err := ListDurableSessions(ctx, sessionDir)
			if err != nil {
				return nil, err
			}
			for _, item := range list {
				if !item.Locked {
					sessionPath = item.Path
					break
				}
			}
		}
		if sessionPath == "" {
			sessionPath = NewDurableSessionPath(sessionDir)
		} else if !filepath.IsAbs(sessionPath) && !strings.ContainsRune(sessionPath, filepath.Separator) {
			candidate := filepath.Join(sessionDir, sessionPath)
			if !strings.HasSuffix(candidate, ".sqlite") && !strings.HasSuffix(candidate, ".db") {
				candidate += ".sqlite"
			}
			sessionPath = candidate
		}

		sqlStorage, rel, err := OpenLockedSessionStorage(ctx, sessionPath)
		if err != nil {
			return nil, err
		}
		dbStorage = sqlStorage
		release = rel
	}

	bundle, err := OpenDurableHarness(ctx, HarnessSetupOptions{
		Cwd:               opts.Cwd,
		AgentDir:          opts.AgentDir,
		Storage:           dbStorage,
		Provider:          opts.Provider,
		Model:             opts.Model,
		Thinking:          opts.Thinking,
		Instructions:      opts.Instructions,
		AllowModelNetwork: opts.AllowModelNetwork,
		Extensions:        extraExtensions,
	})
	if err != nil {
		if release != nil {
			_ = release()
		}
		return nil, err
	}

	if err := bundle.Harness.Resume(); err != nil {
		_ = bundle.Close(hsChordContext(ctx))
		if release != nil {
			_ = release()
		}
		return nil, fmt.Errorf("resume durable harness: %w", err)
	}

	return &DurableAgentRuntime{
		Bundle:      bundle,
		SessionPath: sessionPath,
		SessionDir:  sessionDir,
		releaseLock: release,
	}, nil
}

// SubmitPrompt admits user input to the root conversation.
func (r *DurableAgentRuntime) SubmitPrompt(ctx context.Context, text string, steer bool) (durable.Submission, error) {
	whenBusy := durable.SubmissionWhenBusyFollowUp
	if steer {
		whenBusy = durable.SubmissionWhenBusySteer
	}
	return r.Bundle.Root.Submit(durable.SubmissionDraft{
		RequestId: uuid.NewString(),
		Type:      durable.SubmissionTypeInput,
		Content:   durable.UserInput{Text: &text},
		WhenBusy:  whenBusy,
	}, hsChordContext(ctx))
}

// Abort aborts active tasks on the root conversation.
func (r *DurableAgentRuntime) Abort(ctx context.Context) error {
	return r.Bundle.Root.Abort(hsChordContext(ctx))
}

// Compact triggers manual compaction on the root conversation.
func (r *DurableAgentRuntime) Compact(ctx context.Context, instructions *string) (durable.TaskId, error) {
	return r.Bundle.Root.Compact(instructions, hsChordContext(ctx))
}

// Reset resets the root conversation context with an optional handoff note.
func (r *DurableAgentRuntime) Reset(ctx context.Context, handoff *string) error {
	return r.Bundle.Root.Reset(handoff, hsChordContext(ctx))
}

// Inspect returns a point-in-time snapshot of live durable tasks and submissions.
func (r *DurableAgentRuntime) Inspect(ctx context.Context) (*durable.HarnessInspection, error) {
	return r.Bundle.Harness.Inspect(hsChordContext(ctx))
}

// Usage returns cumulative token/cost usage for the durable harness.
func (r *DurableAgentRuntime) Usage(ctx context.Context) (*durable.UsageState, error) {
	return r.Bundle.Harness.Usage(hsChordContext(ctx))
}

// Close shuts down the durable harness and releases the session file lock cleanly.
func (r *DurableAgentRuntime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	bundle := r.Bundle
	rel := r.releaseLock
	r.releaseLock = nil
	r.mu.Unlock()

	var firstErr error
	if bundle != nil {
		if err := bundle.Close(hsChordContext(ctx)); err != nil {
			firstErr = err
		}
	}
	if rel != nil {
		if err := rel(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RunDurableAgentPrint executes one non-interactive prompt turn and writes the final reply to out.
func RunDurableAgentPrint(ctx context.Context, rt *DurableAgentRuntime, prompt string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	sub, err := rt.SubmitPrompt(ctx, prompt, false)
	if err != nil {
		return err
	}
	chordCtx := hsChordContext(ctx)
	settled, err := sub.Wait(chordCtx)
	if err != nil {
		return err
	}
	if err := rt.Bundle.Harness.WaitForIdle(chordCtx); err != nil {
		return err
	}

	var outputText string
	if settled != nil && settled.Answer != nil {
		view, viewErr := rt.Bundle.Root.Context(chordCtx)
		if viewErr == nil && view != nil {
			for i := len(view.Messages) - 1; i >= 0; i-- {
				if txt := promptExtractMessageText(view.Messages[i]); txt != "" {
					outputText = txt
					break
				}
			}
		}
	}
	if settled != nil && settled.Status != durable.SubmissionStatusDone && outputText == "" {
		reason := string(settled.Status)
		if settled.Reason != nil && *settled.Reason != "" {
			reason = *settled.Reason
		}
		return fmt.Errorf("submission ended with status %s", reason)
	}
	if outputText != "" {
		_, _ = fmt.Fprintln(out, outputText)
	}
	return nil
}
