// Ported from packages/durable/test/harness-generation.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func thgnPtr[T any](v T) *T { return &v }

type thgnModelsWrapper struct {
	ai.Models
	streamSimple   func(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream
	cancelDeferred func(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredCancelOptions) error
}

func (w *thgnModelsWrapper) StreamSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
	if w.streamSimple != nil {
		return w.streamSimple(ctx, model, c, options)
	}
	return w.Models.StreamSimple(ctx, model, c, options)
}

func (w *thgnModelsWrapper) CancelDeferred(ctx context.Context, model *ai.Model, handle *ai.DeferredHandle, options *ai.ModelsDeferredCancelOptions) error {
	if w.cancelDeferred != nil {
		return w.cancelDeferred(ctx, model, handle, options)
	}
	return w.Models.CancelDeferred(ctx, model, handle, options)
}

type thgnRegistryWithoutTask struct {
	base    durable.RegistrySnapshot
	omit    string
	subscri func(func()) func()
}

type thgnFilteredSnapshot struct {
	base durable.RegistrySnapshot
	omit string
}

func (s *thgnFilteredSnapshot) Installed() []*durable.Extension { return s.base.Installed() }
func (s *thgnFilteredSnapshot) Extension(name string) *durable.Extension {
	return s.base.Extension(name)
}
func (s *thgnFilteredSnapshot) Tools() []durable.RegistryTool       { return s.base.Tools() }
func (s *thgnFilteredSnapshot) Sections() []durable.RegistrySection { return s.base.Sections() }
func (s *thgnFilteredSnapshot) Tasks() []durable.AnyTask {
	var out []durable.AnyTask
	for _, task := range s.base.Tasks() {
		if task.TaskKind() != s.omit {
			out = append(out, task)
		}
	}
	return out
}
func (s *thgnFilteredSnapshot) Task(name string) durable.AnyTask {
	if name == s.omit {
		return nil
	}
	return s.base.Task(name)
}

func (r *thgnRegistryWithoutTask) Snapshot() durable.RegistrySnapshot {
	return &thgnFilteredSnapshot{base: r.base, omit: r.omit}
}
func (r *thgnRegistryWithoutTask) Subscribe(listener func()) func() {
	if r.subscri != nil {
		return r.subscri(listener)
	}
	return func() {}
}

func thgnError503() *providers.FauxResponseMessage {
	errStop := ai.StopReasonError
	msg := "503 Service Unavailable"
	return &providers.FauxResponseMessage{
		Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
			StopReason:   &errStop,
			ErrorMessage: &msg,
		}),
	}
}

func thgnLivePublications(h durable.Harness) func() []durable.LiveState {
	var mu sync.Mutex
	var values []durable.LiveState
	h.SubscribeCommits(func(pub durable.CommitPublication, _ chord.Context) {
		for _, ch := range pub.Changes {
			docCh, ok := ch.(durable.DocumentCommitChangeUpdate)
			if ok && docCh.Record.Kind == "pi.live" && docCh.Value != nil {
				var st durable.LiveState
				if jsonx.Decode(docCh.Value, &st) == nil {
					mu.Lock()
					values = append(values, st)
					mu.Unlock()
				}
			}
		}
	})
	return func() []durable.LiveState {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(values)
	}
}

func thgnOpenWithModels(t *testing.T, st durable.Storage, setup *testkit.ChatSetup, models ai.Models) (durable.Harness, durable.Conversation) {
	t.Helper()
	h, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   models,
		Registry: setup.Registry,
		Settings: setup.Settings,
		Now:      setup.Now,
	}, testkit.TestContext)
	if err != nil {
		t.Fatalf("HarnessOpen: %v", err)
	}
	root, err := h.Root(testkit.TestContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
		},
	})
	if err != nil {
		_ = h.Close(nil)
		t.Fatalf("Root: %v", err)
	}
	return h, root
}

