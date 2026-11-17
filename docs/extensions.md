# Extensions & Package Manager

`pi-go` supports built-in native Go extensions, programmatic Go extensions, and sandboxed TypeScript/JavaScript user extensions without requiring an external Node.js runtime.

## 1. Built-in Native Extensions

[`codingagent/extensions`](../codingagent/extensions/index.go) registers four built-in extensions at startup via `core.SetDefaultBuiltinExtensions(BuiltInInlineExtensions)`:

| Name | Package | Replaceable | Responsibility |
|---|---|---|---|
| `llama.cpp` | [`codingagent/extensions/llamaext`](../codingagent/extensions/llamaext/) | No | Local `llama.cpp` server integration and Hugging Face model helpers |
| `codemode` | [`codingagent/extensions/codemodeext`](../codingagent/extensions/codemodeext/) | Yes | Sandboxed QuickJS code execution over registered session tools |
| `tool-search` | [`codingagent/extensions/toolsearchext`](../codingagent/extensions/toolsearchext/) | Yes | Dynamic tool search and discovery |
| `mcp` | [`codingagent/extensions/mcpext`](../codingagent/extensions/mcpext/) | Yes | Model Context Protocol client lifecycle, OAuth, and MCP tool bridging |

CLI controls (defined in [`codingagent/core/args.go`](../codingagent/core/args.go)):

- `--extension, -e <path|builtin:name>` — load an extension file or explicitly enable a built-in extension (repeatable)
- `--no-extensions, -ne` — disable automatic extension discovery and built-in extensions (explicit `-e` paths still load)
- `--no-mcp` — disable built-in MCP server connections and MCP tools

## 2. Programmatic Go Extensions

Embedders calling [`codingagent.Main`](../codingagent/main.go) or [`core.LoadExtensions`](../codingagent/core/extensions_loader.go) can supply Go extensions directly through `MainOptions.ExtensionFactories` / `LoadExtensionsOptions.Factories` using `core.InlineExtension` and `core.ExtensionFactory`.

## 3. User TypeScript & JavaScript Extensions (QuickJS/WASI)

Unlike the initial v1.0.0 porting spec note, `pi-go` supports loading user `.ts`, `.tsx`, `.mts`, `.cts`, `.js`, `.jsx`, `.mjs`, and `.cjs` extensions directly in pure Go via [`codingagent/core/extensions_quickjs.go`](../codingagent/core/extensions_quickjs.go):

1. **In-memory bundling**: `eqjsBundleExtension` bundles the extension entrypoint with `github.com/evanw/esbuild/pkg/api` (`FormatIIFE`, `PlatformNeutral`, `ES2022`), optionally cached when `LoadExtensionsOptions.UseCache` is enabled (`ClearExtensionCache` invalidates cached bundles).
2. **WASI sandbox execution**: the bundled script runs inside [`internal/quickjs`](../internal/quickjs/) backed by `github.com/tetratelabs/wazero`, bridging tools, commands, events, flags, UI interactions, and `pi` extension APIs between Go and the QuickJS guest VM.
3. **Cleanup**: [`core.ShutdownExtensionRuntime`](../codingagent/core/extensions_loader.go) runs registered runtime cleanups (closing QuickJS VMs) when tearing down a runtime directly.

## 4. Package Manager

[`codingagent/core/package_manager.go`](../codingagent/core/package_manager.go) and [`codingagent/package_manager_cli.go`](../codingagent/package_manager_cli.go) implement Pi's package management commands (`pi install`, `pi remove` / `uninstall`, `pi update`, `pi list`, `pi config`) for npm/git extension, skill, prompt-template, and theme packages under `~/.pi/agent` and project-local `.pi` directories.
