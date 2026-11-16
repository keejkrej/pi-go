// Ported from packages/durable/test/harness-live-deltas.test.ts (pi v1.0.0).

package durable_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type thldAction func(api durable.ToolExecutionApi, callCtx chord.Context) error

type thldDriver struct {
	harness durable.Harness
	sub     durable.Submission
	mu      sync.Mutex
	commits [][]chord.Op
	push    func(func(api durable.ToolExecutionApi, callCtx chord.Context) (bool, error))
}

func thldLive(t *testing.T, h durable.Harness) durable.JsonObject {
	t.Helper()
	val, err := h.Snapshot(nil, durable.LiveDoc, durable.ConversationId(1))
	if err != nil {
		t.Fatalf("snapshot LiveDoc: %v", err)
	}
	return val
}

func thldDrive(t *testing.T, limits *durable.ToolOutputLimits) *thldDriver {
	t.Helper()
	setup, err := testkit.NewChatSetup()
	if err != nil {
		t.Fatalf("NewChatSetup: %v", err)
	}

	var mu sync.Mutex
	var actions []func(api durable.ToolExecutionApi, callCtx chord.Context) (bool, error)
	wake := make(chan struct{}, 16)

	if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
		Name:         "drive",
		Description:  "Driven by the test",
		Parameters:   typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		OutputLimits: limits,
		Execute: func(_ any, api durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			for {
				mu.Lock()
				if len(actions) == 0 {
					mu.Unlock()
					var done <-chan struct{}
					if callCtx != nil {
						if sig := callCtx.AbortSignal(); sig != nil {
							done = sig.Done()
						}
					}
					select {
					case <-wake:
						continue
					case <-done:
						return nil, testkit.Aborted(callCtx.AbortSignal())
					}
				}
				fn := actions[0]
				actions = actions[1:]
				mu.Unlock()

				cont, err := fn(api, callCtx)
				if err != nil {
					return nil, err
				}
				if !cont {
					return &durable.ToolExecutionResult{}, nil
				}
			}
		},
	})); err != nil {
		t.Fatalf("AddTool: %v", err)
	}

	setup.Faux.SetResponses([]providers.FauxResponseStep{
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
			providers.FauxToolCall("drive", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("c1")}),
		}, &providers.FauxAssistantMessageOptions{StopReason: thldPtr(ai.StopReasonToolUse)})},
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
			providers.FauxText("done"),
		}, nil)},
	})

	harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}

	drv := &thldDriver{harness: harness}
	harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
		for _, ch := range testkit.DocumentChanges(pub) {
			if ch.Record.Kind == "pi.live" && len(ch.Ops) > 0 {
				drv.mu.Lock()
				drv.commits = append(drv.commits, slices.Clone(ch.Ops))
				drv.mu.Unlock()
			}
		}
	})

	sub, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: thldUserText("go"),
	}, nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	drv.sub = sub

	if err := testkit.WaitFor(func() (bool, error) {
		snap := thldLive(t, harness)
		if snap == nil {
			return false, nil
		}
		tools, ok := snap.Get("tools")
		if !ok || tools == nil {
			return false, nil
		}
		arr, _ := tools.([]any)
		if len(arr) == 0 {
			return false, nil
		}
		obj, _ := arr[0].(*jsonx.Object)
		if obj == nil {
			return false, nil
		}
		st, _ := obj.Get("status")
		return st == "running", nil
	}); err != nil {
		t.Fatalf("wait for running: %v", err)
	}

	drv.push = func(fn func(api durable.ToolExecutionApi, callCtx chord.Context) (bool, error)) {
		mu.Lock()
		actions = append(actions, fn)
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return drv
}

