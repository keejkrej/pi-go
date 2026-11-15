// Ported from packages/durable/test/harness-generation-recovery.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func thgrPtr[T any](v T) *T { return &v }

func thgrSqlitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.sqlite")
}

func thgrOpen(t *testing.T, path string, setup *testkit.ChatSetup, modelsOverride ai.Models) (durable.Harness, durable.Conversation) {
	t.Helper()
	st, err := sqlite.OpenNodeSqliteStorage(path)
	if err != nil {
		t.Fatalf("OpenNodeSqliteStorage: %v", err)
	}
	if modelsOverride == nil {
		h, root, err := testkit.OpenChat(st, setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		return h, root
	}
	h, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   modelsOverride,
		Registry: setup.Registry,
		Settings: setup.Settings,
		Now:      setup.Now,
	}, testkit.TestContext)
	if err != nil {
		t.Fatalf("HarnessOpen: %v", err)
	}
	root, err := h.Root(testkit.TestContext)
	if err != nil {
		_ = h.Close(nil)
		t.Fatalf("Root: %v", err)
	}
	return h, root
}

func thgrLive(t *testing.T, h durable.Harness, cid durable.ConversationId) durable.LiveState {
	t.Helper()
	snap, err := h.Snapshot(nil, durable.LiveDoc, cid)
	if err != nil {
		t.Fatalf("Snapshot(LiveDoc): %v", err)
	}
	var out durable.LiveState
	if snap != nil {
		if err := jsonx.Decode(snap, &out); err != nil {
			t.Fatalf("Decode(LiveDoc): %v", err)
		}
	}
	return out
}

func thgrRunTaskId(t *testing.T, h durable.Harness, cid durable.ConversationId) durable.TaskId {
	t.Helper()
	live := thgrLive(t, h, cid)
	if live.Run == nil {
		t.Fatalf("expected active live.Run, got nil")
	}
	return live.Run.TaskId
}

func thgrCheckpoint(t *testing.T, h durable.Harness, taskId durable.TaskId) durable.GenerationCheckpoint {
	t.Helper()
	rec, err := h.GetTask(nil, taskId)
	if err != nil || rec == nil {
		t.Fatalf("GetTask(%d): %v err=%v", taskId, rec, err)
	}
	var cp durable.GenerationCheckpoint
	if err := jsonx.Decode(rec.State.Checkpoint, &cp); err != nil {
		t.Fatalf("Decode(Checkpoint): %v", err)
	}
	return cp
}

func thgrSubmitInput(t *testing.T, conv durable.Conversation, text string) durable.Submission {
	t.Helper()
	txt := text
	sub, err := conv.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: durable.UserInput{Text: &txt},
	}, nil)
	if err != nil {
		t.Fatalf("Submit(%q): %v", text, err)
	}
	return sub
}

func thgrEntryKinds(entries []durable.EntryRecord) []string {
	kinds := make([]string, len(entries))
	for i, e := range entries {
		kinds[i] = e.Kind
	}
	return kinds
}

func thgrAssistantText(t *testing.T, h durable.Harness, id durable.EntryId) string {
	t.Helper()
	var rec *durable.EntryRecord
	if err := h.Commit(nil, func(tx durable.Tx) error {
		var err error
		rec, err = tx.TypedEntry(durable.AssistantEntry, id)
		return err
	}); err != nil {
		t.Fatalf("TypedEntry(AssistantEntry, %d): %v", id, err)
	}
	if rec == nil || len(rec.Model) == 0 {
		t.Fatalf("missing AssistantEntry %d", id)
	}
	txt, _ := testkit.TextOf(rec.Model[0])
	return txt
}

