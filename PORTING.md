# pi-go porting conventions (binding)

This document is the single source of truth for porting the pi TypeScript monorepo v1.0.0 to Go. TS source
of truth: `/Users/jack/.t3/worktrees/pi/t3code-ef43beb3` (commit `7fbbd5f4a`, release v1.0.0). Every porting
agent must follow it. "Must" means there is no alternative. For an uncovered case, pick the option closest
to these rules, do not invent conventions, and record the case in your NEEDS file (section 14).

Companion file: `porting/filemap.tsv` (columns `ts_path go_file go_import_path kind stem`). It maps every TS
file (src, tests, test helpers, fixtures, examples) to its Go file, package, kind, and unexported-identifier
stem. It is authoritative; sections 2-4 explain how it was derived.

Module `github.com/keejkrej/pi-go`, `go 1.26.0` directive (`go mod tidy` writes `1.26.0` because the
`golang.org/x/*` and `modernc.org/*` modules declare `go 1.26.0`), go1.27.1 toolchain. Product builds use
`CGO_ENABLED=0`. No package may `import "C"`.

Companion file: `port-map.json` (repo root) lists, per porting unit, every TS file with its Go target
(`{ts, go, unit, kind}`) and every test (`{ts_test, go_test, unit, kind}`, plus `reason` for `kind: "skip"`).
Kinds: `src`, `example`, `testkit`, `test`, `test-partial`, `fixture`, `asset` (copied file), `platform` (3.3
sibling), `rewrite` (Go-specific replacement), `barrel`/`drop`/`reference` (`go: null`, nothing to port), `skip`.

## 1. Bootstrap (orchestrator, once, before any porting agent starts)

1. `git mv README.md pi-harness-go-port.md agentloop cmd tools wire legacy/`. Add `legacy/go.mod`
   (`module github.com/keejkrej/pi-go/legacy`, `go 1.26`) so `./...` excludes it. Legacy code is reference
   only; nothing imports it.
2. Write the root `go.mod` with section 11. Add `internal/depspin/depspin.go` (`//go:build depspin`) with a
   blank import of every package in section 11, then `go mod tidy`, so every module is pinned before use.
3. Copy the assets in 3.6.
4. For every `filemap.tsv` row of kind `src`, `testkit`, or `example`, create a stub containing
   `// Ported from <ts_path> (pi v1.0.0).` and the package clause. No `_test.go` stubs. Every package then
   exists and compiles empty.
5. Commit `chore: scaffold pi-go port layout`.

### 1.1 Bootstrap as executed (differences from the steps above)

- Legacy imports were rewritten to `github.com/keejkrej/pi-go/legacy/...` so `cd legacy && go build ./...` works.
- `internal/depspin` has platform files: `depspin.go` (portable modules), `depspin_unix.go` (`x/sys/unix`),
  `depspin_windows.go` (`x/sys/windows`), `depspin_darwin.go` (`purego`, `purego/objc`), so
  `go build -tags depspin ./internal/depspin/` passes on every target in 12. When the first real package imports a
  module, its depspin line may be deleted by the fix-up agent; never by porting agents.
- Stub layout: `// Ported from <ts_path> (pi v1.0.0).`, a blank line, then the package clause. The blank line keeps
  the header out of the package doc. Keep the header as the first line when porting.
- Every Go package has a `doc.go` (orchestrator-owned, no identifiers) with the package comment and the list of TS
  files mapped into it. Porting agents never edit `doc.go` and must not write package comments elsewhere.
- `package main` stubs (`cmd/*`, program examples) also contain `func main() {}` so they build. The owner of that
  file replaces it.
- Package name rule used: last path element, except `package main` for `cmd/*`, `agent/examples/*`,
  `codingagent/examples/sdk/*`, and `codingagent/examples/rpc*`.
- Fixtures (3.4) were copied at bootstrap, except the 8 used only by skipped tests (tui native-clipboard fixtures,
  `mcp-conformance/{README.md,baseline.json}`, `durable/test/fixtures/*.ts`).
- `filemap.tsv` was amended to match the porting units (`port-map.json`):
  - Split files (3.1 "Split files"): `core/agent-session.ts`, `modes/interactive/interactive-mode.ts`,
    `ai/scripts/generate-models.ts`, and the per-unit test splits of `tools.test.ts`,
    `file-mutation-queue.test.ts`, `image-resize-callers.test.ts`.
  - New rows for `packages/ai/scripts/*` and `packages/evals/{evals,docker}/*` (2.3).
  - Moved tests: script tests to `ai/scripts` and `cmd/generate-models`; `evals/test/{acme-server,configured-runtime}`
    to `evals/suites`; `evals/test/harness.test.ts` to `evals/harness_test.go`.
  - `agent/test/e2e.test.ts` is faux-only: kind `test`, `agent/e2e_test.go`.
  - 14 rows that every unit skips became kind `skip` with no stub: `agent/test/utils/{get-current-time,wait-for-tick}`,
    `ai/test/{azure-utils,bedrock-utils,cloudflare-utils,oauth}`, `coding-agent/test/{mcp-conformance/client,
    mcp-conformance/run,rpc-example,test-theme-colors}`, `durable/test/{examples/16-real-model,storage-memory}`,
    `tui/test/image-test`, `examples/extensions/custom-provider-gitlab-duo/test.ts`. `ai/internal/testkit` keeps a
    `doc.go` only; its Go-only `fixture.go` belongs to the ai fix-up agent.

## 2. Module and package layout

### 2.1 Rules

- One top-level directory per TS package: `agent ai chord client codemode codingagent durable evals mcp
  protocol server telemetry tui`. Binaries in `cmd/`. Go-only shared code in `internal/`.
- Default: one TS directory = one Go package. The merges, moves, and drops in 2.3 exist because Go forbids
  import cycles.
- Package name = last path element without hyphens. Packages named `testing` become `*test`; packages that
  would shadow what they wrap get a suffix (`mcpext`, `codemodeext`, `durableagent`, `experimentalcli`).
- Barrel `index.ts` files (re-exports only) are dropped. Consumers import the defining package.

### 2.2 Cycle analysis

The analysis uses a symbol-resolved import graph over `packages/*/src`: barrels and `export ... from` are
followed to the defining file, and `import type` counts. The raw directory graph has 8 cycles (ai, chord,
codemode, tui, mcp, durable, coding-agent with 19 directories, cli/experimental). With the table in 2.3 the Go
package graph is acyclic, verified with and without dynamic `import()` edges. Two TS edges are removed by
design:

- `ai/types.ts` -> `ai/api/*` (`ApiOptionsMap`, a type-level map). Do not port `ApiOptionsMap` or
  `ApiStreamOptions<T>`. Use the interface `ai.ProviderStreamOptions` (4.5). Per-API option structs live in
  `ai/api` and embed `ai.StreamOptions`.
- `core/source-info.ts` -> `core/package-manager.ts` (`PathMetadata`). `PathMetadata` is declared in
  `codingagent/utils/source_info.go`. `package_manager.go` uses `utils.PathMetadata` and must not redeclare it.

### 2.3 Package table (TS paths relative to `packages/<pkg>/src/`)

