// Ported from packages/durable/test/chord-guide.test.ts (pi v1.0.0).

package durable_test

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tcgdLog struct {
	mu      sync.Mutex
	entries [][]any
}

func (l *tcgdLog) Clear() {
	l.mu.Lock()
	l.entries = nil
	l.mu.Unlock()
}

func (l *tcgdLog) Add(vals ...any) {
	l.mu.Lock()
	l.entries = append(l.entries, vals)
	l.mu.Unlock()
}

func (l *tcgdLog) Snapshot() [][]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([][]any, len(l.entries))
	copy(out, l.entries)
	return out
}

var (
	tcgdLogged   = &tcgdLog{}
	tcgdSentMu   sync.Mutex
	tcgdSent     []any
	tcgdRendered []string
)

var tcgdCanvasDoc = func() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.canvas",
		Version: 1,
		Scope:   durable.DocumentScopeSession,
		Initial: func() durable.JsonObject {
			return jsonx.ObjectOf("strokes", []any{})
		},
		CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 99
		},
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

type tcgdCanvasServiceSpec struct{}

var tcgdCanvasSvc = func() *chord.Service[tcgdCanvasServiceSpec] {
	svc, err := chord.DefineService[tcgdCanvasServiceSpec]("app.canvas", nil)
	if err != nil {
		panic(err)
	}
	return svc
}()

type tcgdServiceProxy interface {
	Get(property any) (any, error)
	Call(member string, args ...any) (any, error)
}

type tcgdStateMember interface {
	Subscribe(listener func(value chord.JsonValue, call chord.Context, delivery chord.ReplicatedStateDelivery) error) (func(), error)
}

func tcgdCreateCanvasFacet(session durable.Session, ctx chord.Context) (chord.Facet, error) {
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		_, err := tx.Doc(tcgdCanvasDoc)
		return err
	}); err != nil {
		return chord.Facet{}, err
	}
	state, err := session.DocumentState(ctx, tcgdCanvasDoc)
	if err != nil {
		return chord.Facet{}, err
	}
	if state == nil {
		return chord.Facet{}, fmt.Errorf("canvas was retired during setup")
	}
	return chord.DefineFacet(chord.Facet{
		Id: "app.canvas/session",
		Setup: func(env chord.FacetEnvironment) error {
			env.Own(func() error {
				state.Dispose()
				return nil
			})
			env.Provide(tcgdCanvasSvc, map[string]any{
				"state": state,
				"addStroke": func(stroke *jsonx.Object, callCtx chord.Context) error {
					return session.Commit(callCtx, func(tx durable.Tx) error {
						draft, err := tx.Doc(tcgdCanvasDoc)
						if err != nil {
							return err
						}
						rawStrokes, _ := draft.Value.Get("strokes")
						strokes, _ := rawStrokes.([]any)
						draft.Value.Set("strokes", append(slices.Clone(strokes), stroke))
						return nil
					})
				},
			})
			return nil
		},
	}), nil
}

var tcgdCanvasConsumer = chord.DefineFacet(chord.Facet{
	Id: "app.canvas/consumer",
	Setup: func(env chord.FacetEnvironment) error {
		canvas := env.Use(tcgdCanvasSvc).(tcgdServiceProxy)
		env.OnActivate(func() error {
			rawState, err := canvas.Get("state")
			if err != nil {
				return err
			}
			stateMember := rawState.(tcgdStateMember)
			unsub, err := stateMember.Subscribe(func(value chord.JsonValue, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
				var countOrRetired any = "retired"
				if obj, ok := value.(*jsonx.Object); ok && obj != nil {
					rawStrokes, _ := obj.Get("strokes")
					if strokes, ok := rawStrokes.([]any); ok {
						countOrRetired = len(strokes)
					}
				}
				tcgdLogged.Add(string(delivery.Kind), delivery.Sequence, countOrRetired)
				return nil
			})
			if err != nil {
				return err
			}
			env.Own(func() error {
				unsub()
				return nil
			})
			return nil
		})
		return nil
	},
})

