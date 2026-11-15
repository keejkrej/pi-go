// Ported from packages/durable/test/examples/13-recovery.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
)

type e1rTickerInput struct {
	To int `json:"to"`
}

type e1rTickerState struct {
	Phase string `json:"phase"`
	N     int    `json:"n"`
}

// Example13Recovery runs the 13-recovery example.
func Example13Recovery(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	directory, err := os.MkdirTemp("", "pi-durable-example-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	databasePath := filepath.Join(directory, "session.sqlite")

	var mu sync.Mutex
	reachedTick := func(int) {}
	ticker := durable.DefineTask[e1rTickerInput, e1rTickerState, string, struct{}](
		durable.TaskDefinition[e1rTickerInput, e1rTickerState, string, struct{}]{
			Name:    "example.ticker",
			Version: 1,
			Initial: func(_ e1rTickerInput) e1rTickerState { return e1rTickerState{Phase: "tick", N: 1} },
			Phases: map[string]durable.PhaseHandler[e1rTickerInput, e1rTickerState, string, struct{}]{
				"tick": func(taskCtx chord.Context, task durable.RunningTask[e1rTickerInput, e1rTickerState], runtime *durable.TaskRuntime[e1rTickerInput, e1rTickerState, string, struct{}]) error {
					n := task.Checkpoint.N
					memoKey := fmt.Sprintf("printed-%d", n)
					_, ok, err := runtime.Memo(taskCtx, memoKey)
					if err != nil {
						return err
					}
					if !ok {
						if _, err := runtime.MemoPut(taskCtx, memoKey, true); err != nil {
							return err
						}
						_, _ = fmt.Fprintf(out, "tick %d\n", n)
					}
					mu.Lock()
					cb := reachedTick
					mu.Unlock()
					cb(n)

					if err := runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e1rTickerInput, e1rTickerState]) (*durable.NextTaskState[e1rTickerState, string], error) {
						if n == task.Input.To {
							c := Completed[e1rTickerState, string](task.Checkpoint, fmt.Sprintf("counted to %d", n))
							return &c, nil
						}
						return &durable.NextTaskState[e1rTickerState, string]{
							Status:     durable.TaskStateRunning,
							Checkpoint: e1rTickerState{Phase: "tick", N: n + 1},
						}, nil
					}); err != nil {
						return err
					}
					return runtime.Sleep(taskCtx, time.Now().UnixMilli()+50)
				},
			},
			Abort: func(taskCtx chord.Context, _ durable.RunningTask[e1rTickerInput, e1rTickerState], runtime *durable.TaskRuntime[e1rTickerInput, e1rTickerState, string, struct{}]) error {
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e1rTickerInput, e1rTickerState]) (*durable.NextTaskState[e1rTickerState, string], error) {
					return &durable.NextTaskState[e1rTickerState, string]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "ticker",
		Tasks: []durable.AnyTask{ticker},
	})); err != nil {
		return err
	}
	open := func() (durable.Harness, error) {
		st, err := sqlite.OpenNodeSqliteStorage(databasePath)
		if err != nil {
			return nil, err
		}
		return durable.HarnessOpen(st, durable.HarnessOptions{Models: ai.CreateModels(nil), Registry: registry}, ctx)
	}

	firstRun, err := open()
	if err != nil {
		return err
	}
	root, err := firstRun.Root(ctx)
	if err != nil {
		_ = firstRun.Close(ctx)
		return err
	}
	rawId, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(ticker, e1rTickerInput{To: 5}, durable.TaskOptions{
			Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
		})
	})
	if err != nil {
		_ = firstRun.Close(ctx)
		return err
	}
	tickerId := rawId.(durable.TaskId)

	var once sync.Once
	tickTwo := make(chan struct{})
	mu.Lock()
	reachedTick = func(n int) {
		if n == 2 {
			once.Do(func() { close(tickTwo) })
		}
	}
	mu.Unlock()
	if err := firstRun.Resume(); err != nil {
		_ = firstRun.Close(ctx)
		return err
	}
	<-tickTwo
	if err := firstRun.Close(ctx); err != nil {
		return err
	}
	mu.Lock()
	reachedTick = func(int) {}
	mu.Unlock()

	reader, err := open()
	if err != nil {
		return err
	}
	saved, err := reader.GetTask(ctx, tickerId)
	if err != nil {
		_ = reader.Close(ctx)
		return err
	}
	if err := reader.Close(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "closed; saved checkpoint: %+v memos: %v\n", saved.State, saved.Memos)

	secondRun, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = secondRun.Close(ctx) }()
	counted, err := secondRun.WaitForTask(ctx, tickerId)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "after reopen: %+v\n", counted.State.Outcome)
	return secondRun.Close(ctx)
}
