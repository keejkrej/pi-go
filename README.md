# pi-go

`pi-go` is a pure-Go (`CGO_ENABLED=0`) implementation of the Pi coding agent and its supporting packages, maintaining byte-level compatibility with Pi `v1.5.2` on-disk state (`~/.pi/agent` JSONL sessions, `settings.json`, `auth.json`, `models.json`, trust store, MCP config) and wire formats (JSONL RPC and CBOR).

## Features

- **Four execution modes**:
  - **Interactive TUI** (default when attached to a TTY): terminal interface built on [`tui`](tui/).
  - **Native GPU desktop GUI** (`--gui` / `--mode gui`): three-pane desktop workspace built on [`gui`](gui/) and `github.com/egoist/mygo`.
  - **Print & JSON modes** (`-p` / `--print`, `--mode json`): scriptable non-interactive runs and structured JSON event streams.
  - **RPC mode** (`--mode rpc`): line-delimited JSONL control protocol over stdin/stdout.
- **Built-in & QuickJS extensions**: native Go extensions (`llama.cpp`, `codemode`, `tool-search`, `mcp`) plus in-memory `esbuild` bundling and QuickJS/WASI (`wazero`) execution for TypeScript/JavaScript extensions without an external Node.js runtime.
- **Multi-provider streaming**: unified streaming adapters across Anthropic, OpenAI (Completions, Responses, Codex), Azure OpenAI, AWS Bedrock, Google Generative AI & Vertex, Mistral, Cloudflare Workers AI, and OpenRouter.
- **Package manager & subcommands**: `pi install`, `pi remove`, `pi update`, `pi list`, `pi config`, `pi auth`, and `pi mcp`.
- **Pure-Go build**: compiles with `CGO_ENABLED=0` across macOS, Linux, and Windows (`cmd/pi`).

## Quickstart

Requires Go `1.27.3` or newer (see [`go.mod`](go.mod)).

```bash
# Build the pi CLI binary
CGO_ENABLED=0 go build -o pi ./cmd/pi

# Or install directly
CGO_ENABLED=0 go install ./cmd/pi
```

Common invocations:

```bash
# Interactive TUI in the current directory
./pi
./pi -c                         # continue most recent session
./pi -r                         # pick a session to resume
./pi @file.go "explain this"    # attach file context to initial prompt

# Native GPU desktop GUI
./pi --gui

# Headless print, JSON event stream, or RPC
./pi -p "summarize this repository"
./pi --mode json "list Go packages"
./pi --mode rpc

# Manage auth, MCP servers, and extension packages
./pi auth login
./pi mcp list
./pi install <source>
```

## Configuration & State

By default, Pi reads and writes user state under `~/.pi/agent` (overridable via environment variables and CLI flags):

- `PI_CODING_AGENT_DIR` — override the configuration directory (default `~/.pi/agent`)
- `PI_CODING_AGENT_SESSION_DIR` — override session JSONL directory (also `--session-dir <dir>`)
- `PI_OFFLINE=1` — disable startup network operations (equivalent to `--offline`)

## CLI Entrypoints (`cmd/`)

| Command | Path | Description |
|---|---|---|
| `pi` | [`cmd/pi`](cmd/pi/main.go) | Main Pi CLI (TUI, `--gui`, print, JSON, RPC, package manager, auth, MCP, experimental commands) |
| `pi-ai` | [`cmd/pi-ai`](cmd/pi-ai/main.go) | Standalone AI model/auth CLI |
| `pi-evals` | [`cmd/pi-evals`](cmd/pi-evals/main.go) | Evaluation suite runner |
| `pi-evals-entrypoint` | [`cmd/pi-evals-entrypoint`](cmd/pi-evals-entrypoint/main.go) | Dockerized eval container entrypoint |

Helper stubs in `cmd/`: [`check-model-data`](cmd/check-model-data/main.go), [`generate-models`](cmd/generate-models/generate_models_part1.go), and [`generate-test-image`](cmd/generate-test-image/main.go) (placeholders; not yet implemented).

## Repository Layout

| Path | Purpose |
|---|---|
| [`agent`](agent/) | Core agent loop, event stream, sequential/parallel tool scheduler, proxy client |
| [`ai`](ai/) | Provider APIs, model catalog, auth/OAuth credential resolution, streaming types |
| [`chord`](chord/) | Context/delta state engine, facets, esbuild bundling, node bindings |
| [`client`](client/) | Client transport and RPC/protocol connection layer |
| [`codemode`](codemode/) | Sandboxed QuickJS code-execution runtime and TypeScript declaration builder |
| [`codingagent`](codingagent/) | Coding agent core, sessions, tools, extensions, CLI, TUI/GUI/RPC modes, experimental runtime |
| [`durable`](durable/) | Durable agent harness, environment abstraction, JSONL/SQLite storage |
| [`evals`](evals/) | Evaluation harness, suites, and Docker definitions |
| [`gui`](gui/) | Layer-0 pure-Go GPU UI toolkit (`github.com/egoist/mygo`) decoupled from agent/session layers |
| [`mcp`](mcp/) | Model Context Protocol client, OAuth, JSON-RPC types, stdio/HTTP/in-memory transports |
| [`protocol`](protocol/) | Wire framing, codec, and byte-exact CBOR encoder/decoder |
| [`server`](server/) | Session router, listener, and Unix transport server |
| [`telemetry`](telemetry/) | Metrics and telemetry interfaces (memory and noop implementations) |
| [`tui`](tui/) | Terminal UI framework, components, keybindings, Markdown/ANSI/image rendering |
| [`internal`](internal/) | Pure-Go foundational ports (`jsonx`, `js`, `jsre`, `jsdiff`, `omap`, `typebox`, `quickjs`, `ignore`, `minimatch`, `semver`, `marked`, `mermaid`, `highlight`, `eastasian`, `hostedgit`, `lockfile`, `ansi`, `partialjson`, `sse`, `xspawn`) |
| [`docs`](docs/README.md) | Developer architecture, GUI, extensions, development, and porting docs |
| [`AGENTS.md`](AGENTS.md) | Agent & contributor conventions, build/test commands, and invariants |

## Development & Verification

All unit and integration tests run offline using faux providers and local test fixtures:

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
```

See [`AGENTS.md`](AGENTS.md), [`docs/README.md`](docs/README.md), and [`docs/development.md`](docs/development.md) for detailed developer workflows, cross-compilation notes, and testing guidelines.