| TS source | Go import path (under module) | package |
|---|---|---|
| agent/ | agent | agent |
| ai/ root files (except the ai/compat and cmd/pi-ai rows), ai/utils/, ai/auth/*.ts, ai/api/lazy.ts | ai | ai |
| ai/api/ (rest) | ai/api | api |
| ai/providers/*.models.ts, ai/models.generated.ts | ai/catalog | catalog |
| ai/auth/oauth/, ai/providers/radius-config.ts | ai/oauth | oauth |
| ai/providers/ (rest), ai/providers/images/ | ai/providers | providers |
| ai/compat/; ai/{compat,bedrock-provider,bun-oauth,images,oauth,image-models,legacy-api-aliases}.ts | ai/compat | compat |
| ai/cli.ts | cmd/pi-ai | main |
| ai/scripts/{model-data,models-dev-reasoning-options,openrouter-reasoning-options,openrouter-catalog}.ts (outside src/) | ai/scripts | scripts |
| ai/scripts/generate-models.ts (split, 3.1) | cmd/generate-models | main |
| ai/scripts/check-model-data.ts, ai/scripts/generate-test-image.ts | cmd/check-model-data, cmd/generate-test-image | main |
| chord/, chord/{context,delta,facets,services}/ | chord | chord |
| chord/node/ | chord/node | node |
| client/ | client | client |
| codemode/ | codemode | codemode |
| codemode/runtime/ | codemode/runtime | runtime |
| coding-agent/{main,migrations,package-manager-cli,rpc-entry}.ts | codingagent | codingagent |
| coding-agent/cli.ts | cmd/pi | main |
| coding-agent/config.ts, coding-agent/utils/, core/source-info.ts | codingagent/utils | utils |
| core/** (extensions/, tools/, tools/renderers/, compaction/, export-html/), cli/args.ts, modes/interactive/theme/theme-controller.ts | codingagent/core | core |
| modes/interactive/theme/ (rest), modes/interactive/components/{keybinding-hints,visual-truncate,diff}.ts | codingagent/modes/interactive/theme | theme |
| modes/interactive/components/ (rest), modes/interactive/{external-editor,model-search,model-catalog-refresh}.ts | codingagent/modes/interactive/components | components |
| modes/interactive/ (rest) | codingagent/modes/interactive | interactive |
| modes/rpc/ | codingagent/modes/rpc | rpc |
| modes/ (rest) | codingagent/modes | modes |
| cli/ (rest) | codingagent/cli | cli |
| cli/experimental/, cli/experimental/commands/ | codingagent/cli/experimentalcli | experimentalcli |
| extensions/index.ts | codingagent/extensions | extensions |
| extensions/{mcp,codemode,llama,tool-search}/ | codingagent/extensions/{mcpext,codemodeext,llamaext,toolsearchext} | same as dir |
| experimental/ | codingagent/experimental | experimental |
| experimental/durable/ | codingagent/experimental/durableagent | durableagent |
| experimental/{plugins,services,vacation}/ | codingagent/experimental/{plugins,services,vacation} | same as dir |
| coding-agent/bun/, coding-agent/client/, core/extensions/{virtual-modules,jiti-loader,jiti-static-loader}.ts | dropped | - |
| durable/, durable/harness/, durable/session/ | durable | durable |
| durable/env/ | durable/env | env |
| durable/storage/, storage/jsonl/, storage/sqlite/ | durable/storage, durable/storage/jsonl, durable/storage/sqlite | storage, jsonl, sqlite |
| durable/testing/ | durable/durabletest | durabletest |
| durable/tools/ | durable/tools | tools |
| evals/ (cli.ts -> cmd/pi-evals) | evals | evals |
| packages/evals/evals/ (eval suites and fixtures, outside src/) | evals/suites | suites |
| packages/evals/docker/entrypoint.ts | cmd/pi-evals-entrypoint | main |
| packages/evals/docker/{Dockerfile,Dockerfile.dockerignore,install-runtime.mjs} | rewritten as evals/docker/{Dockerfile,Dockerfile.dockerignore} | - |
| mcp/, mcp/transports/ | mcp | mcp |
| mcp/oauth/, mcp/protocol/ | mcp/oauth, mcp/protocol | oauth, protocol |
| mcp/testing/ (barrel) | dropped | - |
| protocol/, protocol/cbor/ | protocol, protocol/cbor | protocol, cbor |
| server/ | server | server |
| server/testing/ | server/servertest | servertest |
| server/transports/unix/ | server/transports/unix | unix |
| telemetry/, telemetry/testing/ | telemetry, telemetry/telemetrytest | telemetry, telemetrytest |
| tui/, tui/components/ | tui | tui |

Dropped: `bun/` holds Bun runtime shims, `client/index.ts` is a re-export, and `virtual-modules.ts` plus the
jiti loaders only exist to load user TS extensions, which pi-go does not do (section 13).

### 2.4 Dependency layers (porting waves, bottom-up)

```
L0  chord codemode mcp/protocol protocol/cbor telemetry tui           (+ all internal/*)
    cmd/generate-test-image
L1  ai chord/node codemode/runtime durable/env mcp protocol telemetry/telemetrytest
L2  agent ai/api ai/catalog ai/scripts client codingagent/cli/experimentalcli codingagent/experimental/plugins
    codingagent/utils durable mcp/oauth server
L3  ai/oauth cmd/check-model-data codingagent/modes/interactive/theme durable/durabletest durable/storage
    durable/storage/sqlite durable/tools server/servertest server/transports/unix
L4  ai/providers cmd/generate-models durable/storage/jsonl
L5  ai/compat cmd/pi-ai
L6  codingagent/core
L7  codingagent/experimental/services codingagent/extensions/toolsearchext codingagent/modes
    codingagent/modes/interactive/components evals
L8  cmd/pi-evals codingagent/cli codingagent/experimental/durableagent codingagent/experimental/vacation
    codingagent/extensions/codemodeext codingagent/extensions/llamaext codingagent/modes/rpc
L9  codingagent/extensions/mcpext
L10 codingagent/extensions codingagent/modes/interactive
L11 codingagent evals/suites
L12 cmd/pi cmd/pi-evals-entrypoint codingagent/experimental
```

A package imports only strictly lower layers and `internal/*`. If you need an upward import, you have hit a
cycle: stop and write `NEEDS_CYCLE`.

### 2.5 Import aliases

Do not alias imports, except in these cases:
- Two imported packages share a name: alias the non-top-level one(s) as `<parentdir><name>` (`aioauth`,
  `mcpoauth`, `mcpprotocol`).
- `codemode/runtime` is always `codemoderuntime`, because it shadows stdlib `runtime`.
- `golang.org/x/sys/unix` is always `sysunix`.

## 3. Deterministic file mapping

### 3.1 Source files

`filemap.tsv` is authoritative. It was generated as follows:

1. The Go package comes from 2.3. The longest match wins, so file rules beat directory rules.
2. The package home is the TS directory the package maps to by default (`coding-agent/src/core` for
   `codingagent/core`, `chord/src` for `chord`, `ai/src/providers` for `ai/catalog`).
3. Relative name: files under the home use their sub-path joined with `_` (`core/tools/renderers/bash.ts` ->
   `tools_renderers_bash.go`, `chord/src/delta/tracker.ts` -> `delta_tracker.go`). Files moved in from
   elsewhere use their basename (`cli/args.ts` -> `codingagent/core/args.go`).
4. Snake case: lowercase, with `-`, `.`, and camelCase boundaries turned into `_` (`session-manager.ts` ->
   `session_manager.go`, `anthropic.models.ts` -> `anthropic_models.go`, `openai-responses.lazy.ts` ->
   `openai_responses_lazy.go`).
5. A name ending in `_<GOOS>`, `_<GOARCH>`, or `_test` gets `_impl` appended, to avoid implicit build
   constraints.
6. A non-barrel `index.ts` becomes `index.go`, or `<subdir>_index.go` below the home. `cmd/*` entries become
   `main.go`.

Each Go file starts with `// Ported from <ts_path> (pi v1.0.0).` and the package clause. One TS file ports
into one Go file; the exceptions are platform splits (3.3) and split files.

Split files: TS files over 3500 lines are split by line range across porting units. Their `filemap.tsv` rows use
`<ts_path>#L<a>-L<b>` and may repeat one range for several Go files. All parts live in one package and have
their own stems; the struct, constructor, and shared types live in the first file named for the struct.
- `core/agent-session.ts` -> `agent_session_part{1,2,3}.go` (stems `as1`-`as3`); `AgentSession` and its
  constructor are in part 1.
- `modes/interactive/interactive-mode.ts` -> `interactive_mode_helpers.go` (L1-432); `interactive_mode.go`
  (`InteractiveMode` struct and constructor) + `interactive_mode_core.go` (L433-1355);
  `interactive_mode_{resources,session_binding,extension_ui}.go` (L1356-2987);
  `interactive_mode_{input,events,transcript,lifecycle,queues}.go` (L2988-4765); `interactive_mode_selectors.go`
  (L4766-5695); `interactive_mode_{auth,commands}.go` (L5696-7025).
- `ai/scripts/generate-models.ts` -> `cmd/generate-models/generate_models_part{1,2}.go`.
- Tests split across units get one Go test file per unit: `<base>_part<N>_test.go` (see `port-map.json`).

### 3.2 Barrels, lazy files, re-exports

- Barrels (kind `barrel`) produce no Go file. `export ... from` lines in non-barrel files are not ported.
- `*.lazy.ts` files exist for bundle splitting. Port them as eager code. Lazy provider descriptors (for
  example `anthropicMessagesApi`) become package vars that reference the real functions. A lazy file never
  redeclares a function its sibling exports (for example `playArmin3d`).
- Dynamic `import()` becomes a static import. "Bundlers cannot follow this" indirections become direct calls
  (for example `ai/auth/oauth/load.ts` loaders return the provider directly; `registerBundledOAuthFlowLoaders`
  stays as an override hook).

### 3.3 Platform-specific code

When a TS file branches on `process.platform` for OS APIs (syscalls, native modules, signals), the portable
logic stays in the mapped file. The OS parts go in siblings `<base>_unix.go` (`//go:build unix`),
`_windows.go`, `_darwin.go`, `_linux.go`, and `_other.go` (`//go:build !darwin && !linux && !windows`).
Siblings belong to the owner of `<base>.go` and use its stem. Plain string checks on `process.platform`
stay in the main file and use `js.Platform()`.

### 3.4 Tests

- `packages/<pkg>/test/**/<name>.test.ts` -> `<gopkg>/<snake path>_test.go`. `<gopkg>` is the package the
  test imports most after symbol resolution. Name collisions get a `<tspkg>_` prefix. See the TSV.
- Default is the external test package `<name>_test`. Use the internal package only for unexported
  identifiers or test hooks (section 15).
- Not ported: kind `test-skip-e2e` (real-API smoke/e2e) and `skip` (benches, scratch, probes, repros,
  workers). Kind `test-partial`: port all cases except credential/network-gated ones (`skipIf(!token)`),
  which are dropped entirely, not left as skipped Go tests.
- Test helpers (non-`.test.ts` files under `test/`, kind `testkit`) -> `<top>/internal/testkit/<snake path>.go`
  (package `testkit`). Example: `coding-agent/test/suite/harness.ts` -> `codingagent/internal/testkit/suite_harness.go`.
- Fixtures (kind `fixture`, plus every non-TS file under `test/`) are copied byte-for-byte to
  `<top>/testdata/<path>`. Tests find them with `testkit.Fixture(t, "<path>")`, which resolves from the module
  root. Each `<top>/internal/testkit/fixture.go` (Go-only) belongs to the owner of that directory's first
  testkit row in the TSV.

### 3.5 Examples

`coding-agent/examples/**` and `agent/examples/**` map to `<top>/examples/...` (kind `example`). Extension
examples become library packages that export `func Extension(pi core.ExtensionAPI) error`, plus an
`example_test.go` that loads them against the faux provider. SDK and RPC examples become `package main`
programs. They are ported after L10 compiles.

### 3.6 Assets (`//go:embed`; copied by bootstrap, never edited by agents)

| Source | Destination | Embedded by |
|---|---|---|
| ai/src/providers/data/*.json | ai/catalog/data/ | ai/catalog/models_generated.go (`//go:embed data/*.json`) |
| coding-agent/src/modes/interactive/theme/{dark,light,theme-schema}.json | codingagent/utils/assets/theme/ | codingagent/utils/config.go |
| coding-agent/src/core/export-html/{template.html,template.css,template.js,vendor/} | codingagent/utils/assets/export-html/ | codingagent/utils/config.go |
| coding-agent/src/modes/interactive/assets/ | codingagent/utils/assets/interactive/ | codingagent/utils/config.go |
| coding-agent/{README.md,CHANGELOG.md,docs/,examples/,package.json} | codingagent/utils/assets/package/ | codingagent/utils/config.go |
| node_modules/quickjs-wasi/quickjs.wasm | internal/quickjs/quickjs.wasm | internal/quickjs/quickjs.go |
| examples/extensions/dynamic-resources/{SKILL.md,dynamic.json,dynamic.md}, subagent/{agents,prompts}/*.md, doom-overlay/doom/build/{doom.js,doom.wasm} | the Go example package dir, same relative path | the example package |
| coding-agent/scripts/migrate-sessions.sh | codingagent/scripts/ | not embedded (ships as-is) |
| mcp/LICENSES/modelcontextprotocol-typescript-sdk.txt | mcp/LICENSES/ | not embedded (attribution) |
| evals/.gitignore | evals/.gitignore | - |

`config.go` holds one `//go:embed assets` var. The model reads docs with the `read` tool, so the TS path
getters must return real paths. On first call they extract the embedded tree once into
`<os.UserCacheDir()>/pi-go/<VERSION>/` and return paths inside it. This covers `GetPackageDir`,
`GetThemesDir`, `GetExportTemplateDir`, `GetPackageJsonPath`, `GetReadmePath`, `GetDocsPath`,
`GetExamplesPath`, `GetChangelogPath`, `GetInteractiveAssetsDir`, and `GetBundledInteractiveAssetPath`.
The Go-only `ConfigAssetsFS() fs.FS` provides direct reads. `VERSION` is `"1.0.0"` and `PackageName` is
`"@earendil-works/pi-coding-agent"`. `GetQuickJSWasmPath`/`SetEmbeddedQuickJSWasmPath` are ported as-is.
An empty path means `internal/quickjs` uses its embedded module.

## 4. Naming

### 4.1 Exported identifiers

- Exported Go name = TS export name with the first letter uppercased, nothing else (`getEnvApiKey` ->
  `GetEnvApiKey`, `parseUrl` -> `ParseUrl`). No Go initialism style (`ID`, `URL`, `API`).
- `SCREAMING_SNAKE` -> PascalCase words (`DEFAULT_THINKING_LEVEL` -> `DefaultThinkingLevel`). All-caps names
  without `_` stay unchanged (`VERSION`).
- Fields: TS property name with the first letter uppercased. The JSON tag carries the exact TS name.
- Methods likewise. `get foo()` -> `Foo()`, `set foo(v)` -> `SetFoo(v)`, static methods and namespace
  objects (`export const Foo = { bar() {} }`) -> package funcs `<Class><Method>`, constructors ->
  `New<Class>(...) *Class`. Factory functions keep their TS name (`CreateAgentSession`). Default exports use
  the TS name if any, else PascalCase(file base).
- Go-only exported helpers in TS-mapped files must be methods or carry the prefix PascalCase(TS file base),
  for example `SessionManagerDecodeEntry`. Add them only when another file needs them, and list them in your
  NEEDS file. Go-only files (`internal/*`, `testkit/fixture.go`) are exempt.

### 4.2 Unexported identifiers (collision-proof rule)

- Every unexported package-level identifier (func, type, var, const) is `<stem><PascalName>`. `<stem>` is
  your file's `stem` column. Example: `session_manager.go` has stem `sm`, so a private `parseLine` becomes
  `smParseLine`, a type `Entry` becomes `smEntry`, and `DEFAULT_LIMIT` becomes `smDefaultLimit`.
- Stems are unique per Go package directory, tests included. Test-file stems start with `t`.
- Exempt: methods, fields, locals, parameters, `init`, and `_`.
- Before adding any package-level identifier, exported or not, run `grep -rnw '<Name>' <pkgdir>/*.go`. On a
  hit, apply 4.3 or rename yours with your stem.
- Test functions: `Test<FileBasePascal>_<CaseName>`, where `<CaseName>` is PascalCase of the
  `describe`/`it` text, cut to about 60 characters (`TestSessionManager_ParsesLegacyHeader`).

### 4.3 Known exported-name collisions (fixed decisions)

- **Same name in several files of one package.** Every occurrence gets the suffix PascalCase(file base). In
  `ai/api`: `StreamAnthropicMessages`, `StreamSimpleAnthropicMessages`, `StreamOpenaiResponses`, ...;
  `ClassifyCloudflareWorkersAiSystemOne`, `ClassifyLlamaCppClassify`, `ClassifyTypesafeSystemOne`;
  `ConvertMessagesGoogleShared`, `ConvertMessagesOpenaiCompletions`.
- **Value and type with the same name in one file.** The type keeps the name and the value is renamed:
  - chord: `NewEncoder`, `NewDecoder`, `NewReplicatedState`.
  - tui: `NewIndexedColor`, `NewRgbColor`, `NewFuzzyMatch`. The `LAYOUT_NODE` symbol is dropped in favor of
    the `LayoutNode` interface.
  - durable: `NewDocumentCreate`, `NewConversationViews`, `GetToolSlot`, `GetCompactionStatus`. The
    `Harness` const object in `harness/harness.ts` becomes package funcs `HarnessOpen` and siblings. The
    `Harness` type belongs to `harness_types.go`.
  - experimental/services `defineService` consts -> `<Name>Service` (`AgentControllerService`,
    `ModelsService`, `PresentationPluginsService`, `PresentationUIService`, `SessionPluginsService`,
    `SessionDirectoryService`, `SessionManagementService`, `SlashCommandsService`, `TranscriptService`).
  - experimentalcli: `ClientCommandDef`, `ServerCommandDef`.
  - theme: the `theme` Proxy becomes `func CurrentTheme() *Theme`. `setTheme` and related keep their names.
  - Fallback for new cases: a function becomes `New<Name>`, any other value `<Name>Def`.
- **Duplicate type declarations.** One owner; others must not redeclare. `ToolHtmlRenderer`:
  `core/export-html/tool-renderer.ts`. `Provider{,Chat,Image,Classifier}ModelConfig` and
  `ProviderModelConfigBase`: `core/provider-composer.ts`. `CompactionSettings`: `core/compaction/compaction.ts`
  (the settings-manager one is `SettingsCompactionSettings`).
- **Overloads.** One Go function with the widest signature. If arity or return type differs, the primary
  overload keeps `<Name>` and the others become `<Name><Variant>`.

### 4.4 Type mapping (mechanical, so independent agents agree)

| TS | Go |
|---|---|
| `string` | `string` (UTF-8; UTF-16 semantics via 6.1) |
| `number` | `float64`. `int` for indices, counts, lengths, line/column, width/height, token counts, byte sizes, exit codes. `int64` for epoch-ms timestamps and `*Ms` durations (keep TS units and names, no `time.Duration` in signatures). |
| `bigint` / `boolean` | `*big.Int` / `bool` |
| optional `T` / `T \| undefined` | scalar `*T`. Slices, maps, funcs, interfaces, pointers: nil |
| `T \| null` (persisted) | `jsonx.Opt[T]` (5.1) |
| string-literal union | named `string` type + consts `<Type><PascalLiteral>` (`ThinkingLevelHigh`), non-alnum chars removed |
| numeric-literal union | named `int` type + consts |
| discriminated object union | sealed interface + variant structs (5.4) |
| data `interface`/`type` | struct |
| behavior `interface` (methods, callbacks) | Go interface |
| class | struct + methods. `extends` -> embedding, plus an interface for the polymorphic surface |
| `Record<string,T>`/`Map<K,V>`, order observable or persisted | `*omap.Map[K,V]` |
| `Record<string,T>`/`Map<K,V>`, order never observable | `map[K]V` |
| `Set<T>` | `*omap.Set[T]` if iterated, else `map[T]struct{}` |
| `unknown`/`any` JSON-ish | `any` holding jsonx values (5.1) |
| `Uint8Array`/`Buffer` / `Date` | `[]byte` / `time.Time` |
| `RegExp` | `*regexp.Regexp` if RE2-compatible, else `*jsre.Regexp` (6.3) |
| `Promise<T>` / `Promise<void>` | blocking `(T, error)` / `error` |
| sync function that throws on non-bug paths | `(T, error)` |
| `AbortSignal` param or option | `ctx context.Context` as first parameter (7) |
| callback / `on(...)` returning unsubscribe | `func(...)` / method returning `func()` |
| generics | Go generics if they constrain cleanly, else `any` |
| TypeBox `TSchema` | `*typebox.Schema` (9) |

Parameter order follows TS, with `ctx` first. An options object becomes `*XxxOptions`, where nil means
undefined. Port JSDoc to Go doc comments; drop TS-type-level remarks.

### 4.5 Core shapes everyone depends on

- `ai/types.go`:
  - `Model` is a struct (TS `Model<TApi>` loses the generic and keeps `Api string`).
  - `StreamOptions` is a struct.
  - `type ProviderStreamOptions interface{ BaseStreamOptions() *StreamOptions }` is implemented by
    `*StreamOptions` and by every `*api.<Name>Options` (each embeds `ai.StreamOptions`).
  - `Message` and the content blocks are sealed unions (5.4).
  - `type StreamFunction func(ctx context.Context, model *Model, c *Context, opts ProviderStreamOptions) *AssistantMessageEventStream`.
- Every `ai.Message` variant also has the Go-only method `GetRole() string`.
- `agent/types.go`: `type AgentMessage interface{ GetRole() string }` is deliberately open, because TS
  extends it by declaration merging. Custom message structs implement `GetRole`. Decoding an unknown role
  yields `*agent.UnknownAgentMessage{Raw *jsonx.Object}`.

## 5. JSON

All persisted and wire JSON must equal the TS bytes (section 16). Use `internal/jsonx` for every marshal.
Never call `json.Marshal` directly for output.

### 5.1 `internal/jsonx` contract

```go
type Object struct{ /* ordered; JS property order: integer-like keys ascending first, then insertion */ }
func NewObject() *Object
func (o *Object) Set(key string, v any)   // existing key keeps position; new key appended (JS assignment)
// also: Get(key) (any, bool); Delete(key); Keys() []string; Len() int; Clone() *Object (deep); (Un)MarshalJSON
// Untyped value model: nil, bool, float64, string, []any, *Object.
func Parse(data []byte) (any, error)                       // JSON.parse
func ParseErrorMessage(err error, text string) string      // V8/Node 24 SyntaxError message text
func Stringify(v any) (string, error)                      // JSON.stringify(v)
func StringifyIndent(v any, indent string) (string, error) // JSON.stringify(v, null, indent)
func Marshal(v any) ([]byte, error)                        // typed values; same output rules
func MarshalIndent(v any, indent string) ([]byte, error)
func Decode(v any, out any) error                          // untyped value -> typed struct
func Encode(v any) (any, error)                            // typed struct -> untyped value
type Opt[T any] struct{ /* absent | null | value */ }
func Some[T any](v T) Opt[T]; func Null[T any]() Opt[T]
func (o Opt[T]) IsZero() bool    // absent; works with omitzero. Also IsNull() bool, Get() (T, bool)
```

Output rules (identical to V8 `JSON.stringify`):
- No HTML escaping. U+2028/U+2029 are emitted raw. Implementation: encoding/json with `SetEscapeHTML(false)`,
  then un-escape `\u2028`/`\u2029` that are not preceded by an odd run of backslashes.
- Control characters use `\b \f \n \r \t` or lowercase `\u00xx`.
- Numbers follow ES `Number.prototype.toString`, which Go's float64 encoder already matches. `-0` -> `0`,
  NaN/±Inf -> `null`.
- No trailing newline: callers append `"\n"` exactly where TS does. Invalid UTF-8 becomes U+FFFD.

### 5.2 Struct tags

- Tags carry the exact TS property name.
- **Field order = key order of the TS object literal that writes the value.** If several writers differ, use
  the most common one. With no literal, use the TS interface order. This is what makes bytes match.
- Required field: no tag option. A required slice must be non-nil when marshaled, so it encodes as `[]`;
  constructors initialize it.
- Optional `foo?:` -> `json:"foo,omitzero"` with a nil-able type: scalars are pointers, slices and maps are
  nil when absent. An empty non-nil value still emits `[]`/`{}`, as TS does for set-but-empty. Never use
  `omitempty`.
- `foo: T | null` -> `jsonx.Opt[T]` (absent marshals as `null`). `foo?: T | null` -> `jsonx.Opt[T]` with
  `omitzero`.
- Open shapes (`Record<string, unknown>`, `details`, `data`, tool arguments, provider passthrough) ->
  `*jsonx.Object` or `any` holding jsonx values. `json.RawMessage` only for bytes forwarded verbatim and
  never inspected.

### 5.3 Read-modify-write documents

- Hold every file that TS reads, mutates, and writes back as a `*jsonx.Object` tree, with typed accessor
  helpers. This includes `settings.json`, `auth.json`, `models.json`, the trust store, `package.json` edits,
  and MCP config.
- Never round-trip these through Go structs: that drops unknown keys and reorders keys. `Set`/`Delete`
  reproduce JS mutation exactly.
- Write with `jsonx.StringifyIndent(doc, "  ")` plus exactly the suffix TS writes. For example,
  trust-manager appends `"\n"`; settings, auth, and models append nothing.
- Session JSONL: new entries are typed structs (5.2). Entries read from disk keep their parsed
  `*jsonx.Object`. Every rewrite (fork, migration, branch copy) re-stringifies that object, which equals TS
  `JSON.parse` -> `JSON.stringify`. Unknown fields and custom entry types survive.

### 5.4 Discriminated unions

```go
type Content interface{ isContent() }            // sealed; one marker method per union
type TextContent struct {
	Type          string  `json:"type"`          // always "text"; set by constructor/literal
	Text          string  `json:"text"`
	TextSignature *string `json:"textSignature,omitzero"`
}
func (*TextContent) isContent() {}
type UnknownContent struct{ Raw *jsonx.Object }  // unknown discriminator: kept verbatim, re-marshals Raw
func (*UnknownContent) isContent() {}
type ContentList []Content                       // UnmarshalJSON peeks the discriminator, allocates the variant
```

- The discriminator is whatever TS uses (`type`, `role`, `kind`, `method`).
- Every union has an `Unknown<Union>` variant, and unmarshal never fails on an unknown discriminator.
- Narrow with `switch v := x.(type)`.
- A union-typed field uses a list type (`ContentList`) or a holder struct with custom (Un)MarshalJSON.
- The union's owner file exports `Unmarshal<Union>(data []byte) (<Union>, error)` and
  `Decode<Union>(v any) (<Union>, error)`.

### 5.5 Decoding

Typed input is decoded with `encoding/json`; unknown fields are ignored, except where 5.3 applies. Untyped
numbers are `float64`. Type a field as `int` only if no writer produces fractions. `JSON.parse` error text
that reaches users or tests goes through `jsonx.ParseErrorMessage`.

## 6. JS semantics (`internal/js`, `internal/jsre`, `internal/omap`)

### 6.1 Strings

TS indices and lengths are UTF-16 code units. Wherever they are observable, use these helpers instead of
byte indices: persisted values, limits shown to users, cursor positions, `slice` at computed offsets, and
protocol offsets.

```go
func Len(s string) int                              // s.length
func Slice(s string, start, end int) string         // s.slice(start, end); negative ok. Also SliceFrom, Substring
func CharCodeAt(s string, i int) int                // -1 for NaN
func IndexOf(s, sub string, from int) int           // UTF-16 index. Also LastIndexOf(s, sub)
func ByteToU16(s string, b int) int; func U16ToByte(s string, u int) int
func CompareUTF16(a, b string) int                  // default Array.prototype.sort order
func Trim(s string) string                          // JS whitespace set (U+FEFF, U+00A0, U+2028/9, ...). Also TrimStart, TrimEnd, IsJSWhitespace(r)
func ToUpper(s string) string; func ToLower(s string) string // full-Unicode toUpperCase/toLowerCase
func Normalize(s, form string) string               // x/text/unicode/norm
func LocaleCompare(a, b string) int                 // x/text/collate, language.Und
func Split(s, sep string) []string                  // JS split ("" splits into UTF-16 units)
func PadStart(s string, n int, pad string) string; func PadEnd(s string, n int, pad string) string
func NewUTF8StreamDecoder() *UTF8StreamDecoder      // TextDecoder {stream:true}
```

Byte-based TS APIs (`Buffer.byteLength`, byte truncation in `truncate.ts`) use Go bytes directly. Graphemes
and widths use `uniseg` and `internal/eastasian`. tui owns `VisibleWidth`.

### 6.2 Numbers, dates, platform, misc

```go
func NumberToString(x float64) string             // String(x)
func ToFixed(x float64, digits int) string        // Number.prototype.toFixed
func Round(x float64) float64                     // Math.round (ties toward +Inf)
func ParseInt(s string, radix int) float64        // NaN on failure, JS prefix rules. Also ParseFloat(s)
func FormatNumberEnUS(x float64, minFrac, maxFrac int) string // toLocaleString("en-US", ...)
func ToISOString(t time.Time) string              // ms precision, "Z". Also DateNow() int64
func Platform() string                            // "darwin", "linux", "win32", "freebsd", ...
func Arch() string                                // "x64", "arm64", "ia32", ...
func EncodeURIComponent(s string) string; func EncodeURI(s string) string
func DecodeURIComponent(s string) (string, error)
func ErrorString(err error) string                // String(err): "<name>: <message>"
func Sleep(ctx context.Context, ms int64) error   // abortable sleep
const WhitespaceClass = `...`                     // JS \s as an RE2 character-class body
```

### 6.3 Regular expressions

Use stdlib `regexp` when the pattern has no lookaround, no backreferences, and no JS-specific semantics that
matter for the input. Watch two differences:
- JS `\s` also matches `\v` and Unicode spaces. Use `[` + `js.WhitespaceClass` + `]` where those can occur.
- JS `.` excludes `\r` and U+2028/2029.

Otherwise use `internal/jsre`, a wrapper over `github.com/dlclark/regexp2/v2` in ECMAScript mode:

```go
func Compile(pattern, flags string) (*Regexp, error)  // flags: g i m s u y
func MustCompile(pattern, flags string) *Regexp
func (r *Regexp) Test(s string) bool
func (r *Regexp) Exec(s string, from int) *Match        // UTF-16 indices
func (r *Regexp) Replace(s, repl string) string         // $1 $& $<name> $$; honors g
func (r *Regexp) ReplaceFunc(s string, f func(*Match) string) string
func (r *Regexp) Split(s string, limit int) []string
```

User-supplied patterns (settings, extension config) always go through `jsre`. Compile static patterns once,
into package vars named with your stem.

### 6.4 Ordered collections (`internal/omap`)

```go
type Map[K comparable, V any] struct{ /* insertion-ordered */ }
func NewMap[K comparable, V any]() *Map[K, V]
// Get(k) (V, bool); Set(k, v); Delete(k) bool; Has(k) bool; Len() int; Keys() []K; Values() []V;
// All() iter.Seq2[K, V]. String keys marshal in JS object order (integer-like keys first).
type Set[T comparable] struct{ /* insertion-ordered; Add, Has, Delete, Len, Values, All */ }
func NewSet[T comparable]() *Set[T]
```

### 6.5 Process-level shims

- `process.exit(code)` -> `utils.ConfigExit(code)` (Go-only, in `codingagent/utils/config.go`). It runs the
  functions registered with `utils.ConfigOnExit(fn)`, then calls the var `utils.ConfigExitHook(code)`, which
  defaults to `os.Exit`. Library code never calls `os.Exit`. Tests replace `ConfigExitHook`. Packages below
  `codingagent/utils` return errors instead of exiting.
- `process.env` -> `os.Getenv` at call time, never cached at init, so `t.Setenv` works.
- `process.cwd()` -> `os.Getwd()`, `os.homedir()` -> `os.UserHomeDir()`, signals -> `signal.Notify`.
- `process.stdout`/`stderr`/`stdin` -> injected `io.Writer`/`io.Reader` options that default to the `os`
  streams. Every mode, CLI, and print path takes them through options so tests can capture output.

## 7. Concurrency

### 7.1 Mapping

| TS | Go |
|---|---|
| `AbortSignal` | `ctx context.Context`, first parameter |
| `new AbortController()` / `abort(reason)` | `ctx, cancel := context.WithCancelCause(parent)` / `cancel(reason)` |
| `signal.aborted` / `signal.reason` | `ctx.Err() != nil` / `context.Cause(ctx)` |
| `signal.addEventListener("abort", f)` | `stop := context.AfterFunc(ctx, f)`; `defer stop()` when scoped |
| `AbortSignal.any([a, b])` | derive from `a`, plus `context.AfterFunc(b, cancel)` |
| `AbortSignal.timeout(ms)` | `context.WithTimeoutCause` |
| `async` function | blocking function returning `error` |
| un-awaited promise / `void foo()` | `go func(){...}()`; handle errors exactly where TS handles rejections |
| `Promise.all` | `errgroup.Group` writing into a pre-sized slice (ctx cancellation only if TS cancels) |
| `Promise.allSettled` / `Promise.race` | `sync.WaitGroup` + per-item result / `select` over result channels |
| `setTimeout` / `clearTimeout` | `time.AfterFunc` / `Stop()`; the callback runs on another goroutine (7.3) |
| `setInterval` | `time.NewTicker` in a goroutine stopped by ctx or `Stop()` |
| `queueMicrotask` / `nextTick` / `setImmediate` | direct call, or `Post` (7.3) when deferring UI work |
| `for await (const ev of stream)` | `for ev := range stream.All()` |
| Node `Worker` | goroutine (codemode: one wazero module instance per run, `WithCloseOnContextDone(true)`) |
| `child_process.spawn` | `os/exec` via `internal/xspawn` (cross-spawn Windows semantics) |

Abort errors: when TS rejects with an abort message (for example `"Request was aborted"`), Go returns an
error with the same message that also satisfies `errors.Is(err, context.Canceled)`. Build it with the
Go-only `ai.AbortNewError(msg string) error` in `ai/utils_abort.go`. Persisted `stopReason: "aborted"` and
`errorMessage` strings stay identical to TS.

### 7.2 EventStream (`ai/utils_event_stream.go`)

```go
type EventStream[T, R any] struct{ /* unbounded FIFO, single consumer */ }
func NewEventStream[T, R any](isComplete func(T) bool, extractResult func(T) R) *EventStream[T, R]
func (s *EventStream[T, R]) Push(ev T)                                 // never blocks; no-op once done
func (s *EventStream[T, R]) End(result ...R)                           // end(result?): resolves only if given
func (s *EventStream[T, R]) Next(ctx context.Context) (T, bool, error) // false = drained and done
func (s *EventStream[T, R]) All() iter.Seq[T]                          // break-safe; spawns no goroutine
func (s *EventStream[T, R]) Result(ctx context.Context) (R, error)     // await result()
type AssistantMessageEventStream = EventStream[AssistantMessageEvent, *AssistantMessage]
func NewAssistantMessageEventStream() *AssistantMessageEventStream     // class constructor
func CreateAssistantMessageEventStream() *AssistantMessageEventStream  // TS factory
```

Producer pattern: `s := ai.NewAssistantMessageEventStream(); go func() { ...; s.Push(ev); ...; s.End() }(); return s`.
The goroutine watches `ctx` and always terminates the stream with a done/error event or `End`. The legacy
`agentloop.EventStream` leaks its drain goroutine; do not copy it.

### 7.3 Shared state

- Types with state shared across goroutines have an unexported `mu sync.Mutex`, locked around state access
  only: never across callbacks, listeners, extension handlers, I/O, or channel sends (snapshot listeners
  under the lock, call after unlocking).
- Listeners run synchronously, in registration order, on the emitting goroutine (TS order); a listener that
  must not block starts its own goroutine.
- `tui.TUI` owns a UI goroutine. All component mutation and rendering happens there. Other goroutines
  (agent events, timers, process output) use the Go-only `(*TUI).Post(func())`. `RequestRender()` is safe
  from any goroutine and coalesces. Interactive mode wraps every session/agent event handler in `Post`.
- Where TS relies on run-to-completion (check-then-update with no `await` between), hold one lock across the
  region. All packages must pass `go test -race` (CGO enabled for race runs only).

### 7.4 Time in tests

Timer-based tests (`vi.useFakeTimers`, debounce, retries, backoff) use `testing/synctest`. Code uses `time`
directly: no clock interface. Where TS injects `now: () => number`, keep it as a `func() int64` option.

## 8. Errors

- `throw new Error(msg)` -> `errors.New`/`fmt.Errorf` with the exact TS message text. Messages reach users,
  the model, and session files.
- Each TS error class becomes a struct named after the class. It implements `Error()` (the message),
  `Name()` (the TS `name`), and `Unwrap()` when TS sets `cause`. Other fields map like properties. The
  constructor is `New<Class>(...)`. `instanceof X` -> `errors.As`. Example:
  `ModelsError{Code ModelsErrorCode; Message string; Cause error}`.
- Parameterless error classes that are only type-checked become sentinels `Err<Name>`, wrapped with `%w`.
- `err.name === "AbortError"` -> `errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)`.
- `String(err)` / `${err}` -> `js.ErrorString(err)`. It uses `Name()` when implemented, else `"Error"`.
- `panic` only for programmer errors TS would also treat as bugs. Never swallow an error TS propagates;
  swallow exactly where TS has an empty `catch {}`, with a comment.

## 9. JSON Schema / TypeBox (`internal/typebox`)

Decision: a faithful hand port of the TypeBox 1.3.27 subset that pi uses (source: `node_modules/typebox`),
not a generic JSON Schema library. Three behaviors reach the model or tests and must match:
- `Value.Convert` coercion of tool arguments
- localized validation error texts
- schema key order in provider requests

```go
type Schema struct{ *jsonx.Object }   // the schema exactly as TypeBox builds it, key order included
func Object(props *omap.Map[string, *Schema], opts ...*jsonx.Object) *Schema
func String(opts ...*jsonx.Object) *Schema    // likewise Number, Integer, Boolean, Null, Unknown, Any
func Literal(v any, opts ...*jsonx.Object) *Schema
func Union(items []*Schema, opts ...*jsonx.Object) *Schema
func Array(item *Schema, opts ...*jsonx.Object) *Schema
func Record(key, value *Schema, opts ...*jsonx.Object) *Schema
func Optional(s *Schema) *Schema       // Object() leaves it out of "required"
func Unsafe(raw *jsonx.Object) *Schema // Type.Unsafe / foreign schemas (MCP tools)
func FromJSON(v any) (*Schema, error)
type ValidationError struct{ InstancePath, SchemaPath, Keyword, Message string; Params *jsonx.Object }
func Check(s *Schema, v any) bool
func Errors(s *Schema, v any) []ValidationError // TypeBox localized (en) messages
func Convert(s *Schema, v any) any              // Value.Convert; mutates like TS
type Validator struct{ /* compiled */ }
func Compile(s *Schema) (*Validator, error)     // Validator.Check(x) bool, Validator.Errors(x) []ValidationError
```

Required keywords: `type` (incl. arrays), `properties`, `required`, `additionalProperties` (bool and schema),
`patternProperties`, `items` (schema and tuple), `prefixItems`, `minItems`, `maxItems`, `uniqueItems`, `enum`,
`const`, `anyOf`, `oneOf`, `allOf`, `not`, `minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`,
`multipleOf`, `minLength`/`maxLength` (UTF-16), `pattern` (via jsre), `format` (TypeBox default formats),
`$ref`, `$defs`, `definitions`, `default`.

Unknown keywords are ignored, as in TypeBox. `Static<typeof X>` has no runtime form: write the Go struct by
hand next to the schema.

## 10. HTTP and providers

- **Raw `net/http` + `internal/sse`** for Anthropic, OpenAI (completions, responses, codex, azure),
  Google Generative AI, Google Vertex, Mistral, Cloudflare, llama.cpp, pi-messages, typesafe, and every
  OpenAI-compatible provider. pi builds every SDK with `maxRetries: 0` and retries through
  `utils/provider-retry.ts`, so the SDKs only contribute request shaping and error formatting. Reproduce
  from the SDK sources in `node_modules`: URL and method; headers, including SDK-added ones (`x-stainless-*`,
  `anthropic-version`, `x-goog-api-client`, ...), with Go runtime values where the SDK reports Node's; body
  key order; timeout defaults; error message format; stream event parsing.

  Official Go SDKs are rejected: their request shapes, headers, retries, and error strings differ.
- **Bedrock**: `aws-sdk-go-v2/service/bedrockruntime` `ConverseStream` with `config.LoadDefaultConfig`. It
  covers SigV4, event-stream framing, and the profile/SSO/IMDS credential chain. Bearer-token auth
  (`AWS_BEARER_TOKEN_BEDROCK`) and proxy handling mirror `bedrock-converse-stream.ts`.
- **Vertex ADC / key files**: `golang.org/x/oauth2/google` (`CredentialsFromJSON`, `FindDefaultCredentials`,
  scope `https://www.googleapis.com/auth/cloud-platform`).
