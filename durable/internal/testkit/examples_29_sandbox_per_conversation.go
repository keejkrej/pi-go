// Ported from packages/durable/test/examples/29-sandbox-per-conversation.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example29SandboxPerConversation runs the 29-sandbox-per-conversation example.
func Example29SandboxPerConversation(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	sandboxDoc, err := durable.DefineDoc(durable.DocDefinition{
		Kind:    "app.sandbox",
		Version: 1,
		Scope:   durable.DocumentScopeConversation,
		History: durable.DocumentHistoryLatest,
		Fork:    durable.DocumentForkInitial,
		Initial: func() durable.JsonObject { return jsonx.NewObject() },
	})
	if err != nil {
		return err
	}

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	noteCall := func(text string) providers.FauxResponseStep {
		return &providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("write", jsonx.ObjectOf("path", "note.txt", "content", text), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)}
	}
	faux.SetResponses([]providers.FauxResponseStep{
		noteCall("from alice"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Saved."), nil)},
		noteCall("from bob"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("Saved."), nil)},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(storage.NewMemoryStorage(), durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env: func(target durable.EnvTarget, envCtx chord.Context) (env.ExecutionEnv, error) {
			snap, err := target.Read.Snapshot(envCtx, sandboxDoc, target.ConversationId)
			if err != nil || snap == nil {
				return nil, err
			}
			rawPath, ok := snap.Get("path")
			if !ok {
				return nil, nil
			}
			p, _ := rawPath.(string)
			if p == "" {
				return nil, nil
			}
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: p}), nil
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	model := durable.ModelRef{Provider: "faux", ModelId: "faux-1"}
	type sandboxPair struct {
		conv durable.Conversation
		path string
	}
	convFor := func(user string) (sandboxPair, error) {
		p, err := os.MkdirTemp("", fmt.Sprintf("pi-durable-sandbox-%s-*", user))
		if err != nil {
			return sandboxPair{}, err
		}
		conv, err := harness.CreateConversation(durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
			Agent:     &durable.AgentChange{Model: jsonx.Some(model)},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				d, err := tx.Doc(sandboxDoc, id)
				if err != nil {
					return err
				}
				d.Value.Set("path", p)
				return nil
			},
		}, ctx)
		if err != nil {
			_ = os.RemoveAll(p)
			return sandboxPair{}, err
		}
		return sandboxPair{conv: conv, path: p}, nil
	}

	alice, err := convFor("alice")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(alice.path) }()
	bob, err := convFor("bob")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(bob.path) }()

	for _, pair := range []sandboxPair{alice, bob} {
		q := "Leave a note."
		sub, err := pair.conv.Submit(durable.SubmissionDraft{
			Type:    durable.SubmissionTypeInput,
			Content: ai.UserMessageContent{Text: &q},
		}, ctx)
		if err != nil {
			return err
		}
		if _, err := sub.Wait(ctx); err != nil {
			return err
		}
	}

	aliceNote, err := os.ReadFile(filepath.Join(alice.path, "note.txt"))
	if err != nil {
		return err
	}
	bobNote, err := os.ReadFile(filepath.Join(bob.path, "note.txt"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "alice's sandbox: %s\nbob's sandbox: %s\n", string(aliceNote), string(bobNote))
	return harness.Close(ctx)
}
