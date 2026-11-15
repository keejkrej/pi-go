// Ported from packages/durable/test/spec-usage.test.ts (pi v1.0.0).

package durable_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type tsusPaymentInput struct {
	OrderId string  `json:"orderId"`
	Amount  float64 `json:"amount"`
}

type tsusPaymentState struct {
	Phase    string `json:"phase"`
	Reserved bool   `json:"reserved"`
}

type tsusPaymentResult struct {
	Receipt string `json:"receipt"`
}

type tsusPhaseOnlyState struct {
	Phase string `json:"phase"`
}

type tsusSubagentInput struct {
	Task string `json:"task"`
}

type tsusSubagentToolHooks struct{}

func tsusAnswerText(api durable.ToolExecutionApi, answer durable.EntryId, callCtx chord.Context) (string, error) {
	raw, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
		return tx.TypedEntry(durable.AssistantEntry, answer)
	})
	if err != nil {
		return "", err
	}
	entry, ok := raw.(*durable.EntryRecord)
	if !ok || entry == nil || len(entry.Model) == 0 {
		return "", nil
	}
	msg, ok := entry.Model[0].(*ai.AssistantMessage)
	if !ok || msg == nil {
		return "", nil
	}
	var sb strings.Builder
	for _, c := range msg.Content {
		if tc, ok := c.(*ai.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), nil
}

