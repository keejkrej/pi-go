// Ported from packages/durable/test/harness-inbox.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func thinAnswer(text string) *ai.AssistantMessage {
	return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
		providers.FauxText(text),
	}, nil)
}

type thinGatedStep struct {
	reached <-chan struct{}
	release func()
	step    providers.FauxResponseFactory
}

func thinGated(msg *ai.AssistantMessage) *thinGatedStep {
	reached := make(chan struct{})
	gate := testkit.NewDeferred[struct{}]()
	var once sync.Once
	step := providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		var done <-chan struct{}
		if ctx != nil {
			done = ctx.Done()
		}
		select {
		case <-gate.Done():
			return msg, nil
		case <-done:
			return nil, context.Cause(ctx)
		}
	})
	return &thinGatedStep{
		reached: reached,
		release: func() { gate.Resolve(struct{}{}) },
		step:    step,
	}
}

func thinHoldTool(t *testing.T, setup *testkit.ChatSetup, gate *testkit.Deferred[struct{}], res *durable.ToolExecutionResult) {
	t.Helper()
	if res == nil {
		res = &durable.ToolExecutionResult{}
	}
	if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
		Name:        "hold",
		Description: "Waits for the test",
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		Execute: func(_ any, _ durable.ToolExecutionApi, callCtx chord.Context) (*durable.ToolExecutionResult, error) {
			var done <-chan struct{}
			if callCtx != nil {
				if sig := callCtx.AbortSignal(); sig != nil {
					done = sig.Done()
				}
			}
			select {
			case <-gate.Done():
				return res, nil
			case <-done:
				return nil, testkit.Aborted(callCtx.AbortSignal())
			}
		},
	})); err != nil {
		t.Fatalf("AddTool hold: %v", err)
	}
}

func thinHoldCall() *providers.FauxResponseMessage {
	return &providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
			providers.FauxToolCall("hold", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thinPtr("c1")}),
		}, &providers.FauxAssistantMessageOptions{StopReason: thinPtr(ai.StopReasonToolUse)}),
	}
}

func thinStatus(t *testing.T, sub durable.Submission) durable.SubmissionRecord {
	t.Helper()
	st, err := sub.Status(nil)
	if err != nil {
		t.Fatalf("sub.Status: %v", err)
	}
	return *st
}

func thinTranscript(t *testing.T, conv durable.Conversation) []string {
	t.Helper()
	entries, err := testkit.AllEntries(conv)
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	var out []string
	for _, entry := range entries {
		if entry.Kind == "pi.system" {
			continue
		}
		var text string
		var ok bool
		if len(entry.Model) > 0 && entry.Model[0] != nil {
			if _, isTR := entry.Model[0].(*ai.ToolResultMessage); !isTR {
				text, ok = testkit.TextOf(entry.Model[0])
			}
		}
		if ok {
			out = append(out, entry.Kind+":"+text)
		} else {
			out = append(out, entry.Kind)
		}
	}
	return out
}

type thinInboxEntry struct {
	id   durable.SubmissionId
	mode durable.InboxMode
}

func thinInbox(t *testing.T, h durable.Harness, root durable.Conversation) []thinInboxEntry {
	t.Helper()
	snap, err := h.Snapshot(nil, durable.InboxDoc, root.Id())
	if err != nil {
		t.Fatalf("Snapshot InboxDoc: %v", err)
	}
	var st durable.InboxState
	if err := jsonx.Decode(snap, &st); err != nil {
		t.Fatalf("decode InboxState: %v", err)
	}
	out := make([]thinInboxEntry, len(st.Items))
	for i, item := range st.Items {
		out[i] = thinInboxEntry{id: item.Id, mode: item.Mode}
	}
	return out
}

func thinToolRunning(t *testing.T, h durable.Harness, root durable.Conversation) {
	t.Helper()
	if err := testkit.WaitFor(func() (bool, error) {
		snap, err := h.Snapshot(nil, durable.LiveDoc, root.Id())
		if err != nil || snap == nil {
			return false, err
		}
		var st durable.LiveState
		if err := jsonx.Decode(snap, &st); err != nil {
			return false, err
		}
		return len(st.Tools) > 0 && st.Tools[0].Status == durable.ToolSlotRunning, nil
	}); err != nil {
		t.Fatalf("toolRunning: %v", err)
	}
}

type thinRecordingInboxStorage struct {
	*storage.MemoryStorage
	mu      sync.Mutex
	inboxId durable.DocumentId
	written []struct {
		kind  durable.DocumentContentKind
		empty bool
	}
}

func (s *thinRecordingInboxStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.mu.Lock()
	inboxId := s.inboxId
	s.mu.Unlock()

	for _, w := range writes {
		dc, ok := w.(durable.StorageWriteDocumentChange)
		if !ok || inboxId == 0 || dc.Id != inboxId {
			continue
		}
		empty := false
		if dc.Content.Kind == durable.DocumentContentBase && dc.Content.Value != nil {
			var st durable.InboxState
			if err := jsonx.Decode(dc.Content.Value, &st); err == nil && len(st.Items) == 0 {
				empty = true
			}
		}
		s.mu.Lock()
		s.written = append(s.written, struct {
			kind  durable.DocumentContentKind
			empty bool
		}{kind: dc.Content.Kind, empty: empty})
		s.mu.Unlock()
	}
	return s.MemoryStorage.Commit(ctx, writes)
}