func (d *thldDriver) step(t *testing.T, action thldAction) []chord.Op {
	t.Helper()
	d.mu.Lock()
	before := len(d.commits)
	d.mu.Unlock()

	d.push(func(api durable.ToolExecutionApi, callCtx chord.Context) (bool, error) {
		if err := action(api, callCtx); err != nil {
			return false, err
		}
		return true, nil
	})

	if err := testkit.WaitFor(func() (bool, error) {
		d.mu.Lock()
		n := len(d.commits)
		d.mu.Unlock()
		return n > before, nil
	}); err != nil {
		t.Fatalf("step wait: %v", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.commits[len(d.commits)-1])
}

func (d *thldDriver) finish(t *testing.T) [][]chord.Op {
	t.Helper()
	d.mu.Lock()
	before := len(d.commits)
	d.mu.Unlock()

	d.push(func(_ durable.ToolExecutionApi, _ chord.Context) (bool, error) {
		return false, nil
	})
	if _, err := d.sub.Wait(nil); err != nil {
		t.Fatalf("sub.Wait: %v", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.commits[before:])
}

func thldPathEq(p []chord.Seg, want ...any) bool {
	if len(p) != len(want) {
		return false
	}
	for i := range p {
		switch w := want[i].(type) {
		case string:
			ks, ok := p[i].(chord.SegString)
			if !ok || string(ks) != w {
				return false
			}
		case int:
			in, ok := p[i].(chord.SegNumber)
			if !ok || int(in) != w {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func thldIsOutput(op chord.Op) bool {
	switch o := op.(type) {
	case chord.OpSet:
		return thldPathEq(o.Path, "tools", 0, "output")
	case chord.OpDelete:
		return thldPathEq(o.Path, "tools", 0, "output")
	case chord.OpAppend:
		return thldPathEq(o.Path, "tools", 0, "output")
	case chord.OpTruncate:
		return thldPathEq(o.Path, "tools", 0, "output")
	}
	return false
}

func thldIntPtr(v int) *int         { return &v }
func thldF64Ptr(v float64) *float64 { return &v }

type thldRecordingStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	liveId  durable.DocumentId
	written map[durable.Seq]durable.DocumentContentKind
}

func (s *thldRecordingStorage) setLiveId(id durable.DocumentId) {
	s.mu.Lock()
	s.liveId = id
	s.mu.Unlock()
}

func (s *thldRecordingStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	liveId := s.liveId
	s.mu.Unlock()

	var kind durable.DocumentContentKind
	for _, w := range writes {
		if dc, ok := w.(durable.StorageWriteDocumentChange); ok && liveId != 0 && dc.Id == liveId {
			kind = dc.Content.Kind
		}
	}
	seq, err := s.MemoryStorage.Commit(ctx, writes)
	if err == nil && kind != "" {
		s.mu.Lock()
		s.written[seq] = kind
		s.mu.Unlock()
	}
	return seq, err
}

func TestHarnessLiveDeltas(t *testing.T) {
	t.Run("hands a generation over to its tool round and starts a tool with one field write each", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		_, err = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "noop",
			Description: "noop",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{}, nil
			},
		}))
		if err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("noop", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("c1")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thldPtr(ai.StopReasonToolUse)})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("done"),
			}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		var commits [][]chord.Op
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.live" && len(ch.Ops) > 0 {
					mu.Lock()
					commits = append(commits, slices.Clone(ch.Ops))
					mu.Unlock()
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thldUserText("go"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		var tasksPage durable.Page[durable.TaskRecord, durable.Cursor]
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			cid := root.Id()
			p, err := tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 20, nil)
			tasksPage = p
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
		var genIds []durable.TaskId
		var toolId durable.TaskId
		for _, task := range tasksPage.Items {
			switch task.Kind {
			case "pi.generation":
				genIds = append(genIds, task.Id)
			case "pi.tool":
				toolId = task.Id
			}
		}
		slices.Sort(genIds)
		if len(genIds) != 2 || toolId == 0 {
			t.Fatalf("genIds=%v toolId=%d", genIds, toolId)
		}

		entries, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var resultId durable.EntryId
		for _, e := range entries.Items {
			if e.Kind == "pi.tool-result" {
				resultId = e.Id
				break
			}
		}
		if resultId == 0 {
			t.Fatal("missing tool-result entry")
		}

		mu.Lock()
		gotCommits := slices.Clone(commits)
		mu.Unlock()
		if len(gotCommits) != 8 {
			t.Fatalf("expected 8 pi.live commits, got %d: %#v", len(gotCommits), gotCommits)
		}
		// 0: submission sets run
		if len(gotCommits[0]) != 1 {
			t.Fatalf("commit 0 len = %d: %#v", len(gotCommits[0]), gotCommits[0])
		}
		if s, ok := gotCommits[0][0].(chord.OpSet); !ok || !thldPathEq(s.Path, "run") {
			t.Fatalf("commit 0 mismatch: %#v", gotCommits[0])
		}
		// 1: request sets generation
		if len(gotCommits[1]) != 1 {
			t.Fatalf("commit 1 len = %d: %#v", len(gotCommits[1]), gotCommits[1])
		}
		if s, ok := gotCommits[1][0].(chord.OpSet); !ok || !thldPathEq(s.Path, "generation") {
			t.Fatalf("commit 1 mismatch: %#v", gotCommits[1])
		}
		// 2: generation starts tool round and keeps run
		hasDelGen := false
		hasTools := false
		for _, op := range gotCommits[2] {
			if d, ok := op.(chord.OpDelete); ok && thldPathEq(d.Path, "generation") {
				hasDelGen = true
			}
			if s, ok := op.(chord.OpSet); ok && thldPathEq(s.Path, "tools") {
				expectedTools := []any{jsonx.ObjectOf(
					"callId", "c1",
					"name", "noop",
					"taskId", float64(toolId),
					"status", "pending",
				)}
				if jsonx.DeepEqual(s.Value, expectedTools) {
					hasTools = true
				}
			}
		}
		if len(gotCommits[2]) != 2 || !hasDelGen || !hasTools {
			t.Fatalf("commit 2 mismatch: %#v", gotCommits[2])
		}
		// 3: tool intent sets tools[0].status = running
		if len(gotCommits[3]) != 1 {
			t.Fatalf("commit 3 len = %d: %#v", len(gotCommits[3]), gotCommits[3])
		}
		if s, ok := gotCommits[3][0].(chord.OpSet); !ok || !thldPathEq(s.Path, "tools", 0, "status") || s.Value != "running" {
			t.Fatalf("commit 3 mismatch: %#v", gotCommits[3])
		}
		// 4: tool result sets status=done, entry=resultId
		if len(gotCommits[4]) != 2 {
			t.Fatalf("commit 4 len = %d: %#v", len(gotCommits[4]), gotCommits[4])
		}
		// 5: generation tools phase hands run to next generation
		if len(gotCommits[5]) != 2 {
			t.Fatalf("commit 5 len = %d: %#v", len(gotCommits[5]), gotCommits[5])
		}
		// 6: second generation sets generation
		if len(gotCommits[6]) != 1 {
			t.Fatalf("commit 6 len = %d: %#v", len(gotCommits[6]), gotCommits[6])
		}
		// 7: answer ends run
		if len(gotCommits[7]) != 2 {
			t.Fatalf("commit 7 len = %d: %#v", len(gotCommits[7]), gotCommits[7])
		}
	})

	t.Run("appends head output and then only updates dropped counts once window is full", func(t *testing.T) {
		run := thldDrive(t, &durable.ToolOutputLimits{MaxLines: thldIntPtr(2)})
		defer func() { _ = run.harness.Close(nil) }()

		ops := run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("one\n")
			return nil
		})
		if len(ops) != 1 {
			t.Fatalf("step 1 ops = %#v", ops)
		}
		if s, ok := ops[0].(chord.OpSet); !ok || !thldPathEq(s.Path, "tools", 0, "output") || s.Value != "one\n" {
			t.Fatalf("step 1 op = %#v", ops)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("two\n")
			return nil
		})
		if len(ops) != 1 {
			t.Fatalf("step 2 ops = %#v", ops)
		}
		if a, ok := ops[0].(chord.OpAppend); !ok || !thldPathEq(a.Path, "tools", 0, "output") || a.Text != "two\n" {
			t.Fatalf("step 2 op = %#v", ops)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("three\n")
			return nil
		})
		want3 := []chord.Op{
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedBytes")}, Value: float64(6)},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedLines")}, Value: float64(1)},
		}
		if !reflect.DeepEqual(ops, want3) {
			t.Fatalf("step 3 ops = %#v, want %#v", ops, want3)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("four\n")
			return nil
		})
		want4 := []chord.Op{
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedBytes")}, Value: float64(11)},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedLines")}, Value: float64(2)},
		}
		if !reflect.DeepEqual(ops, want4) {
			t.Fatalf("step 4 ops = %#v, want %#v", ops, want4)
		}

		run.finish(t)
	})

	t.Run("slides a tail window as a front trim plus an append", func(t *testing.T) {
		run := thldDrive(t, &durable.ToolOutputLimits{MaxLines: thldIntPtr(3), Retain: durable.ToolOutputRetainTail})
		defer func() { _ = run.harness.Close(nil) }()

		ops := run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("line 1\nline 2\nline 3\n")
			return nil
		})
		if s, ok := ops[0].(chord.OpSet); !ok || len(ops) != 1 || !thldPathEq(s.Path, "tools", 0, "output") || s.Value != "line 1\nline 2\nline 3\n" {
			t.Fatalf("step 1 = %#v", ops)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("line 4\n")
			return nil
		})
		want2 := []chord.Op{
			chord.OpTruncate{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Count: 7},
			chord.OpAppend{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Text: "line 4\n"},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedBytes")}, Value: float64(7)},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedLines")}, Value: float64(1)},
		}
		if !reflect.DeepEqual(ops, want2) {
			t.Fatalf("step 2 = %#v, want %#v", ops, want2)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("line 5\nline 6\n")
			return nil
		})
		want3 := []chord.Op{
			chord.OpTruncate{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Count: 14},
			chord.OpAppend{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Text: "line 5\nline 6\n"},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedBytes")}, Value: float64(21)},
			chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("droppedLines")}, Value: float64(3)},
		}
		if !reflect.DeepEqual(ops, want3) {
			t.Fatalf("step 3 = %#v, want %#v", ops, want3)
		}

		snap := thldLive(t, run.harness)
		var state durable.LiveState
		if err := jsonx.Decode(snap, &state); err != nil {
			t.Fatal(err)
		}
		if len(state.Tools) == 0 || state.Tools[0].Output == nil || *state.Tools[0].Output != "line 4\nline 5\nline 6\n" {
			t.Fatalf("live tool output = %+v", state.Tools)
		}
		run.finish(t)
	})

	t.Run("writes the whole window when Chord overlap search cannot find shared part", func(t *testing.T) {
		wide := thldDrive(t, &durable.ToolOutputLimits{
			MaxBytes: thldIntPtr(100 * 1024),
			MaxLines: thldIntPtr(1_000_000),
			Retain:   durable.ToolOutputRetainTail,
		})
		line := func(idx int) string {
			return fmt.Sprintf("%010d %s\n", idx, strings.Repeat("x", 989))
		}
		var sb strings.Builder
		for i := 0; i < 100; i++ {
			sb.WriteString(line(i))
		}
		wide.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output(sb.String())
			return nil
		})
		slid := wide.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output(line(100) + line(101) + line(102) + line(103))
			return nil
		})
		var verbs []string
		for _, op := range slid {
			if thldIsOutput(op) {
				verbs = append(verbs, op.Verb())
			}
		}
		if !slices.Equal(verbs, []string{"s"}) {
			t.Fatalf("wide slid output verbs = %v, want [s]", verbs)
		}
		wide.finish(t)
		_ = wide.harness.Close(nil)

		repetitive := thldDrive(t, &durable.ToolOutputLimits{
			MaxLines: thldIntPtr(50),
			Retain:   durable.ToolOutputRetainTail,
		})
		repetitive.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output(strings.Repeat("y\n", 50))
			return nil
		})
		repeated := repetitive.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("z\n")
			return nil
		})
		var repOut []chord.Op
		for _, op := range repeated {
			if thldIsOutput(op) {
				repOut = append(repOut, op)
			}
		}
		wantRep := []chord.Op{
			chord.OpTruncate{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Count: 2},
			chord.OpAppend{Path: chord.NonEmptyPath{chord.SegString("tools"), chord.SegNumber(0), chord.SegString("output")}, Text: "z\n"},
		}
		if !reflect.DeepEqual(repOut, wantRep) {
			t.Fatalf("repetitive output ops = %#v, want %#v", repOut, wantRep)
		}
		repetitive.finish(t)
		_ = repetitive.harness.Close(nil)
	})

	t.Run("diffs details leaf by leaf and appends diagnostics", func(t *testing.T) {
		run := thldDrive(t, nil)
		defer func() { _ = run.harness.Close(nil) }()

		ops := run.step(t, func(api durable.ToolExecutionApi, callCtx chord.Context) error {
			return api.Details(jsonx.ObjectOf("step", 1, "log", "a"), callCtx)
		})
		if len(ops) != 1 {
			t.Fatalf("details 1 ops = %#v", ops)
		}
		s0, ok := ops[0].(chord.OpSet)
		if !ok || !thldPathEq(s0.Path, "tools", 0, "details") || !jsonx.DeepEqual(s0.Value, jsonx.ObjectOf("step", float64(1), "log", "a")) {
			t.Fatalf("details 1 op = %#v", ops[0])
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, callCtx chord.Context) error {
			return api.Details(jsonx.ObjectOf("step", 2, "log", "ab"), callCtx)
		})
		hasStep2, hasLogB := false, false
		for _, op := range ops {
			if s, ok := op.(chord.OpSet); ok && thldPathEq(s.Path, "tools", 0, "details", "step") && s.Value == float64(2) {
				hasStep2 = true
			}
			if a, ok := op.(chord.OpAppend); ok && thldPathEq(a.Path, "tools", 0, "details", "log") && a.Text == "b" {
				hasLogB = true
			}
		}
		if !hasStep2 || !hasLogB {
			t.Fatalf("details 2 ops = %#v", ops)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, callCtx chord.Context) error {
			return api.Details(jsonx.ObjectOf("step", 2), callCtx)
		})
		if len(ops) != 1 {
			t.Fatalf("details 3 ops = %#v", ops)
		}
		if d, ok := ops[0].(chord.OpDelete); !ok || !thldPathEq(d.Path, "tools", 0, "details", "log") {
			t.Fatalf("details 3 op = %#v", ops)
		}

		firstDiag := durable.ToolDiagnostic{Severity: durable.ToolDiagnosticInfo, Message: "first"}
		secondDiag := durable.ToolDiagnostic{Severity: durable.ToolDiagnosticWarn, Message: "second"}
		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Diagnostic(firstDiag)
			return nil
		})
		if len(ops) != 1 {
			t.Fatalf("diag 1 ops = %#v", ops)
		}
		if s, ok := ops[0].(chord.OpSet); !ok || !thldPathEq(s.Path, "tools", 0, "diagnostics") {
			t.Fatalf("diag 1 op = %#v", ops)
		}

		ops = run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Diagnostic(secondDiag)
			return nil
		})
		if len(ops) != 1 {
			t.Fatalf("diag 2 ops = %#v", ops)
		}
		if p, ok := ops[0].(chord.OpSplice); !ok || p.Index != 1 || p.Remove != 0 || len(p.Items) != 1 {
			t.Fatalf("diag 2 op = %#v", ops)
		}

		after := run.finish(t)
		settled := after[0]
		hasDone, hasDelDet, hasDelDiag := false, false, false
		for _, op := range settled {
			if s, ok := op.(chord.OpSet); ok && thldPathEq(s.Path, "tools", 0, "status") && s.Value == "done" {
				hasDone = true
			}
			if d, ok := op.(chord.OpDelete); ok && thldPathEq(d.Path, "tools", 0, "details") {
				hasDelDet = true
			}
			if d, ok := op.(chord.OpDelete); ok && thldPathEq(d.Path, "tools", 0, "diagnostics") {
				hasDelDiag = true
			}
		}
		if !hasDone || !hasDelDet || !hasDelDiag {
			t.Fatalf("settled ops = %#v", settled)
		}
	})

	t.Run("streams partial text as appends", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: thldF64Ptr(400),
			TokenSize:       &providers.FauxTokenSizeOptions{Min: thldIntPtr(4), Max: thldIntPtr(4)},
		})
		if err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText(strings.Repeat("word ", 250)),
			}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		var commits [][]chord.Op
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.live" && len(ch.Ops) > 0 {
					mu.Lock()
					commits = append(commits, slices.Clone(ch.Ops))
					mu.Unlock()
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thldUserText("go"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		got := slices.Clone(commits)
		mu.Unlock()
		if len(got) < 5 {
			t.Fatalf("expected > 4 live commits, got %d", len(got))
		}
		partials := got[2 : len(got)-1]
		if len(partials) <= 2 {
			t.Fatalf("expected > 2 partials, got %d", len(partials))
		}
		if len(partials[0]) != 1 {
			t.Fatalf("first partial ops = %#v", partials[0])
		}
		if s, ok := partials[0][0].(chord.OpSet); !ok || !thldPathEq(s.Path, "generation", "message") {
			t.Fatalf("first partial op = %#v", partials[0][0])
		}
		for _, ops := range partials[1:] {
			if len(ops) != 1 {
				t.Fatalf("partial len = %d: %#v", len(ops), ops)
			}
			if a, ok := ops[0].(chord.OpAppend); !ok || !thldPathEq(a.Path, "generation", "message", "content", 0, "text") {
				t.Fatalf("partial op = %#v", ops[0])
			}
		}
	})

	t.Run("stores a complete base exactly in commits where nothing runs", func(t *testing.T) {
		st := &thldRecordingStorage{
			MemoryStorage: storage.NewMemoryStorage(),
			written:       make(map[durable.Seq]durable.DocumentContentKind),
		}
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first", "second"} {
			toolName := name
			if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
				Name:        toolName,
				Description: toolName,
				Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
				Execute: func(_ any, api durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
					api.Output(toolName + " output\n")
					time.Sleep(150 * time.Millisecond)
					api.Output(toolName + " more\n")
					return &durable.ToolExecutionResult{}, nil
				},
			})); err != nil {
				t.Fatal(err)
			}
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("first", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("a")}),
				providers.FauxToolCall("second", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("b")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thldPtr(ai.StopReasonToolUse)})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("done"),
			}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.ToolExecution = durable.ToolExecutionModeSequential
		})

		harness, root, err := testkit.OpenChat(st, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		values := make(map[durable.Seq]durable.JsonObject)
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind != "pi.live" {
					continue
				}
				st.setLiveId(ch.Record.Id)
				if ch.Value != nil {
					mu.Lock()
					values[pub.Seq] = ch.Value
					mu.Unlock()
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thldUserText("go"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		nothingRuns := func(val durable.JsonObject) bool {
			if gen, ok := val.Get("generation"); ok && gen != nil {
				return false
			}
			if toolsRaw, ok := val.Get("tools"); ok && toolsRaw != nil {
				if arr, isArr := toolsRaw.([]any); isArr {
					for _, item := range arr {
						if obj, _ := item.(*jsonx.Object); obj != nil {
							if s, _ := obj.Get("status"); s == "running" {
								return false
							}
						}
					}
				}
			}
			return true
		}

		st.mu.Lock()
		writtenCopy := make(map[durable.Seq]durable.DocumentContentKind, len(st.written))
		for k, v := range st.written {
			writtenCopy[k] = v
		}
		st.mu.Unlock()

		mu.Lock()
		defer mu.Unlock()
		baseCount := 0
		deltaCount := 0
		for seq, kind := range writtenCopy {
			val := values[seq]
			if val == nil {
				t.Fatalf("missing subscribed live value for seq %d", seq)
			}
			if (kind == durable.DocumentContentBase) != nothingRuns(val) {
				t.Fatalf("seq %d kind=%s nothingRuns=%v val=%v", seq, kind, nothingRuns(val), val)
			}
			if kind == durable.DocumentContentBase {
				baseCount++
			} else {
				deltaCount++
			}
		}
		if baseCount < 5 || deltaCount == 0 {
			t.Fatalf("baseCount=%d deltaCount=%d", baseCount, deltaCount)
		}
	})

	t.Run("starts calls the request did not offer as done and marks a faulted tool slot done without an entry", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "bad",
			Description: "bad",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{
					Details:    func() int { return 1 },
					HasDetails: true,
				}, nil
			},
		})); err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("ghost", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("g")}),
				providers.FauxToolCall("bad", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("b")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thldPtr(ai.StopReasonToolUse)})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("done"),
			}, nil)},
		})

		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		var commits [][]chord.Op
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.live" && len(ch.Ops) > 0 {
					mu.Lock()
					commits = append(commits, slices.Clone(ch.Ops))
					mu.Unlock()
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thldUserText("go"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		entries, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ghostResult durable.EntryId
		for _, e := range entries.Items {
			if e.Kind == "pi.tool-result" {
				ghostResult = e.Id
				break
			}
		}

		mu.Lock()
		gotCommits := slices.Clone(commits)
		mu.Unlock()

		var handover []chord.Op
		hasFaultCleanup := false
		for _, ops := range gotCommits {
			for _, op := range ops {
				if s, ok := op.(chord.OpSet); ok && len(s.Path) > 0 && s.Path[0] == chord.SegString("tools") && len(s.Path) == 1 {
					handover = ops
				}
			}
			if len(ops) == 1 {
				if s, ok := ops[0].(chord.OpSet); ok && thldPathEq(s.Path, "tools", 1, "status") && s.Value == "done" {
					hasFaultCleanup = true
				}
			}
		}
		if handover == nil {
			t.Fatalf("missing tools handover commit in %#v", gotCommits)
		}
		foundSlots := false
		for _, op := range handover {
			if s, ok := op.(chord.OpSet); ok && thldPathEq(s.Path, "tools") {
				arr, _ := s.Value.([]any)
				if len(arr) == 2 {
					s0, _ := arr[0].(*jsonx.Object)
					s1, _ := arr[1].(*jsonx.Object)
					if s0 != nil && s1 != nil {
						c0, _ := s0.Get("callId")
						st0, _ := s0.Get("status")
						e0, _ := s0.Get("entry")
						c1, _ := s1.Get("callId")
						st1, _ := s1.Get("status")
						_, hasTid1 := s1.Get("taskId")
						if c0 == "g" && st0 == "done" && e0 == float64(ghostResult) && c1 == "b" && st1 == "pending" && hasTid1 {
							foundSlots = true
						}
					}
				}
			}
		}
		if !foundSlots || !hasFaultCleanup {
			t.Fatalf("foundSlots=%v hasFaultCleanup=%v commits=%#v", foundSlots, hasFaultCleanup, gotCommits)
		}
	})

	t.Run("commits tool-calling answer, its tool tasks, generation wait, and tool round in one commit", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "noop",
			Description: "noop",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				return &durable.ToolExecutionResult{}, nil
			},
		})); err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("noop", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("a")}),
				providers.FauxToolCall("noop", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thldPtr("b")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thldPtr(ai.StopReasonToolUse)})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("done"),
			}, nil)},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		var handover *durable.CommitPublication
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.live" && ch.Value != nil {
					if toolsRaw, ok := ch.Value.Get("tools"); ok {
						if arr, isArr := toolsRaw.([]any); isArr && len(arr) == 2 {
							mu.Lock()
							if handover == nil {
								p := pub
								handover = &p
							}
							mu.Unlock()
						}
					}
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thldUserText("go"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		ho := handover
		mu.Unlock()
		if ho == nil {
			t.Fatal("missing handover publication")
		}
		var kinds []string
		for _, ch := range ho.Changes {
			switch v := ch.(type) {
			case durable.StorageWriteEntry:
				kinds = append(kinds, v.Value.Kind)
			case durable.StorageWriteTask:
				kinds = append(kinds, v.Value.Kind)
			}
		}
		slices.Sort(kinds)
		want := []string{"pi.assistant", "pi.generation", "pi.tool", "pi.tool"}
		if !slices.Equal(kinds, want) {
			t.Fatalf("handover kinds = %v, want %v", kinds, want)
		}
	})

	t.Run("writes an aborted tool slot with field-level ops", func(t *testing.T) {
		run := thldDrive(t, nil)
		defer func() { _ = run.harness.Close(nil) }()

		run.step(t, func(api durable.ToolExecutionApi, _ chord.Context) error {
			api.Output("partial\n")
			return nil
		})
		run.step(t, func(api durable.ToolExecutionApi, callCtx chord.Context) error {
			return api.Details(jsonx.ObjectOf("n", 1), callCtx)
		})

		var liveState durable.LiveState
		if err := jsonx.Decode(thldLive(t, run.harness), &liveState); err != nil {
			t.Fatal(err)
		}
		taskId := *liveState.Tools[0].TaskId

		run.mu.Lock()
		before := len(run.commits)
		run.mu.Unlock()

		if _, err := run.harness.AbortTask(nil, taskId); err != nil {
			t.Fatal(err)
		}

		isAbortCommit := func(ops []chord.Op) bool {
			for _, op := range ops {
				if s, ok := op.(chord.OpSet); ok && thldPathEq(s.Path, "tools", 0, "status") {
					return true
				}
			}
			return false
		}
		var abortCommit []chord.Op
		if err := testkit.WaitFor(func() (bool, error) {
			run.mu.Lock()
			defer run.mu.Unlock()
			for _, ops := range run.commits[before:] {
				if isAbortCommit(ops) {
					abortCommit = slices.Clone(ops)
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}

		if len(abortCommit) != 4 {
			t.Fatalf("abortCommit len = %d: %#v", len(abortCommit), abortCommit)
		}
		hasStatus, hasEntry, hasDelOut, hasDelDet := false, false, false, false
		for _, op := range abortCommit {
			switch o := op.(type) {
			case chord.OpSet:
				if thldPathEq(o.Path, "tools", 0, "status") && o.Value == "done" {
					hasStatus = true
				}
				if thldPathEq(o.Path, "tools", 0, "entry") {
					if _, isNum := o.Value.(float64); isNum {
						hasEntry = true
					}
				}
			case chord.OpDelete:
				if thldPathEq(o.Path, "tools", 0, "output") {
					hasDelOut = true
				}
				if thldPathEq(o.Path, "tools", 0, "details") {
					hasDelDet = true
				}
			}
		}
		if !hasStatus || !hasEntry || !hasDelOut || !hasDelDet {
			t.Fatalf("abortCommit ops missing expected fields: %#v", abortCommit)
		}
		_ = errors.New("")
	})

	t.Run("keeps a complete base exactly while nothing runs", func(t *testing.T) {
		cw := durable.LiveDoc.Definition.CheckpointWhen
		slot := func(status string) *jsonx.Object {
			return jsonx.ObjectOf("callId", "c", "name", "n", "status", status)
		}
		runObj := jsonx.ObjectOf("taskId", float64(1), "inputs", []any{})
		info := durable.CheckpointInfo{DeltasSinceBase: 1000}

		if !cw(jsonx.NewObject(), nil, info) {
			t.Fatal("empty LiveState should checkpoint")
		}
		if cw(jsonx.ObjectOf("run", runObj, "generation", jsonx.ObjectOf("attempt", float64(1))), nil, info) {
			t.Fatal("running generation should not checkpoint")
		}
		if !cw(jsonx.ObjectOf("run", runObj, "tools", []any{slot("pending"), slot("done")}), nil, info) {
			t.Fatal("pending/done tools should checkpoint")
		}
		if cw(jsonx.ObjectOf("run", runObj, "tools", []any{slot("done"), slot("running")}), nil, info) {
			t.Fatal("running tool should not checkpoint")
		}
		if !cw(jsonx.ObjectOf("run", runObj), nil, info) {
			t.Fatal("idle run without generation or running tool should checkpoint")
		}
	})
}

func thldPtr[T any](v T) *T { return &v }

func thldUserText(text string) ai.UserMessageContent {
	return ai.UserMessageContent{Text: &text}
}
