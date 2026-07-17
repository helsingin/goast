# goast

Large Go workspaces are hard for AI agents to inspect once they grow beyond a
few packages. Real systems usually have internal packages, generated protobuf
code, service interfaces, implicit interface implementations, shared config
types, and cross-repository imports that are difficult to understand from raw
file search alone.

That is a code-discovery problem, not just a grep problem.

`goast` is a Go MCP server for indexing Go source across one or more
repositories. It builds a fast in-memory index from Go ASTs and exposes tools
that let AI coding agents discover packages, symbols, source ranges, references,
interfaces, config structs, gRPC services, and cross-repository dependencies
without reading an entire workspace into context.

The default setup runs as a stdio MCP server. Point it at local Go repositories
with a small YAML file or the `GOAST_REPOS` environment variable, connect it to
an MCP-capable coding agent, and use the tool surface as a live code map during
implementation, review, and refactoring work.

It is designed for engineers and teams that need AI assistants to work inside
large Go systems with better grounding: architecture discovery, interface
mapping, dependency review, generated API inspection, configuration analysis,
and reindexing after code changes.

## Quick Start

Install the command:

```bash
go install github.com/helsingin/goast/cmd/goast@latest
```

Or build from a local checkout:

```bash
make build
```

Create a local config file:

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` so it points at one or more Go repositories:

```yaml
include_tests: false
typed_method_references: false

repos:
  - name: service-a
    path: /path/to/service-a
  - path: /path/to/service-b
    include_tests: true
    typed_method_references: true

exclude_patterns:
  - "vendor/**"
  - "testdata/**"

transport: stdio
port: 7400
```

Run the server:

```bash
goast
```

For quick one-off use, skip the YAML file and pass repositories directly:

```bash
GOAST_REPOS=/path/repo-a,/path/repo-b goast
```

Register it with Codex or Claude Code as a stdio MCP server:

```bash
codex mcp add goast \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- goast

claude mcp add goast --scope user --transport stdio \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- goast
```

Run the Go test suite:

```bash
make test
```

Build the binary:

```bash
make build
```

## What It Does

`goast` handles common agent-oriented Go code discovery workflows:

- Indexes multiple Go repositories into one searchable workspace.
- Lists packages with module, repository, import path, and file information.
- Searches functions, methods, types, structs, interfaces, constants, and vars.
- Reads exact source ranges for known symbols, with optional import blocks.
- Maps Go's implicit interface relationships from method sets.
- Finds indexed call sites and package-qualified value references, with
  opt-in `go/types`-proven method selections.
- Lists gRPC service interfaces from generated protobuf files.
- Builds a cross-repository dependency graph from real import statements.
- Searches struct fields by name, type, tag, and doc comment.
- Extracts common config details from `yaml` and `json` tags.
- Runs set operations for missing implementations, missing fields, field
  coverage, and unimplemented generated services.
- Rebuilds the in-memory index during an active agent session.
- Switches a live session between registered Git worktrees without restarting
  the MCP client.
- Reports the published generation, active roots, and captured Git branch/HEAD
  provenance.
- Serves MCP over stdio or streamable HTTP.

The agent does not need to guess where code lives. It can ask for the package
list, search for a symbol, read the implementation, check who imports it, and
refresh the index after edits.

## Go ASTs, MCP, and Agent Context

The Go parser already exposes the structure that agents usually need first:
packages, declarations, receivers, methods, imports, comments, fields, and
source positions. `goast` uses that structure directly instead of requiring a
full build or a running language server. When enabled, a focused, non-fatal
`go/types` pass adds receiver-aware method references when the selection can be
proved from indexed source and available dependencies in a coherent build
context.

That tradeoff keeps startup fast and makes the server useful in workspaces that
contain incomplete branches, generated files, service-specific build tags, or
repositories that are not meant to compile together as one module.

MCP gives the index a stable interface for coding agents. Rather than dumping
large directory trees into a prompt, the agent can call focused tools as it
needs them and keep its context tied to real file paths and source ranges.

## Typical Pattern

```text
AI coding agent
        |
        | MCP tool calls
        v
   goast server
        |
        +-- config loader
        +-- in-memory Go AST index
        +-- package and symbol search
        +-- source-range reader
        +-- reference and dependency analysis
        +-- gRPC service discovery
        +-- config-field search
                |
                v
          local Go repositories
