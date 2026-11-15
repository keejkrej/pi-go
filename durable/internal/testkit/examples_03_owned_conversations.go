// Ported from packages/durable/test/examples/03-owned-conversations.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type e0ocState struct {
	Phase string `json:"phase"`
}

// Example03OwnedConversations runs the 03-owned-conversations example.
func Example03OwnedConversations(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	session := durable.NewSession(storage.NewMemoryStorage())
	defer func() { _ = session.Close(ctx) }()

	supervisor := durable.DefineTask[any, e0ocState, any, struct{}](durable.TaskDefinition[any, e0ocState, any, struct{}]{
		Name:    "example.supervisor",
		Version: 1,
		Initial: func(_ any) e0ocState { return e0ocState{Phase: "ready"} },
		Phases: map[string]durable.PhaseHandler[any, e0ocState, any, struct{}]{
			"ready": func(_ chord.Context, _ durable.RunningTask[any, e0ocState], _ *durable.TaskRuntime[any, e0ocState, any, struct{}]) error {
				return nil
			},
		},
		Abort: func(_ chord.Context, _ durable.RunningTask[any, e0ocState], _ *durable.TaskRuntime[any, e0ocState, any, struct{}]) error {
			return nil
		},
	})

	agentRegistry, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "example.agent-registry",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject { return jsonx.ObjectOf("agents", jsonx.NewObject()) },
	})
	if err != nil {
		return err
	}

	var mainConv *durable.ConversationRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		})
		if err != nil {
			return err
		}
		mainConv = c
		return nil
	}); err != nil {
		return err
	}

	var supervisorId durable.TaskId
	var child *durable.ConversationRecord
	if err := session.Commit(ctx, func(tx durable.Tx) error {
		bg := true
		tid, err := tx.CreateTask(supervisor, nil, durable.TaskOptions{
			Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
			ConversationId: &mainConv.Id,
			Background:     &bg,
		})
		if err != nil {
			return err
		}
		c, err := tx.CreateConversation(durable.ConversationCreateTxOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipTask, TaskId: tid},
		})
		if err != nil {
			return err
		}
		doc, err := tx.Doc(agentRegistry, mainConv.Id)
		if err != nil {
			return err
		}
		agentsRaw, _ := doc.Value.Get("agents")
		agentsObj, _ := agentsRaw.(*jsonx.Object)
		if agentsObj == nil {
			agentsObj = jsonx.NewObject()
			doc.Value.Set("agents", agentsObj)
		}
		agentsObj.Set("researcher", jsonx.ObjectOf(
			"conversationId", float64(c.Id),
			"requestId", fmt.Sprintf("researcher:first-message:%d", tid),
		))
		supervisorId = tid
		child = c
		return nil
	}); err != nil {
		return err
	}

	snap, err := session.Snapshot(ctx, agentRegistry, mainConv.Id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "supervisor task: %d\nchild conversation: %+v\nregistry: %v\n", supervisorId, child, snap)
	return session.Close(ctx)
}