- **WebSockets** (Codex transport, radius relay): `github.com/coder/websocket`. **zstd** (Codex):
  `github.com/klauspost/compress/zstd`.
- **SSE**: one parser, `internal/sse`, shared by providers and MCP. It follows eventsource-parser
  semantics: `\r\n`/`\r`/`\n` line endings, multi-line `data`, `event`, `id`, `retry`, comments. API:
  `sse.NewReader(io.Reader)` with `Next() (Event, error)`.
- **Proxy and transport**: `ai/utils_node_http_proxy.go` owns proxy resolution (`ResolveHttpProxyUrlForTarget`,
  built on `golang.org/x/net/http/httpproxy`). It also owns the Go-only process-wide client:
  `NodeHttpProxyDefaultClient() *http.Client` and `NodeHttpProxyConfigure(idleTimeoutMs int64)`.
  `core/http_dispatcher.go` (`ConfigureHttpDispatcher`, `ApplyHttpProxySettings`) calls them in place of
  the undici dispatcher settings.
- **Injection**: every component that does HTTP takes an optional `HTTPClient *http.Client` (Go-only field,
  default `ai.NodeHttpProxyDefaultClient()`). TS `fetch` mocks become `httptest.Server` or a `RoundTripper`
  stub.
- **User agent**: replicate `pi-user-agent.ts`. Where TS reports `node/<ver>`, Go reports
  `go/<runtime.Version()>`.

