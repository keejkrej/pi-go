// Ported from packages/coding-agent/src/modes/rpc/rpc-client.ts (pi v1.5.2).

package rpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	rcDefaultRequestTimeout = 30 * time.Second
	rcDefaultIdleTimeout    = 60 * time.Second
	rcStartupProbeDelay     = 100 * time.Millisecond
)

// RpcClientOptions configures an RpcClient child process.
type RpcClientOptions struct {
	// CliPath is the executable path to spawn (defaults to "pi" when empty).
	CliPath string
	// Command optionally overrides the executable and leading arguments before "--mode rpc".
	Command []string
	// Cwd sets the working directory for the agent process.
	Cwd string
	// Env sets additional environment variables on top of os.Environ().
	Env map[string]string
	// Provider appends "--provider <provider>" when non-empty.
	Provider string
	// Model appends "--model <model>" when non-empty.
	Model string
	// Args appends extra CLI arguments.
	Args []string
	// StderrTee mirrors child stderr chunks (defaults to os.Stderr; set io.Discard to silence).
	StderrTee io.Writer
	// RequestTimeout overrides the 30s per-RPC command timeout.
	RequestTimeout time.Duration
}

// RpcPromptOptions configures Prompt and PromptAndWait calls on RpcClient.
type RpcPromptOptions struct {
	Images            []*ai.ImageContent
	StreamingBehavior string // "steer" | "followUp"
	Timeout           time.Duration
}

// RpcEventListener receives streamed JSON session events from the RPC child process.
type RpcEventListener func(event modes.JsonAgentSessionEvent)

type rcEventListenerEntry struct {
	id uint64
	fn RpcEventListener
}

type rcPendingRequest struct {
	ch chan rcPendingResult
}

type rcPendingResult struct {
	resp RpcResponse
	err  error
}

type rcSendHook func(ctx context.Context, cmd RpcCommand) (RpcResponse, error)

// RpcClient spawns a pi process in "--mode rpc" and exposes a typed API for all RPC commands.
type RpcClient struct {
	options RpcClientOptions

	mu              sync.Mutex
	started         bool
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdoutRead      io.ReadCloser
	stderrRead      io.ReadCloser
	stopStdoutRead  func()
	stdoutDone      chan struct{}
	stderrDone      chan struct{}
	processDone     chan struct{}
	closed          atomic.Bool
	exited          bool
	exitError       error
	stderrBuf       bytes.Buffer
	requestId       atomic.Int64
	pendingRequests map[string]rcPendingRequest
	listeners       []rcEventListenerEntry
	nextListenerId  atomic.Uint64

	// sendOverride allows deterministic unit tests without spawning a subprocess.
	sendOverride rcSendHook
}

// NewRpcClient constructs an RpcClient with the supplied options.
func NewRpcClient(opts ...*RpcClientOptions) *RpcClient {
	var options RpcClientOptions
	if len(opts) > 0 && opts[0] != nil {
		options = *opts[0]
	}
	return &RpcClient{
		options:         options,
		pendingRequests: make(map[string]rcPendingRequest),
	}
}

// Start spawns the RPC child process and begins reading stdout events and responses.
func (c *RpcClient) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("Client already started")
	}
	c.started = true
	c.closed.Store(false)
	c.exitError = nil
	c.exited = false
	c.stderrBuf.Reset()
	c.mu.Unlock()

	exe, argv := c.buildLaunchArgs()
	cmd := exec.Command(exe, argv...)
	if c.options.Cwd != "" {
		cmd.Dir = c.options.Cwd
	}
	if len(c.options.Env) > 0 {
		env := os.Environ()
		for k, v := range c.options.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		return err
	}

	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return err
	}

	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()

	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	processDone := make(chan struct{})

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdinW
	c.stdoutRead = stdoutR
	c.stderrRead = stderrR
	c.stdoutDone = stdoutDone
	c.stderrDone = stderrDone
	c.processDone = processDone
	c.mu.Unlock()

	stderrTee := c.options.StderrTee
	if stderrTee == nil {
		stderrTee = os.Stderr
	}

	go func() {
		defer close(stderrDone)
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrR.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				c.mu.Lock()
				_, _ = c.stderrBuf.Write(chunk)
				c.mu.Unlock()
				if stderrTee != nil && stderrTee != io.Discard {
					_, _ = stderrTee.Write(chunk)
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	stopStdout := AttachJsonlLineReader(context.Background(), stdoutR, func(line string) {
		if !c.closed.Load() {
			c.handleLine(line)
		}
	}, func() {
		close(stdoutDone)
	})
	c.mu.Lock()
	c.stopStdoutRead = stopStdout
	c.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		// Wait briefly for stderr pipe to flush before formatting the exit error.
		select {
		case <-stderrDone:
		case <-time.After(50 * time.Millisecond):
		}

		c.mu.Lock()
		isCurrent := c.cmd == cmd
		c.exited = true
		var exitErr error
		if isCurrent {
			exitErr = c.createProcessExitErrorLocked(waitErr)
			c.exitError = exitErr
			c.rejectPendingRequestsLocked(exitErr)
		}
		c.mu.Unlock()
		close(processDone)
	}()

	timer := time.NewTimer(rcStartupProbeDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-processDone:
	case <-ctx.Done():
		_ = c.Stop()
		return ctx.Err()
	}

	c.mu.Lock()
	startupErr := c.exitError
	exited := c.exited
	c.mu.Unlock()
	if exited {
		_ = c.Stop()
		if startupErr == nil {
			startupErr = c.createProcessExitErrorLocked(nil)
		}
		return startupErr
	}
	return nil
}

