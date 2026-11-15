// Ported from packages/durable/src/tools/bash.ts (pi v1.0.0).

package tools

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/typebox"
)

const tbshMaxTimeoutSeconds = float64(2_147_483_647) / 1000

// BashToolInput defines arguments accepted by the built-in bash tool.
type BashToolInput struct {
	Command string   `json:"command"`
	Timeout *float64 `json:"timeout,omitzero"`
}

// BashExecution describes the resolved command, working directory, and environment before shell execution.
type BashExecution struct {
	Command    string
	Cwd        string
	Env        map[string]string
	InheritEnv bool
}

// BashPrepare customizes a BashExecution immediately before running it.
type BashPrepare func(execution *BashExecution, api durable.ToolExecutionApi, ctx chord.Context) error

// BashToolOptions configures optional prefixing and preparation for CreateBashTool.
type BashToolOptions struct {
	CommandPrefix string
	Prepare       BashPrepare
}

func tbshBashSchema() *typebox.Schema {
	return tidxObjectSchema(
		tidxProp{"command", typebox.String(jsonx.ObjectOf("description", "Bash command to execute"))},
		tidxProp{"timeout", typebox.Optional(typebox.Number(jsonx.ObjectOf("description", "Timeout in seconds (optional, no default timeout)")))},
	)
}

func tbshFormatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func tbshValidateTimeout(timeout *float64) error {
	if timeout == nil {
		return nil
	}
	if math.IsNaN(*timeout) || math.IsInf(*timeout, 0) || *timeout <= 0 {
		return errors.New("Invalid timeout: must be a finite number of seconds")
	}
	if *timeout > tbshMaxTimeoutSeconds {
		return fmt.Errorf("Invalid timeout: maximum is %s seconds", tbshFormatNumber(tbshMaxTimeoutSeconds))
	}
	return nil
}

// CreateBashTool constructs the durable "bash" tool registration.
func CreateBashTool(options ...*BashToolOptions) *durable.ToolRegistration {
	var opts *BashToolOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return durable.DefineTool(&durable.ToolRegistration{
		Name: "bash",
		Description: fmt.Sprintf(
			"Execute a bash command in the current working directory. Returns combined stdout and stderr. Output is truncated to last %d lines or %dKB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.",
			durable.DefaultMaxLines,
			durable.DefaultMaxBytes/1024,
		),
		Parameters: tbshBashSchema(),
		OutputLimits: &durable.ToolOutputLimits{
			Retain: durable.ToolOutputRetainTail,
		},
		Execute: func(args any, api durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
			input, err := tidxDecodeArgs[BashToolInput](args)
			if err != nil {
				return nil, err
			}
			if err := tbshValidateTimeout(input.Timeout); err != nil {
				return nil, err
			}
			executionEnv, err := RequireEnv(api, ctx)
			if err != nil {
				return nil, err
			}

			command := input.Command
			if opts != nil && opts.CommandPrefix != "" {
				command = opts.CommandPrefix + "\n" + command
			}
			execution := BashExecution{
				Command:    command,
				Cwd:        executionEnv.Cwd(),
				Env:        make(map[string]string),
				InheritEnv: true,
			}
			if opts != nil && opts.Prepare != nil {
				if err := opts.Prepare(&execution, api, ctx); err != nil {
					return nil, err
				}
			}

			inheritEnv := execution.InheritEnv
			result := executionEnv.Exec(
				execution.Command,
				&env.ShellExecOptions{
					Cwd:        execution.Cwd,
					Env:        execution.Env,
					InheritEnv: &inheritEnv,
					Timeout:    input.Timeout,
					OnOutput: func(text string, _ chord.Context) error {
						api.Output(text)
						return nil
					},
					Spill: &env.ShellSpillOptions{
						AfterBytes: durable.DefaultMaxBytes,
						AfterLines: durable.DefaultMaxLines,
					},
				},
				ctx,
			)

			var spillPath string
			if result.Ok {
				spillPath = result.Value.SpillPath
			} else if result.Error != nil {
				spillPath = result.Error.SpillPath
			}
			if spillPath != "" {
				code := "full_output"
				api.Diagnostic(durable.ToolDiagnostic{
					Severity: durable.ToolDiagnosticInfo,
					Code:     &code,
					Message:  "Full output: " + spillPath,
				})
			}

			if !result.Ok {
				if result.Error != nil {
					if result.Error.Code == env.ExecutionErrorAborted && tidxIsAborted(ctx) {
						return nil, result.Error
					}
					if result.Error.Code == env.ExecutionErrorTimeout {
						timeoutDisplay := "<nil>"
						if input.Timeout != nil {
							timeoutDisplay = tbshFormatNumber(*input.Timeout)
						}
						return nil, fmt.Errorf("Command timed out after %s seconds", timeoutDisplay)
					}
					if result.Error.Code == env.ExecutionErrorAborted {
						return nil, errors.New("Command aborted")
					}
				}
				return nil, env.ToError(result.Error)
			}
			if result.Value.ExitCode != 0 {
				return nil, fmt.Errorf("Command exited with code %d", result.Value.ExitCode)
			}
			return &durable.ToolExecutionResult{}, nil
		},
	})
}
