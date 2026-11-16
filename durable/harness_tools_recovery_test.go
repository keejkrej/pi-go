// Ported from packages/durable/test/harness-tools-recovery.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func thtoOpen(t *testing.T, path string, setup *testkit.ChatSetup, opts ...testkit.OpenChatOptions) (durable.Harness, durable.Conversation) {
	t.Helper()
	st, err := sqlite.OpenNodeSqliteStorage(path)
	if err != nil {
		t.Fatalf("OpenNodeSqliteStorage(%s): %v", path, err)
	}
	h, root, err := testkit.OpenChat(st, setup, opts...)
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if err := h.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	return h, root
}

func thtoTool(name string, execute durable.ToolExecuteFn, mutate ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	reg := &durable.ToolRegistration{
		Name:        name,
		Description: name,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		Execute:     execute,
	}
	for _, fn := range mutate {
		fn(reg)
	}
	return durable.DefineTool(reg)
}

func thtoCall(name string, id string) *ai.AssistantMessage {
	if id == "" {
		id = "c1"
	}
	stop := ai.StopReasonToolUse
	return providers.FauxAssistantMessage(
		providers.FauxAssistantBlocks{providers.FauxToolCall(name, jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &id})},
		&providers.FauxAssistantMessageOptions{StopReason: &stop},
	)
}

func thtoDone() *ai.AssistantMessage {
	return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)
}

func thtoResults(entries []durable.EntryRecord) []*ai.ToolResultMessage {
	var out []*ai.ToolResultMessage
	for i := range entries {
		if durable.ToolResultEntry.Is(&entries[i]) && len(entries[i].Model) > 0 {
			if msg, ok := entries[i].Model[0].(*ai.ToolResultMessage); ok {
				out = append(out, msg)
			}
		}
	}
	return out
}

func thtoResultText(msg *ai.ToolResultMessage) string {
	if msg == nil {
		return ""
	}
	parts := make([]string, 0, len(msg.Content))
	for _, item := range msg.Content {
		if tc, ok := item.(*ai.TextContent); ok {
			parts = append(parts, tc.Text)
		} else {
			parts = append(parts, "")
		}
	}
	return strings.Join(parts, "|")
}

func thtoNewSetup(t *testing.T, opts ...*providers.RegisterFauxProviderOptions) *testkit.ChatSetup {
	t.Helper()
	setup, err := testkit.NewChatSetup(opts...)
	if err != nil {
		t.Fatalf("NewChatSetup: %v", err)
	}
	return setup
}

func thtoLiveSnapshot(t *testing.T, h durable.Harness, cid durable.ConversationId) durable.LiveState {
	t.Helper()
	raw, err := h.Snapshot(nil, durable.LiveDoc, cid)
	if err != nil {
		t.Fatalf("Snapshot(LiveDoc): %v", err)
	}
	if raw == nil {
		return durable.LiveState{}
	}
	encoded, err := jsonx.Encode(raw)
	if err != nil {
		t.Fatalf("encode live snapshot: %v", err)
	}
	var state durable.LiveState
	if err := jsonx.Decode(encoded, &state); err != nil {
		t.Fatalf("decode live snapshot: %v", err)
	}
	return state
}

func thtoToolTaskId(t *testing.T, h durable.Harness) durable.TaskId {
	t.Helper()
	var id durable.TaskId
	if err := testkit.WaitFor(func() (bool, error) {
		snap := thtoLiveSnapshot(t, h, 1)
		if len(snap.Tools) > 0 && snap.Tools[0].TaskId != nil {
			id = *snap.Tools[0].TaskId
			return true, nil
		}
		return false, nil
	}); err != nil {
		t.Fatalf("WaitFor toolTaskId: %v", err)
	}
	return id
}

type thtoBlockingTool struct {
	registration *durable.ToolRegistration
	started      *testkit.Deferred[struct{}]
	mu           sync.Mutex
	runs         int
	blocking     int
}

func thtoNewBlockingTool(name string, mutate ...func(*durable.ToolRegistration)) *thtoBlockingTool {
	bt := &thtoBlockingTool{
		started:  testkit.NewDeferred[struct{}](),
		blocking: 1,
	}
	bt.registration = thtoTool(name, func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
		bt.mu.Lock()
		bt.runs++
		run := bt.runs
		shouldBlock := run <= bt.blocking
		bt.mu.Unlock()
		api.Output(fmt.Sprintf("run %d\n", run))
		if err := api.Details(jsonx.ObjectOf("run", run), callCtx); err != nil {
			return nil, err
		}
		if shouldBlock {
			bt.started.Resolve(struct{}{})
			_ = testkit.Aborted(callCtx.AbortSignal())
		}
		return &durable.ToolExecutionResult{}, nil
	}, mutate...)
	return bt
}