func (c *RpcClient) buildLaunchArgs() (string, []string) {
	var exe string
	var prefix []string
	if len(c.options.Command) > 0 {
		exe = c.options.Command[0]
		prefix = append(prefix, c.options.Command[1:]...)
	} else {
		exe = c.options.CliPath
		if exe == "" {
			exe = "pi"
		}
	}

	args := append(prefix, "--mode", "rpc")
	if c.options.Provider != "" {
		args = append(args, "--provider", c.options.Provider)
	}
	if c.options.Model != "" {
		args = append(args, "--model", c.options.Model)
	}
	if len(c.options.Args) > 0 {
		args = append(args, c.options.Args...)
	}
	return exe, args
}

// attachTestStreams binds RpcClient directly to in-process stdin/stdout pipes for unit tests.
func (c *RpcClient) attachTestStreams(stdin io.WriteCloser, stdout io.ReadCloser) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return errors.New("Client already started")
	}
	stdoutDone := make(chan struct{})
	c.started = true
	c.stdin = stdin
	c.stdoutRead = stdout
	c.stdoutDone = stdoutDone
	c.closed.Store(false)
	c.exitError = nil
	c.exited = false
	stopStdout := AttachJsonlLineReader(context.Background(), stdout, func(line string) {
		if !c.closed.Load() {
			c.handleLine(line)
		}
	}, func() {
		close(stdoutDone)
	})
	c.stopStdoutRead = stopStdout
	c.mu.Unlock()
	return nil
}

// Stop terminates the child process (SIGTERM followed by SIGKILL after 1s) and cleans up state.
func (c *RpcClient) Stop() error {
	c.mu.Lock()
	started := c.started
	cmd := c.cmd
	stdin := c.stdin
	stdoutRead := c.stdoutRead
	stderrRead := c.stderrRead
	stopStdout := c.stopStdoutRead
	processDone := c.processDone
	stdoutDone := c.stdoutDone
	stderrDone := c.stderrDone
	if !started {
		c.mu.Unlock()
		return nil
	}
	c.started = false
	c.cmd = nil
	c.stdin = nil
	c.stdoutRead = nil
	c.stderrRead = nil
	c.stopStdoutRead = nil
	c.closed.Store(true)
	c.rejectPendingRequestsLocked(errors.New("Client stopped"))
	c.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}

	if processDone != nil {
		timer := time.NewTimer(1 * time.Second)
		select {
		case <-processDone:
			timer.Stop()
		case <-timer.C:
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-processDone
		}
	}

	if stdoutRead != nil {
		_ = stdoutRead.Close()
	}
	if stderrRead != nil {
		_ = stderrRead.Close()
	}
	if stdoutDone != nil {
		<-stdoutDone
	}
	if stderrDone != nil {
		<-stderrDone
	}
	if stopStdout != nil {
		stopStdout()
	}
	return nil
}