func tcgdRunCanvasExample(session durable.Session) error {
	ctx := chord.BackgroundContext
	provider, err := tcgdCreateCanvasFacet(session, ctx)
	if err != nil {
		return err
	}
	host, err := chord.CreateFacetHost(&chord.FacetOptions{
		Facets: []chord.Facet{provider, tcgdCanvasConsumer},
	})
	if err != nil {
		return err
	}
	defer func() { _ = host.Dispose() }()

	svcRaw, err := host.Services().Use(tcgdCanvasSvc)
	if err != nil {
		return err
	}
	svcMap := svcRaw.(map[string]any)
	addStroke := svcMap["addStroke"].(func(*jsonx.Object, chord.Context) error)
	if err := addStroke(jsonx.ObjectOf(
		"color", "black",
		"points", []any{
			jsonx.ObjectOf("x", 10, "y", 20),
			jsonx.ObjectOf("x", 30, "y", 40),
		},
	), ctx); err != nil {
		return err
	}
	return testkit.Eventually(func() (bool, error) {
		return len(tcgdLogged.Snapshot()) >= 2, nil
	})
}

func tcgdConnectCanvas(transport chord.RemoteServiceTransport, ctx chord.Context) (func() error, error) {
	services, err := chord.CreateRemoteServiceBinding(&chord.RemoteServiceBindingOptions{
		Services:  []chord.ServiceRef{{Id: tcgdCanvasSvc.Id}},
		Transport: transport,
	})
	if err != nil {
		return nil, err
	}
	canvas := services.Use(tcgdCanvasSvc).(tcgdServiceProxy)
	rawState, err := canvas.Get("state")
	if err != nil {
		_ = services.Dispose(chord.BackgroundContext)
		return nil, err
	}
	stop, err := rawState.(tcgdStateMember).Subscribe(func(value chord.JsonValue, _ chord.Context, delivery chord.ReplicatedStateDelivery) error {
		var strokesOrRetired any = "retired"
		if obj, ok := value.(*jsonx.Object); ok && obj != nil {
			strokesOrRetired, _ = obj.Get("strokes")
		}
		tcgdLogged.Add(string(delivery.Kind), delivery.Sequence, strokesOrRetired)
		return nil
	})
	if err != nil {
		_ = services.Dispose(chord.BackgroundContext)
		return nil, err
	}
	if err := services.Ready(ctx); err != nil {
		stop()
		_ = services.Dispose(chord.BackgroundContext)
		return nil, err
	}
	return func() error {
		stop()
		return services.Dispose(chord.BackgroundContext)
	}, nil
}

type tcgdInProcessTransport struct {
	host chord.FacetHost
}

func (t tcgdInProcessTransport) Invoke(call chord.ServiceCall, callCtx chord.Context) (chord.JsonValue, bool, error) {
	return t.host.Services().Invoke(call, callCtx)
}

func (t tcgdInProcessTransport) Subscribe(
	serviceId string,
	mode chord.ServiceMode,
	listener func(update chord.ServiceProviderUpdate, call chord.Context),
	_ chord.Context,
) (chord.ServiceSubscription, error) {
	return t.host.Services().Subscribe(serviceId, mode, func(update chord.ServiceProviderUpdate, ctx chord.Context) error {
		listener(update, ctx)
		return nil
	})
}