func (b *thtoBlockingTool) runCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runs
}

func thtoSubmitInput(t *testing.T, conv durable.Conversation, text string) durable.Submission {
	t.Helper()
	sub, err := conv.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &text},
	}, nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	return sub
}

func TestHarnessToolRecovery(t *testing.T) {
	t.Run("answers an unsafe tool interrupted after intent with its durable partial output", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		bt := thtoNewBlockingTool("work")
		if _, err := testkit.AddTool(setup.Registry, bt.registration); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})

		h, root := thtoOpen(t, path, setup)
		subId := thtoSubmitInput(t, root, "go").Id()
		if _, err := bt.started.Wait(); err != nil {
			t.Fatalf("started.Wait: %v", err)
		}
		taskId := thtoToolTaskId(t, h)
		if err := h.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h, root = thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		taskRec, err := h.GetTask(nil, taskId)
		if err != nil || taskRec == nil {
			t.Fatalf("GetTask(%d): %v (rec=%v)", taskId, err, taskRec)
		}
		cpRaw, err := jsonx.Encode(taskRec.State.Checkpoint)
		if err != nil {
			t.Fatalf("encode checkpoint: %v", err)
		}
		var cp durable.ToolTaskCheckpoint
		if err := jsonx.Decode(cpRaw, &cp); err != nil {
			t.Fatalf("decode checkpoint: %v", err)
		}
		if cp.Phase != "execute" || cp.Replay != durable.ToolReplayUnsafe {
			t.Fatalf("unexpected checkpoint: %+v", cp)
		}
		sub, err := h.Submission(nil, subId)
		if err != nil || sub == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil {
			t.Fatalf("sub.Wait: %v", err)
		}
		if settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("expected done, got %s", settled.Status)
		}
		if bt.runCount() != 1 {
			t.Fatalf("expected 1 run, got %d", bt.runCount())
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		results := thtoResults(entries)
		if len(results) != 1 {
			t.Fatalf("expected 1 tool result, got %d", len(results))
		}
		res := results[0]
		if !res.IsError {
			t.Fatalf("expected isError=true")
		}
		detVal, ok := res.Details.Get()
		if !ok {
			t.Fatalf("expected details to be set")
		}
		detObj, ok := detVal.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object details, got %T", detVal)
		}
		runVal, _ := detObj.Get("run")
		if fmt.Sprint(runVal) != "1" {
			t.Fatalf("expected details.run=1, got %v", runVal)
		}
		wantText := "run 1\n|<harness>\n[error] Tool work was interrupted and may have partially run\n</harness>"
		if got := thtoResultText(res); got != wantText {
			t.Fatalf("result text mismatch:\ngot:  %q\nwant: %q", got, wantText)
		}
		live := thtoLiveSnapshot(t, h, root.Id())
		if live.Run != nil || live.Generation != nil || len(live.Tools) != 0 || len(live.Compactions) != 0 {
			t.Fatalf("expected empty LiveDoc, got %+v", live)
		}
	})

	t.Run("reruns a tool only when both the stored and the current replay policy are safe", func(t *testing.T) {
		type replayCase struct {
			stored  durable.ToolReplayPolicy
			current durable.ToolReplayPolicy
			reruns  bool
		}
		cases := []replayCase{
			{durable.ToolReplaySafe, durable.ToolReplaySafe, true},
			{durable.ToolReplaySafe, durable.ToolReplayUnsafe, false},
			{durable.ToolReplayUnsafe, durable.ToolReplaySafe, false},
		}
		for _, tc := range cases {
			path := filepath.Join(t.TempDir(), fmt.Sprintf("%s-%s.sqlite", tc.stored, tc.current))
			setup := thtoNewSetup(t)
			bt := thtoNewBlockingTool("work", func(r *durable.ToolRegistration) {
				r.Replay = tc.stored
			})
			reg, err := testkit.AddTool(setup.Registry, bt.registration)
			if err != nil {
				t.Fatalf("AddTool: %v", err)
			}
			setup.Faux.SetResponses([]providers.FauxResponseStep{
				&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
				&providers.FauxResponseMessage{Message: thtoDone()},
			})

			h, root := thtoOpen(t, path, setup)
			subId := thtoSubmitInput(t, root, "go").Id()
			if _, err := bt.started.Wait(); err != nil {
				t.Fatalf("started.Wait: %v", err)
			}
			if err := h.Close(nil); err != nil {
				t.Fatalf("Close 1: %v", err)
			}

			reg.Dispose()
			updated := &durable.ToolRegistration{
				Name:        bt.registration.Name,
				Description: bt.registration.Description,
				Parameters:  bt.registration.Parameters,
				Replay:      tc.current,
				Execute:     bt.registration.Execute,
			}
			if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(updated)); err != nil {
				t.Fatalf("AddTool 2: %v", err)
			}
			h, root = thtoOpen(t, path, setup)
			sub, err := h.Submission(nil, subId)
			if err != nil || sub == nil {
				t.Fatalf("Submission: %v", err)
			}
			settled, err := sub.Wait(nil)
			if err != nil || settled.Status != durable.SubmissionStatusDone {
				t.Fatalf("Wait: err=%v status=%v", err, settled)
			}
			entries, err := testkit.AllEntries(root)
			if err != nil {
				t.Fatalf("AllEntries: %v", err)
			}
			results := thtoResults(entries)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			wantRuns := 1
			if tc.reruns {
				wantRuns = 2
			}
			if bt.runCount() != wantRuns {
				t.Fatalf("case (%s->%s): runs=%d, want %d", tc.stored, tc.current, bt.runCount(), wantRuns)
			}
			if results[0].IsError != !tc.reruns {
				t.Fatalf("case (%s->%s): isError=%v, want %v", tc.stored, tc.current, results[0].IsError, !tc.reruns)
			}
			if tc.reruns && thtoResultText(results[0]) != "run 2\n" {
				t.Fatalf("case (%s->%s): text=%q, want %q", tc.stored, tc.current, thtoResultText(results[0]), "run 2\n")
			}
			if err := h.Close(nil); err != nil {
				t.Fatalf("Close 2: %v", err)
			}
		}
	})

	t.Run("reruns a safe tool with the environment of the conversation's cwd at rerun, and not once it is deselected", func(t *testing.T) {
		for _, change := range []string{"cwd", "deselect"} {
			path := filepath.Join(t.TempDir(), change+".sqlite")
			setup := thtoNewSetup(t)
			var mu sync.Mutex
			var cwds []string
			started := testkit.NewDeferred[struct{}]()
			work := thtoTool("work", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				execEnv, err := api.Env(callCtx)
				if err != nil {
					return nil, err
				}
				var cwd string
				if execEnv != nil {
					cwd = execEnv.Cwd()
				}
				mu.Lock()
				cwds = append(cwds, cwd)
				first := len(cwds) == 1
				mu.Unlock()
				if first {
					started.Resolve(struct{}{})
					_ = testkit.Aborted(callCtx.AbortSignal())
				}
				return &durable.ToolExecutionResult{}, nil
			}, func(r *durable.ToolRegistration) {
				r.Replay = durable.ToolReplaySafe
			})
			if _, err := testkit.AddTool(setup.Registry, work); err != nil {
				t.Fatalf("AddTool: %v", err)
			}
			setup.Faux.SetResponses([]providers.FauxResponseStep{
				&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
				&providers.FauxResponseMessage{Message: thtoDone()},
			})
			envFn := func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
				cwd := "/"
				if target.Cwd != nil {
					cwd = *target.Cwd
				}
				return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
			}

			h, root := thtoOpen(t, path, setup, testkit.OpenChatOptions{EnvFn: envFn})
			if err := root.Configure(durable.AgentChange{Cwd: jsonx.Some("/one")}, nil); err != nil {
				t.Fatalf("Configure /one: %v", err)
			}
			subId := thtoSubmitInput(t, root, "go").Id()
			if _, err := started.Wait(); err != nil {
				t.Fatalf("started.Wait: %v", err)
			}
			if change == "cwd" {
				if err := root.Configure(durable.AgentChange{Cwd: jsonx.Some("/two")}, nil); err != nil {
					t.Fatalf("Configure /two: %v", err)
				}
			} else {
				if err := root.Configure(durable.AgentChange{
					Extensions: jsonx.Some(durable.AgentExtensionsChange{List: []*durable.Extension{}}),
				}, nil); err != nil {
					t.Fatalf("Configure empty extensions: %v", err)
				}
			}
			if err := h.Close(nil); err != nil {
				t.Fatalf("Close 1: %v", err)
			}

			h, root = thtoOpen(t, path, setup, testkit.OpenChatOptions{EnvFn: envFn})
			sub, err := h.Submission(nil, subId)
			if err != nil || sub == nil {
				t.Fatalf("Submission: %v", err)
			}
			settled, err := sub.Wait(nil)
			if err != nil || settled.Status != durable.SubmissionStatusDone {
				t.Fatalf("Wait: err=%v settled=%v", err, settled)
			}
			entries, err := testkit.AllEntries(root)
			if err != nil {
				t.Fatalf("AllEntries: %v", err)
			}
			results := thtoResults(entries)
			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			mu.Lock()
			gotCwds := slices.Clone(cwds)
			mu.Unlock()
			if change == "cwd" {
				if !slices.Equal(gotCwds, []string{"/one", "/two"}) {
					t.Fatalf("expected cwds [/one /two], got %v", gotCwds)
				}
				if results[0].IsError {
					t.Fatalf("expected IsError=false")
				}
			} else {
				if !slices.Equal(gotCwds, []string{"/one"}) {
					t.Fatalf("expected cwds [/one], got %v", gotCwds)
				}
				if !strings.Contains(thtoResultText(results[0]), "was interrupted") {
					t.Fatalf("expected interrupted message, got %q", thtoResultText(results[0]))
				}
			}
			if err := h.Close(nil); err != nil {
				t.Fatalf("Close 2: %v", err)
			}
		}
	})

	t.Run("reruns beforeTool when interrupted before intent, and executes once", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		var mu sync.Mutex
		runs := 0
		if _, err := testkit.AddTool(setup.Registry, thtoTool("work", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			mu.Lock()
			runs++
			mu.Unlock()
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		reached := testkit.NewDeferred[struct{}]()
		asked := 0
		var decisions []string
		if _, err := testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			BeforeTool: func(_ *ai.ToolCall, api durable.HookApi, callCtx chord.Context) (*durable.BeforeToolDecision, error) {
				mu.Lock()
				asked++
				attempt := asked
				mu.Unlock()
				decVal, err := api.MemoPut(callCtx, "test:decision", fmt.Sprintf("attempt %d", attempt))
				if err != nil {
					return nil, err
				}
				decStr, _ := decVal.(string)
				mu.Lock()
				decisions = append(decisions, decStr)
				mu.Unlock()
				if attempt == 1 {
					reached.Resolve(struct{}{})
					_ = testkit.Aborted(callCtx.AbortSignal())
				}
				return nil, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})

		h, root := thtoOpen(t, path, setup)
		subId := thtoSubmitInput(t, root, "go").Id()
		if _, err := reached.Wait(); err != nil {
			t.Fatalf("reached.Wait: %v", err)
		}
		if err := h.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h, _ = thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		sub, err := h.Submission(nil, subId)
		if err != nil || sub == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("Wait: err=%v settled=%v", err, settled)
		}
		mu.Lock()
		gotAsked, gotRuns := asked, runs
		gotDecisions := slices.Clone(decisions)
		mu.Unlock()
		if gotAsked != 2 || gotRuns != 1 {
			t.Fatalf("expected [asked, runs]=[2, 1], got [%d, %d]", gotAsked, gotRuns)
		}
		if !slices.Equal(gotDecisions, []string{"attempt 1", "attempt 1"}) {
			t.Fatalf("expected decisions [attempt 1 attempt 1], got %v", gotDecisions)
		}
	})

	t.Run("reruns the generation tools phase interrupted before its commit", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtoTool("work", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		reached := testkit.NewDeferred[struct{}]()
		var mu sync.Mutex
		observed := 0
		if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			AfterTools: func(_ durable.EntryId, _ []durable.EntryId, _ durable.HookApi, callCtx chord.Context) error {
				mu.Lock()
				observed++
				n := observed
				mu.Unlock()
				if n == 1 {
					reached.Resolve(struct{}{})
					_ = testkit.Aborted(callCtx.AbortSignal())
				}
				return nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})

		h, root := thtoOpen(t, path, setup)
		subId := thtoSubmitInput(t, root, "go").Id()
		if _, err := reached.Wait(); err != nil {
			t.Fatalf("reached.Wait: %v", err)
		}
		if err := h.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h, root = thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		sub, err := h.Submission(nil, subId)
		if err != nil || sub == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("Wait: err=%v settled=%v", err, settled)
		}
		mu.Lock()
		gotObserved := observed
		mu.Unlock()
		if gotObserved != 2 {
			t.Fatalf("expected observed=2, got %d", gotObserved)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		kinds := make([]string, len(entries))
		for i, e := range entries {
			kinds[i] = e.Kind
		}
		wantKinds := []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"}
		if !slices.Equal(kinds, wantKinds) {
			t.Fatalf("entry kinds=%v, want %v", kinds, wantKinds)
		}
	})

	t.Run("answers an aborted tool with its partial output and continues the run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		bt := thtoNewBlockingTool("work")
		if _, err := testkit.AddTool(setup.Registry, bt.registration); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})
		h, root := thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		sub := thtoSubmitInput(t, root, "go")
		if _, err := bt.started.Wait(); err != nil {
			t.Fatalf("started.Wait: %v", err)
		}
		taskId := thtoToolTaskId(t, h)
		abortRes, err := h.AbortTask(nil, taskId)
		if err != nil || abortRes != durable.TaskAbortMarked {
			t.Fatalf("AbortTask: res=%s err=%v", abortRes, err)
		}
		settledTask, err := h.WaitForTask(nil, taskId)
		if err != nil || settledTask.State.Outcome == nil {
			t.Fatalf("WaitForTask: err=%v task=%+v", err, settledTask)
		}
		if settledTask.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("expected aborted outcome, got %+v", settledTask.State.Outcome)
		}
		rawTaskRes, ok := settledTask.State.Outcome.Result.Get()
		if !ok {
			t.Fatalf("expected task outcome result")
		}
		encRes, err := jsonx.Encode(rawTaskRes)
		if err != nil {
			t.Fatalf("encode outcome result: %v", err)
		}
		var toolRes durable.ToolTaskResult
		if err := jsonx.Decode(encRes, &toolRes); err != nil {
			t.Fatalf("decode ToolTaskResult: %v", err)
		}
		if toolRes.EntryId <= 0 {
			t.Fatalf("expected positive entryId in result, got %+v", toolRes)
		}
		settledSub, err := sub.Wait(nil)
		if err != nil || settledSub.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub.Wait: err=%v settled=%v", err, settledSub)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		results := thtoResults(entries)
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		want := "run 1\n|<harness>\n[error] Tool work was aborted\n</harness>"
		if got := thtoResultText(results[0]); got != want {
			t.Fatalf("result text=%q, want %q", got, want)
		}
	})

	t.Run("lets context derivation answer a faulted tool and continues the run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, thtoTool("bad", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{
				HasContent: true,
				HasDetails: true,
				Details:    jsonx.ObjectOf("fn", func() int { return 1 }),
			}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		var mu sync.Mutex
		var requests []string
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("bad", "c1")},
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				found := "none"
				if transcript != nil {
					for _, m := range transcript.Messages {
						if tr, ok := m.(*ai.ToolResultMessage); ok {
							found = thtoResultText(tr)
							break
						}
					}
				}
				mu.Lock()
				requests = append(requests, found)
				mu.Unlock()
				return thtoDone(), nil
			}),
		})
		h, root := thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		settled, err := thtoSubmitInput(t, root, "go").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("Wait: err=%v settled=%v", err, settled)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if len(thtoResults(entries)) != 0 {
			t.Fatalf("expected 0 durable tool results, got %d", len(thtoResults(entries)))
		}
		mu.Lock()
		gotReqs := slices.Clone(requests)
		mu.Unlock()
		wantReqs := []string{"Tool result unavailable: history ends before this call completed."}
		if !slices.Equal(gotReqs, wantReqs) {
			t.Fatalf("requests=%v, want %v", gotReqs, wantReqs)
		}
		var tasksPage durable.Page[durable.TaskRecord, durable.Cursor]
		if err := h.Commit(nil, func(tx durable.Tx) error {
			cid := root.Id()
			var scanErr error
			tasksPage, scanErr = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 20, nil)
			return scanErr
		}); err != nil {
			t.Fatalf("ScanTasks commit: %v", err)
		}
		var faulted *durable.TaskRecord
		for i := range tasksPage.Items {
			if tasksPage.Items[i].Kind == durable.ToolTask.TaskKind() {
				faulted = &tasksPage.Items[i]
				break
			}
		}
		if faulted == nil || faulted.State.Status != durable.TaskStateTerminal || faulted.State.Outcome == nil || faulted.State.Outcome.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("expected faulted tool task, got %+v", faulted)
		}
	})

	t.Run("answers a real bash command interrupted by close and reopen, then finishes the run", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "session.sqlite")
		execEnv := env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir})
		setup := thtoNewSetup(t)
		if _, err := testkit.AddTool(setup.Registry, tools.CreateBashTool()); err != nil {
			t.Fatalf("AddTool(bash): %v", err)
		}
		callId := "b"
		stop := ai.StopReasonToolUse
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{providers.FauxToolCall("bash", jsonx.ObjectOf("command", "echo started; sleep 30"), &providers.FauxToolCallOptions{Id: &callId})},
				&providers.FauxAssistantMessageOptions{StopReason: &stop},
			)},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})

		h, root := thtoOpen(t, path, setup, testkit.OpenChatOptions{Env: execEnv})
		subId := thtoSubmitInput(t, root, "go").Id()
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thtoLiveSnapshot(t, h, root.Id())
			return len(snap.Tools) > 0 && snap.Tools[0].Output != nil && *snap.Tools[0].Output == "started\n", nil
		}); err != nil {
			t.Fatalf("WaitFor bash output: %v", err)
		}
		if err := h.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h, root = thtoOpen(t, path, setup, testkit.OpenChatOptions{Env: execEnv})
		defer func() { _ = h.Close(nil) }()
		sub, err := h.Submission(nil, subId)
		if err != nil || sub == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("Wait: err=%v settled=%v", err, settled)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		results := thtoResults(entries)
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		want := "started\n|<harness>\n[error] Tool bash was interrupted and may have partially run\n</harness>"
		if got := thtoResultText(results[0]); got != want {
			t.Fatalf("result text=%q, want %q", got, want)
		}
	})

	t.Run("clears the interrupted attempt's progress before a safe rerun", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := thtoNewSetup(t)
		started := []*testkit.Deferred[struct{}]{
			testkit.NewDeferred[struct{}](),
			testkit.NewDeferred[struct{}](),
		}
		var mu sync.Mutex
		runs := 0
		work := thtoTool("work", func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			mu.Lock()
			run := runs
			runs++
			mu.Unlock()
			if run == 0 {
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.ToolDiagnosticInfo, Message: "first a"})
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.ToolDiagnosticInfo, Message: "first b"})
				if err := api.Details(jsonx.ObjectOf("run", 1, "extra", true), callCtx); err != nil {
					return nil, err
				}
			} else {
				api.Diagnostic(durable.ToolDiagnostic{Severity: durable.ToolDiagnosticInfo, Message: "second"})
				if err := api.Details(jsonx.ObjectOf("run", 2), callCtx); err != nil {
					return nil, err
				}
			}
			started[run].Resolve(struct{}{})
			_ = testkit.Aborted(callCtx.AbortSignal())
			return &durable.ToolExecutionResult{}, nil
		}, func(r *durable.ToolRegistration) {
			r.Replay = durable.ToolReplaySafe
		})
		if _, err := testkit.AddTool(setup.Registry, work); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thtoCall("work", "c1")},
			&providers.FauxResponseMessage{Message: thtoDone()},
		})

		h, root := thtoOpen(t, path, setup)
		thtoSubmitInput(t, root, "go")
		if _, err := started[0].Wait(); err != nil {
			t.Fatalf("started[0].Wait: %v", err)
		}
		if err := h.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}

		h, root = thtoOpen(t, path, setup)
		defer func() { _ = h.Close(nil) }()
		if _, err := started[1].Wait(); err != nil {
			t.Fatalf("started[1].Wait: %v", err)
		}
		live := thtoLiveSnapshot(t, h, root.Id())
		if len(live.Tools) != 1 {
			t.Fatalf("expected 1 live tool slot, got %d", len(live.Tools))
		}
		slot := live.Tools[0]
		wantDiag := []durable.ToolDiagnostic{{Severity: durable.ToolDiagnosticInfo, Message: "second"}}
		if !slices.Equal(slot.Diagnostics, wantDiag) {
			t.Fatalf("diagnostics=%+v, want %+v", slot.Diagnostics, wantDiag)
		}
		detObj, ok := slot.Details.(*jsonx.Object)
		if !ok {
			t.Fatalf("expected *jsonx.Object slot.Details, got %T (%v)", slot.Details, slot.Details)
		}
		if runVal, hasRun := detObj.Get("run"); !hasRun || detObj.Len() != 1 || fmt.Sprint(runVal) != "2" {
			t.Fatalf("details=%v, want {run: 2}", detObj)
		}
	})
}