```

For the default local setup:

```text
developer machine
  MCP-capable coding agent
  goast process
  config.yaml
  one or more local Go repositories
```

For shared tooling, the same server can run over streamable HTTP and expose the
same tool surface to clients that are allowed to inspect the configured source
tree.

## Agent Workflows

### Explore an Unknown Service

Start with `list-packages`, narrow by repository or internal package, then use
`search-symbols` to find constructors, handlers, clients, service types, and
interfaces before reading exact implementations with `read-symbol`.

### Map an Interface

Use `find-implementations` on an interface to find concrete types that satisfy
it. Use `direction=interfaces` on a concrete type to see which indexed
interfaces it appears to implement.

### Review API Surface

Use `list-services` to inspect generated gRPC service interfaces and method
signatures without manually opening protobuf-generated files.

### Inspect Configuration

Use `search-config` to find config fields across repositories by field name,
Go type, `yaml` key, `json` key, or doc comment. This is useful for checking
ports, timeouts, credentials, NATS settings, Kafka settings, and feature flags.

### Understand Repository Coupling

Use `list-dependencies` to see which indexed repositories import packages from
which other indexed repositories. The graph is derived from actual Go import
statements and each repository's module path.

### Refresh After Edits

Use `reindex` after an agent adds, removes, renames, or moves Go symbols. The
server rebuilds the in-memory index from disk so later source reads and line
numbers match the edited workspace.

When the agent moves to another linked Git worktree, pass its absolute root as
`worktree_root`. A successful selection persists across later argument-free
reindexes. Use `reset_worktrees: true` to return to the configured roots, and
use `index-status` whenever the active source tree needs to be proved.

## Tool Surface

The MCP server registers these tools:

- `list-packages`: browse indexed packages.
- `search-symbols`: find declarations by query, kind, repository, package, or
  receiver.
- `read-symbol`: read the source for a function, method, type, const, or var.
- `find-implementations`: map interfaces to implementations or concrete types
  to interfaces.
- `find-references`: find indexed calls and package-qualified references, plus
  opt-in type-proven method selections.
- `list-services`: list gRPC service interfaces from generated protobuf code.
- `list-dependencies`: show cross-repository import dependencies.
- `search-config`: search struct fields, tags, types, and config comments.
- `cross-reference`: run set operations across symbols, fields, and services.
- `reindex`: rebuild the index, optionally selecting or resetting a live Git
  worktree override.
- `index-status`: report the published generation, active roots, counts, and
  captured Git selection provenance.

The tools are intentionally small and composable. A coding agent can combine
them during a refactor instead of relying on one large, lossy codebase summary.

## Configuration

Configuration is loaded in this order:

1. `GOAST_CONFIG`
2. `GOAST_REPOS`
3. `config.yaml` in the working directory

`GOAST_CONFIG` points at a YAML file:

```bash
GOAST_CONFIG=/path/to/config.yaml goast
```

`GOAST_REPOS` is a comma-separated list of local repositories:

```bash
GOAST_REPOS=/path/repo-a,/path/repo-b goast
```

The YAML form supports repository paths, opt-in test and typed-reference
indexing, coherent build contexts, exclude patterns, and transport settings:

```yaml
include_tests: false
typed_method_references: false

repos:
  - name: service-a
    path: /path/to/service-a
  - path: /path/to/service-b
    include_tests: true
    typed_method_references: true

exclude_patterns:
  - "vendor/**"
  - "testdata/**"

transport: stdio
port: 7400
```

Test indexing is disabled by default so a multi-repository fleet does not pay
the index-size and type-checking cost for tests unless they are useful. The
top-level `include_tests` value is the default for every repository. A
repository-level `include_tests` value overrides it, so one focused repository
can include tests while the rest of the fleet remains production-only:

```yaml
include_tests: true

repos:
  - path: /path/to/service-a
  - path: /path/to/large-service-b
    include_tests: false
