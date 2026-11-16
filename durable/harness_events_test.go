// Ported from packages/durable/test/harness-events.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
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

type thevRecorder struct {
	stream  durable.AgentEventStream
	mu      sync.Mutex
	batches [][]durable.AgentEvent
}

func (r *thevRecorder) events() []durable.AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []durable.AgentEvent
	for _, b := range r.batches {
		out = append(out, b...)
	}
	return out
}

func (r *thevRecorder) batchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func (r *thevRecorder) snapshotBatches() [][]durable.AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]durable.AgentEvent, len(r.batches))
	for i, b := range r.batches {
		out[i] = slices.Clone(b)
	}
	return out
}

func thevListen(t *testing.T, h durable.Harness, conv durable.Conversation) *thevRecorder {
	t.Helper()
	stream, err := durable.WatchEvents(h, conv.Id(), nil)
	if err != nil {
		t.Fatalf("WatchEvents: %v", err)
	}
	rec := &thevRecorder{stream: stream}
	if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
		rec.mu.Lock()
		rec.batches = append(rec.batches, slices.Clone(events))
		rec.mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("stream.Start: %v", err)
	}
	return rec
}

func thevSlowSetup(t *testing.T, tps float64) *testkit.ChatSetup {
	t.Helper()
	one := 1
	setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
		TokensPerSecond: &tps,
		TokenSize:       &providers.FauxTokenSizeOptions{Min: &one, Max: &one},
	})
	if err != nil {
		t.Fatalf("NewChatSetup: %v", err)
	}
	return setup
}

func thevCloneAssistant(t *testing.T, msg *ai.AssistantMessage) *ai.AssistantMessage {
	t.Helper()
	raw, err := jsonx.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal AssistantMessage: %v", err)
	}
	var out ai.AssistantMessage
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal AssistantMessage: %v", err)
	}
	return &out
}

func thevCloneContent(t *testing.T, blk ai.Content) ai.Content {
	t.Helper()
	enc, err := jsonx.Encode(blk)
	if err != nil {
		t.Fatalf("encode content: %v", err)
	}
	decoded, err := ai.DecodeContent(enc)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	return decoded
}

func thevStreamedText(events []durable.AgentEvent) string {
	text := ""
	for _, ev := range events {
		switch e := ev.(type) {
		case *durable.MessageStartEvent:
			if am, ok := e.Message.(*ai.AssistantMessage); ok {
				if len(am.Content) > 0 {
					if tc, ok := am.Content[0].(*ai.TextContent); ok {
						text = tc.Text
					} else {
						text = ""
					}
				} else {
					text = ""
				}
			}
		case *durable.MessageUpdateEvent:
			for _, change := range e.Changes {
				if change.Type == "text_start" && change.ContentIndex != nil && *change.ContentIndex == 0 {
					if tc, ok := change.Block.(*ai.TextContent); ok {
						text = tc.Text
					}
				}
				if change.Type == "text_delta" && change.ContentIndex != nil && *change.ContentIndex == 0 && change.Delta != nil {
					text += *change.Delta
				}
			}
		}
	}
	return text
}

func thevApplyChanges(t *testing.T, msg *ai.AssistantMessage, changes []durable.MessageChange) *ai.AssistantMessage {
	t.Helper()
	next := thevCloneAssistant(t, msg)
	for _, change := range changes {
		switch change.Type {
		case "message":
			next = thevCloneAssistant(t, change.Message)
		case "block":
			idx := *change.ContentIndex
			next.Content[idx] = thevCloneContent(t, change.Block)
		case "text_start", "thinking_start", "toolcall_start":
			idx := *change.ContentIndex
			next.Content = slices.Insert(next.Content, idx, thevCloneContent(t, change.Block))
		case "text_delta":
			idx := *change.ContentIndex
			tc := next.Content[idx].(*ai.TextContent)
			tc.Text += *change.Delta
		case "thinking_delta":
			idx := *change.ContentIndex
			tc := next.Content[idx].(*ai.ThinkingContent)
			tc.Thinking += *change.Delta
		case "toolcall_delta":
			idx := *change.ContentIndex
			tc := next.Content[idx].(*ai.ToolCall)
			var cur durable.JsonValue = tc.Arguments
			for i := 0; i < len(change.Path)-1; i++ {
				seg := change.Path[i]
				if s, ok := seg.(chord.SegString); ok {
					v, _ := cur.(*jsonx.Object).Get(string(s))
					cur = v
				} else if n, ok := seg.(chord.SegNumber); ok {
					cur = cur.([]any)[int(n)]
				}
			}
			last := change.Path[len(change.Path)-1]
			if s, ok := last.(chord.SegString); ok {
				obj := cur.(*jsonx.Object)
				prev, _ := obj.Get(string(s))
				prevStr, _ := prev.(string)
				obj.Set(string(s), prevStr+*change.Delta)
			} else if n, ok := last.(chord.SegNumber); ok {
				arr := cur.([]any)
				prev := arr[int(n)]
				prevStr, _ := prev.(string)
				arr[int(n)] = prevStr + *change.Delta
			}
		}
	}
	return next
}