## 11. Dependencies (exact; verified with go1.27.1 on 2026-10-02, CGO_ENABLED=0 builds for darwin/linux/windows)

```
require (
	github.com/alecthomas/chroma/v2 v2.27.0                // syntax highlighting (highlight.js replacement)
	github.com/aws/aws-sdk-go-v2 v1.47.1                    // Bedrock
	github.com/aws/aws-sdk-go-v2/config v1.33.6
	github.com/aws/aws-sdk-go-v2/credentials v1.20.6
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.63.1
	github.com/aws/smithy-go v1.28.2
	github.com/bmatcuk/doublestar/v4 v4.10.2                // fs.globSync
	github.com/charmbracelet/x/vt v0.0.0-20261001101533-953920dd3285 // @xterm/headless (tests only)
	github.com/coder/websocket v1.8.15
	github.com/disintegration/imaging v1.6.2                // Lanczos resize (photon replacement)
	github.com/dlclark/regexp2/v2 v2.8.1                    // ECMAScript regex (internal/jsre)
	github.com/ebitengine/purego v0.11.1                    // darwin native APIs without cgo (purego/objc)
	github.com/evanw/esbuild v0.28.2                        // chord bundler (same version as TS)
	github.com/fsnotify/fsnotify v1.10.1                    // fs.watch
	github.com/google/go-cmp v0.7.0                         // tests
	github.com/google/uuid v1.6.0                           // randomUUID v4; uuidv7 is ported from ai/utils/uuid.ts
	github.com/jezek/xgb v1.3.1                             // X11 clipboard
	github.com/klauspost/compress v1.20.1                   // zstd
	github.com/rivo/uniseg v0.4.7                           // grapheme clusters (Intl.Segmenter)
	github.com/tetratelabs/wazero v1.12.0                   // quickjs.wasm runtime
	go.yaml.in/yaml/v3 v3.0.5                               // YAML (maintained fork of gopkg.in/yaml.v3)
	golang.org/x/image v0.46.0                              // webp/bmp/tiff decode, draw
	golang.org/x/net v0.59.0                                // http/httpproxy
	golang.org/x/oauth2 v0.37.0                             // google ADC
	golang.org/x/sync v0.23.0                               // errgroup, singleflight, semaphore
	golang.org/x/sys v0.48.0                                // unix/windows syscalls, console modes
	golang.org/x/term v0.46.0                               // raw mode, size
	golang.org/x/text v0.42.0                               // collate, norm, cases
	modernc.org/sqlite v1.60.1                              // durable sqlite storage (pure Go)
)
```

