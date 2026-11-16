// Ported from packages/durable/test/examples/24-child-tasks.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
)

type e2ctPaymentInput struct {
	Card string `json:"card"`
}

type e2ctPaymentState struct {
	Phase string `json:"phase"`
	At    int64  `json:"at"`
}

type e2ctPaymentResult struct {
	Card string `json:"card"`
}

type e2ctCheckoutInput struct {
	Cards []string `json:"cards"`
}

type e2ctCheckoutState struct {
	Phase    string           `json:"phase"`
	Payments []durable.TaskId `json:"payments,omitzero"`
}

// Example24ChildTasks runs the 24-child-tasks example.
func Example24ChildTasks(out io.Writer) error {
	sw := NewSyncWriter(out)
	defer sw.Close()
	out = sw
	ctx := TestContext
	var mu sync.Mutex
	charged := map[string]bool{}

	payment := durable.DefineTask[e2ctPaymentInput, e2ctPaymentState, e2ctPaymentResult, struct{}](
		durable.TaskDefinition[e2ctPaymentInput, e2ctPaymentState, e2ctPaymentResult, struct{}]{
			Name:    "example.payment",
			Version: 1,
			Initial: func(_ e2ctPaymentInput) e2ctPaymentState {
				return e2ctPaymentState{Phase: "charge", At: time.Now().UnixMilli() + 100}
			},
			Phases: map[string]durable.PhaseHandler[e2ctPaymentInput, e2ctPaymentState, e2ctPaymentResult, struct{}]{
				"charge": func(taskCtx chord.Context, task durable.RunningTask[e2ctPaymentInput, e2ctPaymentState], runtime *durable.TaskRuntime[e2ctPaymentInput, e2ctPaymentState, e2ctPaymentResult, struct{}]) error {
					card := task.Input.Card
					if strings.HasPrefix(card, "expired") {
						return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2ctPaymentInput, e2ctPaymentState]) (*durable.NextTaskState[e2ctPaymentState, e2ctPaymentResult], error) {
							return &durable.NextTaskState[e2ctPaymentState, e2ctPaymentResult]{
								Status: durable.TaskStateTerminal,
								Outcome: &durable.TypedTaskOutcome[e2ctPaymentResult]{
									Status: durable.TaskOutcomeFailed,
									Error:  &durable.TaskOutcomeError{Message: card + " declined"},
								},
							}, nil
						})
					}
					mu.Lock()
					charged[card] = true
					mu.Unlock()
					if err := runtime.Sleep(taskCtx, task.Checkpoint.At); err != nil {
						return err
					}
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2ctPaymentInput, e2ctPaymentState]) (*durable.NextTaskState[e2ctPaymentState, e2ctPaymentResult], error) {
						c := Completed[e2ctPaymentState, e2ctPaymentResult](task.Checkpoint, e2ctPaymentResult{Card: card})
						return &c, nil
					})
				},
			},
			Abort: func(taskCtx chord.Context, task durable.RunningTask[e2ctPaymentInput, e2ctPaymentState], runtime *durable.TaskRuntime[e2ctPaymentInput, e2ctPaymentState, e2ctPaymentResult, struct{}]) error {
				mu.Lock()
				refunded := charged[task.Input.Card]
				delete(charged, task.Input.Card)
				mu.Unlock()
				suffix := ""
				if refunded {
					suffix = ", refunded"
				}
				_, _ = fmt.Fprintf(out, "  payment %s aborted%s\n", task.Input.Card, suffix)
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2ctPaymentInput, e2ctPaymentState]) (*durable.NextTaskState[e2ctPaymentState, e2ctPaymentResult], error) {
					return &durable.NextTaskState[e2ctPaymentState, e2ctPaymentResult]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[e2ctPaymentResult]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	checkoutTask := durable.DefineTask[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}](
		durable.TaskDefinition[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}]{
			Name:    "example.checkout",
			Version: 1,
			Initial: func(_ e2ctCheckoutInput) e2ctCheckoutState { return e2ctCheckoutState{Phase: "pay"} },
			Phases: map[string]durable.PhaseHandler[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}]{
				"pay": func(taskCtx chord.Context, task durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState], runtime *durable.TaskRuntime[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}]) error {
					return runtime.Commit(taskCtx, func(tx durable.Tx, _ durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState]) (*durable.NextTaskState[e2ctCheckoutState, string], error) {
						payments := make([]durable.TaskId, 0, len(task.Input.Cards))
						for _, card := range task.Input.Cards {
							pid, err := tx.CreateTask(payment, e2ctPaymentInput{Card: card}, durable.TaskOptions{
								Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: task.Id},
							})
							if err != nil {
								return nil, err
							}
							payments = append(payments, pid)
						}
						return &durable.NextTaskState[e2ctCheckoutState, string]{
							Status:     durable.TaskStateWaiting,
							Checkpoint: e2ctCheckoutState{Phase: "decide", Payments: payments},
							On:         payments,
							Policy:     durable.JoinPolicyFailFast,
						}, nil
					})
				},
				"decide": func(taskCtx chord.Context, task durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState], runtime *durable.TaskRuntime[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}]) error {
					outcomes, err := runtime.Outcomes(taskCtx, task.Checkpoint.Payments)
					if err != nil {
						return err
					}
					statuses := make([]string, len(outcomes))
					paid := true
					for i, outcome := range outcomes {
						statuses[i] = string(outcome.Status)
						if outcome.Status != durable.TaskOutcomeCompleted {
							paid = false
						}
					}
					_, _ = fmt.Fprintf(out, "  payments: %s\n", strings.Join(statuses, ", "))
					return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState]) (*durable.NextTaskState[e2ctCheckoutState, string], error) {
						if paid {
							c := Completed[e2ctCheckoutState, string](task.Checkpoint, "order placed")
							return &c, nil
						}
						return &durable.NextTaskState[e2ctCheckoutState, string]{
							Status: durable.TaskStateTerminal,
							Outcome: &durable.TypedTaskOutcome[string]{
								Status: durable.TaskOutcomeFailed,
								Error:  &durable.TaskOutcomeError{Message: "payment failed"},
							},
						}, nil
					})
				},
			},
			Abort: func(taskCtx chord.Context, _ durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState], runtime *durable.TaskRuntime[e2ctCheckoutInput, e2ctCheckoutState, string, struct{}]) error {
				_, _ = fmt.Fprintln(out, "  checkout aborted")
				return runtime.Commit(taskCtx, func(_ durable.Tx, _ durable.RunningTask[e2ctCheckoutInput, e2ctCheckoutState]) (*durable.NextTaskState[e2ctCheckoutState, string], error) {
					return &durable.NextTaskState[e2ctCheckoutState, string]{
						Status:  durable.TaskStateTerminal,
						Outcome: &durable.TypedTaskOutcome[string]{Status: durable.TaskOutcomeAborted},
					}, nil
				})
			},
		},
	)

	registry := durable.CreateRegistry()
	if err := registry.Install(durable.DefineExtension(&durable.Extension{
		Name:  "checkout",
		Tasks: []durable.AnyTask{payment, checkoutTask},
	})); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "pi-durable-example-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	dbPath := filepath.Join(directory, "session.sqlite")

	open := func() (durable.Harness, durable.Conversation, error) {
		st, err := sqlite.OpenNodeSqliteStorage(dbPath)
		if err != nil {
			return nil, nil, err
		}
		h, err := durable.HarnessOpen(st, durable.HarnessOptions{Models: ai.CreateModels(nil), Registry: registry}, ctx)
		if err != nil {
			return nil, nil, err
		}
		r, err := h.Root(ctx)
		if err != nil {
			_ = h.Close(ctx)
			return nil, nil, err
		}
		return h, r, nil
	}

	printGraph := func(graph durable.TaskGraph) {
		nodes := make([]durable.TaskGraphNode, 0, len(graph.Tasks))
		for _, n := range graph.Tasks {
			nodes = append(nodes, n)
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Id < nodes[j].Id })
		var printNode func(node durable.TaskGraphNode, depth int)
		printNode = func(node durable.TaskGraphNode, depth int) {
			status := node.State.Status
			if status == "waiting" {
				parts := make([]string, len(node.State.On))
				for i, id := range node.State.On {
					parts[i] = fmt.Sprintf("%d", id)
				}
				status = "waiting on " + strings.Join(parts, ", ")
			}
			_, _ = fmt.Fprintf(out, "  %s%s %d: %s\n", strings.Repeat("  ", depth), node.Kind, node.Id, status)
			for _, candidate := range nodes {
				if candidate.Owner != nil && *candidate.Owner == node.Id {
					printNode(candidate, depth+1)
				}
			}
		}
		for _, node := range nodes {
			if node.Owner == nil {
				printNode(node, 0)
			}
		}
	}

	checkout := func(root durable.Conversation, cards []string) (durable.TaskId, error) {
		raw, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.CreateTask(checkoutTask, e2ctCheckoutInput{Cards: cards}, durable.TaskOptions{
				Ownership: durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			})
		})
		if err != nil {
			return 0, err
		}
		return raw.(durable.TaskId), nil
	}

	harness, root, err := open()
	if err != nil {
		return err
	}
	defer func() {
		if harness != nil {
			_ = harness.Close(ctx)
		}
	}()

	_, _ = fmt.Fprintln(out, "One card is declined:")
	id, err := checkout(root, []string{"visa-1", "expired-2", "visa-3", "visa-4"})
	if err != nil {
		return err
	}
	settled, err := harness.WaitForTask(ctx, id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  checkout: %s\n", settled.State.Outcome.Status)

	_, _ = fmt.Fprintln(out, "The customer cancels:")
	id, err = checkout(root, []string{"visa-5", "visa-6", "visa-7", "visa-8"})
	if err != nil {
		return err
	}
	if err := harness.Resume(); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	graph, err := harness.TaskGraph(ctx)
	if err != nil {
		return err
	}
	graphVal, _ := graph.Value()
	printGraph(graphVal)
	graph.Dispose()
	if _, err := harness.AbortTask(ctx, id); err != nil {
		return err
	}
	settled, err = harness.WaitForTask(ctx, id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  checkout: %s\n", settled.State.Outcome.Status)

	_, _ = fmt.Fprintln(out, "The process stops while the payments run, and a new one continues:")
	id, err = checkout(root, []string{"visa-9", "visa-10", "visa-11", "visa-12"})
	if err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := harness.Close(ctx); err != nil {
		return err
	}
	harness = nil

	harness, _, err = open()
	if err != nil {
		return err
	}
	settled, err = harness.WaitForTask(ctx, id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "  checkout: %s\n", settled.State.Outcome.Status)
	err = harness.Close(ctx)
	harness = nil
	return err
}
