# Documentation

## Developer Documentation

- **[Architecture Overview](architecture.md)** — Package dependency diagram, layer boundaries, execution modes (`tui`, `gui`, `rpc`, `print`/`json`), and engineering invariants.
- **[Desktop GUI Architecture](gui.md)** — Layer-0 `gui` toolkit over `github.com/egoist/mygo`, three-pane `codingagent/modes/gui` layout, immediate-mode threading rules, and headless testing.
- **[Extensions & Package Manager](extensions.md)** — Built-in Go extensions (`llama.cpp`, `codemode`, `tool-search`, `mcp`), programmatic factories, QuickJS/WASI + `esbuild` user extensions, and package CLI commands.
- **[Development Guide](development.md)** — Toolchain, `CGO_ENABLED=0` builds, cross-compilation, test suites, golden updates, vector generators, and eval Docker images.
- **[Agent & Contributor Rules](../AGENTS.md)** — Repository conventions, invariants, and verification commands.
- **[Porting Conventions & Spec](porting.md)** — Historical TypeScript v1.0.0 to Go porting specification (`§1`–`§16`) and byte-compatibility contracts referenced by code comments.
- **[TS-to-Go File Map](filemap.tsv)** — Authoritative mapping from Pi TypeScript source/test paths to Go packages and unexported identifier stems.

## Embedded User Documentation & Examples

User-facing Pi documentation and TypeScript SDK/extension examples live in [`codingagent/utils/assets/package/`](../codingagent/utils/assets/package/):

- [`codingagent/utils/config.go`](../codingagent/utils/config.go) embeds `assets/` via `//go:embed all:assets` so the compiled binary ships themes, HTML export templates, interactive assets, and docs/examples without external runtime file dependencies.
- [`evals/docker/Dockerfile`](../evals/docker/Dockerfile) also packages these assets directly into the evaluation container images (`with_docs` / `without_docs`).

Key user guides in [`codingagent/utils/assets/package/docs/`](../codingagent/utils/assets/package/docs/):

- **Getting Started**: [Quickstart](../codingagent/utils/assets/package/docs/quickstart.md), [Usage](../codingagent/utils/assets/package/docs/usage.md), [How Pi Works](../codingagent/utils/assets/package/docs/how-pi-works.md)
- **CLI & Configuration**: [CLI](../codingagent/utils/assets/package/docs/cli.md), [Configuration](../codingagent/utils/assets/package/docs/configuration.md), [Settings](../codingagent/utils/assets/package/docs/settings.md), [Environment Variables](../codingagent/utils/assets/package/docs/environment-variables.md), [Keybindings](../codingagent/utils/assets/package/docs/keybindings.md)
- **Providers & Models**: [Providers](../codingagent/utils/assets/package/docs/providers.md), [Models](../codingagent/utils/assets/package/docs/models.md), [Models Configuration](../codingagent/utils/assets/package/docs/models-configuration.md), [Custom Provider](../codingagent/utils/assets/package/docs/custom-provider.md), [llama.cpp](../codingagent/utils/assets/package/docs/llama-cpp.md)
- **Extensibility**: [Extensions](../codingagent/utils/assets/package/docs/extensions.md), [Skills](../codingagent/utils/assets/package/docs/skills.md), [Prompt Templates](../codingagent/utils/assets/package/docs/prompt-templates.md), [Packages](../codingagent/utils/assets/package/docs/packages.md), [MCP](../codingagent/utils/assets/package/docs/mcp.md), [Codemode](../codingagent/utils/assets/package/docs/codemode.md)
- **Sessions & Wire Protocols**: [Sessions](../codingagent/utils/assets/package/docs/sessions.md), [Session Format](../codingagent/utils/assets/package/docs/session-format.md), [Compaction](../codingagent/utils/assets/package/docs/compaction.md), [JSON](../codingagent/utils/assets/package/docs/json.md), [RPC](../codingagent/utils/assets/package/docs/rpc.md), [SDK](../codingagent/utils/assets/package/docs/sdk.md)
- **UI & Terminal**: [TUI](../codingagent/utils/assets/package/docs/tui.md), [Themes](../codingagent/utils/assets/package/docs/themes.md), [Terminal Setup](../codingagent/utils/assets/package/docs/terminal-setup.md), [Security](../codingagent/utils/assets/package/docs/security.md)