Rejected: testify (use `testing` + `go-cmp`), clockwork (`synctest`), Masterminds/semver (npm range
semantics differ), sabhiram/go-gitignore (not node-ignore compatible), fxamacker/cbor (must be byte-exact
with the TS encoder), santhosh-tekuri/jsonschema (9), go-runewidth (tables differ), goldmark (tokens differ
from marked), official LLM SDKs (10), gopkg.in/yaml.v3 (archived).

### 11.1 Hand ports of npm libraries (Go-only `internal/*`; source of truth = the version in `node_modules`)

| npm | Go package | API to port |
|---|---|---|
| typebox 1.3.27 | internal/typebox | section 9 |
| diff 8.0.4 | internal/jsdiff | `DiffLines`, `DiffWords`, `CreateTwoFilesPatch`, `FILE`; `Change{Value string; Added, Removed bool; Count int}`; identical Myers output and patch text |
| ignore 7.0.8 | internal/ignore | `New`, `Add`, `Ignores`, `Test`, `Filter`; node-ignore regex translation, including errors on invalid paths |
| minimatch 10.2.6 | internal/minimatch | `Match(path, pattern string, opts Options) bool` with `NoCase`, `Dot`, `MatchBase`, `NoBrace`, `NoExt`; braces, extglob, `**` |
| semver 7.8.5 | internal/semver | `Valid`, `Compare`, `RCompare`, `Gt`, `Satisfies`, `MaxSatisfying`, `ValidRange` |
| hosted-git-info 9.0.3 | internal/hostedgit | `FromUrl(url string, opts *Options) *GitHost` plus the methods `utils/git.ts` uses |
| proper-lockfile 4.1.2 | internal/lockfile | `Lock(ctx, file, opts) (release func() error, err error)`, `LockSync`; `<file>.lock` dir, mtime refresh every stale/2, stale 10 s default, realpath, retries, `OnCompromised`; must interoperate with a running TS pi |
| partial-json 0.1.7 | internal/partialjson | `Parse(s string, allow Allow) (any, error)` returning jsonx values |
| marked 18.0.11 | internal/marked | `Lexer`, block and inline tokenizers, `Tokenizer` overrides, `TokenizerExtension` (block/inline, start, tokenizer); token structs match `Tokens.*` |
| grok-mermaid 0.2.3 | internal/mermaid | `Render(src string, opts Options) (MermaidArt, error)`, `Span` |
| get-east-asian-width 1.6.0 | internal/eastasian | `Width(r rune, ambiguousAsWide bool) int`, same tables |
| chalk 6.0.0 | internal/ansi | style funcs (`Bold`, `Dim`, `Red`, `Hex`, ...), level detection (`FORCE_COLOR`, `NO_COLOR`, TTY), identical escapes |
| cross-spawn 7.0.6 | internal/xspawn | `Command(ctx, name string, args []string) *exec.Cmd`, Windows `.cmd`/PATHEXT/escaping |
| quickjs-wasi 3.6.2 (dist/index.js, wasi-shim.js) | internal/quickjs | what `codemode/runtime/worker.ts` uses: VM, value handles, `JSException`, `MAX_STACK_SIZE`, eval, host functions, interrupt |
| highlight.js 10.7.3 | internal/highlight | `Highlight(code, lang string) []Token` via chroma, mapped to hljs scope names used by theme keys; languages from `utils/syntax-highlight.ts` |
| eventsource-parser | internal/sse | section 10 |
| undici, http(s)-proxy-agent | - | `net/http` + `httpproxy` |
| photon-node | - | `image/*`, `x/image`, `imaging.Resize(..., imaging.Lanczos)`, `image/jpeg` quality |
| yaml 2.9.0 | - | `go.yaml.in/yaml/v3`, decoded into jsonx values (YAML 1.2 core schema) |