func TestHarnessGeneration(t *testing.T) {
	t.Run("answers an input and settles its submission", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "You are helpful.", true, nil
		}, testkit.AddSectionOptions{Tag: thgnPtr(false)}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("Hello there")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil || settled.Entry == nil {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		if got := thgrAssistantText(t, h, *settled.Answer); got != "Hello there" {
			t.Fatalf("answer = %q", got)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.system", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
		if entries[0].Id != *settled.Entry {
			t.Fatalf("entries[0].Id = %d, want %d", entries[0].Id, *settled.Entry)
		}
		sysMsg, ok := entries[1].Model[0].(*ai.SystemMessage)
		if !ok || sysMsg.Sections == nil {
			t.Fatalf("entries[1] = %#v", entries[1].Model[0])
		}
		preambleVal, hasPreamble := sysMsg.Sections.Get("preamble")
		if !hasPreamble || preambleVal == nil || *preambleVal != "You are helpful." {
			t.Fatalf("preamble = %v (has=%v)", preambleVal, hasPreamble)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
		var tasks durable.Page[durable.TaskRecord, durable.Cursor]
		if err := h.Commit(nil, func(tx durable.Tx) error {
			var err error
			cid := root.Id()
			tasks, err = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 10, nil)
			return err
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if len(tasks.Items) == 0 || tasks.Items[0].Kind != "pi.generation" {
			t.Fatalf("tasks = %+v", tasks.Items)
		}
		task := tasks.Items[0]
		if entries[0].ByTaskId != nil ||
			entries[1].ByTaskId == nil || *entries[1].ByTaskId != task.Id ||
			entries[2].ByTaskId == nil || *entries[2].ByTaskId != task.Id {
			t.Fatalf("byTaskIds = [%v, %v, %v], task=%d", entries[0].ByTaskId, entries[1].ByTaskId, entries[2].ByTaskId, task.Id)
		}
		var res durable.GenerationResult
		resVal, _ := task.State.Outcome.Result.Get()
		if err := jsonx.Decode(resVal, &res); err != nil {
			t.Fatalf("Decode outcome result: %v", err)
		}
		if task.State.Status != durable.TaskStateTerminal ||
			task.State.Outcome == nil || task.State.Outcome.Status != durable.TaskOutcomeCompleted ||
			res.EntryId != *settled.Answer {
			t.Fatalf("task state = %+v, res=%+v", task.State, res)
		}
	})

	t.Run("stores partials as deltas and a complete base once nothing is in flight", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: thgnPtr(200.0),
			TokenSize:       &providers.FauxTokenSizeOptions{Min: thgnPtr(1), Max: thgnPtr(1)},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(strings.Repeat("w", 200))}, nil)},
		})
		cs := testkit.NewControlledStorage()
		h, root, err := testkit.OpenChat(cs, setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		var liveDocId durable.DocumentId
		for _, commit := range cs.Commits() {
			for _, w := range commit {
				if dc, ok := w.(durable.StorageWriteDocumentCreate); ok && dc.Record.Kind == "pi.live" && dc.Record.Scope.Kind == durable.DocumentScopeConversation && dc.Record.Scope.ConversationId == root.Id() {
					liveDocId = dc.Record.Id
				}
			}
		}
		if liveDocId == 0 {
			t.Fatalf("pi.live document create write not found")
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := thgrSubmitInput(t, root, "hi").Wait(nil); err != nil {
			t.Fatalf("Wait: %v", err)
		}
		var contents []durable.DocumentContentKind
		for _, commit := range cs.Commits() {
			for _, w := range commit {
				if ch, ok := w.(durable.StorageWriteDocumentChange); ok && ch.Id == liveDocId {
					contents = append(contents, ch.Content.Kind)
				}
			}
		}
		if !slices.Contains(contents, durable.DocumentContentDelta) {
			t.Fatalf("expected at least one delta write, got %v", contents)
		}
		if contents[len(contents)-1] != durable.DocumentContentBase {
			t.Fatalf("expected final write to be base, got %v", contents)
		}
	})

	t.Run("still ends a run whose input something else already settled", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		busyStep, busyReached := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{busyStep})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		<-busyReached
		reason := "withdrawn"
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.SettleSubmission(sub.Id(), durable.SubmissionSettlement{
				Status: durable.SubmissionStatusUnanswered,
				Reason: &reason,
			})
		}); err != nil {
			t.Fatalf("SettleSubmission: %v", err)
		}
		taskId := thgrRunTaskId(t, h, root.Id())
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settledTask, err := h.WaitForTask(nil, taskId)
		if err != nil || settledTask.State.Outcome == nil || settledTask.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("WaitForTask = %+v err=%v", settledTask, err)
		}
		st, err := sub.Status(nil)
		if err != nil || st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "withdrawn" {
			t.Fatalf("submission status = %+v err=%v", st, err)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
	})

	t.Run("fails with no_model when no model is configured or the model is unknown", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		plain, err := h.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		unset, err := thgrSubmitInput(t, plain, "hi").Wait(nil)
		if err != nil || unset.Status != durable.SubmissionStatusUnanswered || unset.Reason == nil || *unset.Reason != "no_model" {
			t.Fatalf("unset = %+v err=%v", unset, err)
		}

		if err := root.Configure(durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "missing"}),
		}, nil); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		unknown, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || unknown.Status != durable.SubmissionStatusUnanswered || unknown.Reason == nil || *unknown.Reason != "no_model" || unknown.Entry == nil {
			t.Fatalf("unknown = %+v err=%v", unknown, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil || !slices.Equal(thgrEntryKinds(entries), []string{"pi.user"}) {
			t.Fatalf("entries = %+v err=%v", entries, err)
		}
		var tasks durable.Page[durable.TaskRecord, durable.Cursor]
		if err := h.Commit(nil, func(tx durable.Tx) error {
			var err error
			cid := root.Id()
			tasks, err = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 10, nil)
			return err
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if len(tasks.Items) != 1 || tasks.Items[0].State.Status != durable.TaskStateTerminal ||
			tasks.Items[0].State.Outcome == nil || tasks.Items[0].State.Outcome.Status != durable.TaskOutcomeFailed ||
			tasks.Items[0].State.Outcome.Error == nil || tasks.Items[0].State.Outcome.Error.Message != "Model faux/missing is not available" ||
			!jsonx.DeepEqual(tasks.Items[0].State.Outcome.Error.Detail, jsonx.ObjectOf("reason", "no_model")) {
			t.Fatalf("task state = %+v", tasks.Items[0].State)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("root live = %v err=%v", raw, err)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, plain.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("plain live = %v err=%v", raw, err)
		}
	})

	t.Run("retries a retryable error after a durable backoff and then answers", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "p", true, nil
		}, testkit.AddSectionOptions{Tag: thgnPtr(false)}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thgnError503(),
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("recovered")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{
				Enabled:     thgnPtr(true),
				MaxRetries:  thgnPtr(3),
				BaseDelayMs: thgnPtr(int64(1)),
			}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		values := thgnLivePublications(h)
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.system", "pi.assistant", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
		if am, ok := entries[2].Model[0].(*ai.AssistantMessage); !ok || am.StopReason != ai.StopReasonError {
			t.Fatalf("entries[2] = %#v", entries[2].Model[0])
		}
		pubs := values()
		sawRetryErr := false
		sawAttempt2 := false
		for _, v := range pubs {
			if v.Generation != nil {
				if v.Generation.Retry != nil && v.Generation.Retry.Error == "503 Service Unavailable" {
					sawRetryErr = true
				}
				if v.Generation.Attempt == 2 {
					sawAttempt2 = true
				}
			}
		}
		if !sawRetryErr || !sawAttempt2 {
			t.Fatalf("sawRetryErr=%v sawAttempt2=%v pubs=%+v", sawRetryErr, sawAttempt2, pubs)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
	})

	t.Run("fails with model_error once retries are exhausted", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thgnError503(),
			thgnError503(),
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("never")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{
				Enabled:     thgnPtr(true),
				MaxRetries:  thgnPtr(1),
				BaseDelayMs: thgnPtr(int64(1)),
			}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered ||
			settled.Reason == nil || *settled.Reason != "model_error" ||
			settled.Detail != "503 Service Unavailable" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.assistant", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
		if got := setup.Faux.GetPendingResponseCount(); got != 1 {
			t.Fatalf("pending = %d, want 1", got)
		}
	})

	t.Run("fails a retryable error without retrying when the retry policy is disabled", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			thgnError503(),
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("never")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{Enabled: thgnPtr(false)}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "model_error" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		if setup.Faux.State.CallCount != 1 {
			t.Fatalf("CallCount = %d, want 1", setup.Faux.State.CallCount)
		}
	})

	t.Run("reports section wrapper failures while preparing", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "cwd", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "/repo", true, nil
		}); err != nil {
			t.Fatalf("AddSection: %v", err)
		}
		if err := setup.Registry.Install(durable.DefineExtension(&durable.Extension{
			Name: "broken",
			Wraps: []durable.Wrap{
				durable.WrapSection("cwd", func(_ *durable.PromptSection) *durable.PromptSection {
					panic(errors.New("wrapper failed"))
				}),
			},
		})); err != nil {
			t.Fatalf("Install: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("ok")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		saw := false
		for _, r := range setup.Reports() {
			if strings.Contains(r.Error(), "wrapper failed") {
				saw = true
			}
		}
		if !saw {
			t.Fatalf("reports = %v, want wrapper failed", setup.Reports())
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
	})

	t.Run("fails a non-retryable error without retrying", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		errStop := ai.StopReasonError
		msg := "Invalid request"
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
				StopReason:   &errStop,
				ErrorMessage: &msg,
			})},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("never")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered ||
			settled.Reason == nil || *settled.Reason != "model_error" ||
			settled.Detail != "Invalid request" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		var tasks durable.Page[durable.TaskRecord, durable.Cursor]
		if err := h.Commit(nil, func(tx durable.Tx) error {
			var err error
			cid := root.Id()
			tasks, err = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 10, nil)
			return err
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if len(tasks.Items) != 1 || tasks.Items[0].State.Outcome == nil ||
			tasks.Items[0].State.Outcome.Status != durable.TaskOutcomeFailed ||
			tasks.Items[0].State.Outcome.Error == nil ||
			tasks.Items[0].State.Outcome.Error.Message != "Invalid request" ||
			!jsonx.DeepEqual(tasks.Items[0].State.Outcome.Error.Detail, jsonx.ObjectOf("reason", "model_error")) {
			t.Fatalf("task state = %+v", tasks.Items[0].State)
		}
	})

	t.Run("polls a deferred response until it is ready", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{
				PendingFetches: thgnPtr(1),
				PollAfterMs:    thgnPtr(int64(1)),
			},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("deferred answer")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{Deferred: &durable.ConversationDeferredOptions{Enabled: true}}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		values := thgnLivePublications(h)
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		if setup.Faux.State.DeferredFetchCount != 2 {
			t.Fatalf("DeferredFetchCount = %d, want 2", setup.Faux.State.DeferredFetchCount)
		}
		var pollTimes []int64
		for _, v := range values() {
			if v.Generation != nil && v.Generation.Deferred != nil {
				pollTimes = append(pollTimes, v.Generation.Deferred.PollAt)
			}
		}
		if len(pollTimes) != 2 || pollTimes[1] <= pollTimes[0] {
			t.Fatalf("pollTimes = %v", pollTimes)
		}
		if got := thgrAssistantText(t, h, *settled.Answer); got != "deferred answer" {
			t.Fatalf("answer = %q", got)
		}
	})

	t.Run("converts the committed partial when aborted during streaming", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			TokensPerSecond: thgnPtr(20.0),
			TokenSize:       &providers.FauxTokenSizeOptions{Min: thgnPtr(1), Max: thgnPtr(1)},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(strings.Repeat("x", 400))}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		var taskId durable.TaskId
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h, root.Id())
			if snap.Run != nil {
				taskId = snap.Run.TaskId
			}
			if snap.Generation != nil && snap.Generation.Message != nil {
				txt, ok := testkit.TextOf(snap.Generation.Message)
				return ok && txt != "", nil
			}
			return false, nil
		}); err != nil {
			t.Fatalf("WaitFor partial: %v", err)
		}
		partialMsg := thgrLive(t, h, root.Id()).Generation.Message
		partialText, _ := testkit.TextOf(partialMsg)
		abortRes, err := h.AbortTask(nil, taskId)
		if err != nil || abortRes != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = %v err=%v", abortRes, err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatalf("AllEntries: %v", err)
		}
		if got := thgrEntryKinds(entries); !slices.Equal(got, []string{"pi.user", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", got)
		}
		am, ok := entries[1].Model[0].(*ai.AssistantMessage)
		if !ok || am.StopReason != ai.StopReasonAborted {
			t.Fatalf("entries[1] = %#v", entries[1].Model[0])
		}
		if convertedText, _ := testkit.TextOf(am); !strings.HasPrefix(convertedText, partialText) {
			t.Fatalf("converted = %q, want prefix %q", convertedText, partialText)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
		settledTask, err := h.WaitForTask(nil, taskId)
		if err != nil || settledTask.State.Outcome == nil || settledTask.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("WaitForTask = %+v err=%v", settledTask, err)
		}
	})

	t.Run("cancels a deferred response when aborted during polling", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{
				PendingFetches: thgnPtr(100),
				PollAfterMs:    thgnPtr(int64(60000)),
			},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("never")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{Deferred: &durable.ConversationDeferredOptions{Enabled: true}}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		var taskId durable.TaskId
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h, root.Id())
			if snap.Run != nil {
				taskId = snap.Run.TaskId
			}
			return snap.Generation != nil && snap.Generation.Deferred != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor deferred: %v", err)
		}
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		if len(setup.Faux.State.CancelledDeferred) != 1 {
			t.Fatalf("CancelledDeferred = %+v", setup.Faux.State.CancelledDeferred)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
	})

	t.Run("reports a failed deferred cancellation and still ends the run aborted", func(t *testing.T) {
		setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Deferred: &providers.FauxDeferredOptions{
				PendingFetches: thgnPtr(100),
				PollAfterMs:    thgnPtr(int64(60000)),
			},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("never")}, nil)},
		})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{Deferred: &durable.ConversationDeferredOptions{Enabled: true}}
		})
		var repMu sync.Mutex
		var reports []error
		wrapped := &thgnModelsWrapper{
			Models: setup.Models,
			cancelDeferred: func(_ context.Context, _ *ai.Model, _ *ai.DeferredHandle, _ *ai.ModelsDeferredCancelOptions) error {
				return errors.New("cancel failed")
			},
		}
		h, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
			Models:   wrapped,
			Registry: setup.Registry,
			Settings: setup.Settings,
			Now:      setup.Now,
			OnReport: func(err error) {
				repMu.Lock()
				reports = append(reports, err)
				repMu.Unlock()
			},
		}, testkit.TestContext)
		if err != nil {
			t.Fatalf("HarnessOpen: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		root, err := h.Root(testkit.TestContext, durable.RootOptions{
			Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		})
		if err != nil {
			t.Fatalf("Root: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		var taskId durable.TaskId
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h, root.Id())
			if snap.Run != nil {
				taskId = snap.Run.TaskId
			}
			return snap.Generation != nil && snap.Generation.Deferred != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor deferred: %v", err)
		}
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		repMu.Lock()
		gotReports := slices.Clone(reports)
		repMu.Unlock()
		sawCancel := false
		for _, r := range gotReports {
			if strings.Contains(r.Error(), "cancel failed") {
				sawCancel = true
			}
		}
		if !sawCancel {
			t.Fatalf("reports = %v, want cancel failed", gotReports)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
	})

	t.Run("forwards stream options and the thinking level", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		type seenOpts struct {
			ctxErr    bool
			hasSignal bool
			timeoutMs *int64
			headers   map[string]string
			reasoning *ai.ThinkingLevel
		}
		var mu sync.Mutex
		var seen []seenOpts
		record := func(msg string) providers.FauxResponseFactory {
			return func(ctx context.Context, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var s seenOpts
				s.hasSignal = ctx != nil
				if ctx != nil {
					s.ctxErr = ctx.Err() != nil
				}
				if options != nil {
					if options.TimeoutMs != nil {
						v := *options.TimeoutMs
						s.timeoutMs = &v
					}
					if options.Headers != nil {
						s.headers = make(map[string]string, options.Headers.Len())
						for k, v := range options.Headers.All() {
							if v != nil {
								s.headers[k] = *v
							}
						}
					}
					if options.Reasoning != nil {
						v := *options.Reasoning
						s.reasoning = &v
					}
				}
				mu.Lock()
				seen = append(seen, s)
				mu.Unlock()
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(msg)}, nil), nil
			}
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{record("a"), record("b")})
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{
				TimeoutMs: thgnPtr(int64(1234)),
				Headers:   map[string]string{"x-test": "1"},
			}
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if err := root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, nil); err != nil {
			t.Fatalf("Configure high: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := thgrSubmitInput(t, root, "one").Wait(nil); err != nil {
			t.Fatalf("Wait 1: %v", err)
		}
		if err := root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Null[ai.ModelThinkingLevel]()}, nil); err != nil {
			t.Fatalf("Configure null: %v", err)
		}
		setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{TimeoutMs: thgnPtr(int64(99))}
		})
		if _, err := thgrSubmitInput(t, root, "two").Wait(nil); err != nil {
			t.Fatalf("Wait 2: %v", err)
		}
		mu.Lock()
		got := slices.Clone(seen)
		mu.Unlock()
		if len(got) != 2 {
			t.Fatalf("seen = %+v", got)
		}
		if got[0].timeoutMs == nil || *got[0].timeoutMs != 1234 ||
			got[0].headers["x-test"] != "1" ||
			got[0].reasoning == nil || string(*got[0].reasoning) != "high" ||
			!got[0].hasSignal || got[0].ctxErr {
			t.Fatalf("seen[0] = %+v", got[0])
		}
		if got[1].reasoning != nil || got[1].timeoutMs == nil || *got[1].timeoutMs != 99 || len(got[1].headers) != 0 {
			t.Fatalf("seen[1] = %+v", got[1])
		}
	})

	t.Run("reads settings through getters at every decision", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		var mu sync.Mutex
		timeoutMs := int64(111)
		var seen []*int64
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var to *int64
				if options != nil && options.TimeoutMs != nil {
					v := *options.TimeoutMs
					to = &v
				}
				mu.Lock()
				seen = append(seen, to)
				timeoutMs = 222
				mu.Unlock()
				return thgnError503().Message, nil
			}),
			providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				var to *int64
				if options != nil && options.TimeoutMs != nil {
					v := *options.TimeoutMs
					to = &v
				}
				mu.Lock()
				seen = append(seen, to)
				mu.Unlock()
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("ok")}, nil), nil
			}),
		})
		h, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
			Models:   setup.Models,
			Registry: setup.Registry,
			Settings: func() durable.HarnessSettings {
				mu.Lock()
				v := timeoutMs
				mu.Unlock()
				return durable.HarnessSettings{
					Stream: &durable.ConversationStreamOptions{TimeoutMs: &v},
					Retry:  &durable.ConversationRetryPolicyPatch{BaseDelayMs: thgnPtr(int64(1))},
				}
			},
		}, testkit.TestContext)
		if err != nil {
			t.Fatalf("HarnessOpen: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		root, err := h.Root(testkit.TestContext, durable.RootOptions{
			Agent: &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
		})
		if err != nil {
			t.Fatalf("Root: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		mu.Lock()
		got := slices.Clone(seen)
		mu.Unlock()
		if len(got) != 2 || got[0] == nil || *got[0] != 111 || got[1] == nil || *got[1] != 222 {
			t.Fatalf("seen = %v, want [111, 222]", got)
		}
	})

	t.Run("resolves settings over the built-in defaults", func(t *testing.T) {
		def := durable.ResolveSettings(nil)
		if !def.Retry.Enabled || def.Retry.MaxRetries != 3 || def.Retry.BaseDelayMs != 2000 ||
			def.Retry.MaxAgentDelayMs == nil || *def.Retry.MaxAgentDelayMs != 60000 ||
			!def.Compaction.Enabled || def.Compaction.ReserveTokens != 16384 || def.Compaction.KeepRecentTokens != 20000 || def.Compaction.BackgroundTokens != 32768 ||
			def.ToolExecution != durable.ToolExecutionModeParallel ||
			def.SteeringMode != durable.QueueModeOneAtATime ||
			def.FollowUpMode != durable.QueueModeOneAtATime {
			t.Fatalf("ResolveSettings(nil) = %+v", def)
		}
		patched := durable.ResolveSettings(&durable.HarnessSettings{
			Retry:      &durable.ConversationRetryPolicyPatch{Enabled: thgnPtr(false)},
			Compaction: &durable.CompactionPolicyPatch{BackgroundTokens: thgnPtr(0)},
		})
		if patched.Retry.Enabled || patched.Retry.MaxRetries != 3 || patched.Retry.BaseDelayMs != 2000 ||
			!patched.Compaction.Enabled || patched.Compaction.BackgroundTokens != 0 {
			t.Fatalf("patched = %+v", patched)
		}
	})

	t.Run("renders sections that read conversation documents through input.read", func(t *testing.T) {
		agentDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.agent",
			Version: 1,
			Scope:   durable.DocumentScopeConversation,
			History: durable.DocumentHistoryLatest,
			Fork:    durable.DocumentForkCurrent,
			Initial: func() durable.JsonObject {
				return jsonx.ObjectOf("cwd", "/", "kind", "main")
			},
		})
		if err != nil {
			t.Fatalf("DefineDoc: %v", err)
		}
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "cwd", func(input durable.PromptInput, ctx chord.Context) (string, bool, error) {
			snap, err := input.Read.Snapshot(ctx, agentDoc, input.ConversationId)
			if err != nil || snap == nil {
				return "", false, err
			}
			cwd, ok := snap.Get("cwd")
			if !ok {
				return "", false, nil
			}
			s, isStr := cwd.(string)
			return s, isStr, nil
		}); err != nil {
			t.Fatalf("AddSection cwd: %v", err)
		}
		if _, err := testkit.AddSection(setup.Registry, "agents", func(input durable.PromptInput, ctx chord.Context) (string, bool, error) {
			snap, err := input.Read.Snapshot(ctx, agentDoc, input.ConversationId)
			if err != nil {
				return "", false, err
			}
			if snap != nil {
				if kind, _ := snap.Get("kind"); kind == "sub" {
					return "", false, nil
				}
			}
			return "Read AGENTS.md", true, nil
		}); err != nil {
			t.Fatalf("AddSection agents: %v", err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("a")}, nil)},
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("b")}, nil)},
		})
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			doc, err := tx.Doc(agentDoc, root.Id())
			if err != nil {
				return nil, err
			}
			doc.Value.Set("cwd", "/repo")
			return nil, nil
		}); err != nil {
			t.Fatalf("Commit root agentDoc: %v", err)
		}
		subConv, err := h.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Agent:     &durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				doc, err := tx.Doc(agentDoc, id)
				if err != nil {
					return err
				}
				doc.Value.Set("kind", "sub")
				doc.Value.Set("cwd", "/sub")
				return nil
			},
		}, nil)
		if err != nil {
			t.Fatalf("CreateConversation: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := thgrSubmitInput(t, root, "one").Wait(nil); err != nil {
			t.Fatalf("root Wait: %v", err)
		}
		if _, err := thgrSubmitInput(t, subConv, "two").Wait(nil); err != nil {
			t.Fatalf("sub Wait: %v", err)
		}
		readSys := func(conv durable.Conversation) *ai.SystemMessage {
			entries, err := testkit.AllEntries(conv)
			if err != nil {
				t.Fatalf("AllEntries: %v", err)
			}
			for _, e := range entries {
				if e.Kind == "pi.system" && len(e.Model) > 0 {
					return e.Model[0].(*ai.SystemMessage)
				}
			}
			t.Fatalf("missing pi.system in %v", entries)
			return nil
		}
		rootSys := readSys(root)
		cwdVal, _ := rootSys.Sections.Get("cwd")
		agentsVal, _ := rootSys.Sections.Get("agents")
		if cwdVal == nil || *cwdVal != "<cwd>\n/repo\n</cwd>" || agentsVal == nil || *agentsVal != "<agents>\nRead AGENTS.md\n</agents>" {
			t.Fatalf("root sections cwd=%v agents=%v", cwdVal, agentsVal)
		}
		subSys := readSys(subConv)
		subCwd, _ := subSys.Sections.Get("cwd")
		if subSys.Sections.Len() != 1 || subCwd == nil || *subCwd != "<cwd>\n/sub\n</cwd>" {
			t.Fatalf("sub sections = %+v", subSys.Sections)
		}
	})

	t.Run("commits no partial for a response that turns deferred after an empty start event", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		fauxModel := setup.Faux.GetModel()
		handle := &ai.DeferredHandle{
			Provider:    "faux",
			ModelId:     "faux-1",
			Api:         string(fauxModel.Api),
			Id:          "handle-1",
			PollAfterMs: thgnPtr(int64(60000)),
		}
		wrapped := &thgnModelsWrapper{
			Models: setup.Models,
			streamSimple: func(ctx context.Context, model *ai.Model, _ *ai.Context, _ *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
				s := ai.NewAssistantMessageEventStream()
				go func() {
					partial := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
						StopReason: thgnPtr(ai.StopReasonPending),
					})
					partial.Api = ai.Api(model.Api)
					partial.Provider = model.Provider
					partial.Model = model.Id
					s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
					select {
					case <-time.After(300 * time.Millisecond):
						final := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
							StopReason: thgnPtr(ai.StopReasonDeferred),
							Deferred:   handle,
						})
						final.Api = ai.Api(model.Api)
						final.Provider = model.Provider
						final.Model = model.Id
						s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonDeferred, Message: final})
						s.End(final)
					case <-ctx.Done():
						errMsg := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
							StopReason: thgnPtr(ai.StopReasonAborted),
						})
						s.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: errMsg})
						s.End(errMsg)
					}
				}()
				return s
			},
		}
		h, root := thgnOpenWithModels(t, storage.NewMemoryStorage(), setup, wrapped)
		defer func() { _ = h.Close(nil) }()
		values := thgnLivePublications(h)
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		sub := thgrSubmitInput(t, root, "hi")
		if err := testkit.WaitFor(func() (bool, error) {
			snap := thgrLive(t, h, root.Id())
			return snap.Generation != nil && snap.Generation.Deferred != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor deferred: %v", err)
		}
		for _, v := range values() {
			if v.Generation != nil && v.Generation.Message != nil {
				t.Fatalf("unexpected non-nil generation.message publication: %+v", v.Generation.Message)
			}
		}
		taskId := thgrRunTaskId(t, h, root.Id())
		if _, err := h.AbortTask(nil, taskId); err != nil {
			t.Fatalf("AbortTask: %v", err)
		}
		settled, err := sub.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered || settled.Reason == nil || *settled.Reason != "aborted" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		entries, err := testkit.AllEntries(root)
		if err != nil || !slices.Equal(thgrEntryKinds(entries), []string{"pi.user"}) {
			t.Fatalf("entries = %+v err=%v", entries, err)
		}
	})

	t.Run("faults a run task, settling its inputs and converting the committed partial", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		wrapped := &thgnModelsWrapper{
			Models: setup.Models,
			streamSimple: func(ctx context.Context, model *ai.Model, _ *ai.Context, _ *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream {
				s := ai.NewAssistantMessageEventStream()
				go func() {
					partial := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("partial")}, &providers.FauxAssistantMessageOptions{
						StopReason: thgnPtr(ai.StopReasonPending),
					})
					partial.Api = ai.Api(model.Api)
					partial.Provider = model.Provider
					partial.Model = model.Id
					s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: partial})
					select {
					case <-time.After(300 * time.Millisecond):
						badCall := providers.FauxToolCall("bad", jsonx.ObjectOf("invalid", func() {}), nil)
						final := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{badCall}, nil)
						final.Api = ai.Api(model.Api)
						final.Provider = model.Provider
						final.Model = model.Id
						s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: final})
						s.End(final)
					case <-ctx.Done():
						errMsg := providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
							StopReason: thgnPtr(ai.StopReasonAborted),
						})
						s.Push(&ai.AssistantMessageEventError{Type: "error", Reason: ai.StopReasonAborted, Error: errMsg})
						s.End(errMsg)
					}
				}()
				return s
			},
		}
		h, root := thgnOpenWithModels(t, storage.NewMemoryStorage(), setup, wrapped)
		defer func() { _ = h.Close(nil) }()
		values := thgnLivePublications(h)
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		settled, err := thgrSubmitInput(t, root, "hi").Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusUnanswered ||
			settled.Reason == nil || *settled.Reason != "faulted" ||
			settled.Detail == nil || settled.Detail == "" {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		sawPartial := false
		for _, v := range values() {
			if v.Generation != nil && v.Generation.Message != nil {
				if txt, _ := testkit.TextOf(v.Generation.Message); txt == "partial" {
					sawPartial = true
				}
			}
		}
		if !sawPartial {
			t.Fatalf("expected live publication with partial text")
		}
		entries, err := testkit.AllEntries(root)
		if err != nil || !slices.Equal(thgrEntryKinds(entries), []string{"pi.user", "pi.assistant"}) {
			t.Fatalf("entries = %+v err=%v", entries, err)
		}
		am, ok := entries[1].Model[0].(*ai.AssistantMessage)
		if !ok || am.StopReason != ai.StopReasonAborted {
			t.Fatalf("entries[1] = %#v", entries[1].Model[0])
		}
		if txt, _ := testkit.TextOf(am); txt != "partial" {
			t.Fatalf("entries[1] text = %q, want partial", txt)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
		var tasks durable.Page[durable.TaskRecord, durable.Cursor]
		if err := h.Commit(nil, func(tx durable.Tx) error {
			var err error
			cid := root.Id()
			tasks, err = tx.ScanTasks(durable.TaskQuery{ConversationId: &cid}, 10, nil)
			return err
		}); err != nil {
			t.Fatalf("ScanTasks: %v", err)
		}
		if len(tasks.Items) != 1 || tasks.Items[0].State.Status != durable.TaskStateTerminal ||
			tasks.Items[0].State.Outcome == nil || tasks.Items[0].State.Outcome.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("task state = %+v", tasks.Items[0].State)
		}
	})

	t.Run("orphans a blocked run task with full run cleanup", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		v2Gen := durable.DefineTask(durable.TaskDefinition[durable.GenerationInput, durable.GenerationCheckpoint, durable.GenerationResult, durable.GenerationHooks]{
			Name:    "pi.generation",
			Version: 2,
			Initial: durable.GenerationTask.Definition.Initial,
			Phases:  durable.GenerationTask.Definition.Phases,
			Abort:   durable.GenerationTask.Definition.Abort,
		})
		var taskId durable.TaskId
		var subId durable.SubmissionId
		if err := h.Commit(nil, func(tx durable.Tx) error {
			txt := "hi"
			entry, err := tx.AppendTypedEntry(durable.UserEntry, root.Id(), durable.EntryDraft{
				Model: ai.MessageList{&ai.UserMessage{
					Role:      "user",
					Content:   ai.UserMessageContent{Text: &txt},
					Timestamp: 1,
				}},
			})
			if err != nil {
				return err
			}
			sub, err := tx.CreateSubmission(durable.SubmissionCreate{
				ConversationId: root.Id(),
				Type:           durable.SubmissionTypeInput,
				Status:         durable.SubmissionStatusPlaced,
				Entry:          &entry.Id,
			})
			if err != nil {
				return err
			}
			cid := root.Id()
			taskId, err = tx.CreateTask(v2Gen, durable.GenerationInput{}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &cid,
			})
			if err != nil {
				return err
			}
			live, err := tx.Doc(durable.LiveDoc, root.Id())
			if err != nil {
				return err
			}
			live.Value.Set("run", jsonx.ObjectOf("taskId", float64(taskId), "inputs", []any{float64(sub.Id)}))
			subId = sub.Id
			return nil
		}); err != nil {
			t.Fatalf("Commit setup: %v", err)
		}
		if err := h.Resume(); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if _, err := root.Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  durable.UserInput{Text: thgnPtr("busy")},
			WhenBusy: durable.SubmissionWhenBusyReject,
		}, nil); err == nil || !strings.Contains(err.Error(), "is busy") {
			t.Fatalf("expected busy rejection, got %v", err)
		}
		abortRes, err := h.AbortTask(nil, taskId)
		if err != nil || abortRes != durable.TaskAbortMarked {
			t.Fatalf("AbortTask = %v err=%v", abortRes, err)
		}
		settledTask, err := h.WaitForTask(nil, taskId)
		if err != nil || settledTask.State.Outcome == nil ||
			settledTask.State.Outcome.Status != durable.TaskOutcomeOrphaned ||
			settledTask.State.Outcome.Reason == nil || *settledTask.State.Outcome.Reason != string(durable.TaskBlockedTaskTooOld) {
			t.Fatalf("WaitForTask = %+v err=%v", settledTask, err)
		}
		subHandle, err := h.Submission(nil, subId)
		if err != nil || subHandle == nil {
			t.Fatalf("Submission: %v", err)
		}
		subStatus, err := subHandle.Status(nil)
		if err != nil || subStatus.Status != durable.SubmissionStatusUnanswered ||
			subStatus.Reason == nil || *subStatus.Reason != string(durable.TaskBlockedTaskTooOld) {
			t.Fatalf("subStatus = %+v err=%v", subStatus, err)
		}
		if raw, err := h.Snapshot(nil, durable.LiveDoc, root.Id()); err != nil || !jsonx.DeepEqual(raw, jsonx.NewObject()) {
			t.Fatalf("live = %v err=%v", raw, err)
		}
	})

	t.Run("rejects a registry without the built-in tasks", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		empty := durable.CreateRegistry().Snapshot()
		reg := &thgnRegistryWithoutTask{base: empty, omit: "pi.generation"}
		_, err = durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
			Models:   setup.Models,
			Registry: reg,
		}, testkit.TestContext)
		if err == nil || !strings.Contains(err.Error(), "Registry lacks built-in tasks pi.generation") {
			t.Fatalf("expected missing task error, got %v", err)
		}
	})
}

var _ = atomic.Int32{}