// OnEvent subscribes to streamed JSON agent session events. The returned func unsubscribes.
func (c *RpcClient) OnEvent(listener RpcEventListener) func() {
	if listener == nil {
		return func() {}
	}
	id := c.nextListenerId.Add(1)
	c.mu.Lock()
	c.listeners = append(c.listeners, rcEventListenerEntry{id: id, fn: listener})
	c.mu.Unlock()

	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, entry := range c.listeners {
			if entry.id == id {
				c.listeners = append(c.listeners[:i], c.listeners[i+1:]...)
				return
			}
		}
	}
}

// GetStderr returns all stderr output collected from the child process so far.
func (c *RpcClient) GetStderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderrBuf.String()
}

// =========================================================================
// Command Methods
// =========================================================================

// Prompt sends a user prompt to the agent and returns its acceptance disposition.
func (c *RpcClient) Prompt(ctx context.Context, message string, opts ...*RpcPromptOptions) (core.PromptDisposition, error) {
	cmd := &RpcPromptCommand{
		Type:    RpcCmdPrompt,
		Message: message,
	}
	if len(opts) > 0 && opts[0] != nil {
		cmd.Images = opts[0].Images
		if opts[0].StreamingBehavior != "" {
			sb := opts[0].StreamingBehavior
			cmd.StreamingBehavior = &sb
		}
	}
	resp, err := c.send(ctx, cmd)
	if err != nil {
		return "", err
	}
	var data RpcPromptData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return "", err
	}
	return data.Disposition, nil
}

// Steer queues a steering message to interrupt the agent mid-run.
func (c *RpcClient) Steer(ctx context.Context, message string, images ...*ai.ImageContent) (core.QueuedInputDisposition, error) {
	resp, err := c.send(ctx, &RpcSteerCommand{
		Type:    RpcCmdSteer,
		Message: message,
		Images:  images,
	})
	if err != nil {
		return "", err
	}
	var data RpcQueuedInputData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return "", err
	}
	return data.Disposition, nil
}

// FollowUp queues a follow-up message to be processed after the current run finishes.
func (c *RpcClient) FollowUp(ctx context.Context, message string, images ...*ai.ImageContent) (core.QueuedInputDisposition, error) {
	resp, err := c.send(ctx, &RpcFollowUpCommand{
		Type:    RpcCmdFollowUp,
		Message: message,
		Images:  images,
	})
	if err != nil {
		return "", err
	}
	var data RpcQueuedInputData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return "", err
	}
	return data.Disposition, nil
}

// Abort cancels the currently running agent operation.
func (c *RpcClient) Abort(ctx context.Context) error {
	resp, err := c.send(ctx, &RpcAbortCommand{Type: RpcCmdAbort})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// ClearQueue clears queued steering and follow-up messages, returning the cleared text.
func (c *RpcClient) ClearQueue(ctx context.Context) (core.QueuedMessagesText, error) {
	resp, err := c.send(ctx, &RpcClearQueueCommand{Type: RpcCmdClearQueue})
	if err != nil {
		return core.QueuedMessagesText{}, err
	}
	var data core.QueuedMessagesText
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return core.QueuedMessagesText{}, err
	}
	return data, nil
}

// NewSession starts a new session, optionally with parent lineage tracking.
func (c *RpcClient) NewSession(ctx context.Context, parentSession ...string) (RpcCancelledData, error) {
	var parentPtr *string
	if len(parentSession) > 0 && parentSession[0] != "" {
		p := parentSession[0]
		parentPtr = &p
	}
	resp, err := c.send(ctx, &RpcNewSessionCommand{
		Type:          RpcCmdNewSession,
		ParentSession: parentPtr,
	})
	if err != nil {
		return RpcCancelledData{}, err
	}
	var data RpcCancelledData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return RpcCancelledData{}, err
	}
	return data, nil
}

// GetState retrieves the current session state.
func (c *RpcClient) GetState(ctx context.Context) (RpcSessionState, error) {
	resp, err := c.send(ctx, &RpcGetStateCommand{Type: RpcCmdGetState})
	if err != nil {
		return RpcSessionState{}, err
	}
	var data RpcSessionState
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return RpcSessionState{}, err
	}
	return data, nil
}