There is one foundation agent per `internal/*` package (`internal/js`, `jsonx`, `omap`, `jsre`, and the table
above), all in wave L0. Each owner first commits compiling stubs with exactly these signatures (zero values
or `errors.New("TODO")`), so higher layers compile right away, and then implements them. A missing function
is added by the owner; everyone else writes `NEEDS_DEPS`.

## 12. Platforms and native code

- Targets: darwin, linux, and windows on amd64 and arm64; freebsd/amd64 best effort. Always
  `CGO_ENABLED=0`, except for `-race` test runs.
- The prebuilt `.node` modules in `packages/tui/native` are replaced by `tui/native_platform.go` plus
  siblings, with the same API as `NativePlatformHelper`/`NativeClipboard`.
  - darwin: `purego/objc`. `NSPasteboard generalPasteboard` reads `public.utf8-plain-text`,
    `public.png`/`public.tiff` (TIFF -> PNG via `x/image/tiff`), and `public.file-url`. Modifier keys come
    from CoreGraphics `CGEventSourceFlagsState`.
  - linux: X11 `CLIPBOARD` via `jezek/xgb` (TARGETS, UTF8_STRING, image/png, text/uri-list), with the same
    display checks. Wayland and set-text use the CLI tools TS uses (`wl-copy`, `wl-paste`, `xclip`, `xsel`).
  - windows: `x/sys/windows` + lazy `user32`/`kernel32` procs (OpenClipboard, GetClipboardData
    CF_UNICODETEXT/CF_DIB/CF_HDROP, SetConsoleMode ENABLE_VIRTUAL_TERMINAL_INPUT). DIB -> PNG via
    `x/image/bmp`.
  - other: helper is nil, as in TS.