func thevPartialsOf(h durable.Harness, mu *sync.Mutex, out *[]*ai.AssistantMessage) {
	h.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
		for _, ch := range pub.Changes {
			doc, ok := ch.(durable.DocumentCommitChangeUpdate)
			if !ok || doc.Record.Kind != "pi.live" || doc.Value == nil {
				continue
			}
			var live durable.LiveState
			raw, err := jsonx.Marshal(doc.Value)
			if err != nil {
				continue
			}
			if err := jsonx.Unmarshal(raw, &live); err != nil {
				continue
			}
			if live.Generation != nil && live.Generation.Message != nil {
				mu.Lock()
				*out = append(*out, live.Generation.Message)
				mu.Unlock()
			}
		}
	})
}

func thevEncode(t *testing.T, v any) durable.JsonValue {
	t.Helper()
	enc, err := jsonx.Encode(v)
	if err != nil {
		t.Fatalf("jsonx.Encode: %v", err)
	}
	return enc
}

func thevIntPtr(n int) *int       { return &n }
func thevStrPtr(s string) *string { return &s }

func TestHarnessEvents(t *testing.T) {
	t.Run("streams a run as lifecycle events and text deltas that rebuild the answer", func(t *testing.T) {
		setup := thevSlowSetup(t, 400)
		text := strings.Repeat("streamed answer text ", 20)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(text)}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()
		snap := rec.stream.Snapshot()
		if snap.Type != "snapshot" || len(snap.Entries) != 0 || len(snap.Tools) != 0 || len(snap.Inbox) != 0 {
			t.Fatalf("unexpected initial snapshot: %+v", snap)
		}

		var partMu sync.Mutex
		var partials []string
		h.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range pub.Changes {
				doc, ok := ch.(durable.DocumentCommitChangeUpdate)
				if !ok || doc.Record.Kind != "pi.live" || doc.Value == nil {
					continue
				}
				raw, err := jsonx.Marshal(doc.Value)
				if err != nil {
					continue
				}
				var live durable.LiveState
				if err := jsonx.Unmarshal(raw, &live); err != nil {
					continue
				}
				if live.Generation != nil && live.Generation.Message != nil {
					txt, _ := testkit.TextOf(live.Generation.Message)
					partMu.Lock()
					partials = append(partials, txt)
					partMu.Unlock()
				}
			}
		})

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("hi")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		all := rec.events()
		var collapsed []string
		for _, ev := range all {
			typ := ev.AgentEventType()
			if len(collapsed) == 0 || collapsed[len(collapsed)-1] != typ {
				collapsed = append(collapsed, typ)
			}
		}
		wantCollapsed := []string{
			"message_start",
			"message_end",
			"submission",
			"run_start",
			"turn_start",
			"message_start",
			"message_update",
			"message_end",
			"turn_end",
			"run_end",
			"submission",
			"usage_changed",
		}
		if !slices.Equal(collapsed, wantCollapsed) {
			t.Fatalf("collapsed types = %v, want %v", collapsed, wantCollapsed)
		}

		var runStart *durable.RunStartEvent
		for _, ev := range all {
			if rs, ok := ev.(*durable.RunStartEvent); ok {
				runStart = rs
				break
			}
		}
		if runStart == nil || !slices.Equal(runStart.Inputs, []durable.SubmissionId{sub.Id()}) {
			t.Fatalf("run_start = %+v, want inputs=[%d]", runStart, sub.Id())
		}

		var rebuilt []string
		for i, ev := range all {
			isAssistantStart := false
			if ms, ok := ev.(*durable.MessageStartEvent); ok {
				if _, isA := ms.Message.(*ai.AssistantMessage); isA {
					isAssistantStart = true
				}
			}
			if isAssistantStart || ev.AgentEventType() == "message_update" {
				rebuilt = append(rebuilt, thevStreamedText(all[:i+1]))
			}
		}
		partMu.Lock()
		gotPartials := slices.Clone(partials)
		partMu.Unlock()
		if !slices.Equal(rebuilt, gotPartials) {
			t.Fatalf("rebuilt texts len=%d vs partials len=%d\nrebuilt=%v\npartials=%v", len(rebuilt), len(gotPartials), rebuilt, gotPartials)
		}
		if len(gotPartials) <= 1 {
			t.Fatalf("expected multiple partials, got %d", len(gotPartials))
		}

		var lastEnd *durable.MessageEndEvent
		for _, ev := range all {
			if me, ok := ev.(*durable.MessageEndEvent); ok {
				lastEnd = me
			}
		}
		if lastEnd == nil || len(lastEnd.Entry.Model) == 0 {
			t.Fatalf("missing last message_end: %+v", lastEnd)
		}
		endTxt, _ := testkit.TextOf(lastEnd.Entry.Model[0])
		if endTxt != text {
			t.Fatalf("end text = %q, want %q", endTxt, text)
		}
	})

	t.Run("reports tool start, output appends, and the result entry", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		props := omap.NewMap[string, *typebox.Schema]()
		props.Set("n", typebox.Number())
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "print",
			Description: "Prints",
			Parameters:  typebox.Object(props),
			Execute: func(_ any, api durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				api.Output("one\n")
				time.Sleep(150 * time.Millisecond)
				api.Output("two\n")
				<-gate.Done()
				return &durable.ToolExecutionResult{}, nil
			},
		})); err != nil {
			t.Fatal(err)
		}
		toolUse := ai.StopReasonToolUse
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{providers.FauxToolCall("print", jsonx.ObjectOf("n", 1), &providers.FauxToolCallOptions{Id: thevStrPtr("c1")})},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})

		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("go")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		outputText := func() string {
			text := ""
			for _, ev := range rec.events() {
				upd, ok := ev.(*durable.ToolExecutionUpdateEvent)
				if !ok || upd.Output == nil {
					continue
				}
				if upd.Output.Set != nil {
					text = *upd.Output.Set
				} else {
					trim := 0
					if upd.Output.TrimStart != nil {
						trim = *upd.Output.TrimStart
					}
					app := ""
					if upd.Output.Append != nil {
						app = *upd.Output.Append
					}
					text = text[trim:] + app
				}
			}
			return text
		}
		if err := testkit.WaitFor(func() (bool, error) { return outputText() == "one\ntwo\n", nil }); err != nil {
			t.Fatal(err)
		}
		gate.Resolve(struct{}{})
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		all := rec.events()
		var toolEvents []durable.AgentEvent
		for _, ev := range all {
			if strings.HasPrefix(ev.AgentEventType(), "tool_execution") {
				toolEvents = append(toolEvents, ev)
			}
		}
		if len(toolEvents) < 2 {
			t.Fatalf("too few tool events: %+v", toolEvents)
		}
		startEv, ok := toolEvents[0].(*durable.ToolExecutionStartEvent)
		if !ok || startEv.ToolCallId != "c1" || startEv.ToolName != "print" || !jsonx.DeepEqual(startEv.Args, jsonx.ObjectOf("n", 1)) {
			t.Fatalf("first tool event = %#v", toolEvents[0])
		}
		endEv, ok := toolEvents[len(toolEvents)-1].(*durable.ToolExecutionEndEvent)
		if !ok || endEv.ToolCallId != "c1" || endEv.Entry == nil || endEv.Entry.Kind != "pi.tool-result" {
			t.Fatalf("last tool event = %#v", toolEvents[len(toolEvents)-1])
		}

		endIdx := -1
		for i, ev := range all {
			if ev == endEv {
				endIdx = i
				break
			}
		}
		if endIdx < 0 || endIdx+2 >= len(all) {
			t.Fatalf("missing events after tool_execution_end at %d", endIdx)
		}
		seq := []string{all[endIdx].AgentEventType(), all[endIdx+1].AgentEventType(), all[endIdx+2].AgentEventType()}
		if !slices.Equal(seq, []string{"tool_execution_end", "message_start", "message_end"}) {
			t.Fatalf("events around end = %v", seq)
		}
		msgStart := all[endIdx+1].(*durable.MessageStartEvent)
		trm, ok := msgStart.Message.(*ai.ToolResultMessage)
		if !ok || trm.ToolCallId != "c1" {
			t.Fatalf("message after end = %#v", msgStart.Message)
		}

		turnStarts, turnEnds := 0, 0
		for _, ev := range all {
			switch ev.AgentEventType() {
			case "turn_start":
				turnStarts++
			case "turn_end":
				turnEnds++
			}
		}
		if turnStarts != 2 || turnEnds != 2 {
			t.Fatalf("turnStarts=%d turnEnds=%d, want 2/2", turnStarts, turnEnds)
		}
	})

	t.Run("reports queued submissions, inbox changes, and retries", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		release := testkit.NewDeferred[struct{}]()
		heldStep := providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			var done <-chan struct{}
			if ctx != nil {
				done = ctx.Done()
			}
			select {
			case <-release.Done():
			case <-done:
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("first")}, nil), nil
		})
		errReason := ai.StopReasonError
		errMsg := "503 Service Unavailable"
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			heldStep,
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{StopReason: &errReason, ErrorMessage: &errMsg})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("second")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		enabled := true
		maxR := 1
		baseMs := int64(1)
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{
				Enabled:     &enabled,
				MaxRetries:  &maxR,
				BaseDelayMs: &baseMs,
			}
		})

		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()
		if _, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("a")},
		}, nil); err != nil {
			t.Fatal(err)
		}
		followUp, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("f")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			for _, ev := range rec.events() {
				if ev.AgentEventType() == "inbox_update" {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		var inboxes []*durable.InboxUpdateEvent
		for _, ev := range rec.events() {
			if ib, ok := ev.(*durable.InboxUpdateEvent); ok {
				inboxes = append(inboxes, ib)
			}
		}
		if len(inboxes) != 1 || len(inboxes[0].Items) != 1 || inboxes[0].Items[0] != (durable.QueuedItem{Id: followUp.Id(), Mode: durable.InboxModeFollowUp}) {
			t.Fatalf("inboxes = %+v", inboxes)
		}

		release.Resolve(struct{}{})
		if _, err := followUp.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		all := rec.events()
		var types []string
		var subStatuses []durable.SubmissionStatus
		runStarts := 0
		hasRetryStart, hasRetryEnd := false, false
		for _, ev := range all {
			typ := ev.AgentEventType()
			types = append(types, typ)
			switch e := ev.(type) {
			case *durable.AutoRetryStartEvent:
				hasRetryStart = true
			case *durable.AutoRetryEndEvent:
				hasRetryEnd = true
			case *durable.RunStartEvent:
				runStarts++
			case *durable.SubmissionEvent:
				subStatuses = append(subStatuses, e.Record.Status)
			}
		}
		if !hasRetryStart || !hasRetryEnd {
			t.Fatalf("missing retry events in %v", types)
		}
		if runStarts != 2 {
			t.Fatalf("runStarts = %d, want 2", runStarts)
		}
		wantStatuses := []durable.SubmissionStatus{
			durable.SubmissionStatusPlaced,
			durable.SubmissionStatusQueued,
			durable.SubmissionStatusDone,
			durable.SubmissionStatusPlaced,
			durable.SubmissionStatusDone,
		}
		if !slices.Equal(subStatuses, wantStatuses) {
			t.Fatalf("submission statuses = %v, want %v", subStatuses, wantStatuses)
		}
	})

	t.Run("replaces undelivered batches with one snapshot after 100 pending batches", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		stream, err := durable.WatchEvents(h, root.Id(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Stop() }()
		for range 101 {
			if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			}); err != nil {
				t.Fatal(err)
			}
		}
		var mu sync.Mutex
		var batches [][]durable.AgentEvent
		if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
			mu.Lock()
			batches = append(batches, slices.Clone(events))
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()
		mu.Lock()
		got := slices.Clone(batches)
		mu.Unlock()
		if len(got) != 1 || len(got[0]) != 1 {
			t.Fatalf("batches = %+v, want 1 batch of 1 snapshot", got)
		}
		snap, ok := got[0][0].(*durable.SnapshotEvent)
		if !ok || snap.Type != "snapshot" || len(snap.Entries) != 101 {
			t.Fatalf("first event = %#v, want snapshot with 101 entries", got[0][0])
		}
	})

	t.Run("rebuilds every committed partial of thinking, text, and tool-call arguments from message changes", func(t *testing.T) {
		setup := thevSlowSetup(t, 150)
		toolUse := ai.StopReasonToolUse
		msg := providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{
				providers.FauxThinking(strings.Repeat("thinking about it ", 10)),
				providers.FauxText(strings.Repeat("some text ", 10)),
				providers.FauxToolCall("missing", jsonx.ObjectOf("path", strings.Repeat("a/long/path/", 10), "note", strings.Repeat("x", 60)), &providers.FauxToolCallOptions{Id: thevStrPtr("c1")}),
			},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: msg},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		var partMu sync.Mutex
		var partials []*ai.AssistantMessage
		thevPartialsOf(h, &partMu, &partials)
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("go")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		var rebuilt []*ai.AssistantMessage
		var current *ai.AssistantMessage
		all := rec.events()
		for _, ev := range all {
			if me, ok := ev.(*durable.MessageEndEvent); ok && me.Entry.Kind == "pi.assistant" {
				break
			}
			switch e := ev.(type) {
			case *durable.MessageStartEvent:
				if am, ok := e.Message.(*ai.AssistantMessage); ok {
					current = am
				} else {
					continue
				}
			case *durable.MessageUpdateEvent:
				current = thevApplyChanges(t, current, e.Changes)
			default:
				continue
			}
			rebuilt = append(rebuilt, current)
		}
		partMu.Lock()
		gotPartials := slices.Clone(partials)
		partMu.Unlock()
		if len(gotPartials) <= 2 {
			t.Fatalf("expected > 2 partials, got %d", len(gotPartials))
		}
		if len(rebuilt) != len(gotPartials) {
			t.Fatalf("len(rebuilt)=%d != len(partials)=%d", len(rebuilt), len(gotPartials))
		}
		for i := range rebuilt {
			if !jsonx.DeepEqual(thevEncode(t, rebuilt[i].Content), thevEncode(t, gotPartials[i].Content)) {
				t.Fatalf("rebuilt[%d].Content != partials[%d].Content", i, i)
			}
		}
		hasDelta := false
		for _, ev := range all {
			if mu, ok := ev.(*durable.MessageUpdateEvent); ok {
				for _, c := range mu.Changes {
					if c.Type == "thinking_delta" || c.Type == "text_delta" {
						hasDelta = true
					}
				}
			}
		}
		if !hasDelta {
			t.Fatalf("expected thinking_delta or text_delta change")
		}
	})

	t.Run("rebuilds a sliding tail window from output trims and appends", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "tail",
			Description: "Prints lines",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			OutputLimits: &durable.ToolOutputLimits{
				MaxLines: thevIntPtr(3),
				Retain:   durable.ToolOutputRetainTail,
			},
			Execute: func(_ any, api durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				for line := range 6 {
					api.Output("line " + string(rune('0'+line)) + "\n")
					time.Sleep(120 * time.Millisecond)
				}
				<-gate.Done()
				return &durable.ToolExecutionResult{}, nil
			},
		})); err != nil {
			t.Fatal(err)
		}
		toolUse := ai.StopReasonToolUse
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{providers.FauxToolCall("tail", jsonx.ObjectOf(), &providers.FauxToolCallOptions{Id: thevStrPtr("c1")})},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})

		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		var outMu sync.Mutex
		var outputs []string
		h.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range pub.Changes {
				doc, ok := ch.(durable.DocumentCommitChangeUpdate)
				if !ok || doc.Record.Kind != "pi.live" || doc.Value == nil {
					continue
				}
				raw, err := jsonx.Marshal(doc.Value)
				if err != nil {
					continue
				}
				var live durable.LiveState
				if err := jsonx.Unmarshal(raw, &live); err != nil {
					continue
				}
				if len(live.Tools) > 0 && live.Tools[0].Output != nil {
					out := *live.Tools[0].Output
					outMu.Lock()
					if len(outputs) == 0 || outputs[len(outputs)-1] != out {
						outputs = append(outputs, out)
					}
					outMu.Unlock()
				}
			}
		})

		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()
		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("go")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			outMu.Lock()
			defer outMu.Unlock()
			return len(outputs) > 0 && strings.HasSuffix(outputs[len(outputs)-1], "line 5\n"), nil
		}); err != nil {
			t.Fatal(err)
		}
		gate.Resolve(struct{}{})
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		var rebuilt []string
		text := ""
		hasTrimStart := false
		for _, ev := range rec.events() {
			upd, ok := ev.(*durable.ToolExecutionUpdateEvent)
			if !ok || upd.Output == nil {
				continue
			}
			if upd.Output.TrimStart != nil {
				hasTrimStart = true
			}
			if upd.Output.Set != nil {
				text = *upd.Output.Set
			} else {
				trim := 0
				if upd.Output.TrimStart != nil {
					trim = *upd.Output.TrimStart
				}
				app := ""
				if upd.Output.Append != nil {
					app = *upd.Output.Append
				}
				text = text[trim:] + app
			}
			rebuilt = append(rebuilt, text)
		}
		outMu.Lock()
		wantOutputs := slices.Clone(outputs)
		outMu.Unlock()
		if !slices.Equal(rebuilt, wantOutputs) {
			t.Fatalf("rebuilt outputs = %v, want %v", rebuilt, wantOutputs)
		}
		if !hasTrimStart {
			t.Fatalf("expected at least one trimStart update")
		}
	})

	t.Run("emits one exact batch when a run ends and a queued follow-up starts the next", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		release := testkit.NewDeferred[struct{}]()
		heldStep := providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
			var done <-chan struct{}
			if ctx != nil {
				done = ctx.Done()
			}
			select {
			case <-release.Done():
			case <-done:
			}
			return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("first")}, nil), nil
		})
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			heldStep,
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("second")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		input, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("a")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		followUp, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("f")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		release.Resolve(struct{}{})
		if _, err := followUp.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		var boundary []durable.AgentEvent
		for _, batch := range rec.snapshotBatches() {
			for _, ev := range batch {
				if ev.AgentEventType() == "run_end" {
					boundary = batch
					break
				}
			}
			if boundary != nil {
				break
			}
		}
		if boundary == nil {
			t.Fatalf("missing run_end batch")
		}
		var bTypes []string
		var subIds []durable.SubmissionId
		for _, ev := range boundary {
			bTypes = append(bTypes, ev.AgentEventType())
			if se, ok := ev.(*durable.SubmissionEvent); ok {
				subIds = append(subIds, se.Record.Id)
			}
		}
		wantTypes := []string{
			"message_start",
			"message_end",
			"message_start",
			"message_end",
			"turn_end",
			"run_end",
			"submission",
			"submission",
			"inbox_update",
			"usage_changed",
			"run_start",
			"turn_start",
		}
		if !slices.Equal(bTypes, wantTypes) {
			t.Fatalf("boundary types = %v, want %v", bTypes, wantTypes)
		}
		if !slices.Equal(subIds, []durable.SubmissionId{input.Id(), followUp.Id()}) {
			t.Fatalf("boundary submission ids = %v, want [%d %d]", subIds, input.Id(), followUp.Id())
		}
	})

	t.Run("ends a call that never runs and a tool aborted with its generation", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "wait",
			Description: "Waits until aborted",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
				<-callCtx.AbortSignal().Done()
				return nil, testkit.Aborted(callCtx.AbortSignal())
			},
		})); err != nil {
			t.Fatal(err)
		}
		toolUse := ai.StopReasonToolUse
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{
					providers.FauxToolCall("ghost", jsonx.ObjectOf(), &providers.FauxToolCallOptions{Id: thevStrPtr("c1")}),
					providers.FauxToolCall("wait", jsonx.ObjectOf(), &providers.FauxToolCallOptions{Id: thevStrPtr("c2")}),
				},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("go")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			for _, ev := range rec.events() {
				if ev.AgentEventType() == "tool_execution_start" {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		liveRaw, err := h.Snapshot(nil, durable.LiveDoc, root.Id())
		if err != nil {
			t.Fatal(err)
		}
		var live durable.LiveState
		b, _ := jsonx.Marshal(liveRaw)
		_ = jsonx.Unmarshal(b, &live)
		if _, err := h.AbortTask(nil, live.Run.TaskId); err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		all := rec.events()
		round := make([]string, len(all))
		ghostEnd := -1
		for i, ev := range all {
			switch e := ev.(type) {
			case *durable.MessageEndEvent:
				round[i] = "end:" + string(e.Entry.Kind)
			case *durable.MessageStartEvent:
				round[i] = "start:" + e.Message.GetRole()
			default:
				round[i] = ev.AgentEventType()
				if ev.AgentEventType() == "tool_execution_end" && ghostEnd < 0 {
					ghostEnd = i
				}
			}
		}
		if ghostEnd < 1 || ghostEnd+2 >= len(round) {
			t.Fatalf("bad ghostEnd=%d in %v", ghostEnd, round)
		}
		wantSlice := []string{"end:pi.assistant", "tool_execution_end", "start:toolResult", "end:pi.tool-result"}
		if !slices.Equal(round[ghostEnd-1:ghostEnd+3], wantSlice) {
			t.Fatalf("round slice = %v, want %v", round[ghostEnd-1:ghostEnd+3], wantSlice)
		}

		type toolSummary struct {
			typ      string
			callId   string
			hasEntry bool
		}
		var summaries []toolSummary
		for _, ev := range all {
			switch e := ev.(type) {
			case *durable.ToolExecutionStartEvent:
				summaries = append(summaries, toolSummary{e.Type, e.ToolCallId, false})
			case *durable.ToolExecutionEndEvent:
				summaries = append(summaries, toolSummary{e.Type, e.ToolCallId, e.Entry != nil})
			}
		}
		wantSummaries := []toolSummary{
			{"tool_execution_end", "c1", true},
			{"tool_execution_start", "c2", false},
			{"tool_execution_end", "c2", true},
		}
		if !slices.Equal(summaries, wantSummaries) {
			t.Fatalf("tool summaries = %+v, want %+v", summaries, wantSummaries)
		}
	})

	t.Run("reports a steer without run events, a reset as an appended entry, and nothing for other conversations", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "hold",
			Description: "Waits",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				<-gate.Done()
				return &durable.ToolExecutionResult{}, nil
			},
		})); err != nil {
			t.Fatal(err)
		}
		toolUse := ai.StopReasonToolUse
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{providers.FauxToolCall("hold", jsonx.ObjectOf(), &providers.FauxToolCallOptions{Id: thevStrPtr("c1")})},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("done")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		other, err := h.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		input, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("a")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			for _, ev := range rec.events() {
				if ev.AgentEventType() == "tool_execution_start" {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  durable.UserInput{Text: thevStrPtr("s")},
			WhenBusy: durable.SubmissionWhenBusySteer,
		}, nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		before := rec.batchCount()
		if _, err := other.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(other.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		if rec.batchCount() != before {
			t.Fatalf("other conversation changed root stream: %d -> %d", before, rec.batchCount())
		}

		gate.Resolve(struct{}{})
		if _, err := input.Wait(nil); err != nil {
			t.Fatal(err)
		}
		if err := root.Reset(nil, nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		runStarts, runEnds := 0, 0
		for _, ev := range rec.events() {
			switch ev.AgentEventType() {
			case "run_start":
				runStarts++
			case "run_end":
				runEnds++
			}
		}
		if runStarts != 1 || runEnds != 1 {
			t.Fatalf("runStarts=%d runEnds=%d, want 1/1", runStarts, runEnds)
		}
		batches := rec.snapshotBatches()
		last := batches[len(batches)-1]
		var lastTypes []string
		for _, ev := range last {
			lastTypes = append(lastTypes, ev.AgentEventType())
		}
		if !slices.Equal(lastTypes, []string{"entry_appended", "submission"}) {
			t.Fatalf("last batch types = %v, want [entry_appended submission]", lastTypes)
		}
	})

	t.Run("rejects an attachment cancelled while it waits for the Session line", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		entered := testkit.NewDeferred[struct{}]()
		release := testkit.NewDeferred[struct{}]()
		blockingErr := make(chan error, 1)
		go func() {
			_, err := root.Commit(nil, func(_ durable.Tx) (any, error) {
				entered.Resolve(struct{}{})
				<-release.Done()
				return nil, nil
			})
			blockingErr <- err
		}()
		<-entered.Done()

		childCtx := chord.WithCancel(nil)
		attachErr := make(chan error, 1)
		go func() {
			_, err := durable.WatchEvents(h, root.Id(), childCtx.Context)
			attachErr <- err
		}()
		testkit.Flush()
		childCtx.Cancel(errors.New("cancelled"))
		release.Resolve(struct{}{})
		if err := <-blockingErr; err != nil {
			t.Fatal(err)
		}
		if err := <-attachErr; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled error, got %v", err)
		}
	})

	t.Run("applies deltas after an overflow snapshot that holds an in-flight partial", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()

		partial := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("hel")}, nil)
		stream, err := durable.WatchEvents(h, root.Id(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stream.Stop() }()
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			doc, err := tx.Doc(durable.LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			doc.Value.Set("generation", jsonx.ObjectOf("attempt", 1, "message", thevEncode(t, partial)))
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		for range 101 {
			if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
				return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
			}); err != nil {
				t.Fatal(err)
			}
		}

		var mu sync.Mutex
		var batches [][]durable.AgentEvent
		if err := stream.Start(func(events []durable.AgentEvent, _ chord.Context) error {
			mu.Lock()
			batches = append(batches, slices.Clone(events))
			mu.Unlock()
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		mu.Lock()
		firstBatch := slices.Clone(batches[0])
		mu.Unlock()
		snap, ok := firstBatch[0].(*durable.SnapshotEvent)
		if !ok || snap.Type != "snapshot" {
			t.Fatalf("first event = %#v, want snapshot", firstBatch[0])
		}

		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			doc, err := tx.Doc(durable.LiveDoc, root.Id())
			if err != nil {
				return nil, err
			}
			genRaw, _ := doc.Value.Get("generation")
			msgRaw, _ := genRaw.(*jsonx.Object).Get("message")
			contentRaw, _ := msgRaw.(*jsonx.Object).Get("content")
			blk := contentRaw.([]any)[0].(*jsonx.Object)
			prev, _ := blk.Get("text")
			blk.Set("text", prev.(string)+"lo")
			return nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		mu.Lock()
		lastBatch := slices.Clone(batches[len(batches)-1])
		mu.Unlock()
		upd, ok := lastBatch[0].(*durable.MessageUpdateEvent)
		if !ok {
			t.Fatalf("expected MessageUpdateEvent, got %#v", lastBatch[0])
		}
		if len(upd.Changes) != 1 || upd.Changes[0].Type != "text_delta" || *upd.Changes[0].ContentIndex != 0 || *upd.Changes[0].Delta != "lo" {
			t.Fatalf("changes = %+v", upd.Changes)
		}
		rebuilt := thevApplyChanges(t, snap.Generation.Message, upd.Changes)
		if len(rebuilt.Content) != 1 || rebuilt.Content[0].(*ai.TextContent).Text != "hello" {
			t.Fatalf("rebuilt content = %+v, want text=hello", rebuilt.Content)
		}
	})

	t.Run("reports usage-only updates, cleared tool progress, tools ending without entries, and deferred polls", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		change := func(edit func(live *jsonx.Object)) {
			if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
				doc, err := tx.Doc(durable.LiveDoc, root.Id())
				if err != nil {
					return nil, err
				}
				edit(doc.Value)
				return nil, nil
			}); err != nil {
				t.Fatal(err)
			}
		}

		partialMsg := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("partial")}, nil)
		partialJson := thevEncode(t, partialMsg)
		change(func(live *jsonx.Object) {
			live.Set("generation", jsonx.ObjectOf("attempt", 1, "message", partialJson))
		})
		change(func(live *jsonx.Object) {
			genRaw, _ := live.Get("generation")
			msgRaw, _ := genRaw.(*jsonx.Object).Get("message")
			usageRaw, _ := msgRaw.(*jsonx.Object).Get("usage")
			usageRaw.(*jsonx.Object).Set("input", 42)
		})
		change(func(live *jsonx.Object) {
			live.Set("generation", jsonx.ObjectOf("attempt", 1, "deferred", jsonx.ObjectOf("pollAt", 1)))
		})
		change(func(live *jsonx.Object) {
			genRaw, _ := live.Get("generation")
			defRaw, _ := genRaw.(*jsonx.Object).Get("deferred")
			defRaw.(*jsonx.Object).Set("pollAt", 2)
		})
		change(func(live *jsonx.Object) {
			live.Set("tools", []any{jsonx.ObjectOf(
				"callId", "c1",
				"name", "t",
				"status", "running",
				"details", jsonx.ObjectOf("n", 1),
				"diagnostics", []any{},
			)})
		})
		change(func(live *jsonx.Object) {
			toolsRaw, _ := live.Get("tools")
			slot := toolsRaw.([]any)[0].(*jsonx.Object)
			slot.Delete("details")
			slot.Delete("diagnostics")
		})
		change(func(live *jsonx.Object) {
			toolsRaw, _ := live.Get("tools")
			slot := toolsRaw.([]any)[0].(*jsonx.Object)
			slot.Set("status", "done")
		})
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.RetireDoc(durable.UsageDoc, root.Id())
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.RetireDoc(durable.AgentDoc, root.Id())
		}); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		var got [][]durable.AgentEvent
		for _, batch := range rec.snapshotBatches() {
			var filtered []durable.AgentEvent
			for _, ev := range batch {
				if ev.AgentEventType() != "task_failed" {
					filtered = append(filtered, ev)
				}
			}
			got = append(got, filtered)
		}
		wantUsage := partialMsg.Usage
		wantUsage.Input = 42
		want := [][]durable.AgentEvent{
			{&durable.MessageStartEvent{Type: "message_start", Message: thevCloneAssistant(t, partialMsg)}},
			{&durable.MessageUpdateEvent{Type: "message_update", Usage: wantUsage, Changes: []durable.MessageChange{}}},
			{&durable.DeferredPollEvent{Type: "deferred_poll", PollAt: 1}},
			{&durable.DeferredPollEvent{Type: "deferred_poll", PollAt: 2}},
			{&durable.ToolExecutionStartEvent{Type: "tool_execution_start", ToolCallId: "c1", ToolName: "t", Args: jsonx.ObjectOf()}},
			{&durable.ToolExecutionUpdateEvent{
				Type:        "tool_execution_update",
				ToolCallId:  "c1",
				ToolName:    "t",
				Details:     jsonx.Some[durable.JsonValue](nil),
				Diagnostics: jsonx.Some([]durable.ToolDiagnostic{}),
			}},
			{&durable.ToolExecutionEndEvent{Type: "tool_execution_end", ToolCallId: "c1", ToolName: "t"}},
			{&durable.UsageChangedEvent{Type: "usage_changed", Usage: *durable.NewUsageState()}},
			{&durable.AgentChangedEvent{Type: "agent_changed", Agent: durable.AgentState{}}},
		}
		if !jsonx.DeepEqual(thevEncode(t, got), thevEncode(t, want)) {
			t.Fatalf("batches mismatch\ngot:  %v\nwant: %v", thevEncode(t, got), thevEncode(t, want))
		}
	})

	t.Run("ends the stream with the Harness", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := durable.WatchEvents(h, root.Id(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.Close(nil); err != nil {
			t.Fatal(err)
		}
		if end := stream.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("closed = %+v, want session_closed", end)
		}
	})

	t.Run("starts a message at the first committed partial and ends it with the converted entry on abort", func(t *testing.T) {
		setup := thevSlowSetup(t, 100)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(strings.Repeat("x", 400))}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h.Close(nil) }()
		rec := thevListen(t, h, root)
		defer func() { _ = rec.stream.Stop() }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thevStrPtr("hi")},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			for _, ev := range rec.events() {
				if ms, ok := ev.(*durable.MessageStartEvent); ok {
					if _, isA := ms.Message.(*ai.AssistantMessage); isA {
						return true, nil
					}
				}
			}
			return false, nil
		}); err != nil {
			t.Fatal(err)
		}
		liveRaw, err := h.Snapshot(nil, durable.LiveDoc, root.Id())
		if err != nil {
			t.Fatal(err)
		}
		var live durable.LiveState
		b, _ := jsonx.Marshal(liveRaw)
		_ = jsonx.Unmarshal(b, &live)
		if _, err := h.AbortTask(nil, live.Run.TaskId); err != nil {
			t.Fatal(err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatal(err)
		}
		testkit.Flush()
		testkit.Flush()

		assistantStarts, assistantEnds := 0, 0
		for _, ev := range rec.events() {
			switch e := ev.(type) {
			case *durable.MessageStartEvent:
				if _, ok := e.Message.(*ai.AssistantMessage); ok {
					assistantStarts++
				}
			case *durable.MessageEndEvent:
				if e.Entry.Kind == "pi.assistant" {
					assistantEnds++
				}
			}
		}
		if assistantEnds != 1 || assistantStarts != 1 {
			t.Fatalf("assistantStarts=%d assistantEnds=%d, want 1/1", assistantStarts, assistantEnds)
		}
	})
}