// SetModel switches the active model by provider and model ID.
func (c *RpcClient) SetModel(ctx context.Context, provider, modelId string) (*ai.Model, error) {
	resp, err := c.send(ctx, &RpcSetModelCommand{
		Type:     RpcCmdSetModel,
		Provider: provider,
		ModelId:  modelId,
	})
	if err != nil {
		return nil, err
	}
	var data ai.Model
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// CycleModel cycles to the next available model. Returns nil when fewer than 2 models exist.
func (c *RpcClient) CycleModel(ctx context.Context) (*core.ModelCycleResult, error) {
	resp, err := c.send(ctx, &RpcCycleModelCommand{Type: RpcCmdCycleModel})
	if err != nil {
		return nil, err
	}
	if err := rcCheckResponseSuccess(resp); err != nil {
		return nil, err
	}
	raw, ok := resp.Data.Get()
	if !ok || raw == nil {
		return nil, nil
	}
	var data core.ModelCycleResult
	if err := jsonx.Decode(raw, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// GetAvailableModels returns the models currently available in the session.
func (c *RpcClient) GetAvailableModels(ctx context.Context) ([]*ai.Model, error) {
	resp, err := c.send(ctx, &RpcGetAvailableModelsCommand{Type: RpcCmdGetAvailableModels})
	if err != nil {
		return nil, err
	}
	var data RpcAvailableModelsData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Models, nil
}

// SetThinkingLevel updates the thinking level for reasoning models.
func (c *RpcClient) SetThinkingLevel(ctx context.Context, level agent.ThinkingLevel) error {
	resp, err := c.send(ctx, &RpcSetThinkingLevelCommand{
		Type:  RpcCmdSetThinkingLevel,
		Level: level,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// CycleThinkingLevel cycles to the next available thinking level. Returns nil when unsupported.
func (c *RpcClient) CycleThinkingLevel(ctx context.Context) (*RpcCycleThinkingLevelData, error) {
	resp, err := c.send(ctx, &RpcCycleThinkingLevelCommand{Type: RpcCmdCycleThinkingLevel})
	if err != nil {
		return nil, err
	}
	if err := rcCheckResponseSuccess(resp); err != nil {
		return nil, err
	}
	raw, ok := resp.Data.Get()
	if !ok || raw == nil {
		return nil, nil
	}
	var data RpcCycleThinkingLevelData
	if err := jsonx.Decode(raw, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// GetAvailableThinkingLevels lists supported thinking levels for the active model.
func (c *RpcClient) GetAvailableThinkingLevels(ctx context.Context) ([]agent.ThinkingLevel, error) {
	resp, err := c.send(ctx, &RpcGetAvailableThinkingLevelsCommand{Type: RpcCmdGetAvailableThinkingLevels})
	if err != nil {
		return nil, err
	}
	var data RpcAvailableThinkingLevelsData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Levels, nil
}

// SetSteeringMode sets how queued steering messages are drained.
func (c *RpcClient) SetSteeringMode(ctx context.Context, mode agent.QueueMode) error {
	resp, err := c.send(ctx, &RpcSetSteeringModeCommand{
		Type: RpcCmdSetSteeringMode,
		Mode: mode,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// SetFollowUpMode sets how queued follow-up messages are drained.
func (c *RpcClient) SetFollowUpMode(ctx context.Context, mode agent.QueueMode) error {
	resp, err := c.send(ctx, &RpcSetFollowUpModeCommand{
		Type: RpcCmdSetFollowUpMode,
		Mode: mode,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// Compact triggers manual context compaction with optional custom instructions.
func (c *RpcClient) Compact(ctx context.Context, customInstructions ...string) (*core.CompactionResult, error) {
	var instrPtr *string
	if len(customInstructions) > 0 && customInstructions[0] != "" {
		v := customInstructions[0]
		instrPtr = &v
	}
	resp, err := c.send(ctx, &RpcCompactCommand{
		Type:               RpcCmdCompact,
		CustomInstructions: instrPtr,
	})
	if err != nil {
		return nil, err
	}
	var data core.CompactionResult
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SetAutoCompaction enables or disables automatic compaction.
func (c *RpcClient) SetAutoCompaction(ctx context.Context, enabled bool) error {
	resp, err := c.send(ctx, &RpcSetAutoCompactionCommand{
		Type:    RpcCmdSetAutoCompaction,
		Enabled: enabled,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// SetAutoRetry enables or disables automatic retry.
func (c *RpcClient) SetAutoRetry(ctx context.Context, enabled bool) error {
	resp, err := c.send(ctx, &RpcSetAutoRetryCommand{
		Type:    RpcCmdSetAutoRetry,
		Enabled: enabled,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// AbortRetry aborts an in-progress retry wait.
func (c *RpcClient) AbortRetry(ctx context.Context) error {
	resp, err := c.send(ctx, &RpcAbortRetryCommand{Type: RpcCmdAbortRetry})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// Bash executes a user bash command on the session.
func (c *RpcClient) Bash(ctx context.Context, command string, excludeFromContext ...bool) (core.BashResult, error) {
	var exclPtr *bool
	if len(excludeFromContext) > 0 {
		v := excludeFromContext[0]
		exclPtr = &v
	}
	resp, err := c.send(ctx, &RpcBashCommand{
		Type:               RpcCmdBash,
		Command:            command,
		ExcludeFromContext: exclPtr,
	})
	if err != nil {
		return core.BashResult{}, err
	}
	var data core.BashResult
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return core.BashResult{}, err
	}
	return data, nil
}

// AbortBash aborts a running user bash command.
func (c *RpcClient) AbortBash(ctx context.Context) error {
	resp, err := c.send(ctx, &RpcAbortBashCommand{Type: RpcCmdAbortBash})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// GetSessionStats retrieves token and cost statistics for the session.
func (c *RpcClient) GetSessionStats(ctx context.Context) (core.SessionStats, error) {
	resp, err := c.send(ctx, &RpcGetSessionStatsCommand{Type: RpcCmdGetSessionStats})
	if err != nil {
		return core.SessionStats{}, err
	}
	var data core.SessionStats
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return core.SessionStats{}, err
	}
	return data, nil
}

// ExportHtml exports the session to HTML and returns the output file path.
func (c *RpcClient) ExportHtml(ctx context.Context, outputPath ...string) (string, error) {
	var outPtr *string
	if len(outputPath) > 0 && outputPath[0] != "" {
		v := outputPath[0]
		outPtr = &v
	}
	resp, err := c.send(ctx, &RpcExportHtmlCommand{
		Type:       RpcCmdExportHtml,
		OutputPath: outPtr,
	})
	if err != nil {
		return "", err
	}
	var data RpcExportHtmlData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return "", err
	}
	return data.Path, nil
}

// SwitchSession loads a different session JSONL file.
func (c *RpcClient) SwitchSession(ctx context.Context, sessionPath string) (RpcCancelledData, error) {
	resp, err := c.send(ctx, &RpcSwitchSessionCommand{
		Type:        RpcCmdSwitchSession,
		SessionPath: sessionPath,
	})
	if err != nil {
		return RpcCancelledData{}, err
	}
	var data RpcCancelledData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return RpcCancelledData{}, err
	}
	return data, nil
}

// Fork creates a new branched session before the given entry ID.
func (c *RpcClient) Fork(ctx context.Context, entryId string) (RpcForkData, error) {
	resp, err := c.send(ctx, &RpcForkCommand{
		Type:    RpcCmdFork,
		EntryId: entryId,
	})
	if err != nil {
		return RpcForkData{}, err
	}
	var data RpcForkData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return RpcForkData{}, err
	}
	return data, nil
}

// Clone clones the current active branch into a new session.
func (c *RpcClient) Clone(ctx context.Context) (RpcCancelledData, error) {
	resp, err := c.send(ctx, &RpcCloneCommand{Type: RpcCmdClone})
	if err != nil {
		return RpcCancelledData{}, err
	}
	var data RpcCancelledData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return RpcCancelledData{}, err
	}
	return data, nil
}

// GetForkMessages lists user messages available as fork targets.
func (c *RpcClient) GetForkMessages(ctx context.Context) ([]core.ForkableUserMessage, error) {
	resp, err := c.send(ctx, &RpcGetForkMessagesCommand{Type: RpcCmdGetForkMessages})
	if err != nil {
		return nil, err
	}
	var data RpcForkMessagesData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Messages, nil
}

// GetEntries retrieves session entries in append order, optionally only those after since.
func (c *RpcClient) GetEntries(ctx context.Context, since ...string) (RpcEntriesData, error) {
	var sincePtr *string
	if len(since) > 0 && since[0] != "" {
		v := since[0]
		sincePtr = &v
	}
	resp, err := c.send(ctx, &RpcGetEntriesCommand{
		Type:  RpcCmdGetEntries,
		Since: sincePtr,
	})
	if err != nil {
		return RpcEntriesData{}, err
	}
	if err := rcCheckResponseSuccess(resp); err != nil {
		return RpcEntriesData{}, err
	}
	raw, ok := resp.Data.Get()
	if !ok || raw == nil {
		return RpcEntriesData{}, errors.New("missing RPC get_entries data")
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return RpcEntriesData{}, errors.New("RPC get_entries data must be a JSON object")
	}
	out := RpcEntriesData{Entries: []core.SessionEntry{}}
	if leaf, ok := obj.GetString("leafId"); ok {
		out.LeafId = &leaf
	}
	if arr, ok := obj.GetArray("entries"); ok {
		out.Entries = make([]core.SessionEntry, 0, len(arr))
		for _, item := range arr {
			if decoded := rtDecodeSessionEntry(item); decoded != nil {
				out.Entries = append(out.Entries, decoded)
			}
		}
	}
	return out, nil
}

// GetTree retrieves the session entry tree and active leaf ID.
func (c *RpcClient) GetTree(ctx context.Context) (RpcTreeData, error) {
	resp, err := c.send(ctx, &RpcGetTreeCommand{Type: RpcCmdGetTree})
	if err != nil {
		return RpcTreeData{}, err
	}
	if err := rcCheckResponseSuccess(resp); err != nil {
		return RpcTreeData{}, err
	}
	raw, ok := resp.Data.Get()
	if !ok || raw == nil {
		return RpcTreeData{}, errors.New("missing RPC get_tree data")
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return RpcTreeData{}, errors.New("RPC get_tree data must be a JSON object")
	}
	out := RpcTreeData{Tree: []core.SessionTreeNode{}}
	if leaf, ok := obj.GetString("leafId"); ok {
		out.LeafId = &leaf
	}
	if arr, ok := obj.GetArray("tree"); ok {
		out.Tree = make([]core.SessionTreeNode, 0, len(arr))
		for _, item := range arr {
			node, err := rtDecodeSessionTreeNode(item)
			if err != nil {
				return RpcTreeData{}, err
			}
			out.Tree = append(out.Tree, node)
		}
	}
	return out, nil
}

// GetLastAssistantText returns the text content of the most recent assistant message, if any.
func (c *RpcClient) GetLastAssistantText(ctx context.Context) (*string, error) {
	resp, err := c.send(ctx, &RpcGetLastAssistantTextCommand{Type: RpcCmdGetLastAssistantText})
	if err != nil {
		return nil, err
	}
	var data RpcLastAssistantTextData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Text, nil
}

// SetSessionName sets the human-readable session display name.
func (c *RpcClient) SetSessionName(ctx context.Context, name string) error {
	resp, err := c.send(ctx, &RpcSetSessionNameCommand{
		Type: RpcCmdSetSessionName,
		Name: name,
	})
	if err != nil {
		return err
	}
	return rcCheckResponseSuccess(resp)
}

// GetMessages returns all messages currently in the session context.
func (c *RpcClient) GetMessages(ctx context.Context) (agent.AgentMessageList, error) {
	resp, err := c.send(ctx, &RpcGetMessagesCommand{Type: RpcCmdGetMessages})
	if err != nil {
		return nil, err
	}
	if err := rcCheckResponseSuccess(resp); err != nil {
		return nil, err
	}
	raw, ok := resp.Data.Get()
	if !ok || raw == nil {
		return nil, errors.New("missing RPC get_messages data")
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, errors.New("RPC get_messages data must be a JSON object")
	}
	arr, _ := obj.GetArray("messages")
	msgs := make(agent.AgentMessageList, 0, len(arr))
	for _, item := range arr {
		decoded, err := rtDecodeSessionAgentMessage(item)
		if err != nil {
			var rawObj *jsonx.Object
			if cast, ok := item.(*jsonx.Object); ok {
				rawObj = cast
			}
			decoded = &agent.UnknownAgentMessage{Raw: rawObj}
		}
		msgs = append(msgs, decoded)
	}
	return msgs, nil
}

// GetCommands lists extension commands, prompt templates, and skills available via prompt.
func (c *RpcClient) GetCommands(ctx context.Context) ([]RpcSlashCommand, error) {
	resp, err := c.send(ctx, &RpcGetCommandsCommand{Type: RpcCmdGetCommands})
	if err != nil {
		return nil, err
	}
	var data RpcCommandsData
	if err := rcDecodeResponseData(resp, &data); err != nil {
		return nil, err
	}
	return data.Commands, nil
}

// =========================================================================
// Helpers
// =========================================================================

// WaitForIdle blocks until an agent_settled event arrives, timeout elapses, or ctx cancels.
func (c *RpcClient) WaitForIdle(ctx context.Context, timeout ...time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	dur := rcDefaultIdleTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		dur = timeout[0]
	}

	settled := make(chan struct{}, 1)
	unsub := c.OnEvent(func(event modes.JsonAgentSessionEvent) {
		if event != nil && event.AgentSessionEventType() == "agent_settled" {
			select {
			case settled <- struct{}{}:
			default:
			}
		}
	})
	defer unsub()

	timer := time.NewTimer(dur)
	defer timer.Stop()

	select {
	case <-settled:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("Timeout waiting for agent to become idle. Stderr: %s", c.GetStderr())
	}
}

// CollectEvents records streamed events until agent_settled is observed.
func (c *RpcClient) CollectEvents(ctx context.Context, timeout ...time.Duration) ([]modes.JsonAgentSessionEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dur := rcDefaultIdleTimeout
	if len(timeout) > 0 && timeout[0] > 0 {
		dur = timeout[0]
	}

	var mu sync.Mutex
	var events []modes.JsonAgentSessionEvent
	done := false
	doneCh := make(chan struct{})

	unsub := c.OnEvent(func(event modes.JsonAgentSessionEvent) {
		if event == nil {
			return
		}
		mu.Lock()
		if !done {
			events = append(events, event)
			if event.AgentSessionEventType() == "agent_settled" {
				done = true
				close(doneCh)
			}
		}
		mu.Unlock()
	})
	defer unsub()

	timer := time.NewTimer(dur)
	defer timer.Stop()

	select {
	case <-doneCh:
		mu.Lock()
		out := append([]modes.JsonAgentSessionEvent(nil), events...)
		mu.Unlock()
		return out, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("Timeout collecting events. Stderr: %s", c.GetStderr())
	}
}

// PromptAndWait subscribes to events, sends prompt, and waits until agent_settled.
func (c *RpcClient) PromptAndWait(ctx context.Context, message string, opts ...*RpcPromptOptions) ([]modes.JsonAgentSessionEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	dur := rcDefaultIdleTimeout
	if len(opts) > 0 && opts[0] != nil && opts[0].Timeout > 0 {
		dur = opts[0].Timeout
	}

	// Start collecting before sending prompt so early events are never missed.
	type collectResult struct {
		events []modes.JsonAgentSessionEvent
		err    error
	}
	collectCtx, cancelCollect := context.WithCancel(ctx)
	defer cancelCollect()

	eventsCh := make(chan collectResult, 1)
	ready := make(chan struct{})
	go func() {
		// Subscribe synchronously before releasing ready by calling OnEvent first inside helper.
		var mu sync.Mutex
		var events []modes.JsonAgentSessionEvent
		done := false
		doneCh := make(chan struct{})

		unsub := c.OnEvent(func(event modes.JsonAgentSessionEvent) {
			if event == nil {
				return
			}
			mu.Lock()
			if !done {
				events = append(events, event)
				if event.AgentSessionEventType() == "agent_settled" {
					done = true
					close(doneCh)
				}
			}
			mu.Unlock()
		})
		close(ready)
		defer unsub()

		timer := time.NewTimer(dur)
		defer timer.Stop()

		select {
		case <-doneCh:
			mu.Lock()
			out := append([]modes.JsonAgentSessionEvent(nil), events...)
			mu.Unlock()
			eventsCh <- collectResult{events: out}
		case <-collectCtx.Done():
			eventsCh <- collectResult{err: collectCtx.Err()}
		case <-timer.C:
			eventsCh <- collectResult{err: fmt.Errorf("Timeout collecting events. Stderr: %s", c.GetStderr())}
		}
	}()
	<-ready

	if _, err := c.Prompt(ctx, message, opts...); err != nil {
		cancelCollect()
		<-eventsCh
		return nil, err
	}

	res := <-eventsCh
	return res.events, res.err
}

// =========================================================================
// Internal
// =========================================================================

func (c *RpcClient) handleLine(line string) {
	val, err := jsonx.Parse([]byte(line))
	if err != nil {
		return
	}
	obj, ok := val.(*jsonx.Object)
	if !ok || obj == nil {
		return
	}

	typ, _ := obj.GetString("type")
	if typ == "response" {
		if id, hasId := obj.GetString("id"); hasId && id != "" {
			var resp RpcResponse
			if err := jsonx.Decode(obj, &resp); err == nil {
				c.mu.Lock()
				pending, exists := c.pendingRequests[id]
				if exists {
					delete(c.pendingRequests, id)
				}
				c.mu.Unlock()
				if exists {
					pending.ch <- rcPendingResult{resp: resp}
					return
				}
			}
		}
	}

	ev, err := DecodeRpcEvent(obj)
	if err != nil || ev == nil {
		return
	}

	c.mu.Lock()
	snapshot := make([]rcEventListenerEntry, len(c.listeners))
	copy(snapshot, c.listeners)
	c.mu.Unlock()

	for _, entry := range snapshot {
		entry.fn(ev)
	}
}

func (c *RpcClient) createProcessExitErrorLocked(waitErr error) error {
	codeStr := "null"
	sigStr := "null"
	stderr := c.stderrBuf.String()

	if waitErr == nil {
		codeStr = "0"
	} else {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				sigStr = rcSignalName(ws.Signal())
			} else {
				codeStr = strconv.Itoa(exitErr.ExitCode())
			}
		} else {
			return fmt.Errorf("Agent process error: %v. Stderr: %s", waitErr, stderr)
		}
	}
	return fmt.Errorf("Agent process exited (code=%s signal=%s). Stderr: %s", codeStr, sigStr, stderr)
}

func rcSignalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	default:
		return fmt.Sprintf("SIG%d", int(sig))
	}
}

func (c *RpcClient) rejectPendingRequestsLocked(err error) {
	for id, pending := range c.pendingRequests {
		delete(c.pendingRequests, id)
		select {
		case pending.ch <- rcPendingResult{err: err}:
		default:
		}
	}
}

func (c *RpcClient) send(ctx context.Context, command RpcCommand) (RpcResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.sendOverride != nil {
		return c.sendOverride(ctx, command)
	}

	c.mu.Lock()
	started := c.started
	stdin := c.stdin
	if !started || stdin == nil {
		c.mu.Unlock()
		return RpcResponse{}, errors.New("Client not started")
	}
	if c.exitError != nil {
		err := c.exitError
		c.mu.Unlock()
		return RpcResponse{}, err
	}
	if c.exited {
		err := c.createProcessExitErrorLocked(nil)
		c.exitError = err
		c.mu.Unlock()
		return RpcResponse{}, err
	}

	id := fmt.Sprintf("req_%d", c.requestId.Add(1))
	command.SetCommandId(&id)
	line, err := SerializeJsonLine(command)
	if err != nil {
		c.mu.Unlock()
		return RpcResponse{}, err
	}

	ch := make(chan rcPendingResult, 1)
	c.pendingRequests[id] = rcPendingRequest{ch: ch}
	c.mu.Unlock()

	if _, writeErr := io.WriteString(stdin, line); writeErr != nil {
		c.mu.Lock()
		delete(c.pendingRequests, id)
		stderr := c.stderrBuf.String()
		if c.exitError == nil {
			c.exitError = fmt.Errorf("Agent process stdin error: %v. Stderr: %s", writeErr, stderr)
		}
		errToReturn := c.exitError
		c.mu.Unlock()
		return RpcResponse{}, errToReturn
	}

	reqTimeout := c.options.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = rcDefaultRequestTimeout
	}
	timer := time.NewTimer(reqTimeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		return res.resp, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		return RpcResponse{}, ctx.Err()
	case <-timer.C:
		c.mu.Lock()
		delete(c.pendingRequests, id)
		stderr := c.stderrBuf.String()
		c.mu.Unlock()
		return RpcResponse{}, fmt.Errorf("Timeout waiting for response to %s. Stderr: %s", command.CommandType(), stderr)
	}
}

func rcCheckResponseSuccess(resp RpcResponse) error {
	if resp.Success {
		return nil
	}
	if resp.Error != nil && *resp.Error != "" {
		return errors.New(*resp.Error)
	}
	return fmt.Errorf("RPC command %s failed", resp.Command)
}

func rcDecodeResponseData(resp RpcResponse, target any) error {
	if err := rcCheckResponseSuccess(resp); err != nil {
		return err
	}
	raw, ok := resp.Data.Get()
	if !ok {
		return fmt.Errorf("missing data in RPC %s response", resp.Command)
	}
	return jsonx.Decode(raw, target)
}
