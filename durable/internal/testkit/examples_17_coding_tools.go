// Ported from packages/durable/test/examples/17-coding-tools.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage/jsonl"
	"github.com/keejkrej/pi-go/durable/tools"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Example17CodingTools runs the 17-coding-tools example.
func Example17CodingTools(out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	ctx := TestContext
	directory, err := os.MkdirTemp("", "pi-durable-example-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("hello world\n"), 0o600); err != nil {
		return err
	}

	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	toolUse := ai.StopReasonToolUse
	toolCall := func(name string, args *jsonx.Object, id string) providers.FauxResponseStep {
		return &providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall(name, args, &providers.FauxToolCallOptions{Id: &id})},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		)}
	}
	faux.SetResponses([]providers.FauxResponseStep{
		toolCall("read", jsonx.ObjectOf("path", "notes.txt"), "r"),
		toolCall("edit", jsonx.ObjectOf(
			"path", "notes.txt",
			"edits", []any{jsonx.ObjectOf("oldText", "world", "newText", "durable")},
		), "e"),
		toolCall("bash", jsonx.ObjectOf("command", "cat notes.txt"), "b"),
		toolCall("bash", jsonx.ObjectOf("command", "cat /tmp/pi-go-nonexistent-1gb.txt"), "c"),
		&providers.FauxResponseMessage{Message: providers.FauxAssistantMessage(providers.FauxAssistantText("The file now greets durable."), nil)},
	})

	isBigCat := func(call *ai.ToolCall) bool {
		if call == nil || call.Name != "bash" {
			return false
		}
		var in tools.BashToolInput
		if err := jsonx.Decode(call.Arguments, &in); err != nil {
			return false
		}
		return strings.Contains(in.Command, "1gb.txt")
	}
	var mu sync.Mutex
	var start time.Time
	timing := durable.DefineExtension(&durable.Extension{
		Name: "timing",
		Hooks: []durable.HookRegistration{
			durable.Hook(durable.ToolTask, durable.ToolHooks{
				BeforeTool: func(call *ai.ToolCall, _ durable.HookApi, _ chord.Context) (*durable.BeforeToolDecision, error) {
					if isBigCat(call) {
						mu.Lock()
						start = time.Now()
						mu.Unlock()
					}
					return nil, nil
				},
				AfterTool: func(call *ai.ToolCall, _ *durable.ToolExecutionResult, _ durable.HookApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
					if isBigCat(call) {
						mu.Lock()
						elapsed := time.Since(start)
						mu.Unlock()
						_, _ = fmt.Fprintf(out, "cat /tmp/1gb.txt took %d ms\n", elapsed.Milliseconds())
					}
					return nil, nil
				},
			}),
		},
	})

	registry := durable.CreateRegistry()
	if err := registry.Install(tools.CodingTools); err != nil {
		return err
	}
	if err := registry.Install(timing); err != nil {
		return err
	}
	st, err := jsonl.OpenNodeJsonlStorage(directory, ctx)
	if err != nil {
		return err
	}
	harness, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env: func(target durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			cwd := directory
			if target.Cwd != nil {
				cwd = *target.Cwd
			}
			return env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}), nil
		},
	}, ctx)
	if err != nil {
		return err
	}
	defer func() { _ = harness.Close(ctx) }()

	root, err := harness.Root(ctx, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
			Cwd:   jsonx.Some(directory),
		},
	})
	if err != nil {
		return err
	}

	q := "Greet durable instead."
	sub, err := root.Submit(durable.SubmissionDraft{
		Type:    durable.SubmissionTypeInput,
		Content: ai.UserMessageContent{Text: &q},
	}, ctx)
	if err != nil {
		return err
	}
	settled, err := sub.Wait(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "status: %s\n", settled.Status)

	transcript, err := root.Entries(durable.ConversationEntryQuery{}, 20, nil, ctx)
	if err != nil {
		return err
	}
	items := slices.Clone(transcript.Items)
	slices.Reverse(items)
	for _, entry := range items {
		if !durable.ToolResultEntry.Is(&entry) {
			_, _ = fmt.Fprintf(out, "%s\n", entry.Kind)
			continue
		}
		res, ok := entry.Model[0].(*ai.ToolResultMessage)
		if !ok {
			continue
		}
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*ai.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		txt := sb.String()
		if len(txt) > 200 {
			txt = txt[:200] + "…"
		}
		_, _ = fmt.Fprintf(out, "%s %s: %q\n", entry.Kind, res.ToolName, txt)
	}
	if settled.Status == durable.SubmissionStatusDone && settled.Type == durable.SubmissionTypeInput && settled.Answer != nil {
		raw, err := root.Commit(ctx, func(tx durable.Tx) (any, error) {
			return tx.TypedEntry(durable.AssistantEntry, *settled.Answer)
		})
		if err != nil {
			return err
		}
		if answer, ok := raw.(*durable.EntryRecord); ok && answer != nil && len(answer.Model) > 0 {
			if msg, ok := answer.Model[0].(*ai.AssistantMessage); ok {
				_, _ = fmt.Fprintf(out, "answer: %v\n", msg.Content)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "notes.txt"))
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "file: %q\nstorage: %s\n", string(data), directory)
	return harness.Close(ctx)
}
