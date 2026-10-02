# Porting the pi harness to Go

The reusable harness is the **agent loop plus its data/event/tool contracts** — not
the provider zoo, the coding-agent batteries, sessions, extensions, or the TUI.
Source of truth: `packages/agent/src/agent-loop.ts` (~748 ln) and the relevant
slice of `packages/agent/src/types.ts` + `packages/ai/src/types.ts`.

## Tier 1 — Port (the engine)

1. **Loop state machine** (`agent-loop.ts` `runLoop`)
   - outer/inner turn loop: assistant turn → detect tool calls → execute → repeat until none
   - tool execution ordering: sequential vs parallel (preflight sequentially, run
     concurrently, emit `tool_execution_end` in *completion* order, then tool-result
     messages in *source* order). May simplify to sequential-only for a first cut.
   - stop / continuation hooks: `shouldStopAfterTurn`, `prepareNextTurn`,
     `getSteeringMessages`, `getFollowUpMessages`
   - abort via `AbortSignal` → Go `context.Context`
   - preserve the exact event emission order
     (`agent_start → turn_start → message_start/end → tool_execution_* → turn_end → agent_end`)

2. **Data model** — `AgentMessage` / `UserMessage` / `AssistantMessage` /
   `ToolResultMessage`, content blocks (`text` / `thinking` / `toolCall` / `image`),
   `ToolCall`, `Usage`, `StopReason`. (Skip `thinking` if reasoning is unused.)

3. **Streaming event model + accumulator** — the `AssistantMessageEvent` union
   (`start` / `text_*` / `thinking_*` / `toolcall_*` / `done` / `error`) and the fold
   that builds the running `partial` message from deltas → a Go event channel + reducer.

4. **Tool contract** — `AgentTool` (`name`, JSON-schema params,
   `execute(id, params, ctx) → result`, parallel/sequential mode) + arg validation +
   `AgentToolResult` shaping.

5. **`convertToLlm` seam** — `AgentMessage[] → wire Message[]`, injected so the host
   owns message shaping.

## Tier 2 — Reimplement thin (rewrite, don't port)

6. **One wire adapter** — OpenAI-completions only: build the chat-completions request,
   parse the response, emit `AssistantMessageEvent`s. If the backend is non-streaming,
   synthesize the event sequence from a single response (no SSE needed). Ignore
   `pi-ai/providers/*` and `pi-ai/api/*` entirely.

7. **File tools** (`read` / `write` / `edit` / `grep` / `find`) — plain `AgentTool`
   implementations over file IO + grep. Trivial; not part of the loop.

## Tier 3 — Do NOT port

- `pi-ai` provider/model/auth machinery: registry, `AuthStorage`, OAuth, generated
  model tables, the 60 providers.
- Resource loader, extensions, skills, prompt templates, themes, context files.
- `SessionManager` / JSONL persistence / branching / `navigateTree`.
- `AgentSession` and `AgentHarness` wrapper layers — port the loop they sit on, not them.
- Compaction + branch summarization — **conditional**: skip if prompts stay within the
  context window; port `shouldCompact` + a summarize step only if worst-case runs overflow.
- Built-in retry — own it at the host level.
- coding-agent bash tool, HTML export, model cycling, TUI, orchestrator.

## Gotchas

- **TypeBox → Go**: tool schemas (`Type.Object(...)`) become JSON Schema + manual arg
  validation. Mechanical but pervasive.
- **No-throw contract**: a tool / `convertToLlm` error must become an error tool result
  or error event, never a panic that escapes the loop.
- **Parallel tool ordering** is the one place a naive port diverges — test event ordering
  explicitly.

## Suggested layout

```
agentloop/    # loop, message/event types, tool interface, accumulator
wire/openai/  # the one backend adapter (request → events)
tools/        # file tools + custom tools
host/         # supplies model config, transport, system prompt, tools; consumes events
```

Rough size: ~2–2.5k lines of Go for the core. The bulk of pi (providers, coding-agent,
sessions, TUI) is out of scope by construction.
