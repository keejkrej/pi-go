# AGENTS.md

Guidance for coding agents and contributors working in `github.com/keejkrej/pi-go`.

## Project Overview

`pi-go` is a pure-Go (`CGO_ENABLED=0`, Go `1.27.3+`) implementation of the Pi coding agent and its monorepo packages, maintaining byte-level compatibility with Pi `v1.5.2` on-disk state (`~/.pi/agent` JSONL sessions, `settings.json`, `auth.json`, `models.json`, trust store, MCP config) and wire formats (JSONL RPC and CBOR), alongside a native GPU desktop GUI (`--gui`) and QuickJS/WASI TypeScript/JavaScript extension execution.

## Build, Vet, and Test Commands

- **Build main CLI**:
  ```bash
  CGO_ENABLED=0 go build -o pi ./cmd/pi
  ```
  Always pass `-o` when building binaries or clean up temporary build outputs afterward. `cmd/pi` cross-compiles cleanly with `CGO_ENABLED=0` for `darwin`, `linux`, and `windows`.
- **Full build & vet** (`darwin` / `linux` — `client` and `server/transports/unix` require Unix domain sockets):
  ```bash
  CGO_ENABLED=0 go build ./...
  CGO_ENABLED=0 go vet ./...
  ```
- **Formatting & module checks**:
  ```bash
  gofmt -l .
  go mod tidy -diff
  ```
- **Run full test suite**:
  ```bash
  CGO_ENABLED=0 go test ./...
  ```
- **Run package tests with race detector**:
  ```bash
  CGO_ENABLED=0 go test -race ./<pkg>/...
  ```
- **Update golden fixtures** (flag registered in [`ai/internal/testkit`](ai/internal/testkit/fixture.go) and [`codingagent/internal/testkit`](codingagent/internal/testkit/fixture.go)):
  ```bash
  CGO_ENABLED=0 go test ./<pkg>/... -update
  ```
- **Dependencies**: Use `go mod tidy`. Never add `replace` directives or local machine path dependencies to `go.mod` or `package.json`.

## Package Architecture & Layer Boundaries

Dependencies flow strictly upward with zero import cycles:

1. **Foundation (`internal/*`)**: Pure-Go ports (`jsonx`, `js`, `jsre`, `jsdiff`, `omap`, `typebox`, `quickjs`, `ignore`, `minimatch`, `semver`, `marked`, `mermaid`, `highlight`, `eastasian`, `hostedgit`, `lockfile`, `ansi`, `partialjson`, `sse`, `xspawn`).
2. **Layer-0 UI Toolkits & Core Primitives**:
   - [`tui`](tui/) — terminal UI framework, input/mouse decoding, Markdown/ANSI/image rendering, and virtual terminal tests.
   - [`gui`](gui/) — decoupled Layer-0 GPU desktop toolkit over `github.com/egoist/mygo`.
   - [`telemetry`](telemetry/), [`chord`](chord/), [`codemode`](codemode/), [`protocol`](protocol/) (`protocol/cbor`), [`mcp/protocol`](mcp/protocol/).
3. **LLM, Transport, & Runtime**:
   - [`ai`](ai/) (`ai/api`, `ai/catalog`, `ai/oauth`, `ai/providers`, `ai/compat`), [`mcp`](mcp/) (`mcp/oauth`), [`agent`](agent/), [`client`](client/), [`server`](server/), [`durable`](durable/) (`durable/env`, `durable/storage/...`, `durable/tools`).
4. **Application & Evals**:
   - [`codingagent`](codingagent/) (`utils`, `core`, `cli`, `modes/...`, `extensions/...`, `experimental/...`), [`evals`](evals/), [`cmd/*`](cmd/).

Enforced structural seams:

- **`gui/` isolation**: `gui/` must **never** import `codingagent`, `agent`, or `ai`. All agent-aware desktop UI lives in [`codingagent/modes/gui`](codingagent/modes/gui/).
- **`PathMetadata` ownership**: `PathMetadata` is declared in [`codingagent/utils/source_info.go`](codingagent/utils/source_info.go) so `codingagent/core` and `codingagent/utils` remain acyclic.
- **Provider stream options**: Per-provider stream option structs live in `ai/api` and implement `ai.ProviderStreamOptions`.