var tcgdReviewDoc = func() *durable.DocFamilyToken {
	tok, err := durable.DefineDocFamily(durable.DocFamilyDefinition{
		Kind:    "app.diff-review",
		Version: 1,
		Family:  true,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkCurrent,
		Initial: func(seed durable.JsonValue) durable.JsonObject {
			obj := seed.(*jsonx.Object)
			p, _ := obj.Get("path")
			patch, _ := obj.Get("patch")
			return jsonx.ObjectOf("path", p, "patch", patch, "comments", []any{})
		},
		CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 49
		},
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

type tcgdDiffReviewServiceSpec struct{}

var tcgdDiffReviewsSvc = func() *chord.Service[tcgdDiffReviewServiceSpec] {
	svc, err := chord.DefineService[tcgdDiffReviewServiceSpec]("app.diff-reviews", nil)
	if err != nil {
		panic(err)
	}
	return svc
}()

type tcgdReviewSpec struct {
	Key  string
	Seed *jsonx.Object
}

func tcgdReviewFacet(
	session durable.Session,
	conversationId durable.ConversationId,
	reviews []tcgdReviewSpec,
	ctx chord.Context,
) chord.Facet {
	return chord.DefineFacet(chord.Facet{
		Id: "app.diff-reviews/session",
		Setup: func(env chord.FacetEnvironment) error {
			instances := env.ProvideMany(tcgdDiffReviewsSvc).(chord.ServiceSpawner[any])
			env.OnActivate(func() error {
				for _, rev := range reviews {
					review := rev
					if err := session.Commit(ctx, func(tx durable.Tx) error {
						_, err := tx.Doc(tcgdReviewDoc, conversationId, review.Key, review.Seed)
						return err
					}); err != nil {
						return err
					}
					state, err := session.DocumentState(ctx, tcgdReviewDoc, conversationId, review.Key)
					if err != nil {
						return err
					}
					if state == nil {
						return fmt.Errorf("review was retired during setup")
					}
					env.Own(func() error {
						state.Dispose()
						return nil
					})
					keyJson, err := jsonx.Stringify([]any{int64(conversationId), review.Key})
					if err != nil {
						return err
					}
					instances.Spawn(keyJson, map[string]any{
						"state": state,
						"identity": func(_ chord.Context) (*jsonx.Object, error) {
							return jsonx.ObjectOf("conversationId", int64(conversationId), "key", review.Key), nil
						},
						"addComment": func(comment *jsonx.Object, callCtx chord.Context) error {
							return session.Commit(callCtx, func(tx durable.Tx) error {
								draft, err := tx.Doc(tcgdReviewDoc, conversationId, review.Key, review.Seed)
								if err != nil {
									return err
								}
								rawComments, _ := draft.Value.Get("comments")
								comments, _ := rawComments.([]any)
								draft.Value.Set("comments", append(slices.Clone(comments), comment))
								return nil
							})
						},
					})
				}
				return nil
			})
			return nil
		},
	})
}

var tcgdJobOutputDoc = func() *durable.DocToken {
	tok, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.job-output",
		Version: 1,
		Scope:   durable.DocumentScopeTask,
		Initial: func() durable.JsonObject {
			return jsonx.ObjectOf("stdout", "", "chunks", 0)
		},
		CheckpointWhen: func(_ durable.JsonObject, _ []chord.Op, info durable.CheckpointInfo) bool {
			return info.DeltasSinceBase >= 99
		},
	})
	if err != nil {
		panic(err)
	}
	return tok
}()

type tcgdJobInput struct {
	Command string `json:"command"`
}

type tcgdJobCheckpoint struct {
	Phase string `json:"phase"`
}

func tcgdAppendJobOutput(
	rt *durable.TaskRuntime[tcgdJobInput, tcgdJobCheckpoint, any, struct{}],
	chunk string,
	ctx chord.Context,
) error {
	return rt.Commit(ctx, func(tx durable.Tx, _ durable.RunningTask[tcgdJobInput, tcgdJobCheckpoint]) (*durable.NextTaskState[tcgdJobCheckpoint, any], error) {
		draft, err := tx.Doc(tcgdJobOutputDoc, rt.TaskId())
		if err != nil {
			return nil, err
		}
		curOut, _ := draft.Value.Get("stdout")
		stdout, _ := curOut.(string)
		combined := stdout + chunk
		if len(combined) > 50_000 {
			combined = combined[len(combined)-50_000:]
		}
		curChunks, _ := draft.Value.Get("chunks")
		chunksFloat, _ := curChunks.(float64)
		draft.Value.Set("stdout", combined)
		draft.Value.Set("chunks", chunksFloat+1)
		return nil, nil
	})
}