func TestHarnessGenerationRecovery(t *testing.T) {
	t.Run("reruns preparation interrupted before its commit", func(t *testing.T) {
		path := thgrSqlitePath(t)
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		var calls atomic.Int32
		reached := testkit.NewDeferred[struct{}]()
		if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, ctx chord.Context) (string, bool, error) {
			if calls.Add(1) == 1 {
				reached.Resolve(struct{}{})
				return "", false, testkit.Aborted(ctx.AbortSignal())
			}
			return "p", true, nil
		}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("answer")}, nil)},
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub, err := root1.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: durable.UserInput{Text: thgrPtr("hi")},
		}, nil)
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
		if _, err := reached.Wait(); err != nil {
			t.Fatalf("reached.Wait: %v", err)
		}
		taskId := thgrRunTaskId(t, h1, root1.Id())
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, root2 := thgrOpen(t, path, setup, nil)
		defer func() { _ = h2.Close(nil) }()
		cp := thgrCheckpoint(t, h2, taskId)
		if cp.Phase != "prepare" || cp.Attempt != 1 {
			t.Fatalf("checkpoint = %+v, want prepare/1", cp)
		}
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		sub2, err := h2.Submission(nil, sub.Id())
		if err != nil || sub2 == nil {
			t.Fatalf("Submission(%d): %v", sub.Id(), err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root2)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.system", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
	})

	t.Run("resends a request interrupted before any partial without repeating preparation", func(t *testing.T) {
		path := thgrSqlitePath(t)
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "p", true, nil
		}, testkit.AddSectionOptions{Tag: thgrPtr(false)}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		reached := testkit.NewDeferred[struct{}]()
		var mu sync.Mutex
		var sent [][]string
		var timeouts []*int64
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				reached.Resolve(struct{}{})
				<-ctx.Done()
				return nil, ctx.Err()
			}),
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				roles := make([]string, len(transcript.Messages))
				for i, m := range transcript.Messages {
					roles[i] = m.GetRole()
				}
				var to *int64
				if options != nil && options.TimeoutMs != nil {
					v := *options.TimeoutMs
					to = &v
				}
				mu.Lock()
				sent = append(sent, roles)
				timeouts = append(timeouts, to)
				mu.Unlock()
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("answer")}, nil), nil
			}),
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{TimeoutMs: thgrPtr(int64(1234))}
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub := thgrSubmitInput(t, root1, "hi")
		if _, err := reached.Wait(); err != nil {
			t.Fatalf("reached.Wait: %v", err)
		}
		taskId := thgrRunTaskId(t, h1, root1.Id())
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, root2 := thgrOpen(t, path, setup, nil)
		defer func() { _ = h2.Close(nil) }()
		cp := thgrCheckpoint(t, h2, taskId)
		if cp.Phase != "request" || cp.Attempt != 1 ||
			cp.ThinkingLevel == nil || *cp.ThinkingLevel != ai.ModelThinkingLevelOff ||
			cp.StreamOptions == nil || cp.StreamOptions.TimeoutMs == nil || *cp.StreamOptions.TimeoutMs != 1234 {
			t.Fatalf("checkpoint = %+v", cp)
		}
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{TimeoutMs: thgrPtr(int64(999))}
		})
		liveSnap := thgrLive(t, h2, root2.Id())
		if liveSnap.Generation == nil || liveSnap.Generation.Attempt != 1 {
			t.Fatalf("live.Generation = %+v", liveSnap.Generation)
		}
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		sub2, err := h2.Submission(nil, sub.Id())
		if err != nil || sub2 == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		mu.Lock()
		gotSent := slices.Clone(sent)
		gotTimeouts := slices.Clone(timeouts)
		mu.Unlock()
		if len(gotSent) != 1 || !slices.Equal(gotSent[0], []string{"user", "system"}) {
			t.Fatalf("sent = %v", gotSent)
		}
		if len(gotTimeouts) != 1 || gotTimeouts[0] == nil || *gotTimeouts[0] != 1234 {
			t.Fatalf("timeouts = %v, want [1234]", gotTimeouts)
		}
	})

	t.Run("converts a committed partial into an aborted entry and resends the same messages", func(t *testing.T) {
		path := thgrSqlitePath(t)
		slow, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: thgrPtr(20.0),
			TokenSize:       &providers.FauxTokenSizeOptions{Min: thgrPtr(1), Max: thgrPtr(1)},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		slow.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(strings.Repeat("z", 400))}, nil)},
		})

		h1, root1 := thgrOpen(t, path, slow, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		var wmu sync.Mutex
		var watched string
		gotPartial := make(chan struct{})
		var once sync.Once
		watch, err := h1.WatchDoc(nil, durable.LiveDoc, root1.Id())
		if err != nil {
			t.Fatalf("WatchDoc: %v", err)
		}
		defer watch.Stop()
		if err := watch.Start(func(value durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			var state durable.LiveState
			if value != nil && jsonx.Decode(value, &state) == nil && state.Generation != nil && state.Generation.Message != nil {
				if txt, ok := testkit.TextOf(state.Generation.Message); ok && txt != "" {
					wmu.Lock()
					if watched == "" {
						watched = txt
					}
					wmu.Unlock()
					once.Do(func() { close(gotPartial) })
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("watch.Start: %v", err)
		}
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub := thgrSubmitInput(t, root1, "hi")
		<-gotPartial
		wmu.Lock()
		gotWatched := watched
		wmu.Unlock()
		watch.Stop()
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		fresh, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup fresh: %v", err)
		}
		var smu sync.Mutex
		var sent [][]string
		fresh.Faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, transcript *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				roles := make([]string, len(transcript.Messages))
				for i, m := range transcript.Messages {
					roles[i] = m.GetRole()
				}
				smu.Lock()
				sent = append(sent, roles)
				smu.Unlock()
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("answer")}, nil), nil
			}),
		})

		h2, root2 := thgrOpen(t, path, fresh, nil)
		defer func() { _ = h2.Close(nil) }()
		liveSnap := thgrLive(t, h2, root2.Id())
		if liveSnap.Generation == nil || liveSnap.Generation.Message == nil {
			t.Fatalf("expected persisted partial on reopen, got %+v", liveSnap.Generation)
		}
		partial, _ := testkit.TextOf(liveSnap.Generation.Message)
		if !strings.HasPrefix(partial, gotWatched) {
			t.Fatalf("partial = %q, want prefix %q", partial, gotWatched)
		}
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		sub2, err := h2.Submission(nil, sub.Id())
		if err != nil || sub2 == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		smu.Lock()
		gotSent := slices.Clone(sent)
		smu.Unlock()
		if len(gotSent) != 1 || !slices.Equal(gotSent[0], []string{"user"}) {
			t.Fatalf("sent = %v, want [[user]]", gotSent)
		}
		entries, err := testkit.AllEntries(root2)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
		am, ok := entries[1].Model[0].(*ai.AssistantMessage)
		if !ok || am.StopReason != ai.StopReasonAborted {
			t.Fatalf("entries[1] = %#v, want AssistantMessage aborted", entries[1].Model[0])
		}
		if txt, _ := testkit.TextOf(am); txt != partial {
			t.Fatalf("entries[1] text = %q, want %q", txt, partial)
		}
		if raw, err := h2.Snapshot(nil, durable.LiveDoc, root2.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("final LiveDoc = %v err=%v, want {}", raw, err)
		}
	})

	t.Run("resumes a retry backoff after reopen", func(t *testing.T) {
		path := thgrSqlitePath(t)
		var now atomic.Int64
		now.Store(1000)
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.SetNow(now.Load)
		errStop := ai.StopReasonError
		errMsg := "503 Service Unavailable"
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
				StopReason:   &errStop,
				ErrorMessage: &errMsg,
			})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("recovered")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{
				Enabled:     thgrPtr(true),
				MaxRetries:  thgrPtr(2),
				BaseDelayMs: thgrPtr(int64(60000)),
			}
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub := thgrSubmitInput(t, root1, "hi")
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h1, root1.Id())
			return snap.Generation != nil && snap.Generation.Retry != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor retry: %v", err)
		}
		taskId := thgrRunTaskId(t, h1, root1.Id())
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, root2 := thgrOpen(t, path, setup, nil)
		defer func() { _ = h2.Close(nil) }()
		cp := thgrCheckpoint(t, h2, taskId)
		if cp.Phase != "retry" || cp.Attempt != 1 || cp.Until == nil || *cp.Until != 61000 {
			t.Fatalf("checkpoint = %+v", cp)
		}
		liveSnap := thgrLive(t, h2, root2.Id())
		if liveSnap.Run == nil || liveSnap.Run.TaskId != taskId || !slices.Equal(liveSnap.Run.Inputs, []durable.SubmissionId{sub.Id()}) ||
			liveSnap.Generation == nil || liveSnap.Generation.Attempt != 1 ||
			liveSnap.Generation.Retry == nil || liveSnap.Generation.Retry.At != 61000 || liveSnap.Generation.Retry.Error != "503 Service Unavailable" {
			t.Fatalf("liveSnap = %+v", liveSnap)
		}

		now.Store(61000)
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		sub2, err := h2.Submission(nil, sub.Id())
		if err != nil || sub2 == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root2)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
	})

	t.Run("resumes polling a deferred response after reopen", func(t *testing.T) {
		path := thgrSqlitePath(t)
		var now atomic.Int64
		now.Store(1000)
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{PollAfterMs: thgrPtr(int64(60000))},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.SetNow(now.Load)
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{
				Deferred: &durable.ConversationDeferredOptions{Enabled: true},
			}
		})
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("deferred answer")}, nil)},
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub := thgrSubmitInput(t, root1, "hi")
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h1, root1.Id())
			return snap.Generation != nil && snap.Generation.Deferred != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor deferred: %v", err)
		}
		taskId := thgrRunTaskId(t, h1, root1.Id())
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, _ := thgrOpen(t, path, setup, nil)
		defer func() { _ = h2.Close(nil) }()
		cp := thgrCheckpoint(t, h2, taskId)
		if cp.Phase != "poll" || cp.Attempt != 1 || cp.PollAt == nil || *cp.PollAt != 61000 {
			t.Fatalf("checkpoint = %+v", cp)
		}
		now.Store(61000)
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		sub2, err := h2.Submission(nil, sub.Id())
		if err != nil || sub2 == nil {
			t.Fatalf("Submission: %v", err)
		}
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone || settled.Answer == nil {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		if got := thgrAssistantText(t, h2, *settled.Answer); got != "deferred answer" {
			t.Fatalf("answer = %q, want %q", got, "deferred answer")
		}
		if setup.Faux.State.DeferredFetchCount != 1 {
			t.Fatalf("DeferredFetchCount = %d, want 1", setup.Faux.State.DeferredFetchCount)
		}
	})

	t.Run("fails no_model when the pinned model is gone after reopen, in request and in poll", func(t *testing.T) {
		path := thgrSqlitePath(t)
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{PollAfterMs: thgrPtr(int64(60000))},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		busyStep, busyReached := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			busyStep,
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("deferred")}, nil)},
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub1 := thgrSubmitInput(t, root1, "one")
		<-busyReached
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, _ := thgrOpen(t, path, setup, ai.CreateModels(nil))
		closed2 := false
		defer func() {
			if !closed2 {
				_ = h2.Close(nil)
			}
		}()
		if err := h2.Resume(); err != nil {
			t.Fatalf("Resume 2: %v", err)
		}
		reopened1, err := h2.Submission(nil, sub1.Id())
		if err != nil || reopened1 == nil {
			t.Fatalf("Submission 1: %v", err)
		}
		settled1, err := reopened1.Wait(nil)
		if err != nil || settled1.Status != durable.SubmissionStatusUnanswered || settled1.Reason == nil || *settled1.Reason != "no_model" {
			t.Fatalf("settled1 = %+v err=%v", settled1, err)
		}
		if err := h2.Close(nil); err != nil {
			t.Fatalf("Close 2: %v", err)
		}
		closed2 = true

		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{
				Deferred: &durable.ConversationDeferredOptions{Enabled: true},
			}
		})
		h3, root3 := thgrOpen(t, path, setup, nil)
		closed3 := false
		defer func() {
			if !closed3 {
				_ = h3.Close(nil)
			}
		}()
		if err := h3.Resume(); err != nil {
			t.Fatalf("Resume 3: %v", err)
		}
		sub2 := thgrSubmitInput(t, root3, "two")
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h3, root3.Id())
			return snap.Generation != nil && snap.Generation.Deferred != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor deferred 2: %v", err)
		}
		if err := h3.Close(nil); err != nil {
			t.Fatalf("Close 3: %v", err)
		}
		closed3 = true

		h4, _ := thgrOpen(t, path, setup, ai.CreateModels(nil))
		defer func() { _ = h4.Close(nil) }()
		if err := h4.Resume(); err != nil {
			t.Fatalf("Resume 4: %v", err)
		}
		reopened2, err := h4.Submission(nil, sub2.Id())
		if err != nil || reopened2 == nil {
			t.Fatalf("Submission 2: %v", err)
		}
		settled2, err := reopened2.Wait(nil)
		if err != nil || settled2.Status != durable.SubmissionStatusUnanswered || settled2.Reason == nil || *settled2.Reason != "no_model" {
			t.Fatalf("settled2 = %+v err=%v", settled2, err)
		}
	})

	t.Run("runs a print-style turn and reads the durable answer after reopen", func(t *testing.T) {
		path := thgrSqlitePath(t)
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "You are terse.", true, nil
		}, testkit.AddSectionOptions{Tag: thgrPtr(false)}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("42")}, nil)},
		})

		h1, root1 := thgrOpen(t, path, setup, nil)
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		if err := h1.Resume(); err != nil {
			t.Fatalf("Resume 1: %v", err)
		}
		sub := thgrSubmitInput(t, root1, "answer?")
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone || settled.Answer == nil {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		answer := thgrAssistantText(t, h1, *settled.Answer)
		if answer != "42" {
			t.Fatalf("answer = %q, want %q", answer, "42")
		}
		if err := h1.Close(nil); err != nil {
			t.Fatalf("Close 1: %v", err)
		}
		closed1 = true

		h2, _ := thgrOpen(t, path, setup, nil)
		defer func() { _ = h2.Close(nil) }()
		reopened, err := h2.Submission(nil, sub.Id())
		if err != nil || reopened == nil {
			t.Fatalf("Submission: %v", err)
		}
		gotStatus, err := reopened.Status(nil)
		if err != nil || gotStatus == nil || gotStatus.Status != settled.Status ||
			gotStatus.Answer == nil || *gotStatus.Answer != *settled.Answer {
			t.Fatalf("status = %+v, want %+v err=%v", gotStatus, settled, err)
		}
		if got := thgrAssistantText(t, h2, *settled.Answer); got != answer {
			t.Fatalf("reopened answer = %q, want %q", got, answer)
		}
	})
}
