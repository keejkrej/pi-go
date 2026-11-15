// Ported from packages/coding-agent/src/core/tools/bash.ts (pi v1.0.0).

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

const (
	tbMaxTimeoutMs        float64 = 2_147_483_647
	tbStructuredOutputMax int     = 1024 * 1024
	tbMaxTimeoutSeconds   float64 = tbMaxTimeoutMs / 1000
)

// BashToolSystemPromptContribution holds the system prompt snippet and guidelines for bash.
var BashToolSystemPromptContribution = ToolSystemPromptContribution{
	Snippet:    "Execute bash commands (ls, grep, find, etc.)",
	Guidelines: []string{"You can inspect PI_* environment variables for current model and session details."},
}

// BashToolInput defines parameters for the bash and powershell tools.
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitzero"`
}

// BashToolOutput is the structured result exposed to programmatic callers such as scripts.
type BashToolOutput struct {
	Output          string  `json:"output"`
	Truncated       bool    `json:"truncated"`
	FullOutputPath  *string `json:"full_output_path,omitzero"`
	ExitCode        int     `json:"exit_code"`
	WallTimeSeconds float64 `json:"wall_time_seconds"`
}

// BashToolDetails provides truncation and temp-file metadata for rendered shell results.
type BashToolDetails struct {
	Truncation     *TruncationResult `json:"truncation,omitzero"`
	FullOutputPath *string           `json:"fullOutputPath,omitzero"`
}

// BashOperationsOptions configures a single BashOperations.Exec invocation.
type BashOperationsOptions struct {
	OnData  func(data []byte)
	Signal  context.Context
	Timeout *float64
	Env     []string
}

// BashOperationsResult returns the exit code of a shell execution (nil if unavailable).
type BashOperationsResult struct {
	ExitCode *int `json:"exitCode"`
}

// BashOperations delegates command execution (for example local shells, SSH, or containers).
type BashOperations interface {
	Exec(ctx context.Context, command, cwd string, options BashOperationsOptions) (BashOperationsResult, error)
}

// BashOperationsFunc adapts a function to the BashOperations interface.
type BashOperationsFunc func(ctx context.Context, command, cwd string, options BashOperationsOptions) (BashOperationsResult, error)

// Exec invokes f.
func (f BashOperationsFunc) Exec(ctx context.Context, command, cwd string, options BashOperationsOptions) (BashOperationsResult, error) {
	return f(ctx, command, cwd, options)
}

// BashSpawnContext describes the command, cwd, and environment just before spawning a shell.
type BashSpawnContext struct {
	Command string
	Cwd     string
	Env     []string
}

// BashSpawnHook adjusts the command, working directory, or environment before shell launch.
type BashSpawnHook func(sctx BashSpawnContext) BashSpawnContext

// LocalBashOperationsOptions configures CreateLocalBashOperations.
type LocalBashOperationsOptions struct {
	ShellPath string
}

// BashToolOptions configures CreateBashToolDefinition, CreateBashTool, and CreateShellToolDefinition.
type BashToolOptions struct {
	Operations               BashOperations
	CommandPrefix            string
	ShellPath                string
	ExposeSessionEnvironment *bool
	SpawnHook                BashSpawnHook
}

// ShellToolConfig parameterizes a concrete shell tool (bash vs powershell).
type ShellToolConfig struct {
	Name             string
	Label            string
	ShellName        string
	Prompt           string
	PromptSnippet    string
	PromptGuidelines []string
	TempFilePrefix   string
}

// BashRenderState tracks elapsed runtime state while rendering streaming shell calls.
type BashRenderState = ToolTimerState

type tbShellExitInfo struct {
	ExitCode     int
	AfterSeconds float64
}

type tbShellTimeoutError struct {
	timeoutSeconds float64
	shellExit      *tbShellExitInfo
}

func (e *tbShellTimeoutError) Error() string {
	return fmt.Sprintf("timeout:%s", jsonx.FormatNumber(e.timeoutSeconds))
}