- Terminal (`tui/terminal.go`): `x/term` for raw mode and size. Resize uses `SIGWINCH` in
  `terminal_unix.go` and console polling in `terminal_windows.go`. A stdin goroutine reads raw bytes into
  `stdin_buffer.go`, which keeps the TS escape-sequence timing.
- Processes: unix uses `SysProcAttr{Setpgid: true}` and `syscall.Kill(-pgid, sig)`; windows uses
  `taskkill /T /F`. This matches `utils/shell.ts` and `child-process.ts`.
- Paths: `filepath` for OS paths and `path` for POSIX keys, exactly where TS uses `path` vs `path.posix`.

## 13. Extension API (native Go)

User TS/JS extensions are not loaded. `core/extensions_loader.go` keeps discovery (settings, packages, `pi`
manifests, paths, `builtin:<name>` resources). For each discovered `.ts`/`.js`/`.mjs` entry it emits the
warning diagnostic `pi-go cannot load JavaScript/TypeScript extensions: <path>` and skips the entry. Skills,
prompts, themes, and other non-code package resources load normally.

`codingagent/core/extensions_types.go` mirrors `core/extensions/types.ts` member by member:

```go
type NoResult = struct{}
// nil *R == TS void/undefined
type ExtensionHandler[E, R any] func(ctx context.Context, event *E, ectx ExtensionContext) (*R, error)
type ExtensionFactory func(pi ExtensionAPI) error
type InlineExtension struct { // TS union normalized; a bare factory has an empty Name
	Name        string
	Factory     ExtensionFactory
	Hidden      bool
	Replaceable bool
	Builtin     bool
}
type ExtensionAPI interface {
	// One method per TS on(event, handler) overload: "On" + PascalCase(event), returning unsubscribe.
	OnSessionStart(h ExtensionHandler[SessionStartEvent, NoResult]) func()
	OnToolCall(h ExtensionHandler[ToolCallEvent, ToolCallEventResult]) func()
	OnBeforeProviderRequest(h ExtensionHandler[BeforeProviderRequestEvent, BeforeProviderRequestEventResult]) func()
	// ... every other event in types.ts, same pattern
	RegisterTool(tool ToolDefinition)
	RegisterCommand(name string, opts RegisteredCommandOptions)
	RegisterShortcut(key string, opts ShortcutOptions)
	RegisterFlag(name string, opts FlagOptions)
	GetFlag(name string) any // bool | string | nil
	RegisterMessageRenderer(customType string, r MessageRenderer)
	SendMessage(msg CustomMessageInput, opts *SendMessageOptions)
	SendUserMessage(content UserMessageContent, opts *SendUserMessageOptions)
	AppendEntry(customType string, data any)
	Exec(ctx context.Context, command string, args []string, opts *ExecOptions) (ExecResult, error)
	SetModel(ctx context.Context, model *ai.Model) (bool, error)
	Events() *EventBus
	// ... every remaining member: PascalCase name, TS parameter order, Promise -> (T, error) with ctx first
}
type ExtensionContext interface{ /* TS members as methods; ui -> ExtensionUIContext interface */ }
type ToolDefinition struct {
	Name, Label, Description string
	Parameters               *typebox.Schema
	// ... remaining TS fields in TS order
	Execute      func(ctx context.Context, toolCallID string, params *jsonx.Object, onUpdate AgentToolUpdateCallback, ectx ExtensionContext) (AgentToolResult, error)
	RenderCall   func(args *jsonx.Object, th *theme.Theme, rctx ToolRenderContext) tui.Component // nil = default
	RenderResult func(result AgentToolResult, opts ToolRenderResultOptions, th *theme.Theme, rctx ToolRenderContext) tui.Component
}
```