```

Internal test files (`package widget`) join the normal package at its ordinary
import path. External test packages (`package widget_test`) use a distinct,
non-importable synthetic path such as
`example.com/project/widget [widget_test]`, so their symbols and callers cannot
collide with either `example.com/project/widget` or a real legal import path
that happens to end in `_test`.

Exclude patterns are applied even when test indexing is enabled. Consequently,
an explicit `**/*_test.go` pattern excludes test files from every opted-in
repository. Patterns use slash-separated path segments; `**` matches zero or
more directories, while a pattern without a slash is matched against every
file basename. If `exclude_patterns` is omitted, the defaults are `vendor/**`
and `testdata/**`.

Typed method references are also opt-in because retaining and type-checking
package syntax has a real startup and peak-memory cost in large fleets. The
top-level `typed_method_references` value is the default; an individual
repository can override it. Syntax-derived function and package-qualified
references remain enabled regardless.

With typed analysis enabled and no `build_contexts`, GoAST uses the server's
host Go build context. Explicit contexts union references from multiple
coherent variants while deduplicating shared files by exact file, line, and
column:

```yaml
build_contexts:
  - goos: linux
    goarch: amd64
    cgo_enabled: false
  - goos: js
    goarch: wasm
    cgo_enabled: false
    build_tags: [purego]
```

Build and tool tags are normalized and invalid contexts fail indexing instead
of silently producing an empty graph. When an explicit target differs from the
host and `cgo_enabled` is omitted, it defaults to `false`. Host tool tags are
preserved when GOARCH is unchanged; when GOARCH changes they are cleared unless
`tool_tags` is supplied. Standard-library imports and type sizes come from the
selected target rather than host export data. Receiver selections that still
cannot be proven are omitted, never guessed.

An optional repository `name` remains stable when the active path moves to a
linked worktree. If omitted, GoAST freezes the configured path's basename as
the logical name when that configuration is loaded; it does not rename the
repository after a worktree switch.

### Live Git Worktree Switching

GoAST can switch a running MCP server to another registered worktree of a
configured Git repository:

```json
{
  "worktree_root": "/absolute/path/to/feature-worktree"
}
```

The path must be absolute, resolve to the exact top level of a worktree listed
by `git worktree list`, and share Git's canonical common directory with at
least one configured repository. An unrelated clone is rejected even when it
has the same module path and commit. In a monorepo, every configured Go module
belonging to that Git repository is mapped to the same relative directory in
the selected worktree; other configured repositories are unchanged.

The override belongs to one running GoAST process and persists across ordinary
`reindex` calls. Separate stdio GoAST processes can therefore work on different
worktrees concurrently. HTTP clients connected to the same server share its
single process-local selection and must coordinate changes. Return to the
YAML/environment roots with:

```json
{
  "reset_worktrees": true
}
```

Validation, configuration loading, indexing, and selected-worktree provenance
verification complete before publication. The selected branch and HEAD must
match the pre-build selection when checked after the rebuild. A rejected path,
missing mapped `go.mod`, configuration error, changed selection, or index build
failure leaves the prior index, generation, roots, and worktree selection
authoritative.
Rebuilds are serialized while ordinary queries continue reading the previous
immutable generation. If a configuration reload removes the owner of an active
override, the reindex fails until `reset_worktrees` explicitly clears it.

`index-status` reports configured and active module paths, logical repository
names, symbol/package counts, generation, and branch/HEAD selection provenance.
Override provenance comes from the selection verified for that published
generation; Git metadata for ordinary configured roots is best-effort. HEAD
identifies the selected Git revision; uncommitted source is indexed but is
intentionally not represented by the HEAD value. Live switching requires the
`git` executable at runtime with support for
`git worktree list --porcelain -z`. Ordinary indexing of configured non-Git
source directories remains supported when no worktree override is requested.

## MCP Client Setup

`goast` is normally run by the client as a stdio subprocess. Install it first:

```bash
go install github.com/helsingin/goast/cmd/goast@latest
```

If `goast` is not on your shell `PATH`, use the absolute path to the installed
binary in the client config. With the default Go layout, that is usually:

```bash
$(go env GOPATH)/bin/goast
```

### Codex

Register `goast` as a Codex stdio MCP server:

```bash
codex mcp add goast \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- goast
```

Use an absolute binary path if needed:

```bash
codex mcp add goast \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- "$(go env GOPATH)/bin/goast"
```

Check the registered server:

```bash
codex mcp get goast
```

Remove an older registration before adding a replacement:

```bash
codex mcp remove goast
```

### Claude Code

Register `goast` as a user-scoped Claude Code stdio MCP server:

```bash
claude mcp add goast --scope user --transport stdio \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- goast
```

Use an absolute binary path if needed:

```bash
claude mcp add goast --scope user --transport stdio \
  --env GOAST_CONFIG=/path/to/config.yaml \
  -- "$(go env GOPATH)/bin/goast"
```

Check the registered server:

```bash
claude mcp get goast
```

Remove an older registration before adding a replacement:

```bash
claude mcp remove goast --scope user
```

### Claude Desktop

Add the server to the Claude Desktop config:

```json
{
  "mcpServers": {
    "goast": {
      "command": "goast",
      "env": {
        "GOAST_CONFIG": "/path/to/config.yaml"
      }
    }
  }
}
```

## Transport

`stdio` is the default transport for desktop and CLI-based MCP clients.

To run streamable HTTP mode, set the transport and port in config:

```yaml
transport: http
port: 7400
```

The HTTP server exposes MCP at:

```text
http://127.0.0.1:7400/mcp
```

## What You Can Build With It

### AI Codebase Navigator

Connect a coding agent to large Go repositories and give it precise tools for
finding code, reading implementations, mapping dependencies, and staying current
after edits.

### Architecture Discovery Tool

Index multiple service repositories and inspect package boundaries, generated
service interfaces, config types, and cross-repository imports from one place.

### Refactoring Companion

Use symbol search, source reads, implementation mapping, references, and
cross-reference checks to support broad refactors without relying only on text
search.

### API and Config Review Assistant

Use `list-services` and `search-config` to review generated API surfaces and
runtime configuration shapes before changing service contracts or deployment
settings.

## What an Integration Needs

An integration needs:

- Local filesystem access to the Go repositories that should be indexed.
- A `config.yaml`, `GOAST_CONFIG`, or `GOAST_REPOS` value.
- An MCP client that can run a stdio command or connect to streamable HTTP.
- A policy decision about which repositories and generated files should be in
  scope.
- A policy decision about whether tests should be indexed globally or only for
  selected repositories.
- A policy decision about whether typed method references justify their
  startup/memory cost and which build contexts they should cover.
- A habit of calling `reindex` after code-changing agent operations.
- For live worktree switching, a local `git` executable supporting
  `git worktree list --porcelain -z` and registered linked worktrees belonging
  to configured repositories.

No repository needs to compile as part of startup. `goast` parses source files
and builds its structural index from Go ASTs. Opted-in repositories retain only
method selections that the build-context-aware `go/types` pass can prove.

## What It Is Not

`goast` is not a Go language server. It does not replace `gopls`, editor
diagnostics, build-tag-aware builds, complete type checking, code completion,
or refactoring tools.

It is not a full semantic analyzer. When enabled, `find-references` uses
`go/types` to index proved method calls, method values, method expressions, and
promoted methods in declared build contexts, in addition to syntax-derived
function and package-qualified references. A receiver selection that cannot be
resolved because source is incomplete, ill-typed, excluded, or unavailable is
omitted rather than guessed.

It is not a security scanner, compliance engine, build system, or generic
full-text search service. It is a focused MCP server for structured Go code
discovery.

## Main Files

- [`cmd/goast/main.go`](cmd/goast/main.go): command entry point and transport
  startup.
- [`internal/config`](internal/config): config loading from YAML and
  environment variables.
- [`internal/index`](internal/index): Go parsing, package indexing, symbol
  indexing, references, dependencies, and search helpers.
- [`internal/server`](internal/server): MCP server construction and tool
  registration.
- [`internal/tools`](internal/tools): MCP tool handlers.
- [`testdata/sample_repo`](testdata/sample_repo): sample repository used by
  tests.
- [`config.example.yaml`](config.example.yaml): starter config file.

## Verification

Run the regular test suite:

```bash
make test
```

Run vet:

```bash
make lint
```

Build the command:

```bash
make build
```

Run the optional integration test against a real local Go repository:

```bash
GOAST_INTEGRATION_REPO=/path/to/go/repo go test ./internal/tools
```

Set `GOAST_INTEGRATION_INCLUDE_TESTS=1` to exercise test indexing and
`GOAST_INTEGRATION_TYPED_METHOD_REFERENCES=1` for typed receiver selections. To
require a specific reference target, also set
`GOAST_INTEGRATION_REFERENCE_PACKAGE` and
`GOAST_INTEGRATION_REFERENCE_NAME` (methods use `Receiver.Method`).
