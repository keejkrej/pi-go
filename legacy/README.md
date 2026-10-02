# pi-go

A small, standard-library-only Go port of the [pi](https://github.com/) agent
loop. It contains three packages plus an example host:

- **`agentloop`** — the engine. A faithful port of pi's agent loop: the streaming
  event model, JSON-Schema argument validation, the tool-execution scheduler
  (sequential and parallel), and an unbounded event stream with a terminal result.
- **`wire/openai`** — one backend adapter. A clean-room, streaming OpenAI Chat
  Completions client that satisfies `agentloop.StreamFn`.
- **`tools`** — local file tools (`read`, `write`, `edit`, `grep`, `find`) built
  on `agentloop.AgentTool`.
- **`cmd/pi-agent`** — a ~150-line example host wiring the three together.

There are **no external dependencies**. `go.mod` has no `require` block, and the
module targets Go 1.26.

## Package layout

```
go.mod
agentloop/        engine: types, events, stream, tool, validate, config, loop, faux
wire/openai/      OpenAI Chat Completions streaming adapter
tools/            local file tools (read/write/edit/grep/find)
cmd/pi-agent/     example host (package main)
```

## How it fits together

The engine is driven by a single function value, `StreamFn`, that turns a request
into a stream of assistant-message events:

```go
type StreamFn func(ctx context.Context, model *Model, c *Context, opts *StreamOptions) *AssistantMessageEventStream
```

`agentloop.AgentLoop` runs turns: it calls the `StreamFn`, accumulates the
assistant message, executes any tool calls it requests, appends the tool results,
and loops until the model stops (or a `ShouldStopAfterTurn` / terminate hint /
abort fires). Everything the loop does is reported as an `AgentEvent` on the
returned stream.

```
prompts ──► AgentLoop ──► StreamFn ──► assistant message
                │                          │
                │                          └─► tool calls ──► AgentTool.Execute ──► tool results
                │                                                                        │
                └──────────────── next turn ◄────────────────────────────────────────────┘
```

## Public API of `agentloop`

Construct messages and run the loop:

```go
func NewUserText(text string) *UserMessage

func AgentLoop(ctx context.Context, prompts []AgentMessage, agentCtx *AgentContext,
    config *AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage]

func AgentLoopContinue(ctx context.Context, agentCtx *AgentContext,
    config *AgentLoopConfig, streamFn StreamFn) *EventStream[AgentEvent, []AgentMessage]
```

`AgentLoopContinue` resumes an existing context without appending a prompt. If the
context is empty or its last message is an assistant message, it returns a stream
that immediately ends with a nil result and emits no events.

The returned stream's terminal event is `AgentEndEvent`; `Result()` returns the
slice of new messages produced by the run.

Key configuration (`AgentLoopConfig`):

- `Model *Model` and `ConvertToLlm func([]AgentMessage) ([]Message, error)` are
  required. `ConvertToLlm` maps the host's transcript into the LLM-visible message
  subset.
- `ToolExecution ExecutionMode` — `""`/`"parallel"` (default) runs independent
  tool calls concurrently; `"sequential"` runs them one at a time. A single tool
  that declares `ExecutionMode() == "sequential"` forces the whole batch
  sequential.
- Optional hooks: `TransformContext`, `GetApiKey`, `BeforeToolCall`,
  `AfterToolCall`, `ShouldStopAfterTurn`, `PrepareNextTurn`,
  `GetSteeringMessages`, `GetFollowUpMessages`.
- `AutoRetry *AutoRetryConfig` — when set, a turn whose assistant response fails
  with a transient provider/transport error (per `IsRetryableAssistantError`, a
  port of pi's retry classifier) is restarted with exponential backoff. The
  errored assistant message is dropped from the context and the run result, and
  an `AutoRetryEvent` is emitted before each backoff sleep.
- Per-request wire knobs forwarded to the `StreamFn` on every turn: `Headers`
  (override/suppress model default headers), `ToolChoice`
  (`auto`/`none`/`required`/forced function), `OnPayload` (inspect/replace the
  outgoing request body — the universal escape hatch), and
  `MaxRetries`/`MaxRetryDelay` (pre-stream HTTP retries in the adapter, safe
  because they happen before any event is emitted; honors `Retry-After`).

Event ordering guarantees (the central correctness property of the port):

- `tool_execution_start` events fire in source order.
- For parallel execution, `tool_execution_end` events fire in **completion**
  order, while the tool-result `message_start`/`message_end` events are emitted in
  **source** order.
- For sequential execution, everything is strictly in source order with no
  overlap.

Other exported building blocks: the `Content` union (`TextContent`,
`ThinkingContent`, `ImageContent`, `ToolCall`), the message types (`UserMessage`,
`AssistantMessage`, `ToolResultMessage`), the `AgentTool`/`FuncTool` interface,
`ValidateToolArguments`, the generic `EventStream[T, R]`, and `FauxStreamFn` (a
scripted `StreamFn` for tests and examples). See the package doc comments for the
full surface.

## Running `cmd/pi-agent`

```bash
export OPENAI_API_KEY=sk-...
# optional:
export OPENAI_BASE_URL=https://api.openai.com/v1   # default
export PI_MODEL=gpt-4o-mini                          # default

go run ./cmd/pi-agent "list the Go files in this directory and summarize them"
# or pipe the instruction on stdin:
echo "what does loop.go do?" | go run ./cmd/pi-agent
```

The host reads the instruction from the command line (or stdin), wires the file
tools rooted at `.`, runs one `AgentLoop`, and prints a readable trace: assistant
text deltas, tool invocations with arguments and a result snippet, and turn/agent
boundaries. Ctrl-C aborts via `signal.NotifyContext`.

## Plugging in a custom tool

Implement `agentloop.AgentTool`, or use `agentloop.FuncTool` for a closure-based
tool:

```go
tool := &agentloop.FuncTool{
    NameVal:        "echo",
    LabelVal:       "Echo",
    DescriptionVal: "Echoes its message argument back.",
    ParametersVal: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "message": map[string]any{"type": "string"},
        },
        "required": []any{"message"},
    },
    Mode: agentloop.ExecutionParallel,
    ExecuteFn: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate agentloop.UpdateFunc) (agentloop.ToolResult, error) {
        return agentloop.TextResult(params["message"].(string)), nil
    },
}
```

Add it to `AgentContext.Tools`. Arguments are validated and coerced against
`Parameters()` before `Execute` is called; return an `error` to produce an error
tool-result (panics are recovered and converted to errors too).

## Plugging in a custom StreamFn

Any function matching the `StreamFn` signature works in place of
`openai.StreamSimple`. It must never return nil and never panic: encode failures
as a `StartEvent` followed by an `ErrorEvent` whose final `AssistantMessage` has
`StopReason` `error` or `aborted`. For tests, `agentloop.FauxStreamFn` replays
scripted `*AssistantMessage` values:

```go
stream := agentloop.AgentLoop(ctx, prompts, agentCtx, config,
    agentloop.FauxStreamFn(
        agentloop.FauxToolCall("call_1", "read", map[string]any{"path": "x.go"}),
        agentloop.FauxText("Done."),
    ))
```

## Ported pi features beyond the thin loop

The port has grown a parity layer with pi's `@earendil-works/pi-ai` OpenAI
adapter and selected agent-runtime utilities:

- **Model compat switches** (`agentloop.Compat` on `Model.Compat`): thinking
  request formats (`openai` `reasoning_effort`, `zai`, `qwen`,
  `qwen-chat-template`, `chat-template` with `ChatTemplateVar` placeholders,
  `deepseek`, `openrouter`, `together`, `ant-ling`, `string-thinking`),
  developer-role system prompts, `strict:false` tool mode, tool-result `name`,
  synthetic assistant after tool results, `stream_options` opt-out, and
  `ExtraBody` (arbitrary extra request fields, e.g. vLLM
  `chat_template_kwargs` cousins).
- **Thinking replay**: assistant thinking blocks are re-sent under the delta
  field name they arrived on (`reasoning_content` etc.), or folded into plain
  text with `RequiresThinkingAsText` — matching pi's signature replay.
- **Tool-result images**: when the model declares `image` input, image blocks in
  tool results are re-emitted as a follow-up user message ("Attached image(s)
  from tool result:"), since OpenAI `role:"tool"` messages cannot carry images.
- **Retry**: `IsRetryableAssistantError` (pi's classifier regexes), pre-stream
  HTTP retries with `Retry-After` support in the adapter, and loop-level
  `AutoRetry` with exponential backoff.
- **Transcript persistence**: `MarshalMessages`/`UnmarshalMessages` round-trip
  the sealed message/content unions using pi's JSONL field names, so hosts can
  store and resume transcripts.
- **Output truncation**: `TruncateHead`/`TruncateTail`/`TruncateLine` port pi's
  line+byte tool-output limits.

## Out of scope vs pi

This remains a focused port. Intentionally omitted:

- **No provider zoo.** Only OpenAI Chat Completions streaming; no Anthropic,
  Bedrock, Vertex, or Responses-API adapters. No cache-control markers or
  session-affinity headers.
- **No session tree.** Transcripts serialize to JSON, but there is no
  entry-tree storage, branching, forking, or leaf navigation.
- **No TUI.** `cmd/pi-agent` is a plain stdout trace, not an interactive UI.
- **No context compaction / summarization.** The context grows as the loop runs;
  there is no automatic trimming.