- The TS generics `TDetails`/`TState` become `any`. Built-in tools define concrete detail types
  (`*BashToolDetails`, ...) and type-assert them. Details are persisted, so they follow section 5.
- Tool params arrive as `*jsonx.Object` after `typebox.Convert` + `Check`. Tools decode them into their own
  params struct with `jsonx.Decode`.
- `extensions_runner.go` stores handlers per event name as type-erased closures. Result merging, ordering,
  error isolation, timeouts, and `replaceable` conflict handling are identical to `runner.ts`.
- Built-ins: `codingagent/extensions` exports `var BuiltInExtensions []core.InlineExtension`, mirroring
  `extensions/index.ts` in order and flags. Each `*ext` package exports
  `func Extension(pi core.ExtensionAPI) error` (the TS default export) plus its other TS exports.
- Compiled-in third-party extensions use `codingagent.Main(ctx, args []string, options *MainOptions) error`
  with `MainOptions.ExtensionFactories []core.InlineExtension` (TS `main`/`MainOptions`). `cmd/pi` calls
  `cli.SetupCli()` then `codingagent.Main`. The SDK (`core/sdk.go`) takes `[]core.InlineExtension` where TS
  takes inline factories.
- `examples/extensions/*` become Go extension packages (3.5) whose behavior matches the TS example.

## 14. Rules for parallel agents

1. **Ownership.** You own exactly your assigned `filemap.tsv` rows: their Go files, platform siblings (3.3),
   mapped `_test.go` files, and new `testdata/` files only your tests use. Never create, edit, format, or
   delete anything else, including `go.mod`, `go.sum`, `doc.go` files, `port-map.json`, other agents' stubs,
   `internal/*` (unless you own it), and this document.
2. **Read in full.** Read your TS file, and the TS files defining the types you use, completely before
   writing Go. Port behavior completely: every branch, message string, default, edge case, and JSDoc. No
   TODOs, no simplified logic, no stubbed functions in final output.
3. **Cross-file references.** Refer to other files' symbols by the names that 4.1-4.3 derive. If a symbol is
   not ported yet, keep the reference. `undefined: X` is the only tolerated error, and only when `X` is a TS
   export owned by another `filemap.tsv` row (check with grep in the TS source). Never add placeholder
   definitions for others' symbols.
4. **Dependencies and NEEDS.** go.mod is pre-populated. Never run `go get`, `go mod tidy`, or `go mod edit`.
   For anything you lack, append one line to `porting/needs/<go file path with / replaced by __>.txt`:
   `NEEDS_DEPS <module|internal pkg> <symbol> <why>`, `NEEDS_API <owner file> <signature> <why>`,
   `NEEDS_CYCLE <import> <why>`, or `NEEDS_DECISION <question>`.
5. **Checks**, from the module root, in order, with `CGO_ENABLED=0`:
   ```
   gofmt -l -e <your files>                                  # must print nothing
   ~/go/bin/gopls check <your files>                         # works even if sibling files are broken
   go build -gcflags=-e ./<pkgdir>/ 2>&1 | grep -E '<file1>|<file2>'
   go vet ./<pkgdir>/ 2>&1 | grep -E '<file1>|<file2>'
   go test ./<pkgdir>/ -run '^Test<YourFilePascal>_' -count=1  # once the package compiles
   ```
   Only rule-3 errors may remain in your files. Keep files parseable at all times: one syntax error blocks
   type-checking for every agent in the package.
6. **Git.** Never run `git stash`, `git reset`, `git checkout`/`git restore` on paths, `git clean`,
   `git add -A`/`git add .`, or `git commit --no-verify`. Commit only when the orchestrator says so, staging
   only your paths explicitly, with the message `port(<go pkg>): <ts file>`.
7. **No scope drift.** Do not refactor, rename, or improve TS behavior. Do not port code you do not own. If
   TS has a bug, port it and mark it `// TS parity:`.
8. **Grep before declaring** (4.2). Every unexported identifier uses your stem.
9. **Determinism.** Never iterate a Go map where order is observable (6.4). Sort only where TS sorts, with the
   same comparator: `js.LocaleCompare` for `localeCompare`, and `js.CompareUTF16` for a default `sort()`.
10. **Done** means: rule 5 passes (rule-3 references aside), your tests are ported and pass where the
    package compiles, and your NEEDS file is final.

Waves: the orchestrator runs agents layer by layer (2.4). After each layer, one fix-up agent per package
resolves NEEDS files and leftover undefined references. It makes `go build ./... && go vet ./...` pass for
that layer before the next layer starts.

## 15. Tests

- Port every non-e2e `*.test.ts` case one-to-one, with the same assertions and the same fixture inputs. Use
  table tests where a TS file repeats one assertion shape. `toEqual` -> `cmp.Diff`. `toThrow(msg)` -> error
  message equality or containment, exactly as TS asserts.
- No network. Model calls use the faux provider (`ai/providers/faux.go` `FauxProvider`, or
  `compat.RegisterFauxProvider` where TS tests use it). HTTP-level provider tests use `httptest.Server` or a
  `RoundTripper` stub replaying recorded SSE bodies from `testdata`. Credential-gated cases are not ported.
- The coding-agent suite (`test/suite/`) uses `codingagent/internal/testkit/suite_harness.go` + the faux
  provider, mirroring `harness.ts`.
- Isolation: filesystem tests set `PI_CODING_AGENT_DIR` and `HOME` to `t.TempDir()` via `t.Setenv` and work
  under `t.TempDir()`. `t.Setenv` tests are not `t.Parallel()`. Time: `testing/synctest` (7.4).
- `vi.mock` -> dependency injection through options TS already has. Otherwise the source owner adds an
  unexported hook var named with its stem (`smReadFile = os.ReadFile`), which an internal test swaps and
  restores with `t.Cleanup`. Request hooks via `NEEDS_API`.
- Snapshots (`toMatchSnapshot`, `__snapshots__`) -> golden files in `testdata/golden/<test>/`. The `-update`
  flag lives in `<top>/internal/testkit`.
- TUI rendering tests use `charmbracelet/x/vt` through `tui/internal/testkit/virtual_terminal.go`, the port
  of `tui/test/virtual-terminal.ts`.
- Golden data from TS: agents may run TS source directly (`node --experimental-strip-types /tmp/<script>.ts`;
  no npm build/test, no network), commit the output under `testdata/golden/` with a note naming the script,
  and delete the script.
- Each package must also be `-race` and `go vet` clean. Each persisted format needs a round-trip test: TS
  fixture -> Go parse -> Go write must reproduce the original bytes.

## 16. Interop guarantees (byte-compatible with TS v1.0.0)

Go and TS pi must run against the same `~/.pi/agent` concurrently and read each other's files.

| Artifact | Guarantee |
|---|---|
| `~/.pi/agent` layout | Same directory and file names, the same env vars (`PI_CODING_AGENT_DIR`, `PI_CODING_AGENT_SESSION_DIR`, `PI_OFFLINE`, and every other one TS reads), the same modes (`auth.json` 0600, dirs 0700 where TS sets them). Go-only data lives in `os.UserCacheDir()/pi-go`, never in the agent dir. |
| Sessions JSONL | Same path scheme (cwd encoding, file names), header/entry shapes, key order, compact `JSON.stringify` + `"\n"`, create/append semantics, ID formats (uuidv7 and short IDs as TS generates them), and migrations of old versions. |
| settings.json, auth.json, models.json, trust store, MCP config | Read-modify-write via `jsonx.Object` (5.3), 2-space indent, same trailing-newline behavior, proper-lockfile-compatible locks (`internal/lockfile`). |
| RPC mode | Same JSONL framing on stdin/stdout, the command/response/event shapes and key order of `modes/rpc/rpc-types.ts`, the same error strings. |
| JSON event / print mode | Byte-identical stdout for the same events. |
| CBOR protocol | Identical bytes: integer width selection, float encoding choice, map keys in insertion order, the same tags. |
| CLI | The same flags, aliases, positionals, help text, and exit codes. `cli/args.ts` is ported as the hand-written parser it is; no `flag`, `pflag`, or cobra. |
| HTML export | The same embedded template and the same output for the same session. |
| Model catalog | The same embedded JSON, producing equal `Model` values. |
| OAuth | The same client IDs, scopes, redirect ports, PKCE, and credential JSON in `auth.json`. |
| Package manager | The same install layout under the agent dir, invoking npm/git exactly as TS does. |

Any user-observable deviation is a bug unless it is listed here. Listed deviations: the runtime token in user
agents, syntax-highlight token boundaries (chroma vs highlight.js), grapheme segmentation edge cases (uniseg
vs ICU), and user TS/JS extensions not loading.
