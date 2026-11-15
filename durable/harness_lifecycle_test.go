// Ported from packages/durable/test/harness-lifecycle.test.ts (pi v1.0.0).

package durable_test

import (
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
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

type thlcStep struct {
	Phase string `json:"phase"`
}

func thlcOneStep(name string, run func() error) *durable.Task[any, thlcStep, any, struct{}] {
	return durable.DefineTask(durable.TaskDefinition[any, thlcStep, any, struct{}]{
		Name:    name,
		Version: 1,
		Initial: func(_ any) thlcStep { return thlcStep{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[any, thlcStep, any, struct{}]{
			"run": func(ctx chord.Context, _ durable.RunningTask[any, thlcStep], runtime *durable.TaskRuntime[any, thlcStep, any, struct{}]) error {
				if run != nil {
					if err := run(); err != nil {
						return err
					}
				}
				return runtime.Commit(ctx, func(_ durable.Tx, _ durable.RunningTask[any, thlcStep]) (*durable.NextTaskState[thlcStep, any], error) {
					next := testkit.Completed[thlcStep, any](thlcStep{Phase: "run"}, nil)
					return &next, nil
				})
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[any, thlcStep], _ *durable.TaskRuntime[any, thlcStep, any, struct{}]) error {
			return nil
		},
	})
}

func thlcStart(t *testing.T, conv durable.Conversation, task durable.AnyTask, background ...bool) durable.TaskId {
	t.Helper()
	bg := len(background) > 0 && background[0]
	raw, err := conv.Commit(nil, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(task, nil, durable.TaskOptions{
			Ownership:  durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			Background: &bg,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw.(durable.TaskId)
}

func thlcStorageOpen(st durable.Storage) bool {
	_, err := st.Task(nil, durable.TaskId(1))
	return err == nil
}

func thlcSeedRunningTask(t *testing.T, st durable.Storage) {
	t.Helper()
	cidRaw, err := st.MintId()
	if err != nil {
		t.Fatal(err)
	}
	tidRaw, err := st.MintId()
	if err != nil {
		t.Fatal(err)
	}
	cid := durable.ConversationId(cidRaw)
	tid := durable.TaskId(tidRaw)
	if _, err := st.Commit(nil, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: cid},
		},
		durable.StorageWriteTask{
			Type: durable.StorageWriteTypeTask,
			Value: durable.TaskRecord{
				Id:             tid,
				ConversationId: cid,
				Kind:           "test.seeded",
				Version:        1,
				State: durable.TaskState{
					Status:     durable.TaskStateRunning,
					Checkpoint: jsonx.ObjectOf("phase", "run"),
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

type thlcFailingClose struct {
	*testkit.ControlledStorage
}

func (s *thlcFailingClose) Close(ctx chord.Context) error {
	_ = s.ControlledStorage.Close(ctx)
	return errors.New("close failed")
}

func thlcAsyncErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- fn()
	}()
	return ch
}

func thlcAsyncDone(fn func() error) (<-chan struct{}, func() error) {
	done := make(chan struct{})
	var err error
	go func() {
		err = fn()
		close(done)
	}()
	return done, func() error {
		<-done
		return err
	}
}

func TestHarnessLifecycle(t *testing.T) {
	t.Run("Harness open closes without cancelled caller context and rethrows original error", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		thlcSeedRunningTask(t, st)
		reader := testkit.NewCountingReader(durable.CreateRegistry())
		held := st.HoldCommits()
		st.FailNextCommit(errors.New("disk full"))
		child := chord.WithCancel(nil)

		opening := thlcAsyncErr(func() error {
			_, err := durable.HarnessOpen(st, durable.HarnessOptions{
				Models:   ai.CreateModels(nil),
				Registry: reader,
			}, child.Context)
			return err
		})
		<-held.Entered
		child.Cancel(errors.New("caller gave up"))
		held.Release()

		if err := <-opening; err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("expected disk full error, got %v", err)
		}
		if reader.Subscriptions() != 0 {
			t.Fatalf("reader.Subscriptions() = %d, want 0", reader.Subscriptions())
		}
		if thlcStorageOpen(st) {
			t.Fatalf("expected storage to be closed")
		}
	})

	t.Run("Harness open reports a failing close and still rethrows open error", func(t *testing.T) {
		st := &thlcFailingClose{ControlledStorage: testkit.NewControlledStorage()}
		thlcSeedRunningTask(t, st)
		st.FailNextCommit(errors.New("disk full"))

		var mu sync.Mutex
		var reports []string
		_, err := durable.HarnessOpen(st, durable.HarnessOptions{
			Models:   ai.CreateModels(nil),
			Registry: durable.CreateRegistry(),
			OnReport: func(e error) {
				mu.Lock()
				reports = append(reports, e.Error())
				mu.Unlock()
			},
		}, nil)
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("expected disk full, got %v", err)
		}
		mu.Lock()
		got := slices.Clone(reports)
		mu.Unlock()
		if !slices.Equal(got, []string{"close failed"}) {
			t.Fatalf("reports = %v, want [close failed]", got)
		}
	})

	t.Run("Harness close joins a task handler that ignores its signal before closing Storage", func(t *testing.T) {
		st := storage.NewMemoryStorage()
		reached := testkit.NewDeferred[struct{}]()
		gate := testkit.NewDeferred[struct{}]()
		var readAfterRelease bool
		stubborn := thlcOneStep("test.close-stubborn", func() error {
			reached.Resolve(struct{}{})
			<-gate.Done()
			readAfterRelease = thlcStorageOpen(st)
			return nil
		})
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{stubborn})
		if err != nil {
			t.Fatal(err)
		}
		root, _ := opened.Harness.Root(nil)
		thlcStart(t, root, stubborn)
		if err := opened.Harness.Resume(); err != nil {
			t.Fatal(err)
		}
		<-reached.Done()

		closingDone, waitClose := thlcAsyncDone(func() error { return opened.Harness.Close(nil) })
		if testkit.SettledChannel(closingDone) {
			t.Fatal("expected Close to wait for stubborn handler")
		}
		if !thlcStorageOpen(st) {
			t.Fatal("expected storage still open while handler runs")
		}
		gate.Resolve(struct{}{})
		if err := waitClose(); err != nil {
			t.Fatal(err)
		}
		if !readAfterRelease {
			t.Fatal("expected storage open right after gate release")
		}
		if thlcStorageOpen(st) {
			t.Fatal("expected storage closed after Close returns")
		}
	})

	t.Run("Harness close joins a tool execute and a hook that ignore their signal before closing Storage", func(t *testing.T) {
		for _, where := range []string{"tool", "hook"} {
			t.Run(where, func(t *testing.T) {
				setup, _ := testkit.NewChatSetup()
				st := storage.NewMemoryStorage()
				reached := testkit.NewDeferred[struct{}]()
				gate := testkit.NewDeferred[struct{}]()
				var readAfterRelease bool
				stubborn := func() {
					reached.Resolve(struct{}{})
					<-gate.Done()
					readAfterRelease = thlcStorageOpen(st)
				}
				if _, err := testkit.AddTool(setup.Registry, durable.DefineTool(&durable.ToolRegistration{
					Name:        "wait",
					Description: "wait",
					Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
					Execute: func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
						if where == "tool" {
							stubborn()
						}
						return &durable.ToolExecutionResult{}, nil
					},
				})); err != nil {
					t.Fatal(err)
				}
				if _, err := testkit.AddHooks(setup.Registry, durable.GenerationTask, durable.GenerationHooks{
					BeforeRequest: func(req durable.GenerationRequest, _ durable.HookApi, _ chord.Context) (*durable.GenerationRequest, error) {
						if where == "hook" {
							stubborn()
						}
						return &req, nil
					},
				}); err != nil {
					t.Fatal(err)
				}
				setup.Faux.SetResponses([]providers.FauxResponseStep{
					&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
						providers.FauxToolCall("wait", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: thlcPtr("c1")}),
					}, &providers.FauxAssistantMessageOptions{StopReason: thlcPtr(ai.StopReasonToolUse)})},
					&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
						providers.FauxText("done"),
					}, nil)},
				})
				harness, root, err := testkit.OpenChat(st, setup)
				if err != nil {
					t.Fatal(err)
				}
				text := "go"
				if _, err := root.Submit(durable.SubmissionDraft{
					Type:    durable.SubmissionTypeInput,
					Content: durable.UserInput{Text: &text},
				}, nil); err != nil {
					t.Fatal(err)
				}
				<-reached.Done()

				closingDone, waitClose := thlcAsyncDone(func() error { return harness.Close(nil) })
				if testkit.SettledChannel(closingDone) {
					t.Fatalf("expected Close to wait for %s", where)
				}
				if !thlcStorageOpen(st) {
					t.Fatal("expected storage still open")
				}
				gate.Resolve(struct{}{})
				if err := waitClose(); err != nil {
					t.Fatal(err)
				}
				if !readAfterRelease {
					t.Fatal("expected readAfterRelease = true")
				}
				if thlcStorageOpen(st) {
					t.Fatal("expected storage closed after Close")
				}
			})
		}
	})

	t.Run("lets a new Harness open the same Storage once close resolved: no old invocation code runs", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "session.sqlite")
		var mu sync.Mutex
		var log []string
		gate := testkit.NewDeferred[struct{}]()
		gen := 1
		stubborn := thlcOneStep("test.generations", func() error {
			mu.Lock()
			mine := gen
			log = append(log, "start "+string(rune('0'+mine)))
			mu.Unlock()
			if mine == 1 {
				<-gate.Done()
			}
			mu.Lock()
			log = append(log, "end "+string(rune('0'+mine)))
			mu.Unlock()
			return nil
		})

		s1, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		first, err := testkit.OpenTasks(s1, []durable.AnyTask{stubborn})
		if err != nil {
			t.Fatal(err)
		}
		root1, _ := first.Harness.Root(nil)
		id := thlcStart(t, root1, stubborn)
		_ = first.Harness.Resume()
		if err := testkit.Eventually(func() (bool, error) {
			mu.Lock()
			n := len(log)
			mu.Unlock()
			return n == 1, nil
		}); err != nil {
			t.Fatal(err)
		}
		closingDone, waitClose := thlcAsyncDone(func() error {
			if err := first.Harness.Close(nil); err != nil {
				return err
			}
			mu.Lock()
			log = append(log, "closed")
			mu.Unlock()
			return nil
		})
		if testkit.SettledChannel(closingDone) {
			t.Fatal("expected close to wait for task 1")
		}
		gate.Resolve(struct{}{})
		if err := waitClose(); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got1 := slices.Clone(log)
		mu.Unlock()
		if !slices.Equal(got1, []string{"start 1", "end 1", "closed"}) {
			t.Fatalf("log1 = %v", got1)
		}

		mu.Lock()
		gen = 2
		mu.Unlock()
		s2, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		second, err := testkit.OpenTasks(s2, []durable.AnyTask{stubborn})
		if err != nil {
			t.Fatal(err)
		}
		_ = second.Harness.Resume()
		if _, err := second.Harness.WaitForTask(nil, id); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got2 := slices.Clone(log)
		mu.Unlock()
		if !slices.Equal(got2, []string{"start 1", "end 1", "closed", "start 2", "end 2"}) {
			t.Fatalf("log2 = %v", got2)
		}
		_ = second.Harness.Close(nil)
	})

	t.Run("keeps shutting down after a cancelled close and second close awaits the same shutdown", func(t *testing.T) {
		st := storage.NewMemoryStorage()
		reached := testkit.NewDeferred[struct{}]()
		gate := testkit.NewDeferred[struct{}]()
		stubborn := thlcOneStep("test.close-cancelled", func() error {
			reached.Resolve(struct{}{})
			<-gate.Done()
			return nil
		})
		opened, err := testkit.OpenTasks(st, []durable.AnyTask{stubborn})
		if err != nil {
			t.Fatal(err)
		}
		root, _ := opened.Harness.Root(nil)
		thlcStart(t, root, stubborn)
		_ = opened.Harness.Resume()
		<-reached.Done()

		child := chord.WithCancel(nil)
		cancelled := thlcAsyncErr(func() error { return opened.Harness.Close(child.Context) })
		child.Cancel(errors.New("stop waiting"))
		if err := <-cancelled; err == nil || !strings.Contains(err.Error(), "stop waiting") {
			t.Fatalf("expected stop waiting, got %v", err)
		}
		if err := opened.Harness.Commit(nil, func(_ durable.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed commit error, got %v", err)
		}
		if !thlcStorageOpen(st) {
			t.Fatal("expected storage still open while closing")
		}
		secondDone, waitSecond := thlcAsyncDone(func() error { return opened.Harness.Close(nil) })
		if testkit.SettledChannel(secondDone) {
			t.Fatal("expected second Close to wait for gate")
		}
		gate.Resolve(struct{}{})
		if err := waitSecond(); err != nil {
			t.Fatal(err)
		}
		if thlcStorageOpen(st) {
			t.Fatal("expected storage closed")
		}
	})

	t.Run("settles durably a commit whose committer was cancelled while in Storage", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, _ := opened.Harness.Root(nil)

		held := st.HoldCommits()
		child := chord.WithCancel(nil)
		var entryId durable.EntryId
		committing := thlcAsyncErr(func() error {
			raw, err := root.Commit(child.Context, func(tx durable.Tx) (any, error) {
				rec, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
				if err != nil {
					return nil, err
				}
				return rec.Id, nil
			})
			if err != nil {
				return err
			}
			entryId = raw.(durable.EntryId)
			return nil
		})
		<-held.Entered
		child.Cancel(errors.New("committer gave up"))
		held.Release()
		if err := <-committing; err != nil {
			t.Fatalf("committing err = %v", err)
		}
		page, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].Id != entryId {
			t.Fatalf("page.Items = %+v, want id %d", page.Items, entryId)
		}
	})

	t.Run("publishes no frame to states and watches from a commit that settles during close", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.close-frames",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatal(err)
		}
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := opened.Harness.Root(nil)
		if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
			d, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			d.Value.Set("text", "before")
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		docState, err := opened.Harness.DocumentState(nil, notesDoc)
		if err != nil {
			t.Fatal(err)
		}
		docWatch, err := opened.Harness.WatchDoc(nil, notesDoc)
		if err != nil {
			t.Fatal(err)
		}
		viewState, err := root.ViewState(nil)
		if err != nil {
			t.Fatal(err)
		}
		viewWatch, err := root.Watch(nil)
		if err != nil {
			t.Fatal(err)
		}
		graphState, err := opened.Harness.TaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}
		graphWatch, err := opened.Harness.WatchTaskGraph(nil)
		if err != nil {
			t.Fatal(err)
		}

		var mu sync.Mutex
		var frames []string
		record := func(name string) {
			mu.Lock()
			frames = append(frames, name)
			mu.Unlock()
		}
		unsubGraph := graphState.Subscribe(func(_ durable.TaskGraph, _ chord.Context, d chord.ReplicatedStateDelivery) error {
			if d.Kind == chord.ReplicatedStateDeliveryKindUpdate {
				record("graphState")
			}
			return nil
		})
		defer unsubGraph()
		_ = graphWatch.Start(func(_ durable.TaskGraph, _ []chord.Op, _ chord.Context) error {
			record("graphWatch")
			return nil
		})
		unsubDoc := docState.Subscribe(func(_ durable.JsonObject, _ chord.Context, d chord.ReplicatedStateDelivery) error {
			if d.Kind == chord.ReplicatedStateDeliveryKindUpdate {
				record("docState")
			}
			return nil
		})
		defer unsubDoc()
		unsubView := viewState.Subscribe(func(_ durable.ConversationView, _ chord.Context, d chord.ReplicatedStateDelivery) error {
			if d.Kind == chord.ReplicatedStateDeliveryKindUpdate {
				record("viewState")
			}
			return nil
		})
		defer unsubView()
		_ = docWatch.Start(func(_ durable.JsonObject, _ []chord.Op, _ chord.Context) error {
			record("docWatch")
			return nil
		})
		_ = viewWatch.Start(func(_ durable.ConversationView, _ []chord.Op, _ chord.Context) error {
			record("viewWatch")
			return nil
		})

		docVal, _ := docState.Value()
		viewVal, _ := viewState.Value()

		held := st.HoldCommits()
		stepTask := thlcOneStep("test.close-graph", nil)
		committing := thlcAsyncErr(func() error {
			return opened.Harness.Commit(nil, func(tx durable.Tx) error {
				d, err := tx.Doc(notesDoc)
				if err != nil {
					return err
				}
				d.Value.Set("text", "during close")
				if _, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"}); err != nil {
					return err
				}
				cid := root.Id()
				_, err = tx.CreateTask(stepTask, nil, durable.TaskOptions{
					Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
					ConversationId: &cid,
				})
				return err
			})
		})
		<-held.Entered
		closeStarted := make(chan struct{})
		opened.Harness.SubscribeClose(func() { close(closeStarted) })
		closing := thlcAsyncErr(func() error { return opened.Harness.Close(nil) })
		<-closeStarted
		held.Release()
		if err := <-committing; err != nil {
			t.Fatal(err)
		}
		if err := <-closing; err != nil {
			t.Fatal(err)
		}
		testkit.Flush()

		mu.Lock()
		gotFrames := slices.Clone(frames)
		mu.Unlock()
		if len(gotFrames) != 0 {
			t.Fatalf("frames = %v, want empty", gotFrames)
		}
		if afterDoc, _ := docState.Value(); afterDoc != docVal {
			t.Fatalf("docState mutated: %v vs %v", afterDoc, docVal)
		}
		if afterView, _ := viewState.Value(); !reflect.DeepEqual(afterView, viewVal) {
			t.Fatalf("viewState mutated: %+v vs %+v", afterView, viewVal)
		}
		if gVal, _ := graphState.Value(); len(gVal.Tasks) != 0 {
			t.Fatalf("graphState = %+v, want empty", gVal)
		}
		if end := graphWatch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("graphWatch end = %+v", end)
		}
		if end := docWatch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("docWatch end = %+v", end)
		}
		if end := viewWatch.Closed(); end.Reason != durable.WatchEndSessionClosed {
			t.Fatalf("viewWatch end = %+v", end)
		}
		commits := st.Commits()
		last := commits[len(commits)-1]
		hasEntry := false
		for _, w := range last {
			if w.WriteType() == durable.StorageWriteTypeEntry {
				hasEntry = true
			}
		}
		if !hasEntry {
			t.Fatalf("expected entry write in last commit: %#v", last)
		}
	})

	t.Run("leaves no subscription behind a watch acquisition cancelled on the line", func(t *testing.T) {
		notesDoc, err := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.cancelled-watch",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		if err != nil {
			t.Fatal(err)
		}
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, _ := opened.Harness.Root(nil)
		_ = opened.Harness.Commit(nil, func(tx durable.Tx) error {
			d, err := tx.Doc(notesDoc)
			if err != nil {
				return err
			}
			d.Value.Set("text", "x")
			return nil
		})

		unloader := opened.Harness.(interface{ UnloadDocuments() error })
		_ = unloader.UnloadDocuments()
		find := st.HoldFindDocument()
		child := chord.WithCancel(nil)
		docErrCh := thlcAsyncErr(func() error {
			_, err := opened.Harness.WatchDoc(child.Context, notesDoc)
			return err
		})
		<-find.Entered
		child.Cancel(errors.New("cancelled"))
		find.Release()
		if err := <-docErrCh; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled docWatch error, got %v", err)
		}

		_ = unloader.UnloadDocuments()
		find = st.HoldFindDocument()
		child2 := chord.WithCancel(nil)
		viewErrCh := thlcAsyncErr(func() error {
			_, err := root.Watch(child2.Context)
			return err
		})
		<-find.Entered
		child2.Cancel(errors.New("cancelled"))
		find.Release()
		if err := <-viewErrCh; err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("expected cancelled viewWatch error, got %v", err)
		}

		first, err := root.ViewState(nil)
		if err != nil {
			t.Fatal(err)
		}
		first.Dispose()
		second, err := root.ViewState(nil)
		if err != nil {
			t.Fatal(err)
		}
		second.Dispose()
	})

	t.Run("rejects conversation and Harness operations once close begins and inspect queued before reports closing", func(t *testing.T) {
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := opened.Harness.Root(nil)
		rawEntry, err := root.Commit(nil, func(tx durable.Tx) (any, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		if err != nil {
			t.Fatal(err)
		}
		entry := rawEntry.(*durable.EntryRecord)
		submission, err := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "note"},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}

		held := st.HoldCommits()
		blocking := thlcAsyncErr(func() error {
			return opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
				return err
			})
		})
		<-held.Entered

		var queuedInspect *durable.HarnessInspection
		inspectCh := thlcAsyncErr(func() error {
			var err error
			queuedInspect, err = opened.Harness.Inspect(nil)
			return err
		})
		// Ensure Inspect is admitted and queued behind blocking on the line before Close seals admission.
		testkit.Flush()
		testkit.Flush()
		closeStarted := make(chan struct{})
		opened.Harness.SubscribeClose(func() { close(closeStarted) })
		closing := thlcAsyncErr(func() error { return opened.Harness.Close(nil) })
		<-closeStarted

		text := "x"
		ops := map[string]func() error{
			"submit": func() error {
				_, err := root.Submit(durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: durable.UserInput{Text: &text}}, nil)
				return err
			},
			"agent": func() error { _, err := root.Agent(nil); return err },
			"configure": func() error {
				return root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)}, nil)
			},
			"commit": func() error {
				_, err := root.Commit(nil, func(_ durable.Tx) (any, error) { return nil, nil })
				return err
			},
			"context": func() error { _, err := root.Context(nil); return err },
			"entries": func() error { _, err := root.Entries(durable.ConversationEntryQuery{}, 10, nil, nil); return err },
			"fork": func() error {
				_, err := root.Fork(entry.Id, durable.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless}}, nil)
				return err
			},
			"compact":          func() error { _, err := root.Compact(nil, nil); return err },
			"reset":            func() error { return root.Reset(nil, nil) },
			"abort":            func() error { return root.Abort(nil) },
			"conversationIdle": func() error { return root.WaitForIdle(nil) },
			"viewState":        func() error { _, err := root.ViewState(nil); return err },
			"watch":            func() error { _, err := root.Watch(nil); return err },
			"taskGraph":        func() error { _, err := opened.Harness.TaskGraph(nil); return err },
			"watchTaskGraph":   func() error { _, err := opened.Harness.WatchTaskGraph(nil); return err },
			"status":           func() error { _, err := submission.Status(nil); return err },
			"wait":             func() error { _, err := submission.Wait(nil); return err },
			"abortSubmission":  func() error { _, err := submission.Abort(nil); return err },
			"root":             func() error { _, err := opened.Harness.Root(nil); return err },
			"conversation":     func() error { _, err := opened.Harness.Conversation(root.Id(), nil); return err },
			"createConversation": func() error {
				_, err := opened.Harness.CreateConversation(durable.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless}}, nil)
				return err
			},
			"getTask":     func() error { _, err := opened.Harness.GetTask(nil, 1); return err },
			"inspect":     func() error { _, err := opened.Harness.Inspect(nil); return err },
			"submission":  func() error { _, err := opened.Harness.Submission(nil, submission.Id()); return err },
			"abortTask":   func() error { _, err := opened.Harness.AbortTask(nil, 1); return err },
			"waitForTask": func() error { _, err := opened.Harness.WaitForTask(nil, 1); return err },
			"harnessIdle": func() error { return opened.Harness.WaitForIdle(nil) },
			"usage":       func() error { _, err := opened.Harness.Usage(nil); return err },
		}
		for name, op := range ops {
			if err := op(); err == nil || !strings.Contains(err.Error(), "closed") {
				t.Fatalf("op %s = %v, want closed error", name, err)
			}
		}
		if err := opened.Harness.Resume(); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("Resume = %v, want closed error", err)
		}
		held.Release()
		if err := <-blocking; err != nil {
			t.Fatal(err)
		}
		if err := <-inspectCh; err != nil {
			t.Fatal(err)
		}
		if queuedInspect.Scheduling != durable.HarnessSchedulingClosing {
			t.Fatalf("queuedInspect.Scheduling = %q, want closing", queuedInspect.Scheduling)
		}
		if err := <-closing; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("completes reads queued at the seal and rejects queued waits and acquisitions that would follow commits", func(t *testing.T) {
		notesDoc, _ := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.queued-at-seal",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		absentDoc, _ := durable.DefineDoc(durable.DocDefinition{
			Kind:    "test.queued-absent",
			Version: 1,
			Scope:   durable.DocumentScopeSession,
			Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
		})
		pendingTask := thlcOneStep("test.queued-pending", nil)
		st := testkit.NewControlledStorage()
		opened, err := testkit.OpenTasks(st, nil)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := opened.Harness.Root(nil)
		_ = opened.Harness.Commit(nil, func(tx durable.Tx) error {
			_, err := tx.Doc(notesDoc)
			return err
		})
		taskId := thlcStart(t, root, pendingTask)
		writeSub, err := root.Submit(durable.SubmissionDraft{
			Type:  durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{Kind: "note"},
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writeSub.Wait(nil); err != nil {
			t.Fatal(err)
		}

		held := st.HoldCommits()
		blocking := thlcAsyncErr(func() error {
			return opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "blocker"})
				return err
			})
		})
		<-held.Entered

		classify := func(fn func() (bool, error)) <-chan string {
			ch := make(chan string, 1)
			go func() {
				isNil, err := fn()
				if err != nil {
					if strings.Contains(err.Error(), "closed") {
						ch <- "closed"
					} else {
						ch <- err.Error()
					}
					return
				}
				if isNil {
					ch <- "nil"
				} else {
					ch <- "resolved"
				}
			}()
			return ch
		}

		results := map[string]<-chan string{
			"context":       classify(func() (bool, error) { _, err := root.Context(nil); return false, err }),
			"snapshot":      classify(func() (bool, error) { v, err := opened.Harness.Snapshot(nil, notesDoc); return v == nil, err }),
			"settledWait":   classify(func() (bool, error) { _, err := writeSub.Wait(nil); return false, err }),
			"absentWatch":   classify(func() (bool, error) { w, err := opened.Harness.WatchDoc(nil, absentDoc); return w == nil, err }),
			"absentState":   classify(func() (bool, error) { s, err := opened.Harness.DocumentState(nil, absentDoc); return s == nil, err }),
			"waitForTask":   classify(func() (bool, error) { _, err := opened.Harness.WaitForTask(nil, taskId); return false, err }),
			"documentState": classify(func() (bool, error) { _, err := opened.Harness.DocumentState(nil, notesDoc); return false, err }),
			"watchDoc":      classify(func() (bool, error) { _, err := opened.Harness.WatchDoc(nil, notesDoc); return false, err }),
			"viewState":     classify(func() (bool, error) { _, err := root.ViewState(nil); return false, err }),
			"watch":         classify(func() (bool, error) { _, err := root.Watch(nil); return false, err }),
			"taskGraph":     classify(func() (bool, error) { _, err := opened.Harness.TaskGraph(nil); return false, err }),
		}
		// Give the goroutines a turn so they queue on the session line before Close seals admission.
		testkit.Flush()
		closeStarted := make(chan struct{})
		opened.Harness.SubscribeClose(func() { close(closeStarted) })
		closing := thlcAsyncErr(func() error { return opened.Harness.Close(nil) })
		<-closeStarted
		held.Release()
		if err := <-blocking; err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"context":       "resolved",
			"snapshot":      "resolved",
			"settledWait":   "resolved",
			"absentWatch":   "nil",
			"absentState":   "nil",
			"waitForTask":   "closed",
			"documentState": "closed",
			"watchDoc":      "closed",
			"viewState":     "closed",
			"watch":         "closed",
			"taskGraph":     "closed",
		}
		for name, ch := range results {
			got := <-ch
			if got != want[name] {
				t.Fatalf("queued[%s] = %q, want %q", name, got, want[name])
			}
		}
		if err := <-closing; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("scheduling on a paused Harness", func(t *testing.T) {
		type pausedFixture struct {
			opened *testkit.OpenTasksResult
			root   durable.Conversation
			taskId durable.TaskId
			subId  durable.SubmissionId
			ran    func() bool
		}
		newPaused := func(t *testing.T) pausedFixture {
			t.Helper()
			var mu sync.Mutex
			ran := false
			marker := thlcOneStep("test.marker", func() error {
				mu.Lock()
				ran = true
				mu.Unlock()
				return nil
			})
			opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), []durable.AnyTask{marker})
			if err != nil {
				t.Fatal(err)
			}
			root, _ := opened.Harness.Root(nil)
			id := thlcStart(t, root, marker, true)
			var subId durable.SubmissionId
			if err := opened.Harness.Commit(nil, func(tx durable.Tx) error {
				rec, err := tx.CreateSubmission(durable.SubmissionCreate{
					ConversationId: root.Id(),
					Type:           durable.SubmissionTypeWrite,
					Status:         durable.SubmissionStatusQueued,
				})
				if err != nil {
					return err
				}
				subId = rec.Id
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			return pausedFixture{
				opened: opened,
				root:   root,
				taskId: id,
				subId:  subId,
				ran: func() bool {
					mu.Lock()
					defer mu.Unlock()
					return ran
				},
			}
		}

		t.Run("never schedules from a read-only viewer", func(t *testing.T) {
			f := newPaused(t)
			defer func() { _ = f.opened.Harness.Close(nil) }()
			notesDoc, _ := durable.DefineDoc(durable.DocDefinition{
				Kind:    "test.viewer-notes",
				Version: 1,
				Scope:   durable.DocumentScopeSession,
				Initial: func() durable.JsonObject { return jsonx.ObjectOf("text", "") },
			})
			_ = f.opened.Harness.Commit(nil, func(tx durable.Tx) error {
				_, err := tx.Doc(notesDoc)
				return err
			})
			_, _ = f.opened.Harness.Inspect(nil)
			_, _ = f.opened.Harness.GetTask(nil, f.taskId)
			_, _ = f.opened.Harness.Usage(nil)
			_, _ = f.opened.Harness.Conversation(f.root.Id(), nil)
			_, _ = f.opened.Harness.Snapshot(nil, notesDoc)
			if st, err := f.opened.Harness.DocumentState(nil, notesDoc); err == nil && st != nil {
				st.Dispose()
			}
			if w, err := f.opened.Harness.WatchDoc(nil, notesDoc); err == nil && w != nil {
				w.Stop()
			}
			if sub, err := f.opened.Harness.Submission(nil, f.subId); err == nil && sub != nil {
				_, _ = sub.Status(nil)
			}
			_, _ = f.root.Agent(nil)
			_, _ = f.root.Context(nil)
			_, _ = f.root.Entries(durable.ConversationEntryQuery{}, 10, nil, nil)
			if vs, err := f.root.ViewState(nil); err == nil && vs != nil {
				vs.Dispose()
			}
			if vw, err := f.root.Watch(nil); err == nil && vw != nil {
				vw.Stop()
			}
			if gs, err := f.opened.Harness.TaskGraph(nil); err == nil && gs != nil {
				gs.Dispose()
			}
			if gw, err := f.opened.Harness.WatchTaskGraph(nil); err == nil && gw != nil {
				gw.Stop()
			}
			testkit.Flush()
			testkit.Flush()
			if f.ran() {
				t.Fatal("viewer unexpectedly triggered scheduling")
			}
			if insp, err := f.opened.Harness.Inspect(nil); err != nil || insp.Scheduling != durable.HarnessSchedulingPaused {
				t.Fatalf("inspect = %+v err=%v, want paused", insp, err)
			}
		})

		t.Run("schedules from every progress call", func(t *testing.T) {
			calls := []struct {
				name string
				fn   func(pausedFixture) error
			}{
				{"submit", func(f pausedFixture) error {
					_, err := f.root.Submit(durable.SubmissionDraft{
						Type:  durable.SubmissionTypeWrite,
						Entry: &durable.EntryDraft{Kind: "note"},
					}, nil)
					return err
				}},
				{"compact", func(f pausedFixture) error {
					_, err := f.root.Compact(nil, nil)
					return err
				}},
				{"abort", func(f pausedFixture) error {
					return f.root.Abort(nil)
				}},
				{"conversationIdle", func(f pausedFixture) error {
					return f.root.WaitForIdle(nil)
				}},
				{"submissionWait", func(f pausedFixture) error {
					sub, err := f.opened.Harness.Submission(nil, f.subId)
					if err != nil {
						return err
					}
					_, err = sub.Wait(nil)
					return err
				}},
				{"waitForTask", func(f pausedFixture) error {
					_, err := f.opened.Harness.WaitForTask(nil, f.taskId)
					return err
				}},
				{"harnessIdle", func(f pausedFixture) error {
					return f.opened.Harness.WaitForIdle(nil)
				}},
			}
			for _, tc := range calls {
				t.Run(tc.name, func(t *testing.T) {
					f := newPaused(t)
					done := thlcAsyncErr(func() error { return tc.fn(f) })
					if err := testkit.Eventually(func() (bool, error) { return f.ran(), nil }); err != nil {
						t.Fatalf("%s did not enable scheduling: %v", tc.name, err)
					}
					_ = f.opened.Harness.Close(nil)
					<-done
				})
			}
		})
	})

	t.Run("runs what registry holds at resume: definition installed or replaced after open", func(t *testing.T) {
		var mu sync.Mutex
		var log []string
		define := func(label string) *durable.Task[any, thlcStep, any, struct{}] {
			return thlcOneStep("test.late-definition", func() error {
				mu.Lock()
				log = append(log, label)
				mu.Unlock()
				return nil
			})
		}
		opened, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = opened.Harness.Close(nil) }()
		root, _ := opened.Harness.Root(nil)
		missing := thlcStart(t, root, define("unused"))

		insp, err := opened.Harness.Inspect(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(insp.Tasks) != 1 || insp.Tasks[0].State.Kind != durable.TaskInspectionBlocked || insp.Tasks[0].State.Reason != durable.TaskBlockedMissingTask {
			t.Fatalf("insp 1 = %+v", insp.Tasks)
		}

		installed, err := testkit.AddTask(opened.Registry, define("v1"))
		if err != nil {
			t.Fatal(err)
		}
		insp, err = opened.Harness.Inspect(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(insp.Tasks) != 1 || insp.Tasks[0].State.Kind != durable.TaskInspectionReady {
			t.Fatalf("insp 2 = %+v", insp.Tasks)
		}

		installed.Dispose()
		if _, err := testkit.AddTask(opened.Registry, define("v2")); err != nil {
			t.Fatal(err)
		}
		_ = opened.Harness.Resume()
		if _, err := opened.Harness.WaitForTask(nil, missing); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := slices.Clone(log)
		mu.Unlock()
		if !slices.Equal(got, []string{"v2"}) {
			t.Fatalf("log = %v, want [v2]", got)
		}
	})
}

func thlcPtr[T any](v T) *T { return &v }
