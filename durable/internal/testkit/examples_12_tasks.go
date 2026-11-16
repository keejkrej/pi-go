// Ported from packages/durable/test/examples/12-tasks.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
)

type e1tPaymentInput struct {
	Amount int `json:"amount"`
}

type e1tPaymentState struct {
	Phase string `json:"phase"`
	Key   string `json:"key,omitzero"`
}

type e1tPaymentResult struct {
	Receipt int `json:"receipt"`
}

// Example12Tasks runs the 12-tasks example.
func Example12Tasks(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	var mu sync.Mutex
	payments := map[string]int{}

	payment := durable.DefineTask[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}](
		durable.TaskDefinition[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}]{
			Name:    "example.payment",
			Version: 1,
			Initial: func(_ e1tPaymentInput) e1tPaymentState { return e1tPaymentState{Phase: "prepare"} },
			Phases: map[string]durable.PhaseHandler[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}]{
				"prepare": func(taskCtx chord.Context, task durable.RunningTask[e1tPaymentInput, e1tPaymentState], runtime *durable.TaskRuntime[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}]) error {
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e1tPaymentInput, e1tPaymentState]) (*durable.NextTaskState[e1tPaymentState, e1tPaymentResult], error) {
						return &durable.NextTaskState[e1tPaymentState, e1tPaymentResult]{
							Status:     durable.TaskStateRunning,
							Checkpoint: e1tPaymentState{Phase: "charge", Key: fmt.Sprintf("payment-%d", task.Id)},
						}, nil
					})
				},
				"charge": func(taskCtx chord.Context, task durable.RunningTask[e1tPaymentInput, e1tPaymentState], runtime *durable.TaskRuntime[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}]) error {
					key := task.Checkpoint.Key
					mu.Lock()
					receipt, exists := payments[key]
					if !exists {
						receipt = task.Input.Amount * 100
						payments[key] = receipt
					}
					mu.Unlock()
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e1tPaymentInput, e1tPaymentState]) (*durable.NextTaskState[e1tPaymentState, e1tPaymentResult], error) {
						c := Completed[e1tPaymentState, e1tPaymentResult](task.Checkpoint, e1tPaymentResult{Receipt: receipt})
						return &c, nil
					})
				},
			},
			Abort: func(taskCtx chord.Context, task durable.RunningTask[e1tPaymentInput, e1tPaymentState], runtime *durable.TaskRuntime[e1tPaymentInput, e1tPaymentState, e1tPaymentResult, struct{}]) error {
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e1tPaymentInput, e1tPaymentState]) (*durable.NextTaskState[e1tPaymentState, e1tPaymentResult], error) {
					return &durable.NextTaskState[e1tPaymentState, e1tPaymentResult]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[e1tPaymentResult]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "payments",
		Tasks: []durable.AnyTask{payment},
	})); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   ai.CreateModels(nil),
		Registry: registry,
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx)
	if err != nil {
		return err
	}
	rawPaymentId, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
		return tx.CreateTask(payment, e1tPaymentInput{Amount: 5}, durable.TaskOptions{
			Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
		})
	})
	if err != nil {
		return err
	}
	paymentId := rawPaymentId.(durable.TaskId)
	paid, err := harness.WaitForTask(ctx, paymentId)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "payment outcome: %+v\n", paid.State.Outcome)
	return harness.Close(ctx)
}