func TestHarnessInbox(t *testing.T) {
	t.Run("queues busy submissions and places writes before user items at final boundary one follow-up per run", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("second")},
			&providers.FauxResponseMessage{Message: thinAnswer("third")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		<-first.reached

		f1, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f1")}, nil)
		if err != nil {
			t.Fatal(err)
		}
		writeSub, err := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "note", Data: "w"},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		f2, err := root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  thinUserText("f2"),
			WhenBusy: durable.SubmissionWhenBusyFollowUp,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}

		for _, s := range []durable.Submission{f1, writeSub, f2} {
			if st := thinStatus(t, s).Status; st != durable.SubmissionStatusQueued {
				t.Fatalf("submission %d status = %s, want queued", s.Id(), st)
			}
		}
		wantInbox := []thinInboxEntry{
			{f1.Id(), durable.InboxModeFollowUp},
			{writeSub.Id(), durable.InboxModeWrite},
			{f2.Id(), durable.InboxModeFollowUp},
		}
		if got := thinInbox(t, harness, root); !slices.Equal(got, wantInbox) {
			t.Fatalf("inbox = %+v, want %+v", got, wantInbox)
		}
		if got := thinTranscript(t, root); !slices.Equal(got, []string{"pi.user:a"}) {
			t.Fatalf("transcript before release = %v", got)
		}

		first.release()
		if _, err := f2.Wait(nil); err != nil {
			t.Fatal(err)
		}

		wantTrans := []string{
			"pi.user:a",
			"pi.assistant:first",
			"note",
			"pi.user:f1",
			"pi.assistant:second",
			"pi.user:f2",
			"pi.assistant:third",
		}
		if got := thinTranscript(t, root); !slices.Equal(got, wantTrans) {
			t.Fatalf("transcript = %v, want %v", got, wantTrans)
		}

		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatal(err)
		}
		var answers []durable.EntryId
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				answers = append(answers, e.Id)
			}
		}
		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != answers[0] {
			t.Fatalf("input status = %+v", st)
		}
		if st := thinStatus(t, writeSub); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("write status = %+v", st)
		}
		if st := thinStatus(t, f1); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != answers[1] {
			t.Fatalf("f1 status = %+v", st)
		}
		if st := thinStatus(t, f2); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != answers[2] {
			t.Fatalf("f2 status = %+v", st)
		}
		if got := thinInbox(t, harness, root); len(got) != 0 {
			t.Fatalf("expected empty inbox, got %+v", got)
		}
		liveSnap, err := harness.Snapshot(nil, durable.LiveDoc, root.Id())
		if err != nil || !jsonx.DeepEqual(liveSnap, jsonx.NewObject()) {
			t.Fatalf("expected empty LiveDoc, got %v err=%v", liveSnap, err)
		}
	})

	t.Run("places every follow-up in one successor run with followUpMode all", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("both")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.FollowUpMode = durable.QueueModeAll
		})

		if _, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil); err != nil {
			t.Fatal(err)
		}
		<-first.reached
		f1, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f1")}, nil)
		f2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f2")}, nil)
		first.release()
		settled, err := f2.Wait(nil)
		if err != nil {
			t.Fatal(err)
		}
		st1 := thinStatus(t, f1)
		if st1.Status != settled.Status || !reflect.DeepEqual(st1.Answer, settled.Answer) || st1.Entry == nil {
			t.Fatalf("f1=%+v settled=%+v", st1, settled)
		}
		want := []string{"pi.user:a", "pi.assistant:first", "pi.user:f1", "pi.user:f2", "pi.assistant:both"}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
	})

	t.Run("rejects a third busy input when whenBusy is reject", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		_, err = root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  thinUserText("b"),
			WhenBusy: durable.SubmissionWhenBusyReject,
		}, nil)
		var busyErr *durable.ConversationBusy
		if err == nil || !strings.Contains(err.Error(), "busy") || !thinIsErrorType(err, &busyErr) {
			t.Fatalf("expected ConversationBusy error, got %v", err)
		}
		first.release()
	})

	t.Run("places a steer and follow-up around pending tools cleanly with one-at-a-time steering", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		thinHoldTool(t, setup, gate, nil)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("after steer")},
			&providers.FauxResponseMessage{Message: thinAnswer("after second steer")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		thinToolRunning(t, harness, root)
		s1, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("s1"), WhenBusy: durable.SubmissionWhenBusySteer}, nil)
		s2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("s2"), WhenBusy: durable.SubmissionWhenBusySteer}, nil)
		gate.Resolve(struct{}{})
		if _, err := s2.Wait(nil); err != nil {
			t.Fatal(err)
		}

		want := []string{
			"pi.user:a",
			"pi.assistant",
			"pi.tool-result",
			"pi.user:s1",
			"pi.assistant:after steer",
			"pi.user:s2",
			"pi.assistant:after second steer",
		}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
		entries, _ := testkit.AllEntries(root)
		var answers []durable.EntryId
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				answers = append(answers, e.Id)
			}
		}
		if st := thinStatus(t, input); st.Answer == nil || *st.Answer != answers[1] {
			t.Fatalf("input settled answer = %v, want %d", st.Answer, answers[1])
		}
		if st := thinStatus(t, s1); st.Answer == nil || *st.Answer != answers[1] {
			t.Fatalf("s1 settled answer = %v, want %d", st.Answer, answers[1])
		}
		if st := thinStatus(t, s2); st.Answer == nil || *st.Answer != answers[2] {
			t.Fatalf("s2 settled answer = %v, want %d", st.Answer, answers[2])
		}
	})

	t.Run("places a reset during streaming at postAssistant so queued follow-up runs from the cut", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		var reqMu sync.Mutex
		var requests [][]string
		capture := func(resp *ai.AssistantMessage) providers.FauxResponseFactory {
			return func(_ context.Context, tc *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var cur []string
				for _, m := range tc.Messages {
					cur = append(cur, testkit.DescribeMessage(m))
				}
				reqMu.Lock()
				requests = append(requests, cur)
				reqMu.Unlock()
				return resp, nil
			}
		}
		first := thinGated(thinAnswer("old"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			capture(thinAnswer("fresh")),
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		if err := root.Reset(nil, nil); err != nil {
			t.Fatal(err)
		}
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		first.release()
		if _, err := f.Wait(nil); err != nil {
			t.Fatal(err)
		}

		wantTrans := []string{"pi.user:a", "pi.assistant:old", "pi.reset", "pi.user:f", "pi.assistant:fresh"}
		if got := thinTranscript(t, root); !slices.Equal(got, wantTrans) {
			t.Fatalf("transcript = %v, want %v", got, wantTrans)
		}
		entries, _ := testkit.AllEntries(root)
		var resetEntry durable.EntryRecord
		for _, e := range entries {
			if durable.ResetEntry.Is(&e) {
				resetEntry = e
			}
		}
		if resetEntry.Head == nil || *resetEntry.Head != resetEntry.Id {
			t.Fatalf("reset head mismatch: %+v", resetEntry)
		}
		reqMu.Lock()
		gotReq := slices.Clone(requests)
		reqMu.Unlock()
		if len(gotReq) != 1 || !slices.Equal(gotReq[0], []string{"user:f", "system:"}) {
			t.Fatalf("requests = %#v", gotReq)
		}
		if setup.Faux.State.CallCount != 2 {
			t.Fatalf("CallCount = %d, want 2", setup.Faux.State.CallCount)
		}
	})

	t.Run("places a queued reset after the answer at the final boundary", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		if err := root.Reset(thinPtr("handoff"), nil); err != nil {
			t.Fatal(err)
		}
		first.release()
		if _, err := input.Wait(nil); err != nil {
			t.Fatal(err)
		}
		if err := harness.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input status = %+v", st)
		}
		want := []string{"pi.user:a", "pi.assistant:first", "pi.reset:handoff"}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
		ctxView, err := root.Context(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ctxView.Messages) != 1 || testkit.DescribeMessage(ctxView.Messages[0]) != "user:handoff" {
			t.Fatalf("context messages = %#v", ctxView.Messages)
		}
	})

	t.Run("resets an idle conversation at once with or without handoff text", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		setup.SetNow(func() int64 { return 7 })
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		}); err != nil {
			t.Fatal(err)
		}
		if err := root.Reset(nil, nil); err != nil {
			t.Fatal(err)
		}
		view, err := root.Context(nil)
		if err != nil {
			t.Fatal(err)
		}
		if view.Head == nil || view.Head.Kind != "pi.reset" || len(view.Head.Model) != 0 || len(view.Messages) != 0 {
			t.Fatalf("unexpected plain reset view: %+v", view)
		}
		if err := root.Reset(thinPtr("carry on"), nil); err != nil {
			t.Fatal(err)
		}
		view, err = root.Context(nil)
		if err != nil {
			t.Fatal(err)
		}
		if view.Head == nil || view.Head.Head == nil || *view.Head.Head != view.Head.Id ||
			len(view.Messages) != 1 || testkit.DescribeMessage(view.Messages[0]) != "user:carry on" {
			t.Fatalf("unexpected handoff reset view: %+v", view)
		}
		if um, ok := view.Messages[0].(*ai.UserMessage); !ok || um.Timestamp != 7 {
			t.Fatalf("expected timestamp 7, got %+v", view.Messages[0])
		}
	})

	t.Run("makes a queued head write stale when it targets an entry before the active range", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("second")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		oldAny, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if err != nil {
			t.Fatal(err)
		}
		old := oldAny.(*durable.EntryRecord)
		if err := root.Reset(nil, nil); err != nil {
			t.Fatal(err)
		}
		view, err := root.Context(nil)
		if err != nil {
			t.Fatal(err)
		}
		reset := view.Head
		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached

		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		stale, _ := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(old.Id)},
		}, nil)
		resetHead, _ := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(reset.Id)},
		}, nil)
		first.release()
		if _, err := f.Wait(nil); err != nil {
			t.Fatal(err)
		}
		_ = input

		settledStale, _ := stale.Wait(nil)
		if settledStale.Status != durable.SubmissionStatusUnanswered || settledStale.Reason == nil || *settledStale.Reason != "stale" {
			t.Fatalf("stale write = %+v", settledStale)
		}
		settledReset, _ := resetHead.Wait(nil)
		if settledReset.Status != durable.SubmissionStatusDone {
			t.Fatalf("resetHead write = %+v", settledReset)
		}
	})

	t.Run("stops follow-ups behind a failed run until a fresh idle input rescues them", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		failing := thinGated(providers.FauxAssistantMessage(nil, &providers.FauxAssistantMessageOptions{
			StopReason:   thinPtr(ai.StopReasonError),
			ErrorMessage: thinPtr("invalid request"),
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			failing.step,
			&providers.FauxResponseMessage{Message: thinAnswer("for f")},
			&providers.FauxResponseMessage{Message: thinAnswer("for g")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-failing.reached
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		failing.release()
		settledIn, _ := input.Wait(nil)
		if settledIn.Status != durable.SubmissionStatusUnanswered {
			t.Fatalf("failed input = %+v", settledIn)
		}
		if err := harness.WaitForIdle(nil); err != nil {
			t.Fatal(err)
		}
		if st := thinStatus(t, f).Status; st != durable.SubmissionStatusQueued {
			t.Fatalf("expected queued f after failure, got %s", st)
		}
		if got := thinInbox(t, harness, root); !slices.Equal(got, []thinInboxEntry{{f.Id(), durable.InboxModeFollowUp}}) {
			t.Fatalf("inbox = %+v", got)
		}

		g, _ := root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  thinUserText("g"),
			WhenBusy: durable.SubmissionWhenBusyReject,
		}, nil)
		if _, err := g.Wait(nil); err != nil {
			t.Fatal(err)
		}
		if st := thinStatus(t, f); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("expected f done, got %+v", st)
		}
		want := []string{"pi.user:f", "pi.assistant:for f", "pi.user:g", "pi.assistant:for g"}
		got := thinTranscript(t, root)
		if len(got) < 4 || !slices.Equal(got[len(got)-4:], want) {
			t.Fatalf("transcript tail = %v, want %v", got, want)
		}
	})

	t.Run("keeps queued submissions across reopen and settles them afterwards", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("first again")},
			&providers.FauxResponseMessage{Message: thinAnswer("f")},
		})

		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h1, root1, err := testkit.OpenChat(st1, setup)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		fSub, _ := root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		fId := fSub.Id()
		if err := h1.Close(nil); err != nil {
			t.Fatal(err)
		}

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h2, root2, err := testkit.OpenChat(st2, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h2.Close(nil) }()

		sub2, err := h2.Submission(nil, fId)
		if err != nil {
			t.Fatal(err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v", settled)
		}
		got := thinTranscript(t, root2)
		if len(got) < 2 || !slices.Equal(got[len(got)-2:], []string{"pi.user:f", "pi.assistant:f"}) {
			t.Fatalf("transcript = %v", got)
		}
	})

	t.Run("commits inbox changes as positional Chord operations and a base when empty", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("second")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		var mu sync.Mutex
		var ops [][]chord.Op
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.inbox" && len(ch.Ops) > 0 {
					mu.Lock()
					ops = append(ops, slices.Clone(ch.Ops))
					mu.Unlock()
				}
			}
		})

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		w1, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}, nil)
		f1, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f1")}, nil)
		w2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}, nil)
		f2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f2")}, nil)
		w3, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}, nil)

		mu.Lock()
		pushOps := slices.Clone(ops)
		mu.Unlock()
		if len(pushOps) != 5 {
			t.Fatalf("expected 5 inbox push ops, got %d: %#v", len(pushOps), pushOps)
		}
		expectedIds := []durable.SubmissionId{w1.Id(), f1.Id(), w2.Id(), f2.Id(), w3.Id()}
		for i, id := range expectedIds {
			sp, ok := pushOps[i][0].(chord.OpSplice)
			if len(pushOps[i]) != 1 || !ok || !thldPathEq(sp.Path, "items") || sp.Index != i || sp.Remove != 0 || len(sp.Items) != 1 {
				t.Fatalf("pushOps[%d] = %#v", i, pushOps[i])
			}
			itemObj, _ := sp.Items[0].(*jsonx.Object)
			gotId, _ := itemObj.Get("id")
			if gotId != float64(id) {
				t.Fatalf("pushOps[%d] id = %v, want %d", i, gotId, id)
			}
		}

		first.release()
		if _, err := input.Wait(nil); err != nil {
			t.Fatal(err)
		}
		if _, err := f2.Wait(nil); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		allOps := slices.Clone(ops)
		mu.Unlock()
		drainOps := allOps[5]
		// Every write and the first follow-up leave; only f2 remains. Sequential Chord splices remove [0..2] then [1] (w3 shifted from 4 to 1).
		hasRemHead := false
		hasRemTail := false
		for _, op := range drainOps {
			sp, ok := op.(chord.OpSplice)
			if !ok || len(sp.Items) != 0 {
				t.Fatalf("expected pure removal splice in ops[5], got %#v", drainOps)
			}
			if sp.Index == 0 && sp.Remove == 3 {
				hasRemHead = true
			}
			if (sp.Index == 1 || sp.Index == 4) && sp.Remove == 1 {
				hasRemTail = true
			}
		}
		encoded, _ := jsonx.Marshal(drainOps)
		if !hasRemHead || !hasRemTail || strings.Contains(string(encoded), "f2") {
			t.Fatalf("ops[5] = %s", encoded)
		}
	})

	t.Run("settles a stale write at once while idle and a queued one behind waiting items", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		failing := thinGated(providers.FauxAssistantMessage(nil, &providers.FauxAssistantMessageOptions{
			StopReason:   thinPtr(ai.StopReasonError),
			ErrorMessage: thinPtr("invalid request"),
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			failing.step,
			&providers.FauxResponseMessage{Message: thinAnswer("for f")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		oldAny, _ := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		old := oldAny.(*durable.EntryRecord)
		_ = root.Reset(nil, nil)
		beforeEntries, _ := testkit.AllEntries(root)
		idle, _ := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(old.Id)},
		}, nil)
		settledIdle, _ := idle.Wait(nil)
		if settledIdle.Status != durable.SubmissionStatusUnanswered || settledIdle.Reason == nil || *settledIdle.Reason != "stale" {
			t.Fatalf("idle stale write = %+v", settledIdle)
		}
		if curEntries, _ := testkit.AllEntries(root); len(curEntries) != len(beforeEntries) {
			t.Fatalf("entries grew on stale write: %d -> %d", len(beforeEntries), len(curEntries))
		}

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-failing.reached
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		failing.release()
		_, _ = input.Wait(nil)
		_ = harness.WaitForIdle(nil)

		queued, _ := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "summary", Head: durable.EntryHeadAt(old.Id)},
		}, nil)
		settledQueued, _ := queued.Wait(nil)
		if settledQueued.Status != durable.SubmissionStatusUnanswered || settledQueued.Reason == nil || *settledQueued.Reason != "stale" {
			t.Fatalf("queued stale write = %+v", settledQueued)
		}
		if stF, _ := f.Wait(nil); stF.Status != durable.SubmissionStatusDone {
			t.Fatalf("f settled = %+v", stF)
		}
	})

	t.Run("keeps an onYield continuation across a queued plain write with the run original input", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("second")},
		})
		yields := 0
		_, _ = testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				yields++
				if yields == 1 {
					return &durable.GenerationYieldDecision{Continue: thinUserText("more")}, nil
				}
				return nil, nil
			},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		writeSub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note"}}, nil)
		first.release()
		_, _ = input.Wait(nil)

		entries, _ := testkit.AllEntries(root)
		want := []string{"pi.user:a", "pi.assistant:first", "note", "pi.user:more", "pi.assistant:second"}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
		lastId := entries[len(entries)-1].Id
		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != lastId {
			t.Fatalf("input status = %+v", st)
		}
		if st := thinStatus(t, writeSub); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("write status = %+v", st)
		}
	})

	t.Run("drops an onYield continuation for a queued reset", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		_, _ = testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
			OnYield: func(_ *ai.AssistantMessage, _ durable.HookApi, _ chord.Context) (*durable.GenerationYieldDecision, error) {
				return &durable.GenerationYieldDecision{Continue: thinUserText("more")}, nil
			},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		_ = root.Reset(nil, nil)
		first.release()
		_, _ = input.Wait(nil)
		_ = harness.WaitForIdle(nil)

		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input status = %+v", st)
		}
		want := []string{"pi.user:a", "pi.assistant:first", "pi.reset"}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
		if setup.Faux.State.CallCount != 1 {
			t.Fatalf("CallCount = %d, want 1", setup.Faux.State.CallCount)
		}
	})

	t.Run("adds every steer to the run at postTools boundary with steeringMode all", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		thinHoldTool(t, setup, gate, nil)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("after tools")},
			&providers.FauxResponseMessage{Message: thinAnswer("follow-up")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.SteeringMode = durable.QueueModeAll
		})

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		thinToolRunning(t, harness, root)
		s1, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("s1"), WhenBusy: durable.SubmissionWhenBusySteer}, nil)
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		s2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("s2"), WhenBusy: durable.SubmissionWhenBusySteer}, nil)
		gate.Resolve(struct{}{})
		_, _ = f.Wait(nil)

		want := []string{
			"pi.user:a",
			"pi.assistant",
			"pi.tool-result",
			"pi.user:s1",
			"pi.user:s2",
			"pi.assistant:after tools",
			"pi.user:f",
			"pi.assistant:follow-up",
		}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
		entries, _ := testkit.AllEntries(root)
		var answers []durable.EntryId
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				answers = append(answers, e.Id)
			}
		}
		for _, sub := range []durable.Submission{input, s1, s2} {
			if st := thinStatus(t, sub); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != answers[1] {
				t.Fatalf("sub %d = %+v, want answer %d", sub.Id(), st, answers[1])
			}
		}
	})

	t.Run("queues an idle steer behind waiting items and places it with first follow-up in ID order", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		failing := thinGated(providers.FauxAssistantMessage(nil, &providers.FauxAssistantMessageOptions{
			StopReason:   thinPtr(ai.StopReasonError),
			ErrorMessage: thinPtr("invalid request"),
		}))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			failing.step,
			&providers.FauxResponseMessage{Message: thinAnswer("both")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-failing.reached
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		failing.release()
		_, _ = input.Wait(nil)
		_ = harness.WaitForIdle(nil)

		steer, _ := root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  thinUserText("s"),
			WhenBusy: durable.SubmissionWhenBusySteer,
		}, nil)
		settled, _ := steer.Wait(nil)
		if st := thinStatus(t, f); st.Status != durable.SubmissionStatusDone || !reflect.DeepEqual(st.Answer, settled.Answer) {
			t.Fatalf("f=%+v steer=%+v", st, settled)
		}
		got := thinTranscript(t, root)
		want := []string{"pi.user:f", "pi.user:s", "pi.assistant:both"}
		if len(got) < 3 || !slices.Equal(got[len(got)-3:], want) {
			t.Fatalf("transcript = %v, want suffix %v", got, want)
		}
	})

	t.Run("starts a queued follow-up after a terminating round", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		thinHoldTool(t, setup, gate, &durable.ToolExecutionResult{
			Control: &durable.ToolControl{Terminate: true},
		})
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("follow-up")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		thinToolRunning(t, harness, root)
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		gate.Resolve(struct{}{})
		_, _ = f.Wait(nil)

		entries, _ := testkit.AllEntries(root)
		var calling durable.EntryRecord
		for _, e := range entries {
			if e.Kind == "pi.assistant" {
				calling = e
				break
			}
		}
		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone || st.Answer == nil || *st.Answer != calling.Id {
			t.Fatalf("input status = %+v, calling=%d", st, calling.Id)
		}
		want := []string{"pi.user:a", "pi.assistant", "pi.tool-result", "pi.user:f", "pi.assistant:follow-up"}
		if got := thinTranscript(t, root); !slices.Equal(got, want) {
			t.Fatalf("transcript = %v, want %v", got, want)
		}
	})

	t.Run("writes the last handoff in call order and then runs queued follow-ups in the new context", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		firstGate := testkit.NewDeferred[struct{}]()
		secondGate := testkit.NewDeferred[struct{}]()
		thinHoldTool(t, setup, firstGate, &durable.ToolExecutionResult{
			Control: &durable.ToolControl{Handoff: thinPtr("one")},
		})
		_, _ = testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
			Name:        "later",
			Description: "Finishes first",
			Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
			Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				<-secondGate.Done()
				return &durable.ToolExecutionResult{
					Control: &durable.ToolControl{Handoff: thinPtr("two")},
				}, nil
			},
		}))
		round := &providers.FauxResponseMessage{
			Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxToolCall("hold", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thinPtr("c1")}),
				providers.FauxToolCall("later", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thinPtr("c2")}),
			}, &providers.FauxAssistantMessageOptions{StopReason: thinPtr(ai.StopReasonToolUse)}),
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			round,
			&providers.FauxResponseMessage{Message: thinAnswer("follow-up")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		thinToolRunning(t, harness, root)
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		secondGate.Resolve(struct{}{})
		_ = testkit.WaitFor(func() (bool, error) {
			snap, err := harness.Snapshot(nil, durable.LiveDoc, root.Id())
			if err != nil || snap == nil {
				return false, err
			}
			var st durable.LiveState
			if err := jsonx.Decode(snap, &st); err != nil {
				return false, err
			}
			return len(st.Tools) > 1 && st.Tools[1].Status == durable.ToolSlotDone, nil
		})
		firstGate.Resolve(struct{}{})
		_, _ = f.Wait(nil)

		if st := thinStatus(t, input); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input status = %+v", st)
		}
		got := thinTranscript(t, root)
		want := []string{"pi.tool-result", "pi.reset:two", "pi.user:f", "pi.assistant:follow-up"}
		if len(got) < 4 || !slices.Equal(got[len(got)-4:], want) {
			t.Fatalf("transcript = %v, want suffix %v", got, want)
		}
	})

	t.Run("leaves the inbox alone when the run task is aborted", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("never"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		f, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f")}, nil)
		var st durable.LiveState
		snap, _ := harness.Snapshot(nil, durable.LiveDoc, root.Id())
		_ = jsonx.Decode(snap, &st)
		_, _ = harness.AbortTask(nil, st.Run.TaskId)

		settledIn, _ := input.Wait(nil)
		if settledIn.Status != durable.SubmissionStatusUnanswered || settledIn.Reason == nil || *settledIn.Reason != "aborted" {
			t.Fatalf("input = %+v", settledIn)
		}
		_ = harness.WaitForIdle(nil)
		if fst := thinStatus(t, f).Status; fst != durable.SubmissionStatusQueued {
			t.Fatalf("f status = %s, want queued", fst)
		}
		if got := thinInbox(t, harness, root); !slices.Equal(got, []thinInboxEntry{{f.Id(), durable.InboxModeFollowUp}}) {
			t.Fatalf("inbox = %+v", got)
		}
	})

	t.Run("returns a queued submission for its repeated request ID without a second item", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		queued, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f"), RequestId: "r"}, nil)
		again, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f"), RequestId: "r"}, nil)
		if again.Id() != queued.Id() {
			t.Fatalf("again=%d queued=%d", again.Id(), queued.Id())
		}
		if got := thinInbox(t, harness, root); !slices.Equal(got, []thinInboxEntry{{queued.Id(), durable.InboxModeFollowUp}}) {
			t.Fatalf("inbox = %+v", got)
		}
		first.release()
	})

	t.Run("withdraws a middle item with one positional removal", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{first.step})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		var items []durable.Submission
		for _, text := range []string{"x", "y", "z"} {
			s, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText(text)}, nil)
			items = append(items, s)
		}
		var mu sync.Mutex
		var ops [][]chord.Op
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.inbox" && len(ch.Ops) > 0 {
					mu.Lock()
					ops = append(ops, slices.Clone(ch.Ops))
					mu.Unlock()
				}
			}
		})
		if _, err := items[1].Abort(nil); err != nil {
			t.Fatal(err)
		}

		mu.Lock()
		gotOps := slices.Clone(ops)
		mu.Unlock()
		wantOp := chord.OpSplice{
			Path:   chord.Path{chord.SegString("items")},
			Index:  1,
			Remove: 1,
			Items:  []chord.JsonValue{},
		}
		if len(gotOps) != 1 || len(gotOps[0]) != 1 || !reflect.DeepEqual(gotOps[0][0], wantOp) {
			t.Fatalf("ops = %#v, want [%#v]", gotOps, wantOp)
		}
		wantInbox := []thinInboxEntry{
			{items[0].Id(), durable.InboxModeFollowUp},
			{items[2].Id(), durable.InboxModeFollowUp},
		}
		if got := thinInbox(t, harness, root); !slices.Equal(got, wantInbox) {
			t.Fatalf("inbox = %+v, want %+v", got, wantInbox)
		}
		first.release()
	})

	t.Run("stores the inbox as a base exactly when it becomes empty", func(t *testing.T) {
		st := &thinRecordingInboxStorage{MemoryStorage: storage.NewMemoryStorage()}
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		first := thinGated(thinAnswer("first"))
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			first.step,
			&providers.FauxResponseMessage{Message: thinAnswer("second")},
		})
		harness, root, err := testkit.OpenChat(st, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			for _, ch := range testkit.DocumentChanges(pub) {
				if ch.Record.Kind == "pi.inbox" {
					st.mu.Lock()
					st.inboxId = ch.Record.Id
					st.mu.Unlock()
				}
			}
		})

		input, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		<-first.reached
		_, _ = root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f1")}, nil)
		f2, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("f2")}, nil)
		first.release()
		_, _ = input.Wait(nil)
		_, _ = f2.Wait(nil)

		st.mu.Lock()
		written := slices.Clone(st.written)
		st.mu.Unlock()
		want := []struct {
			kind  durable.DocumentContentKind
			empty bool
		}{
			{durable.DocumentContentDelta, false},
			{durable.DocumentContentDelta, false},
			{durable.DocumentContentBase, true},
		}
		if !slices.Equal(written, want) {
			t.Fatalf("written = %+v, want %+v", written, want)
		}
	})

	t.Run("keeps a complete inbox base while empty and a usage base on every change", func(t *testing.T) {
		info := durable.CheckpointInfo{DeltasSinceBase: 1000}
		item := jsonx.ObjectOf("id", float64(1), "mode", "followUp", "content", "x")
		if !durable.InboxDoc.Definition.CheckpointWhen(jsonx.ObjectOf("items", []any{}), nil, info) {
			t.Fatal("empty inbox should checkpoint")
		}
		if durable.InboxDoc.Definition.CheckpointWhen(jsonx.ObjectOf("items", []any{item}), nil, info) {
			t.Fatal("non-empty inbox should not checkpoint")
		}
		if !durable.UsageDoc.Definition.CheckpointWhen(jsonx.ObjectOf("models", jsonx.NewObject(), "tools", jsonx.NewObject()), nil, info) {
			t.Fatal("usage doc should checkpoint on every change")
		}
	})
}