func tbResolveTimeout(timeout *float64) (*time.Duration, error) {
	if timeout == nil {
		return nil, nil
	}
	if math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout <= 0 {
		return nil, errors.New("Invalid timeout: must be a finite number of seconds")
	}
	timeoutMs := *timeout * 1000
	if timeoutMs > tbMaxTimeoutMs {
		return nil, fmt.Errorf("Invalid timeout: maximum is %s seconds", jsonx.FormatNumber(tbMaxTimeoutSeconds))
	}
	d := time.Duration(timeoutMs * float64(time.Millisecond))
	return &d, nil
}

func tbConfigureDetachedProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	attr := &syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	v := reflect.ValueOf(attr).Elem()
	if runtime.GOOS != "windows" {
		if f := v.FieldByName("Setpgid"); f.IsValid() && f.CanSet() && f.Kind() == reflect.Bool {
			f.SetBool(true)
		}
	} else {
		if f := v.FieldByName("HideWindow"); f.IsValid() && f.CanSet() && f.Kind() == reflect.Bool {
			f.SetBool(true)
		}
	}
	cmd.SysProcAttr = attr
}

func tbResolveWaitExitCode(code int, state *os.ProcessState) int {
	if code >= 0 {
		return code
	}
	if state != nil {
		if ws, ok := state.Sys().(execSignaledWaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return 1
}

// CreateLocalShellOperations constructs a BashOperations backed by a local shell binary.
func CreateLocalShellOperations(shellName string, resolveShellConfig func() (utils.ShellConfig, error)) BashOperations {
	return BashOperationsFunc(func(ctx context.Context, command, cwd string, options BashOperationsOptions) (BashOperationsResult, error) {
		timeoutDur, err := tbResolveTimeout(options.Timeout)
		if err != nil {
			return BashOperationsResult{}, err
		}

		execCtx := ctx
		if options.Signal != nil {
			execCtx = options.Signal
		}
		if execCtx == nil {
			execCtx = context.Background()
		}
		if execCtx.Err() != nil {
			return BashOperationsResult{}, errors.New("aborted")
		}

		shellConfig, err := resolveShellConfig()
		if err != nil {
			return BashOperationsResult{}, err
		}
		if _, err := os.Stat(cwd); err != nil {
			return BashOperationsResult{}, fmt.Errorf("Working directory does not exist: %s\nCannot execute %s commands.", cwd, shellName)
		}

		env := options.Env
		if env == nil {
			env = utils.GetShellEnv()
		}

		commandFromStdin := shellConfig.CommandTransport == "stdin"
		args := slices.Clone(shellConfig.Args)
		if !commandFromStdin {
			args = append(args, command)
		}

		cmd := utils.SpawnProcessIn(context.Background(), cwd, env, shellConfig.Shell, args)
		tbConfigureDetachedProcess(cmd)
		if commandFromStdin {
			cmd.Stdin = strings.NewReader(command)
		}

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			return BashOperationsResult{}, err
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			return BashOperationsResult{}, err
		}

		if err := cmd.Start(); err != nil {
			return BashOperationsResult{}, xspawn.VerifyENOENT(cmd, err)
		}

		pid := cmd.Process.Pid
		if pid > 0 {
			utils.TrackDetachedChildPid(pid)
			defer utils.UntrackDetachedChildPid(pid)
		}

		startedAt := time.Now()
		var stateMu sync.Mutex
		var shellExited bool
		var shellExitedAfter time.Duration
		var timedOut bool
		var exitedBeforeDeadlineAfter *time.Duration
		var procState *os.ProcessState

		var timeoutTimer *time.Timer
		if timeoutDur != nil {
			timeoutTimer = time.AfterFunc(*timeoutDur, func() {
				stateMu.Lock()
				timedOut = true
				if shellExited {
					after := shellExitedAfter
					exitedBeforeDeadlineAfter = &after
				}
				stateMu.Unlock()
				if pid > 0 {
					utils.KillProcessTree(pid)
				}
			})
			defer timeoutTimer.Stop()
		}

		stopWatch := make(chan struct{})
		if execCtx.Done() != nil {
			go func() {
				select {
				case <-execCtx.Done():
					if pid > 0 {
						utils.KillProcessTree(pid)
					}
				case <-stopWatch:
				}
			}()
		}

		code, waitErr := utils.WaitForChildStreams(
			func() (int, error) {
				st, wErr := cmd.Process.Wait()
				stateMu.Lock()
				shellExited = true
				shellExitedAfter = time.Since(startedAt)
				procState = st
				stateMu.Unlock()
				if wErr != nil {
					return -1, xspawn.VerifyENOENT(cmd, wErr)
				}
				return st.ExitCode(), nil
			},
			stdoutPipe,
			stderrPipe,
			options.OnData,
			options.OnData,
		)
		close(stopWatch)

		if execCtx.Err() != nil {
			return BashOperationsResult{}, errors.New("aborted")
		}

		stateMu.Lock()
		isTimedOut := timedOut
		st := procState
		afterBeforeDeadline := exitedBeforeDeadlineAfter
		stateMu.Unlock()

		resolvedExitCode := tbResolveWaitExitCode(code, st)

		if isTimedOut {
			var info *tbShellExitInfo
			if afterBeforeDeadline != nil {
				secs := math.Round(afterBeforeDeadline.Seconds()*10) / 10
				info = &tbShellExitInfo{
					ExitCode:     resolvedExitCode,
					AfterSeconds: secs,
				}
			}
			var timeoutSecs float64
			if options.Timeout != nil {
				timeoutSecs = *options.Timeout
			}
			return BashOperationsResult{}, &tbShellTimeoutError{
				timeoutSeconds: timeoutSecs,
				shellExit:      info,
			}
		}

		if waitErr != nil {
			return BashOperationsResult{}, waitErr
		}
		return BashOperationsResult{ExitCode: &resolvedExitCode}, nil
	})
}

// CreateLocalBashOperations creates BashOperations using the default or configured bash binary.
func CreateLocalBashOperations(options ...*LocalBashOperationsOptions) BashOperations {
	var customPath string
	if len(options) > 0 && options[0] != nil {
		customPath = options[0].ShellPath
	}
	return CreateLocalShellOperations("bash", func() (utils.ShellConfig, error) {
		if customPath != "" {
			return utils.GetShellConfig(customPath)
		}
		return utils.GetShellConfig()
	})
}

func tbSetEnv(env []string, key, value string) []string {
	for i, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if ok && (k == key || (runtime.GOOS == "windows" && strings.EqualFold(k, key))) {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

func tbDeleteEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			out = append(out, entry)
			continue
		}
		drop := false
		for _, target := range keys {
			if k == target || (runtime.GOOS == "windows" && strings.EqualFold(k, target)) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

func tbResolveSpawnContext(
	command, cwd string,
	spawnHook BashSpawnHook,
	exposeSessionEnv bool,
	ectx ExtensionToolContext,
) BashSpawnContext {
	env := tbDeleteEnv(utils.GetShellEnv(),
		"PI_SESSION_ID",
		"PI_SESSION_FILE",
		"PI_PROVIDER",
		"PI_MODEL",
		"PI_REASONING_LEVEL",
	)
	if exposeSessionEnv && ectx != nil {
		if sm := ectx.SessionManager(); sm != nil {
			if sid := sm.GetSessionId(); sid != "" {
				env = tbSetEnv(env, "PI_SESSION_ID", sid)
			}
			if sfile := sm.GetSessionFile(); sfile != nil && *sfile != "" {
				env = tbSetEnv(env, "PI_SESSION_FILE", *sfile)
			}
		}
		if m := ectx.Model(); m != nil {
			env = tbSetEnv(env, "PI_PROVIDER", string(m.Provider))
			env = tbSetEnv(env, "PI_MODEL", m.Id)
		}
		if tl := ectx.ThinkingLevel(); tl != "" {
			env = tbSetEnv(env, "PI_REASONING_LEVEL", string(tl))
		}
	}
	base := BashSpawnContext{
		Command: command,
		Cwd:     cwd,
		Env:     env,
	}
	if spawnHook != nil {
		return spawnHook(base)
	}
	return base
}

func tbBashSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"command", typebox.String(jsonx.ObjectOf("description", "Shell command to execute"))},
		tiProp{"timeout", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Timeout in seconds (optional, no default timeout)")))},
	)
}

func tbBashOutputSchema() *typebox.Schema {
	return tiObjectSchema(
		tiProp{"output", typebox.String(jsonx.ObjectOf("description", "Combined stdout and stderr, possibly truncated"))},
		tiProp{"truncated", typebox.Boolean()},
		tiProp{"full_output_path", typebox.Optional(typebox.String(jsonx.ObjectOf("description", "Full output, when truncated")))},
		tiProp{"exit_code", typebox.Number()},
		tiProp{"wall_time_seconds", typebox.Number()},
	)
}

func tbAppendStatus(text, status string) string {
	if text == "" {
		return status
	}
	return text + "\n\n" + status
}

// CreateShellToolDefinition constructs a ToolDefinition for bash or powershell using config.
func CreateShellToolDefinition(cwd string, config ShellToolConfig, options ...*BashToolOptions) ToolDefinition {
	var opt BashToolOptions
	if len(options) > 0 && options[0] != nil {
		opt = *options[0]
	}

	ops := opt.Operations
	if ops == nil {
		ops = CreateLocalBashOperations(&LocalBashOperationsOptions{ShellPath: opt.ShellPath})
	}
	exposeSessionEnv := true
	if opt.ExposeSessionEnvironment != nil {
		exposeSessionEnv = *opt.ExposeSessionEnvironment
	}

	snippet := config.PromptSnippet
	var guidelines []string
	if exposeSessionEnv && len(config.PromptGuidelines) > 0 {
		guidelines = slices.Clone(config.PromptGuidelines)
	}

	def := ToolDefinition{
		Name:                config.Name,
		Label:               config.Label,
		Description:         fmt.Sprintf("Execute a %s command in the current working directory. Returns stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.", config.ShellName, DefaultMaxLines, DefaultMaxBytes/1024),
		PromptSnippet:       &snippet,
		PromptGuidelines:    guidelines,
		Parameters:          tbBashSchema(),
		OutputSchema:        tbBashOutputSchema(),
		ConstrainedSampling: tiStrictPreferJsonSchema(),
		Execute: func(
			ctx context.Context,
			_ string,
			params *jsonx.Object,
			onUpdate agent.AgentToolUpdateCallback,
			ectx ExtensionToolContext,
		) (agent.AgentToolResult, error) {
			var input BashToolInput
			if params != nil {
				if err := jsonx.Decode(params, &input); err != nil {
					return agent.AgentToolResult{}, err
				}
			}

			resolvedCommand := input.Command
			if opt.CommandPrefix != "" {
				resolvedCommand = opt.CommandPrefix + "\n" + input.Command
			}
			spawnCtx := tbResolveSpawnContext(
				resolvedCommand,
				tiResolveToolCwd(ectx, cwd),
				opt.SpawnHook,
				exposeSessionEnv,
				ectx,
			)

			output := NewOutputAccumulator(&OutputAccumulatorOptions{
				TempFilePrefix: config.TempFilePrefix,
			})

			var (
				mu              sync.Mutex
				acceptingOutput = true
				updateTimer     *time.Timer
				updateDirty     bool
				lastUpdateAt    time.Time
			)

			buildPartialResult := func(snapshot OutputSnapshot) agent.AgentToolResult {
				var truncPtr *TruncationResult
				if snapshot.Truncation.Truncated {
					tCopy := snapshot.Truncation
					truncPtr = &tCopy
				}
				var detailsVal any
				if truncPtr != nil || snapshot.FullOutputPath != nil {
					detailsVal = &BashToolDetails{
						Truncation:     truncPtr,
						FullOutputPath: snapshot.FullOutputPath,
					}
				}
				return agent.AgentToolResult{
					Content: ai.ContentList{&ai.TextContent{Type: "text", Text: snapshot.Content}},
					Details: detailsVal,
				}
			}

			clearUpdateTimerLocked := func() {
				if updateTimer != nil {
					updateTimer.Stop()
					updateTimer = nil
				}
			}

			emitOutputUpdate := func() {
				if onUpdate == nil {
					return
				}
				mu.Lock()
				if !updateDirty {
					mu.Unlock()
					return
				}
				updateDirty = false
				lastUpdateAt = time.Now()
				snapshot := output.Snapshot(&SnapshotOptions{PersistIfTruncated: true})
				mu.Unlock()

				onUpdate(buildPartialResult(snapshot))
			}

			scheduleOutputUpdate := func() {
				if onUpdate == nil {
					return
				}
				mu.Lock()
				updateDirty = true
				elapsed := time.Since(lastUpdateAt)
				throttle := time.Duration(BashUpdateThrottleMs) * time.Millisecond
				delay := throttle - elapsed
				if lastUpdateAt.IsZero() || delay <= 0 {
					clearUpdateTimerLocked()
					mu.Unlock()
					emitOutputUpdate()
					return
				}
				if updateTimer == nil {
					updateTimer = time.AfterFunc(delay, func() {
						mu.Lock()
						updateTimer = nil
						mu.Unlock()
						emitOutputUpdate()
					})
				}
				mu.Unlock()
			}

			if onUpdate != nil {
				onUpdate(agent.AgentToolResult{
					Content: ai.ContentList{},
					Details: nil,
				})
			}

			handleData := func(data []byte) {
				mu.Lock()
				if !acceptingOutput {
					mu.Unlock()
					return
				}
				_ = output.Append(data)
				mu.Unlock()
				scheduleOutputUpdate()
			}

			finishOutput := func() OutputSnapshot {
				mu.Lock()
				acceptingOutput = false
				output.Finish()
				clearUpdateTimerLocked()
				mu.Unlock()

				emitOutputUpdate()

				mu.Lock()
				snapshot := output.Snapshot(&SnapshotOptions{PersistIfTruncated: true})
				_ = output.CloseTempFile()
				mu.Unlock()
				return snapshot
			}

			formatOutput := func(snapshot OutputSnapshot, emptyText string) (string, *BashToolDetails) {
				trunc := snapshot.Truncation
				text := snapshot.Content
				if text == "" {
					text = emptyText
				}
				var details *BashToolDetails
				if trunc.Truncated {
					tCopy := trunc
					details = &BashToolDetails{
						Truncation:     &tCopy,
						FullOutputPath: snapshot.FullOutputPath,
					}
					fullPath := ""
					if snapshot.FullOutputPath != nil {
						fullPath = *snapshot.FullOutputPath
					}
					startLine := trunc.TotalLines - trunc.OutputLines + 1
					endLine := trunc.TotalLines
					if trunc.LastLinePartial {
						lastLineSize := FormatSize(output.GetLastLineBytes())
						text += fmt.Sprintf(
							"\n\n[Showing last %s of line %d (line is %s). Full output: %s]",
							FormatSize(trunc.OutputBytes),
							endLine,
							lastLineSize,
							fullPath,
						)
					} else if trunc.TruncatedBy.OrElse("") == "lines" {
						text += fmt.Sprintf(
							"\n\n[Showing lines %d-%d of %d. Full output: %s]",
							startLine,
							endLine,
							trunc.TotalLines,
							fullPath,
						)
					} else {
						text += fmt.Sprintf(
							"\n\n[Showing lines %d-%d of %d (%s limit). Full output: %s]",
							startLine,
							endLine,
							trunc.TotalLines,
							FormatSize(DefaultMaxBytes),
							fullPath,
						)
					}
				}
				return text, details
			}

			defer func() {
				mu.Lock()
				clearUpdateTimerLocked()
				mu.Unlock()
			}()

			startedAt := time.Now()
			execRes, err := ops.Exec(ctx, spawnCtx.Command, spawnCtx.Cwd, BashOperationsOptions{
				OnData:  handleData,
				Signal:  ctx,
				Timeout: input.Timeout,
				Env:     spawnCtx.Env,
			})
			if err != nil {
				snapshot := finishOutput()
				text, _ := formatOutput(snapshot, "")
				if err.Error() == "aborted" {
					return agent.AgentToolResult{}, errors.New(tbAppendStatus(text, "Command aborted"))
				}
				if strings.HasPrefix(err.Error(), "timeout:") {
					timeoutSecs := strings.TrimPrefix(err.Error(), "timeout:")
					status := fmt.Sprintf("Command timed out after %s seconds", timeoutSecs)
					var timeoutErr *tbShellTimeoutError
					if errors.As(err, &timeoutErr) && timeoutErr.shellExit != nil {
						status += fmt.Sprintf(
							". The shell exited with code %d after %s seconds, but background processes it started kept writing output until the timeout.",
							timeoutErr.shellExit.ExitCode,
							jsonx.FormatNumber(timeoutErr.shellExit.AfterSeconds),
						)
					}
					return agent.AgentToolResult{}, errors.New(tbAppendStatus(text, status))
				}
				return agent.AgentToolResult{}, err
			}

			snapshot := finishOutput()
			outputText, details := formatOutput(snapshot, "(no output)")
			if execRes.ExitCode == nil {
				return agent.AgentToolResult{}, errors.New(tbAppendStatus(outputText, "Command terminated without an exit code"))
			}
			exitCode := *execRes.ExitCode
			wallTimeSeconds := math.Round(time.Since(startedAt).Seconds()*10) / 10
			fullOutput, err := output.ReadFullOutput(tbStructuredOutputMax)
			if err != nil {
				fullOutput = FullOutput{}
			}
			var structuredFullPath *string
			if fullOutput.Truncated && snapshot.FullOutputPath != nil {
				structuredFullPath = snapshot.FullOutputPath
			}
			structured := BashToolOutput{
				Output:          fullOutput.Content,
				Truncated:       fullOutput.Truncated,
				FullOutputPath:  structuredFullPath,
				ExitCode:        exitCode,
				WallTimeSeconds: wallTimeSeconds,
			}

			var detailsVal any
			if details != nil {
				detailsVal = details
			}
			if exitCode != 0 {
				isErr := true
				return agent.AgentToolResult{
					Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: tbAppendStatus(outputText, fmt.Sprintf("Command exited with code %d", exitCode))}},
					Details:           detailsVal,
					StructuredContent: structured,
					IsError:           &isErr,
				}, nil
			}
			return agent.AgentToolResult{
				Content:           ai.ContentList{&ai.TextContent{Type: "text", Text: outputText}},
				Details:           detailsVal,
				StructuredContent: structured,
			}, nil
		},
	}
	tiApplyToolRenderers(&def, CreateShellRenderers(config.Prompt))
	return def
}

var tbBashToolConfig = ShellToolConfig{
	Name:             "bash",
	Label:            "bash",
	ShellName:        "bash",
	Prompt:           "$",
	PromptSnippet:    BashToolSystemPromptContribution.Snippet,
	PromptGuidelines: BashToolSystemPromptContribution.Guidelines,
	TempFilePrefix:   "pi-bash",
}

// CreateBashToolDefinition creates the ToolDefinition for the built-in bash tool.
func CreateBashToolDefinition(cwd string, options ...*BashToolOptions) ToolDefinition {
	return CreateShellToolDefinition(cwd, tbBashToolConfig, options...)
}

// CreateBashTool wraps CreateBashToolDefinition as an executable *agent.AgentTool.
func CreateBashTool(cwd string, options ...*BashToolOptions) *agent.AgentTool {
	return WrapToolDefinition(CreateBashToolDefinition(cwd, options...))
}