// tsusCompileExamples checks type compatibility for all spec usage snippets without executing them.
func tsusCompileExamples(
	ctx chord.Context,
	runtimeModels ai.Models,
	runtimeEnvFactory func(durable.EnvTarget, chord.Context) (env.ExecutionEnv, error),
) {
	// §7.1 Extensions and host setup
	contextFiles := durable.DefineExtension(&durable.Extension{
		Name: "context-files",
		Sections: []*durable.PromptSection{
			durable.Section("context_files", func(input durable.PromptInput, _ chord.Context) (string, bool, error) {
				cwd := ""
				if input.Env != nil {
					cwd = input.Env.Cwd()
				}
				return "Context from AGENTS.md", cwd != "", nil
			}),
		},
	})

	skills := durable.DefineExtension(&durable.Extension{
		Name: "skills",
		Sections: []*durable.PromptSection{
			durable.SimplePromptSection("skills", "Available skills: code-review, test"),
		},
	})

	skillsV2 := durable.DefineExtension(&durable.Extension{
		Name: "skills",
		Sections: []*durable.PromptSection{
			durable.SimplePromptSection("skills", "Available skills v2: code-review, test, refactor"),
		},
	})

	readTool := tools.CreateReadTool()
	editTool := tools.CreateEditTool()
	writeTool := tools.CreateWriteTool()
	bashTool := tools.CreateBashTool()

	coding := durable.DefineExtension(&durable.Extension{
		Name:  "coding",
		Tools: []*durable.ToolRegistration{readTool, editTool, writeTool, bashTool},
	})

	permissions := durable.DefineExtension(&durable.Extension{
		Name: "permissions",
		Hooks: []durable.HookRegistration{
			durable.Hook(durable.ToolTask, durable.ToolHooks{
				BeforeTool: func(call *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
					if call != nil && call.Name == "bash" {
						var args tools.BashToolInput
						if err := jsonx.Decode(call.Arguments, &args); err == nil && strings.Contains(args.Command, "rm -rf") {
							reason := "destructive command blocked"
							return &durable.BeforeToolDecision{
								Block: &reason,
							}, nil
						}
					}
					return nil, nil
				},
			}),
		},
	})

	reviewer := durable.DefineExtension(&durable.Extension{
		Name:  "reviewer",
		Tools: []*durable.ToolRegistration{readTool},
		Sections: []*durable.PromptSection{
			durable.SimplePromptSection("role", "You are a code reviewer. Report issues clearly."),
		},
	})

	timing := durable.DefineExtension(&durable.Extension{
		Name: "timing",
		Wraps: []durable.Wrap{
			durable.WrapTool(bashTool, func(inner *durable.ToolRegistration) *durable.ToolRegistration {
				cp := *inner
				orig := inner.Execute
				cp.Execute = func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					start := time.Now()
					res, err := orig(args, api, callCtx)
					_ = time.Since(start)
					return res, err
				}
				return &cp
			}),
		},
	})

	venv := durable.DefineExtension(&durable.Extension{
		Name: "venv",
		Tools: []*durable.ToolRegistration{
			tools.CreateBashTool(&tools.BashToolOptions{
				CommandPrefix: "source .venv/bin/activate",
			}),
		},
	})

	// §7.2 PlanModeDoc & PlanMode
	planModeDoc, _ := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.planMode",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryRewindable,
		Fork:    durable.DocumentForkAsOf,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("active", false) },
	})

	planMode := durable.DefineExtension(&durable.Extension{
		Name: "plan-mode",
		Sections: []*durable.PromptSection{
			durable.Section("plan_mode", func(input durable.PromptInput, callCtx chord.Context) (string, bool, error) {
				snap, err := input.Read.Snapshot(callCtx, planModeDoc, input.ConversationId)
				if err != nil || snap == nil {
					return "", false, err
				}
				active, _ := snap.Get("active")
				enabled, _ := active.(bool)
				return "Plan mode is active. Do not mutate files.", enabled, nil
			}),
		},
		Hooks: []durable.HookRegistration{
			durable.Hook(durable.ToolTask, durable.ToolHooks{
				BeforeTool: func(call *ai.ToolCall, api durable.HookApi, callCtx chord.Context) (*durable.BeforeToolDecision, error) {
					snap, err := api.Snapshot(callCtx, planModeDoc, api.ConversationId())
					if err != nil || snap == nil {
						return nil, err
					}
					active, _ := snap.Get("active")
					if enabled, _ := active.(bool); enabled && call != nil && (call.Name == "edit" || call.Name == "write") {
						reason := "edits disabled in plan mode"
						return &durable.BeforeToolDecision{
							Block: &reason,
						}, nil
					}
					return nil, nil
				},
			}),
		},
	})

	// §7.3 Subagent & SubagentTools
	subagentProps := omap.NewMap[string, *typebox.Schema]()
	subagentProps.Set("task", typebox.String())

	var subagent *durable.Extension
	subagent = durable.DefineExtension(&durable.Extension{
		Name: "subagent",
		Tools: []*durable.ToolRegistration{
			durable.DefineTool(&durable.ToolRegistration{
				Name:        "subagent",
				Description: "Run a task in a child conversation",
				Parameters:  typebox.Object(subagentProps),
				Replay:      durable.ToolReplaySafe,
				Execute: func(args any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
					var in tsusSubagentInput
					if err := jsonx.Decode(args, &in); err != nil {
						return nil, err
					}
					rawChild, err := api.Commit(callCtx, func(tx durable.Tx) (any, error) {
						tid := api.TaskId()
						existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &tid}, 1, nil)
						if err != nil {
							return durable.ConversationId(0), err
						}
						if len(existing.Items) > 0 {
							return existing.Items[0].Id, nil
						}
						created, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
							Ownership: durable.ConversationOwnership{
								Kind:   durable.ConversationOwnershipTask,
								TaskId: tid,
							},
						})
						if err != nil {
							return durable.ConversationId(0), err
						}
						if err := durable.Configure(tx, created.Id, durable.AgentChange{
							Extensions: jsonx.Some(durable.AgentExtensionsEdit(nil, []*durable.Extension{subagent})),
						}); err != nil {
							return durable.ConversationId(0), err
						}
						return created.Id, nil
					})
					if err != nil {
						return nil, err
					}
					childId := rawChild.(durable.ConversationId)
					if err := api.Details(jsonx.ObjectOf("conversationId", float64(childId)), callCtx); err != nil {
						return nil, err
					}
					conv, err := api.Conversation(callCtx, childId)
					if err != nil {
						return nil, err
					}
					sub, err := conv.Submit(durable.InputSubmissionDraft{
						RequestId: fmt.Sprintf("subagent:%d", api.TaskId()),
						Content:   ai.UserMessageContent{Text: &in.Task},
					}, callCtx)
					if err != nil {
						return nil, err
					}
					settled, err := sub.Wait(callCtx)
					if err != nil {
						return nil, err
					}
					if settled.Status != durable.SubmissionStatusDone || settled.Answer == nil {
						return &durable.ToolExecutionResult{
							Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: string(settled.Status)}},
							HasContent: true,
							IsError:    true,
						}, nil
					}
					text, err := tsusAnswerText(api, *settled.Answer, callCtx)
					if err != nil {
						return nil, err
					}
					return &durable.ToolExecutionResult{
						Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: text}},
						HasContent: true,
					}, nil
				},
			}),
		},
	})

	subagentTools := durable.DefineExtension(&durable.Extension{
		Name:  "subagent-tools",
		Tools: []*durable.ToolRegistration{readTool},
	})

	// §7.4 Chat
	chat := durable.DefineExtension(&durable.Extension{
		Name:     "chat",
		Sections: []*durable.PromptSection{durable.SimplePromptSection("chat", "Be concise and helpful.")},
	})

	// §5.1 Payment & Follow tasks
	paymentTask := durable.DefineTask(durable.TaskDefinition[tsusPaymentInput, tsusPaymentState, tsusPaymentResult, tsusSubagentToolHooks]{
		Name:    "app.payment",
		Version: 1,
		Initial: func(_ tsusPaymentInput) tsusPaymentState {
			return tsusPaymentState{Phase: "prepare"}
		},
		Phases: map[string]durable.PhaseHandler[tsusPaymentInput, tsusPaymentState, tsusPaymentResult, tsusSubagentToolHooks]{
			"prepare": func(callCtx chord.Context, task durable.RunningTask[tsusPaymentInput, tsusPaymentState], rt *durable.TaskRuntime[tsusPaymentInput, tsusPaymentState, tsusPaymentResult, tsusSubagentToolHooks]) error {
				_, ok, err := rt.Memo(callCtx, "prepare")
				if err != nil {
					return err
				}
				if !ok {
					if _, err := rt.MemoPut(callCtx, "prepare", task.Input.OrderId != ""); err != nil {
						return err
					}
				}
				if task.Input.OrderId == "" {
					return rt.Commit(callCtx, func(_ durable.Tx, _ durable.RunningTask[tsusPaymentInput, tsusPaymentState]) (*durable.NextTaskState[tsusPaymentState, tsusPaymentResult], error) {
						return &durable.NextTaskState[tsusPaymentState, tsusPaymentResult]{
							Status:  durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[tsusPaymentResult]{Status: durable.TaskOutcomeCompleted, Result: tsusPaymentResult{Receipt: "aborted"}, HasRes: true},
						}, nil
					})
				}
				return rt.Commit(callCtx, func(_ durable.Tx, _ durable.RunningTask[tsusPaymentInput, tsusPaymentState]) (*durable.NextTaskState[tsusPaymentState, tsusPaymentResult], error) {
					return &durable.NextTaskState[tsusPaymentState, tsusPaymentResult]{
						Status:     durable.TaskStateRunning,
						Checkpoint: tsusPaymentState{Phase: "charge", Reserved: true},
					}, nil
				})
			},
			"charge": func(callCtx chord.Context, task durable.RunningTask[tsusPaymentInput, tsusPaymentState], rt *durable.TaskRuntime[tsusPaymentInput, tsusPaymentState, tsusPaymentResult, tsusSubagentToolHooks]) error {
				charged, ok, err := rt.Memo(callCtx, "charge")
				if err != nil {
					return err
				}
				if !ok {
					charged, err = rt.MemoPut(callCtx, "charge", fmt.Sprintf("rcpt-%s-%.2f", task.Input.OrderId, task.Input.Amount))
					if err != nil {
						return err
					}
				}
				receipt, _ := charged.(string)
				return rt.Commit(callCtx, func(_ durable.Tx, cur durable.RunningTask[tsusPaymentInput, tsusPaymentState]) (*durable.NextTaskState[tsusPaymentState, tsusPaymentResult], error) {
					return &durable.NextTaskState[tsusPaymentState, tsusPaymentResult]{
						Status:     durable.TaskStateTerminal,
						Checkpoint: cur.Checkpoint,
						Outcome:    &durable.TypedTaskOutcome[tsusPaymentResult]{Status: durable.TaskOutcomeCompleted, Result: tsusPaymentResult{Receipt: receipt}, HasRes: true},
					}, nil
				})
			},
		},
		Abort: func(callCtx chord.Context, _ durable.RunningTask[tsusPaymentInput, tsusPaymentState], rt *durable.TaskRuntime[tsusPaymentInput, tsusPaymentState, tsusPaymentResult, tsusSubagentToolHooks]) error {
			return rt.Commit(callCtx, func(_ durable.Tx, _ durable.RunningTask[tsusPaymentInput, tsusPaymentState]) (*durable.NextTaskState[tsusPaymentState, tsusPaymentResult], error) {
				return &durable.NextTaskState[tsusPaymentState, tsusPaymentResult]{
					Status:  durable.TaskStateTerminal,
					Outcome: &durable.TypedTaskOutcome[tsusPaymentResult]{Status: durable.TaskOutcomeAborted},
				}, nil
			})
		},
	})

	followTask := durable.DefineTask(durable.TaskDefinition[durable.TaskId, tsusPhaseOnlyState, string, tsusSubagentToolHooks]{
		Name:    "app.follow",
		Version: 1,
		Initial: func(_ durable.TaskId) tsusPhaseOnlyState { return tsusPhaseOnlyState{Phase: "follow"} },
		Phases: map[string]durable.PhaseHandler[durable.TaskId, tsusPhaseOnlyState, string, tsusSubagentToolHooks]{
			"follow": func(callCtx chord.Context, task durable.RunningTask[durable.TaskId, tsusPhaseOnlyState], rt *durable.TaskRuntime[durable.TaskId, tsusPhaseOnlyState, string, tsusSubagentToolHooks]) error {
				rec, err := rt.GetTask(callCtx, task.Input)
				if err != nil || rec == nil {
					return err
				}
				out, err := rt.WaitForTask(callCtx, task.Input)
				if err != nil {
					return err
				}
				_ = rt.Sleep(callCtx, rt.Now()+10)
				_, _ = rt.Outcomes(callCtx, []durable.TaskId{task.Input})
				_, _ = rt.Conversation(callCtx, rec.ConversationId)
				return rt.Commit(callCtx, func(_ durable.Tx, cur durable.RunningTask[durable.TaskId, tsusPhaseOnlyState]) (*durable.NextTaskState[tsusPhaseOnlyState, string], error) {
					return &durable.NextTaskState[tsusPhaseOnlyState, string]{
						Status:     durable.TaskStateTerminal,
						Checkpoint: cur.Checkpoint,
						Outcome:    &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: string(out.State.Status), HasRes: true},
					}, nil
				})
			},
		},
	})

	// Registry & harness wiring
	registry := durable.CreateRegistry()
	_ = registry.Install(contextFiles)
	_ = registry.Install(skills)
	_ = registry.Install(coding)
	_ = registry.Install(permissions)
	_ = registry.Install(reviewer)
	_ = registry.Install(timing)
	_ = registry.Install(venv)
	_ = registry.Install(planMode)
	_ = registry.Install(subagent)
	_ = registry.Install(subagentTools)
	_ = registry.Install(chat)
	_ = registry.Install(skillsV2)
	_ = registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "tasks",
		Tasks: []durable.AnyTask{paymentTask, followTask},
	}))

	// containerEnv & liveSettings sequence
	containerDoc, _ := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.container",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject { return jsonx.NewObject() },
	})

	maxRetries := 2
	harness, _ := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   runtimeModels,
		Registry: registry,
		Settings: func() durable.HarnessSettings {
			return durable.HarnessSettings{
				Retry: &durable.ConversationRetryPolicyPatch{MaxRetries: &maxRetries},
			}
		},
		Env: func(target durable.EnvTarget, envCtx chord.Context) (env.ExecutionEnv, error) {
			snap, err := target.Read.Snapshot(envCtx, containerDoc, target.ConversationId)
			if err == nil && snap != nil {
				if rawCwd, ok := snap.Get("cwd"); ok {
					if cwd, ok := rawCwd.(string); ok && cwd != "" {
						return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
					}
				}
			}
			if runtimeEnvFactory != nil {
				return runtimeEnvFactory(target, envCtx)
			}
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{}), nil
		},
	}, ctx)

	// sequences: host, spawn, tableRules, revokedDraft, childInToolCommit
	root, _ := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
		},
	})

	// host: AgentToolsRemove, AgentExtensionsEdit, PlanModeDoc mutation, reload/uninstall
	_ = root.Configure(durable.AgentChange{
		Tools:      jsonx.Some(durable.AgentToolsRemove(writeTool, editTool)),
		Extensions: jsonx.Some(durable.AgentExtensionsEdit([]*durable.Extension{planMode}, []*durable.Extension{reviewer})),
	}, ctx)
	_, _ = root.Commit(ctx, func(tx durable.Tx) (any, error) {
		d, err := tx.Doc(planModeDoc, durable.RootConversationId)
		if err != nil {
			return nil, err
		}
		d.Value.Set("active", true)
		return nil, nil
	})
	_ = registry.Install(skillsV2)
	registry.Uninstall(timing)

	// childInToolCommit / spawn / tableRules / revokedDraft
	anchorTask := durable.DefineTask(durable.TaskDefinition[string, tsusPhaseOnlyState, string, tsusSubagentToolHooks]{
		Name:    "app.anchor",
		Version: 1,
		Initial: func(_ string) tsusPhaseOnlyState { return tsusPhaseOnlyState{Phase: "anchor"} },
		Phases: map[string]durable.PhaseHandler[string, tsusPhaseOnlyState, string, tsusSubagentToolHooks]{
			"anchor": func(callCtx chord.Context, task durable.RunningTask[string, tsusPhaseOnlyState], rt *durable.TaskRuntime[string, tsusPhaseOnlyState, string, tsusSubagentToolHooks]) error {
				var leaked durable.JsonObject
				if err := rt.Commit(callCtx, func(tx durable.Tx, cur durable.RunningTask[string, tsusPhaseOnlyState]) (*durable.NextTaskState[tsusPhaseOnlyState, string], error) {
					tid := rt.TaskId()
					conv, err := tx.Conversation(durable.RootConversationId)
					if err != nil {
						return nil, err
					}
					_ = conv
					child, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
						Ownership: durable.ConversationOwnership{
							Kind:   durable.ConversationOwnershipTask,
							TaskId: tid,
						},
					})
					if err != nil {
						return nil, err
					}
					d, err := tx.Doc(durable.LiveDoc, durable.RootConversationId)
					if err != nil {
						return nil, err
					}
					d.Value.Delete("generation")
					noteKind, _ := durable.DefineEntry("app.note")
					_, _ = tx.AppendEntry(durable.RootConversationId, durable.EntryDraft{
						Kind: noteKind.Kind,
						Data: jsonx.ObjectOf("child", float64(child.Id)),
					})
					_, _ = tx.CreateTask(followTask, tid, durable.TaskOptions{
						Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: tid},
					})
					leaked = d.Value
					return &durable.NextTaskState[tsusPhaseOnlyState, string]{
						Status:     durable.TaskStateTerminal,
						Checkpoint: cur.Checkpoint,
						Outcome:    &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeCompleted, Result: "anchored", HasRes: true},
					}, nil
				}); err != nil {
					return err
				}
				_ = leaked
				return nil
			},
		},
	})
	_ = anchorTask

	_ = harness.Close(ctx)
}

func TestSpecUsage(t *testing.T) {
	v := reflect.ValueOf(tsusCompileExamples)
	if v.Kind() != reflect.Func || v.IsNil() {
		t.Fatal("tsusCompileExamples should be a non-nil compile-checked function")
	}
}