func TestHarnessUsage(t *testing.T) {
	t.Run("totals assistant usage per model and tool usage per tool and sums the Session", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		spent := ai.Usage{
			Input:       1,
			Output:      2,
			CacheRead:   3,
			CacheWrite:  4,
			TotalTokens: 10,
			Cost:        ai.UsageCost{Input: 0.1, Output: 0.2, CacheRead: 0.3, CacheWrite: 0.4, Total: 1},
		}
		gate := testkit.NewDeferred[struct{}]()
		gate.Resolve(struct{}{})
		thinHoldTool(t, setup, gate, &durable.ToolExecutionResult{Usage: &spent})
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("done")},
			&providers.FauxResponseMessage{Message: thinAnswer("other")},
		})

		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		_, _ = sub.Wait(nil)

		entries, _ := testkit.AllEntries(root)
		var expected ai.Usage
		var toolRes *ai.ToolResultMessage
		for _, e := range entries {
			if e.Kind == "pi.assistant" && len(e.Model) > 0 {
				if am, ok := e.Model[0].(*ai.AssistantMessage); ok {
					expected.Input += am.Usage.Input
					expected.Output += am.Usage.Output
					expected.TotalTokens += am.Usage.TotalTokens
				}
			}
			if e.Kind == "pi.tool-result" && len(e.Model) > 0 {
				toolRes, _ = e.Model[0].(*ai.ToolResultMessage)
			}
		}

		usageSnap, err := harness.Snapshot(nil, durable.UsageDoc, root.Id())
		if err != nil {
			t.Fatal(err)
		}
		var usageState durable.UsageState
		if err := jsonx.Decode(usageSnap, &usageState); err != nil {
			t.Fatal(err)
		}
		mUsage := usageState.Models["faux/faux-1"]
		if mUsage.Input != expected.Input || mUsage.Output != expected.Output || mUsage.TotalTokens != expected.TotalTokens {
			t.Fatalf("model usage = %+v, want %+v", mUsage, expected)
		}
		if !reflect.DeepEqual(usageState.Tools["hold"], spent) {
			t.Fatalf("tool usage = %+v, want %+v", usageState.Tools, spent)
		}
		if toolRes == nil || toolRes.Usage == nil || !reflect.DeepEqual(*toolRes.Usage, spent) {
			t.Fatalf("tool result usage = %+v, want %+v", toolRes, spent)
		}

		fork, err := root.Fork(entries[len(entries)-1].Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		forkSnap, _ := harness.Snapshot(nil, durable.UsageDoc, fork.Id())
		var forkZero durable.UsageState
		_ = jsonx.Decode(forkSnap, &forkZero)
		if len(forkZero.Models) != 0 || len(forkZero.Tools) != 0 {
			t.Fatalf("expected zero usage after fork, got %+v", forkZero)
		}
		forkSub, _ := fork.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("b")}, nil)
		_, _ = forkSub.Wait(nil)

		forkSnap2, _ := harness.Snapshot(nil, durable.UsageDoc, fork.Id())
		var forkUsage durable.UsageState
		_ = jsonx.Decode(forkSnap2, &forkUsage)
		total, err := harness.Usage(nil)
		if err != nil {
			t.Fatal(err)
		}
		if total.Models["faux/faux-1"].Output != expected.Output+forkUsage.Models["faux/faux-1"].Output {
			t.Fatalf("total output = %d, want %d", total.Models["faux/faux-1"].Output, expected.Output+forkUsage.Models["faux/faux-1"].Output)
		}
		if !reflect.DeepEqual(total.Tools["hold"], spent) {
			t.Fatalf("total tools = %+v", total.Tools)
		}
	})

	t.Run("counts failed attempts converted partials and tool usage replaced by afterTool", func(t *testing.T) {
		tps := 200.0
		one := 1
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: &tps,
			TokenSize:       &providers.FauxTokenSizeOptions{Min: &one, Max: &one},
		})
		if err != nil {
			t.Fatal(err)
		}
		spent := ai.Usage{Input: 5, Output: 0, TotalTokens: 5}
		gate := testkit.NewDeferred[struct{}]()
		gate.Resolve(struct{}{})
		thinHoldTool(t, setup, gate, nil)
		_, _ = testkit.AddHooks(setup.Registry, durable.ToolTask, durable.ToolHooks{
			AfterTool: func(_ *ai.ToolCall, res *durable.ToolExecutionResult, _ durable.HookApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
				next := *res
				next.Usage = &spent
				return &next, nil
			},
		})
		retryable := providers.FauxAssistantMessage(nil, &providers.FauxAssistantMessageOptions{
			StopReason:   thinPtr(ai.StopReasonError),
			ErrorMessage: thinPtr("503 Service Unavailable"),
		})
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: retryable},
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("done")},
			&providers.FauxResponseMessage{Message: thinAnswer(strings.Repeat("x", 400))},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()
		en := true
		mr := 1
		bd := int64(1)
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{Enabled: &en, MaxRetries: &mr, BaseDelayMs: &bd}
		})

		subA, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		_, _ = subA.Wait(nil)
		var uState durable.UsageState
		snap, _ := harness.Snapshot(nil, durable.UsageDoc, root.Id())
		_ = jsonx.Decode(snap, &uState)
		if !reflect.DeepEqual(uState.Tools["hold"], spent) {
			t.Fatalf("tools usage = %+v, want %+v", uState.Tools, spent)
		}

		subB, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("b")}, nil)
		var runTaskId durable.TaskId
		_ = testkit.WaitFor(func() (bool, error) {
			liveSnap, err := harness.Snapshot(nil, durable.LiveDoc, root.Id())
			if err != nil || liveSnap == nil {
				return false, err
			}
			var ls durable.LiveState
			if err := jsonx.Decode(liveSnap, &ls); err != nil {
				return false, err
			}
			if ls.Generation != nil && ls.Generation.Message != nil && ls.Run != nil {
				runTaskId = ls.Run.TaskId
				return true, nil
			}
			return false, nil
		})
		_, _ = harness.AbortTask(nil, runTaskId)
		_, _ = subB.Wait(nil)

		entries, _ := testkit.AllEntries(root)
		var reasons []ai.StopReason
		outputSum := 0
		for _, e := range entries {
			if e.Kind == "pi.assistant" && len(e.Model) > 0 {
				if am, ok := e.Model[0].(*ai.AssistantMessage); ok {
					reasons = append(reasons, am.StopReason)
					outputSum += am.Usage.Output
				}
			}
		}
		wantReasons := []ai.StopReason{ai.StopReasonError, ai.StopReasonToolUse, ai.StopReasonStop, ai.StopReasonAborted}
		if !slices.Equal(reasons, wantReasons) {
			t.Fatalf("assistant stopReasons = %v, want %v", reasons, wantReasons)
		}
		snap, _ = harness.Snapshot(nil, durable.UsageDoc, root.Id())
		uState = durable.UsageState{}
		_ = jsonx.Decode(snap, &uState)
		if uState.Models["faux/faux-1"].Output != outputSum {
			t.Fatalf("usage output = %d, want %d", uState.Models["faux/faux-1"].Output, outputSum)
		}
	})

	t.Run("keeps tools named like object prototype keys in the ledger and the Session total", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		usage := ai.Usage{Input: 1, Output: 1, TotalTokens: 2}
		names := []string{"constructor", "__proto__", "toString"}
		for _, name := range names {
			n := name
			if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
				return nil, durable.RecordUsageForTest(tx, root.Id(), "tools", n, usage)
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
				return nil, durable.RecordUsageForTest(tx, root.Id(), "tools", n, usage)
			}); err != nil {
				t.Fatal(err)
			}
		}
		total, err := harness.Usage(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if total.Tools[name].Output != 2 {
				t.Fatalf("tool %q output = %d, want 2", name, total.Tools[name].Output)
			}
		}
	})

	t.Run("keeps usage totals exact across reopen counting a partial converted after reopen once", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		tps := 200.0
		one := 1
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: &tps,
			TokenSize:       &providers.FauxTokenSizeOptions{Min: &one, Max: &one},
		})
		if err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: thinAnswer("first")},
			&providers.FauxResponseMessage{Message: thinAnswer(strings.Repeat("x", 400))},
			&providers.FauxResponseMessage{Message: thinAnswer("again")},
		})

		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h1, root1, err := testkit.OpenChat(st1, setup)
		if err != nil {
			t.Fatal(err)
		}
		s1, _ := root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		_, _ = s1.Wait(nil)
		_, _ = root1.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("b")}, nil)
		_ = testkit.WaitFor(func() (bool, error) {
			liveSnap, err := h1.Snapshot(nil, durable.LiveDoc, root1.Id())
			if err != nil || liveSnap == nil {
				return false, err
			}
			var ls durable.LiveState
			_ = jsonx.Decode(liveSnap, &ls)
			return ls.Generation != nil && ls.Generation.Message != nil, nil
		})
		_ = h1.Close(nil)

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h2, root2, err := testkit.OpenChat(st2, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h2.Close(nil) }()

		_ = h2.WaitForIdle(nil)
		entries, _ := testkit.AllEntries(root2)
		outputSum := 0
		for _, e := range entries {
			if e.Kind == "pi.assistant" && len(e.Model) > 0 {
				if am, ok := e.Model[0].(*ai.AssistantMessage); ok {
					outputSum += am.Usage.Output
				}
			}
		}
		total, err := h2.Usage(nil)
		if err != nil {
			t.Fatal(err)
		}
		if total.Models["faux/faux-1"].Output != outputSum {
			t.Fatalf("total output = %d, want %d", total.Models["faux/faux-1"].Output, outputSum)
		}
	})

	t.Run("commits each live document change together with the transcript work that caused it", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		gate := testkit.NewDeferred[struct{}]()
		gate.Resolve(struct{}{})
		thinHoldTool(t, setup, gate, nil)
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thinHoldCall(),
			&providers.FauxResponseMessage{Message: thinAnswer("done")},
		})
		harness, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		type commitCount struct {
			entries int
			live    bool
			usage   bool
		}
		var mu sync.Mutex
		var commits []commitCount
		harness.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
			var c commitCount
			for _, ch := range pub.Changes {
				if _, ok := ch.(durable.StorageWriteEntry); ok {
					c.entries++
				}
			}
			for _, dc := range testkit.DocumentChanges(pub) {
				if dc.Record.Kind == "pi.live" && dc.Value != nil {
					if g, ok := dc.Value.Get("generation"); ok && g != nil {
						if gm, ok := g.(*jsonx.Object); ok {
							if m, ok := gm.Get("message"); ok && m != nil {
								c.live = true
							}
						}
					}
				}
				if dc.Record.Kind == "pi.usage" {
					c.usage = true
				}
			}
			if c.usage {
				mu.Lock()
				commits = append(commits, c)
				mu.Unlock()
			}
		})

		sub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thinUserText("a")}, nil)
		_, _ = sub.Wait(nil)
		mu.Lock()
		got := slices.Clone(commits)
		mu.Unlock()
		if len(got) == 0 {
			t.Fatal("expected usage commits")
		}
		for i, c := range got {
			if c.entries < 1 {
				t.Fatalf("usage commit %d had %d entries, want >= 1", i, c.entries)
			}
		}
	})
}

func thinIsErrorType[T error](err error, target *T) bool {
	if err == nil {
		return false
	}
	v, ok := err.(T)
	if ok {
		*target = v
	}
	return ok
}

func thinPtr[T any](v T) *T { return &v }

func thinUserText(text string) ai.UserMessageContent {
	return ai.UserMessageContent{Text: &text}
}