## Core Engineering Invariants

- **Pure Go (`CGO_ENABLED=0`)**: Never `import "C"`. Platform-specific syscalls or OS frameworks go in `*_darwin.go`, `*_linux.go`, `*_unix.go`, `*_windows.go`, or `*_other.go` using `golang.org/x/sys` or `github.com/ebitengine/purego`.
- **On-disk & JSON compatibility (`internal/jsonx`)**: Use `jsonx.Object` for persisted `~/.pi/agent` documents (`settings.json`, `auth.json`, `models.json`, sessions JSONL, trust store, MCP config) so key order and unknown fields survive read-modify-write. Never iterate a Go `map` where order is observable; use `internal/omap` or sort explicitly (`js.CompareUTF16` / `js.LocaleCompare`).
- **JS semantics**: Use `internal/js` for UTF-16 code-unit indexing and `internal/jsre` where ECMAScript regex rules apply.
- **Unexported identifier stems & `doc.go`**: In multi-file packages, prefix unexported top-level declarations with the file's short stem (see [`docs/filemap.tsv`](docs/filemap.tsv)) to avoid intra-package name collisions. Keep package doc comments exclusively in `doc.go`.
- **Concurrency & lock discipline**: Never hold a `sync.Mutex` across listener callbacks, `StreamFn` calls, tool execution, blocking I/O, or `mygo.Window.Update`.
- **Desktop GUI (`gui/` & `codingagent/modes/gui/`)**: `github.com/egoist/mygo/ui` elements are immediate-mode and expire after each frame — never retain `ui.Element` values in structs or globals (persist `ui.Handle`, `ui.ListState`, `ui.ScrollState`, `ui.TextBuffer`, `editor.State`, `editor.DiffState`, or component structs). Marshal cross-goroutine updates via `GUI.Post` or `GUI.RequestRender`. Test headlessly with `ui.NewTester` and `GuiModeOptions{SkipWindow: true}`.
- **Embedded assets (`codingagent/utils/assets/`)**: Embedded via `//go:embed all:assets` in [`codingagent/utils/config.go`](codingagent/utils/config.go) and packaged by [`evals/docker/Dockerfile`](evals/docker/Dockerfile). Do not move or rename paths under `codingagent/utils/assets/` without updating both.

## Testing & Hygiene Rules

- **Offline tests only**: Never make live network or real LLM API calls in unit/integration tests. Use `providers.FauxProvider` ([`ai/providers`](ai/providers/)), `httptest.Server`, or local `testdata` fixtures.
- **Filesystem isolation**: Use `t.TempDir()` and `t.Setenv("HOME", ...)` / `t.Setenv("PI_CODING_AGENT_DIR", ...)` / `t.Setenv("PI_CODING_AGENT_SESSION_DIR", ...)`. Never combine `t.Setenv` with `t.Parallel()`.
- **No local path dependencies**: Never hardcode developer home directories (`~/workspace/...`, `/Users/<user>/...`, `C:/Users/...`) in code, tests, or scripts. Gate optional external cross-repo checks on explicit environment variables (`WAKU_REPO_DIR`, `PI_REPO_DIR`, `NODE_MODULES`) that skip or return cleanly when unset.

## Documentation

- [`docs/README.md`](docs/README.md) — Documentation index (developer guides + embedded user docs)
- [`docs/architecture.md`](docs/architecture.md) — Package dependency diagram, modes, and engineering invariants
- [`docs/gui.md`](docs/gui.md) — Native GPU desktop GUI toolkit and runtime mode
- [`docs/extensions.md`](docs/extensions.md) — Built-in Go extensions, QuickJS/WASI user extensions, and package manager
- [`docs/development.md`](docs/development.md) — Build, cross-compile, testing, snapshot updates, and Docker evals
- [`docs/porting.md`](docs/porting.md) — Historical TS-to-Go porting specification and interop contracts (`§1`–`§16`)
- [`docs/filemap.tsv`](docs/filemap.tsv) — TS-to-Go file mapping and unexported identifier stems