func tcgdObserveJob(
	api durable.DocumentObserver,
	producerTaskId durable.TaskId,
	finished <-chan struct{},
	ctx chord.Context,
) error {
	watch, err := api.WatchDoc(ctx, tcgdJobOutputDoc, producerTaskId)
	if err != nil || watch == nil {
		return err
	}
	if val := watch.Value(); val == nil {
		tcgdLogged.Add("retired")
	} else {
		stdout, _ := val.Get("stdout")
		tcgdLogged.Add(stdout)
	}
	defer watch.Stop()
	if err := watch.Start(func(value durable.JsonObject, ops []chord.Op, _ chord.Context) error {
		tcgdSentMu.Lock()
		tcgdSent = append(tcgdSent, jsonx.ObjectOf("value", value, "ops", ops))
		text := "retired"
		if value != nil {
			s, _ := value.Get("stdout")
			text, _ = s.(string)
		}
		tcgdRendered = append(tcgdRendered, text)
		tcgdSentMu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	<-finished
	return nil
}

func TestChordUsageGuide(t *testing.T) {
	ctx := testkit.TestContext

	t.Run("runs the canvas: facet host, remote client, withdrawal and detach before Harness close", func(t *testing.T) {
		tcgdLogged.Clear()
		res, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenTasks failed: %v", err)
		}
		harness := res.Harness
		if err := tcgdRunCanvasExample(harness); err != nil {
			t.Fatalf("tcgdRunCanvasExample failed: %v", err)
		}
		want1 := [][]any{
			{"hydrate", 0, 0},
			{"update", 1, 1},
		}
		if !reflect.DeepEqual(tcgdLogged.Snapshot(), want1) {
			t.Fatalf("logged = %#v, want %#v", tcgdLogged.Snapshot(), want1)
		}
		snap, err := harness.Snapshot(ctx, tcgdCanvasDoc)
		if err != nil || snap == nil {
			t.Fatalf("Snapshot failed: %v %v", snap, err)
		}
		rawStrokes, _ := snap.Get("strokes")
		if len(rawStrokes.([]any)) != 1 {
			t.Fatalf("expected 1 stroke, got %v", rawStrokes)
		}

		tcgdLogged.Clear()
		provider, err := tcgdCreateCanvasFacet(harness, ctx)
		if err != nil {
			t.Fatalf("tcgdCreateCanvasFacet failed: %v", err)
		}
		host, err := chord.CreateFacetHost(&chord.FacetOptions{
			Facets: []chord.Facet{provider},
		})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		detach, err := tcgdConnectCanvas(tcgdInProcessTransport{host: host}, ctx)
		if err != nil {
			t.Fatalf("tcgdConnectCanvas failed: %v", err)
		}
		svcRaw, err := host.Services().Use(tcgdCanvasSvc)
		if err != nil {
			t.Fatalf("Services.Use failed: %v", err)
		}
		addStroke := svcRaw.(map[string]any)["addStroke"].(func(*jsonx.Object, chord.Context) error)
		if err := addStroke(jsonx.ObjectOf("color", "red", "points", []any{}), ctx); err != nil {
			t.Fatalf("addStroke failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			return len(tcgdLogged.Snapshot()) == 2, nil
		}); err != nil {
			t.Fatalf("Eventually failed: %v (logged=%#v)", err, tcgdLogged.Snapshot())
		}
		snapEntries := tcgdLogged.Snapshot()
		var got2 [][]any
		for _, entry := range snapEntries {
			strokesSlice := entry[2].([]any)
			got2 = append(got2, []any{entry[0], len(strokesSlice)})
		}
		want2 := [][]any{{"hydrate", 1}, {"update", 2}}
		if !reflect.DeepEqual(got2, want2) {
			t.Fatalf("logged frames = %#v, want %#v", got2, want2)
		}

		var order []string
		order = append(order, "detach start")
		if err := detach(); err != nil {
			t.Fatalf("detach failed: %v", err)
		}
		order = append(order, "detach end", "dispose start")
		if err := host.Dispose(); err != nil {
			t.Fatalf("host.Dispose failed: %v", err)
		}
		order = append(order, "dispose end", "close start")
		if err := harness.Close(ctx); err != nil {
			t.Fatalf("harness.Close failed: %v", err)
		}
		order = append(order, "close end")
		wantOrder := []string{"detach start", "detach end", "dispose start", "dispose end", "close start", "close end"}
		if !reflect.DeepEqual(order, wantOrder) {
			t.Fatalf("shutdown order = %v, want %v", order, wantOrder)
		}
		if _, err := host.Services().Use(tcgdCanvasSvc); err == nil || !strings.Contains(err.Error(), "disposed") {
			t.Fatalf("expected disposed error from host.Services().Use, got %v", err)
		}
		if err := harness.Commit(ctx, func(durable.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("expected closed error from harness.Commit, got %v", err)
		}
	})

	t.Run("runs the diff reviews with a keyed consumer", func(t *testing.T) {
		res, err := testkit.OpenTasks(storage.NewMemoryStorage(), nil)
		if err != nil {
			t.Fatalf("OpenTasks failed: %v", err)
		}
		harness := res.Harness
		root, err := harness.Root(ctx)
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		reviews := []tcgdReviewSpec{{
			Key:  "review-7",
			Seed: jsonx.ObjectOf("path", "a.ts", "patch", "-old\n+new"),
		}}

		type seenFrame struct {
			Key      string
			Comments int
		}
		var seenMu sync.Mutex
		var seen []seenFrame

		consumer := chord.DefineFacet(chord.Facet{
			Id: "app.diff-reviews/consumer",
			Setup: func(env chord.FacetEnvironment) error {
				env.Observe(tcgdDiffReviewsSvc, func(service any, callCtx chord.Context) error {
					proxy := service.(tcgdServiceProxy)
					identRaw, err := proxy.Call("identity", callCtx)
					if err != nil {
						return err
					}
					identObj := identRaw.(*jsonx.Object)
					keyVal, _ := identObj.Get("key")
					keyStr := keyVal.(string)

					rawState, err := proxy.Get("state")
					if err != nil {
						return err
					}
					unsub, err := rawState.(tcgdStateMember).Subscribe(func(value chord.JsonValue, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
						commentsLen := -1
						if obj, ok := value.(*jsonx.Object); ok && obj != nil {
							if arr, ok := func() ([]any, bool) {
								v, _ := obj.Get("comments")
								a, ok := v.([]any)
								return a, ok
							}(); ok {
								commentsLen = len(arr)
							}
						}
						seenMu.Lock()
						seen = append(seen, seenFrame{Key: keyStr, Comments: commentsLen})
						seenMu.Unlock()
						return nil
					})
					if err != nil {
						return err
					}
					env.Own(func() error {
						unsub()
						return nil
					})
					_, err = proxy.Call("addComment", jsonx.ObjectOf("id", "c1", "line", 1, "text", "why?"), callCtx)
					return err
				})
				return nil
			},
		})
		host, err := chord.CreateFacetHost(&chord.FacetOptions{
			Facets: []chord.Facet{tcgdReviewFacet(harness, root.Id(), reviews, ctx), consumer},
		})
		if err != nil {
			t.Fatalf("CreateFacetHost failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			seenMu.Lock()
			defer seenMu.Unlock()
			for _, f := range seen {
				if f.Comments == 1 {
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			t.Fatalf("Eventually failed: %v", err)
		}
		seenMu.Lock()
		gotSeen := slices.Clone(seen)
		seenMu.Unlock()
		wantSeen := []seenFrame{{Key: "review-7", Comments: 0}, {Key: "review-7", Comments: 1}}
		if !reflect.DeepEqual(gotSeen, wantSeen) {
			t.Fatalf("seen = %#v, want %#v", gotSeen, wantSeen)
		}
		snap, err := harness.Snapshot(ctx, tcgdReviewDoc, root.Id(), "review-7")
		if err != nil || snap == nil {
			t.Fatalf("Snapshot failed: %v %v", snap, err)
		}
		p, _ := snap.Get("path")
		commentsVal, _ := snap.Get("comments")
		comments := commentsVal.([]any)
		if p != "a.ts" || len(comments) != 1 {
			t.Fatalf("unexpected review snapshot: %v", snap)
		}
		c1Id, _ := comments[0].(*jsonx.Object).Get("id")
		if c1Id != "c1" {
			t.Fatalf("expected comment c1, got %v", comments[0])
		}
		_ = host.Dispose()
		_ = harness.Close(ctx)
	})

	t.Run("runs the job output watch until the producer retires its document", func(t *testing.T) {
		tcgdLogged.Clear()
		tcgdSentMu.Lock()
		tcgdSent = nil
		tcgdRendered = nil
		tcgdSentMu.Unlock()

		gate := testkit.NewDeferred[struct{}]()
		job := durable.DefineTask[tcgdJobInput, tcgdJobCheckpoint, any, struct{}](
			durable.TaskDefinition[tcgdJobInput, tcgdJobCheckpoint, any, struct{}]{
				Name:    "app.job",
				Version: 1,
				Initial: func(_ tcgdJobInput) tcgdJobCheckpoint {
					return tcgdJobCheckpoint{Phase: "running"}
				},
				Phases: map[string]durable.PhaseHandler[tcgdJobInput, tcgdJobCheckpoint, any, struct{}]{
					"running": func(c chord.Context, task durable.RunningTask[tcgdJobInput, tcgdJobCheckpoint], rt *durable.TaskRuntime[tcgdJobInput, tcgdJobCheckpoint, any, struct{}]) error {
						if err := tcgdAppendJobOutput(rt, fmt.Sprintf("$ %s\n", task.Input.Command), c); err != nil {
							return err
						}
						if _, err := gate.Wait(); err != nil {
							return err
						}
						if err := tcgdAppendJobOutput(rt, "ok\n", c); err != nil {
							return err
						}
						return rt.Commit(c, func(_ durable.Tx, cur durable.RunningTask[tcgdJobInput, tcgdJobCheckpoint]) (*durable.NextTaskState[tcgdJobCheckpoint, any], error) {
							next := testkit.Completed[tcgdJobCheckpoint, any](cur.Checkpoint, nil)
							return &next, nil
						})
					},
				},
				Abort: func(_ chord.Context, _ durable.RunningTask[tcgdJobInput, tcgdJobCheckpoint], _ *durable.TaskRuntime[tcgdJobInput, tcgdJobCheckpoint, any, struct{}]) error {
					return nil
				},
			},
		)
		res, err := testkit.OpenTasks(storage.NewMemoryStorage(), []durable.AnyTask{job})
		if err != nil {
			t.Fatalf("OpenTasks failed: %v", err)
		}
		harness := res.Harness
		defer func() { _ = harness.Close(ctx) }()
		root, err := harness.Root(ctx)
		if err != nil {
			t.Fatalf("Root failed: %v", err)
		}
		rawId, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(job, tcgdJobInput{Command: "make"}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			t.Fatalf("CreateTask failed: %v", err)
		}
		tid := rawId.(durable.TaskId)
		if err := harness.Resume(); err != nil {
			t.Fatalf("Resume failed: %v", err)
		}
		if err := testkit.Eventually(func() (bool, error) {
			snap, err := harness.Snapshot(ctx, tcgdJobOutputDoc, tid)
			if err != nil || snap == nil {
				return false, err
			}
			chunks, _ := snap.Get("chunks")
			return chunks == float64(1), nil
		}); err != nil {
			t.Fatalf("Eventually chunk 1 failed: %v", err)
		}

		finished := make(chan struct{})
		waitErrCh := make(chan error, 1)
		go func() {
			_, err := harness.WaitForTask(ctx, tid)
			close(finished)
			waitErrCh <- err
		}()
		observingErrCh := make(chan error, 1)
		go func() {
			observingErrCh <- tcgdObserveJob(harness, tid, finished, ctx)
		}()
		if err := testkit.Eventually(func() (bool, error) {
			return len(tcgdLogged.Snapshot()) == 1, nil
		}); err != nil {
			t.Fatalf("Eventually initial watch log failed: %v", err)
		}
		gate.Resolve(struct{}{})
		if err := <-waitErrCh; err != nil {
			t.Fatalf("WaitForTask failed: %v", err)
		}
		if err := <-observingErrCh; err != nil {
			t.Fatalf("observeJob failed: %v", err)
		}
		if !reflect.DeepEqual(tcgdLogged.Snapshot(), [][]any{{"$ make\n"}}) {
			t.Fatalf("logged = %#v", tcgdLogged.Snapshot())
		}
		tcgdSentMu.Lock()
		gotRendered := slices.Clone(tcgdRendered)
		tcgdSentMu.Unlock()
		if len(gotRendered) < 1 || gotRendered[0] != "$ make\nok\n" {
			t.Fatalf("rendered = %#v", gotRendered)
		}
		afterWatch, err := harness.WatchDoc(ctx, tcgdJobOutputDoc, tid)
		if err != nil || afterWatch != nil {
			t.Fatalf("expected nil WatchDoc after task completion/retirement, got (%v, %v)", afterWatch, err)
		}
	})
}
