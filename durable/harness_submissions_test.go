// Ported from packages/durable/test/harness-submissions.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
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
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
)

type thsbHeldReads struct {
	*testkit.ControlledStorage
	mu      sync.Mutex
	hold    bool
	entered chan struct{}
	once    sync.Once
	release *testkit.Deferred[struct{}]
}

func (s *thsbHeldReads) Submission(ctx chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold {
		s.once.Do(func() { close(s.entered) })
		<-s.release.Done()
	}
	return s.ControlledStorage.Submission(ctx, id)
}

func TestHarnessSubmissions(t *testing.T) {
	t.Run("admits an input runs generation to its answer and returns entries oldest-first on replay", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		setup.SetNow(func() int64 { return 1000 })
		if _, err := testkit.AddSection(setup.Registry, "role", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "helpful", true, nil
		}); err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("hello back"),
			}, nil)},
		})
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thsbUserText("hi"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		settled, err := sub.Wait(nil)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("settled = %+v", settled)
		}

		all, err := testkit.AllEntries(root)
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, e := range all {
			kinds = append(kinds, e.Kind)
		}
		if !slices.Equal(kinds, []string{"pi.user", "pi.system", "pi.assistant"}) {
			t.Fatalf("entry kinds = %v", kinds)
		}
		if settled.Entry == nil || *settled.Entry != all[0].Id || settled.Answer == nil || *settled.Answer != all[2].Id {
			t.Fatalf("settled ids = entry:%v answer:%v all:%+v", settled.Entry, settled.Answer, all)
		}
		if len(all[2].Model) == 0 {
			t.Fatalf("expected assistant model payload")
		}
		if txt, ok := testkit.TextOf(all[2].Model[0]); !ok || txt != "hello back" {
			t.Fatalf("assistant text = %q ok=%v", txt, ok)
		}
		if st, err := sub.Status(nil); err != nil || st.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub.Status = %+v err=%v", st, err)
		}

		// Replaying conversation.Entries returns history newest-first by default, with Limit and Cursor pagination.
		firstPage, err := root.Entries(durable.ConversationEntryQuery{}, 2, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var firstKinds []string
		for _, e := range firstPage.Items {
			firstKinds = append(firstKinds, e.Kind)
		}
		if !slices.Equal(firstKinds, []string{"pi.assistant", "pi.system"}) || firstPage.Next == nil {
			t.Fatalf("firstPage = %+v", firstPage)
		}
		secondPage, err := root.Entries(durable.ConversationEntryQuery{}, 2, firstPage.Next, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(secondPage.Items) != 1 || secondPage.Items[0].Kind != "pi.user" || secondPage.Next != nil {
			t.Fatalf("secondPage = %+v", secondPage)
		}
	})

	t.Run("writes a passive entry without triggering generation and settles its submission", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"body": "remember"}},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		settled, err := sub.Wait(nil)
		if err != nil {
			t.Fatal(err)
		}
		if settled.Type != durable.SubmissionTypeWrite || settled.Status != durable.SubmissionStatusDone || settled.Entry == nil {
			t.Fatalf("settled = %+v", settled)
		}
		if setup.Faux.State.CallCount != 0 {
			t.Fatalf("CallCount = %d, want 0", setup.Faux.State.CallCount)
		}
		entries, _ := testkit.AllEntries(root)
		if len(entries) != 1 || entries[0].Kind != "note" || entries[0].Id != *settled.Entry {
			t.Fatalf("entries = %+v", entries)
		}
	})

	t.Run("deduplicates by conversation + requestId and rejects a mismatched type", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		busyStep, reached := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{busyStep})
		harness, root, err := testkit.OpenChat(st, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		first, err := root.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeInput,
			Content:   thsbUserText("hi"),
			RequestId: "r1",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		<-reached
		commits := len(st.Commits())

		// Deduplication runs before the busy check.
		again, err := root.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeInput,
			Content:   thsbUserText("different"),
			RequestId: "r1",
		}, nil)
		if err != nil || again.Id() != first.Id() {
			t.Fatalf("again=%v id=%v err=%v", again, first.Id(), err)
		}
		if len(st.Commits()) != commits {
			t.Fatalf("commits grew from %d to %d", commits, len(st.Commits()))
		}
		_, err = root.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeWrite,
			Entry:     &durable.EntryDraft{Kind: "note"},
			RequestId: "r1",
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "Request r1 already identifies a submission of type input") {
			t.Fatalf("expected type mismatch error, got %v", err)
		}
		statusRec, _ := first.Status(nil)
		if statusRec.RequestId == nil || *statusRec.RequestId != "r1" || statusRec.Status != durable.SubmissionStatusPlaced {
			t.Fatalf("first status = %+v", statusRec)
		}

		other, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		write, err := other.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeWrite,
			Entry:     &durable.EntryDraft{Kind: "note"},
			RequestId: "r1",
		}, nil)
		if err != nil || write.Id() == first.Id() {
			t.Fatalf("write=%v first=%v err=%v", write, first, err)
		}
		write2, err := other.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeWrite,
			Entry:     &durable.EntryDraft{Kind: "note"},
			RequestId: "r1",
		}, nil)
		if err != nil || write2.Id() != write.Id() {
			t.Fatalf("write2=%v write=%v err=%v", write2, write, err)
		}
	})

	t.Run("reports abort results and looks submissions up by conversation", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		release := testkit.NewDeferred[struct{}]()
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			providers.FauxResponseFactory(func(_ context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
				<-release.Done()
				return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("answer")}, nil), nil
			}),
		})
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		sub, err := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thsbUserText("hi"),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := sub.Abort(nil); err != nil || res != durable.SubmissionAbortAlreadyPlaced {
			t.Fatalf("Abort = %s err=%v", res, err)
		}
		if res, err := harness.AbortSubmission(nil, sub.Id(), root.Id()); err != nil || res != durable.SubmissionAbortAlreadyPlaced {
			t.Fatalf("AbortSubmission root = %s err=%v", res, err)
		}
		other, _ := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if res, err := harness.AbortSubmission(nil, sub.Id(), other.Id()); err != nil || res != durable.SubmissionAbortNotFound {
			t.Fatalf("AbortSubmission other = %s err=%v", res, err)
		}
		if res, err := harness.AbortSubmission(nil, durable.SubmissionId(999_999)); err != nil || res != durable.SubmissionAbortNotFound {
			t.Fatalf("AbortSubmission missing = %s err=%v", res, err)
		}
		if missing, err := harness.Submission(nil, durable.SubmissionId(999_999)); err != nil || missing != nil {
			t.Fatalf("Submission missing = %v err=%v", missing, err)
		}

		release.Resolve(struct{}{})
		_, _ = sub.Wait(nil)
		if res, err := sub.Abort(nil); err != nil || res != durable.SubmissionAbortSettled {
			t.Fatalf("sub.Abort after settle = %s err=%v", res, err)
		}
		if res, err := harness.AbortSubmission(nil, sub.Id()); err != nil || res != durable.SubmissionAbortSettled {
			t.Fatalf("AbortSubmission after settle = %s err=%v", res, err)
		}
	})

	t.Run("cancels only a wait and rejects pending waits on close", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		busyStep, _ := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{busyStep})
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}

		sub, _ := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thsbUserText("hi"),
		}, nil)
		cctx := chord.WithCancel(nil)
		cancelErr := testkit.NewDeferred[error]()
		go func() {
			_, err := sub.Wait(cctx.Context)
			cancelErr.Resolve(err)
		}()
		pendingErr := testkit.NewDeferred[error]()
		go func() {
			_, err := sub.Wait(nil)
			pendingErr.Resolve(err)
		}()
		cctx.Cancel(errors.New("stop waiting"))
		if err, _ := cancelErr.Wait(); err == nil {
			t.Fatal("expected cancelled wait error")
		}
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusPlaced {
			t.Fatalf("status = %+v", st)
		}
		if err := harness.Close(nil); err != nil {
			t.Fatal(err)
		}
		if err, _ := pendingErr.Wait(); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("pending wait err = %v", err)
		}
	})

	t.Run("rejects a wait whose submission read spans the start of close", func(t *testing.T) {
		st := &thsbHeldReads{
			ControlledStorage: testkit.NewControlledStorage(),
			entered:           make(chan struct{}),
			release:           testkit.NewDeferred[struct{}](),
		}
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		busyStep, _ := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{busyStep})
		harness, root, err := testkit.OpenChat(st, setup)
		if err != nil {
			t.Fatal(err)
		}

		sub, _ := root.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: thsbUserText("hi"),
		}, nil)
		st.mu.Lock()
		st.hold = true
		st.mu.Unlock()

		waitingErr := testkit.NewDeferred[error]()
		go func() {
			_, err := sub.Wait(nil)
			waitingErr.Resolve(err)
		}()
		<-st.entered

		closeDone := testkit.NewDeferred[error]()
		go func() {
			closeDone.Resolve(harness.Close(nil))
		}()
		st.release.Resolve(struct{}{})

		if err, _ := waitingErr.Wait(); err == nil || !strings.Contains(err.Error(), "Harness is closed") {
			t.Fatalf("waiting err = %v", err)
		}
		if err, _ := closeDone.Wait(); err != nil {
			t.Fatalf("close err = %v", err)
		}
	})

	t.Run("reacquires a submission after reopen and settles it durably", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		busyStep, reached := testkit.Unanswered()
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			busyStep,
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("after restart"),
			}, nil)},
		})

		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h1, root1, err := testkit.OpenChat(st1, setup)
		if err != nil {
			t.Fatal(err)
		}
		firstSub, _ := root1.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeInput,
			Content:   thsbUserText("hi"),
			RequestId: "print",
		}, nil)
		id := firstSub.Id()
		<-reached
		if err := h1.Close(nil); err != nil {
			t.Fatal(err)
		}

		st2, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h2, _, err := testkit.OpenChat(st2, setup)
		if err != nil {
			t.Fatal(err)
		}
		sub2, err := h2.Submission(nil, id)
		if err != nil || sub2 == nil {
			t.Fatalf("Submission after reopen: %v err=%v", sub2, err)
		}
		if st, _ := sub2.Status(nil); st.Status != durable.SubmissionStatusPlaced {
			t.Fatalf("status = %+v", st)
		}
		_ = h2.Resume()
		settled, err := sub2.Wait(nil)
		if err != nil || settled.Status != durable.SubmissionStatusDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		_ = h2.Close(nil)

		st3, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatal(err)
		}
		h3, root3, err := testkit.OpenChat(st3, setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = h3.Close(nil) }()

		sub3, _ := h3.Submission(nil, id)
		settled3, _ := sub3.Wait(nil)
		if !reflect.DeepEqual(settled3, settled) {
			t.Fatalf("reopened settled = %+v, want %+v", settled3, settled)
		}
		again, err := root3.Submit(durable.SubmissionDraft{
			Type:      durable.SubmissionTypeInput,
			Content:   thsbUserText("hi"),
			RequestId: "print",
		}, nil)
		if err != nil || again.Id() != id {
			t.Fatalf("again=%v want id=%d err=%v", again, id, err)
		}
	})

	t.Run("enables scheduling when a caller submits or waits", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatal(err)
		}
		setup.Faux.SetResponses([]providers.FauxResponseStep{
			&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxText("answer"),
			}, nil)},
		})
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		// No Resume(): submitting asks for progress.
		sub, _ := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: thsbUserText("hi")}, nil)
		if settled, err := sub.Wait(nil); err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		_ = harness.Close(nil)

		passiveSetup, _ := testkit.NewChatSetup()
		passive, passiveRoot, err := testkit.OpenChat(testkit.NewControlledStorage(), passiveSetup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = passive.Close(nil) }()

		tidAny, err := passiveRoot.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(durable.GenerationTask, durable.GenerationInput{}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		taskId := tidAny.(durable.TaskId)
		if rec, _ := passive.GetTask(nil, taskId); rec.State.Status != durable.TaskStatePending {
			t.Fatalf("expected pending task before wait, got %s", rec.State.Status)
		}
		if settledTask, err := passive.WaitForTask(nil, taskId); err != nil || settledTask.State.Status != durable.TaskStateTerminal {
			t.Fatalf("settledTask = %+v err=%v", settledTask, err)
		}
	})

	t.Run("settles submissions by their current record in the transaction", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup()
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		eAny, _ := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		entry := eAny.(*durable.EntryRecord).Id

		createSub := func(rec durable.SubmissionCreate) durable.SubmissionId {
			var id durable.SubmissionId
			if err := harness.Commit(nil, func(tx durable.Tx) error {
				created, err := tx.CreateSubmission(rec)
				if err == nil {
					id = created.Id
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return id
		}
		queued := createSub(durable.SubmissionCreate{ConversationId: root.Id(), Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued})
		write := createSub(durable.SubmissionCreate{ConversationId: root.Id(), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionStatusQueued})
		ans := durable.SubmissionSettlement{Status: durable.SubmissionStatusDone, Answer: &entry}

		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.SettleSubmission(queued, ans)
		}); err == nil || !strings.Contains(err.Error(), "is not a placed input") {
			t.Fatalf("expected error for queued input settle, got %v", err)
		}
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.SettleSubmission(write, ans)
		}); err == nil || !strings.Contains(err.Error(), "is not a placed input") {
			t.Fatalf("expected error for write settle, got %v", err)
		}
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return nil, tx.SettleSubmission(durable.SubmissionId(999_999), durable.SubmissionSettlement{
				Status: durable.SubmissionStatusUnanswered,
				Reason: thsbPtr("x"),
			})
		}); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("expected error for missing submission, got %v", err)
		}

		var placed durable.SubmissionId
		if err := harness.Commit(nil, func(tx durable.Tx) error {
			created, err := tx.CreateSubmission(durable.SubmissionCreate{
				ConversationId: root.Id(),
				Type:           durable.SubmissionTypeInput,
				Status:         durable.SubmissionStatusPlaced,
				Entry:          &entry,
			})
			if err != nil {
				return err
			}
			placed = created.Id
			if err := tx.SettleSubmission(placed, ans); err != nil {
				return err
			}
			return tx.SettleSubmission(placed, durable.SubmissionSettlement{
				Status: durable.SubmissionStatusUnanswered,
				Reason: thsbPtr("late"),
			})
		}); err != nil {
			t.Fatal(err)
		}
		sub, _ := harness.Submission(nil, placed)
		st, _ := sub.Status(nil)
		if st.Status != durable.SubmissionStatusDone || st.Entry == nil || *st.Entry != entry || st.Answer == nil || *st.Answer != entry {
			t.Fatalf("placed status = %+v", st)
		}
	})

	t.Run("appends and reads typed entries through tokens", func(t *testing.T) {
		counterTok, err := durable.DefineEntry("app.counter")
		if err != nil {
			t.Fatal(err)
		}
		markerTok, err := durable.DefineEntry("app.marker")
		if err != nil {
			t.Fatal(err)
		}
		setup, _ := testkit.NewChatSetup()
		harness, root, err := testkit.OpenChat(testkit.NewControlledStorage(), setup)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = harness.Close(nil) }()

		cAny, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendTypedEntry(counterTok, root.Id(), durable.EntryDraft{Data: map[string]any{"n": 1}})
		})
		if err != nil {
			t.Fatal(err)
		}
		counter := cAny.(*durable.EntryRecord)
		if counter.Kind != "app.counter" || counter.ConversationId != root.Id() {
			t.Fatalf("counter = %+v", counter)
		}

		mAny, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendTypedEntry(markerTok, root.Id(), durable.EntryDraft{})
		})
		if err != nil {
			t.Fatal(err)
		}
		marker := mAny.(*durable.EntryRecord)
		if marker.Kind != "app.marker" {
			t.Fatalf("marker = %+v", marker)
		}

		var readCounter, wrongKind, missing *durable.EntryRecord
		if _, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			var err error
			readCounter, err = tx.TypedEntry(counterTok, counter.Id)
			if err != nil {
				return nil, err
			}
			wrongKind, err = tx.TypedEntry(markerTok, counter.Id)
			if err != nil {
				return nil, err
			}
			missing, err = tx.TypedEntry(counterTok, durable.EntryId(999_999))
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(readCounter, counter) {
			t.Fatalf("readCounter=%+v counter=%+v", readCounter, counter)
		}
		if wrongKind != nil || missing != nil {
			t.Fatalf("expected nil wrongKind=%+v missing=%+v", wrongKind, missing)
		}
		if !counterTok.Is(counter) || durable.AssistantEntry.Is(counter) {
			t.Fatal("Entry.Is mismatch")
		}
		if durable.UserEntry.Kind != "pi.user" || durable.AssistantEntry.Kind != "pi.assistant" {
			t.Fatal("builtin entry kind mismatch")
		}
	})
}

func thsbPtr[T any](v T) *T { return &v }

func thsbUserText(text string) ai.UserMessageContent {
	return ai.UserMessageContent{Text: &text}
}
