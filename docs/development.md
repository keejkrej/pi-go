# Development Guide

## 1. Prerequisites & Building

- **Go toolchain**: Go `1.27.3` or newer ([`go.mod`](../go.mod))
- **CGO disabled**: product and test builds target `CGO_ENABLED=0`

Build the main `pi` binary:

```bash
CGO_ENABLED=0 go build -o pi ./cmd/pi
```

Build all packages on `darwin` or `linux`:

```bash
CGO_ENABLED=0 go build ./...
```

Cross-compiling `cmd/pi` (including Windows):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o pi-linux ./cmd/pi
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o pi.exe ./cmd/pi
```

> [!NOTE]
> `client` and `server/transports/unix` depend on Unix domain socket syscalls (`golang.org/x/sys/unix`), so full-module `go build ./...` targets `darwin` and `linux`, whereas `cmd/pi` builds across `darwin`, `linux`, and `windows`.

## 2. Running Checks & Tests

Run formatting, module hygiene, `go vet`, and the full offline test suite:

```bash
gofmt -l .
go mod tidy -diff
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
```

Race detector check on a package:

```bash
CGO_ENABLED=0 go test -race ./agent/...
```

Updating golden test fixtures (for suites that register the `-update` flag via [`ai/internal/testkit`](../ai/internal/testkit/fixture.go) or [`codingagent/internal/testkit`](../codingagent/internal/testkit/fixture.go)):

```bash
CGO_ENABLED=0 go test ./codingagent/... -update
```

## 3. Optional External Verification & Vector Generators

All unit and integration tests pass offline without external checkouts or `node_modules`. A few opt-in checks and vector regeneration scripts accept explicit environment variables:

| Variable | Consumer | Purpose |
|---|---|---|
| `WAKU_REPO_DIR` | [`waku_parity_test.go`](../codingagent/modes/gui/waku_parity_test.go) | Verify live token/layout parity against a local Waku checkout |
| `PI_WAKU_SNAPSHOT_DIR` | [`waku_composer_spec_test.go`](../codingagent/modes/gui/waku_composer_spec_test.go) | Dump/compare headless GUI composer layout snapshots |
| `CROSS_SPAWN_PARSE_JS` / `PI_REPO_DIR` / `NODE_MODULES` | [`command_windows_test.go`](../internal/xspawn/command_windows_test.go) | Differential check against npm `cross-spawn/lib/parse.js` |
| `MARKED_ESM_PATH` / `PI_REPO_DIR` / `NODE_MODULES` | [`lex_test.go`](../internal/marked/lex_test.go), [`internal/marked/testdata/`](../internal/marked/testdata/) | Live differential check against `marked/lib/marked.esm.js` and rule regeneration |
| `PI_REPO_DIR` / `NODE_MODULES` | `internal/*/testdata/*.{mjs,cjs}` | Regenerate Node/npm reference test vectors (`eastasian`, `ignore`, `marked`, `mermaid`, `minimatch`, `semver`, `typebox`) |
| `QUICKJS_WASI_DIR` | [`internal/quickjs/testdata/gen_vectors.mjs`](../internal/quickjs/testdata/gen_vectors.mjs) | Regenerate QuickJS/WASI guest vectors |

See package-local `testdata/README.md` files (such as [`internal/js/testdata/README.md`](../internal/js/testdata/README.md) and [`internal/eastasian/testdata/README.md`](../internal/eastasian/testdata/README.md)) for generator invocation details.

## 4. Evaluation Container Images

[`evals/docker/Dockerfile`](../evals/docker/Dockerfile) builds two evaluator targets from the repository:

- `with_docs` — packages `pi-evals-entrypoint` with embedded `codingagent/utils/assets/package` docs and examples.
- `without_docs` — control image stripping `README.md`, `CHANGELOG.md`, `docs/`, and `examples/` from `/repo/package`.

[`evals/docker/Dockerfile.dockerignore`](../evals/docker/Dockerfile.dockerignore) excludes host build outputs (`/pi`, `/pi-*`, `/*.exe`, `.git`, `.eval`, etc.) from the Docker context.
