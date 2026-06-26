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
repos:
  - path: /path/to/service-a
  - path: /path/to/service-b

exclude_patterns:
  - "vendor/**"
  - "**/*_test.go"
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
- Finds indexed call sites and package-qualified value references.
- Lists gRPC service interfaces from generated protobuf files.
- Builds a cross-repository dependency graph from real import statements.
- Searches struct fields by name, type, tag, and doc comment.
- Extracts common config details from `yaml` and `json` tags.
- Runs set operations for missing implementations, missing fields, field
  coverage, and unimplemented generated services.
- Rebuilds the in-memory index during an active agent session.
- Serves MCP over stdio or streamable HTTP.

The agent does not need to guess where code lives. It can ask for the package
list, search for a symbol, read the implementation, check who imports it, and
refresh the index after edits.

## Go ASTs, MCP, and Agent Context

The Go parser already exposes the structure that agents usually need first:
packages, declarations, receivers, methods, imports, comments, fields, and
source positions. `goast` uses that structure directly instead of requiring a
full build, a running language server, or a type-checking environment for every
indexed repository.

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

## Tool Surface

The MCP server registers these tools:

- `list-packages`: browse indexed packages.
- `search-symbols`: find declarations by query, kind, repository, package, or
  receiver.
- `read-symbol`: read the source for a function, method, type, const, or var.
- `find-implementations`: map interfaces to implementations or concrete types
  to interfaces.
- `find-references`: find indexed call sites and package-qualified references.
- `list-services`: list gRPC service interfaces from generated protobuf code.
- `list-dependencies`: show cross-repository import dependencies.
- `search-config`: search struct fields, tags, types, and config comments.
- `cross-reference`: run set operations across symbols, fields, and services.
- `reindex`: rebuild the index from disk.

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

The YAML form supports repository paths, exclude patterns, and transport
settings:

```yaml
repos:
  - path: /path/to/service-a
  - path: /path/to/service-b

exclude_patterns:
  - "vendor/**"
  - "**/*_test.go"
  - "testdata/**"

transport: stdio
port: 7400
```

When no exclude patterns are provided, `goast` skips `vendor/**`,
`**/*_test.go`, and `testdata/**` by default.

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
- A habit of calling `reindex` after code-changing agent operations.

No repository needs to compile as part of startup. `goast` parses source files
and builds its index from Go ASTs.

## What It Is Not

`goast` is not a Go language server. It does not replace `gopls`, editor
diagnostics, build tags, type checking, code completion, or refactoring tools.

It is not a full semantic analyzer. Package-qualified references are indexed,
but local value method calls such as `x.Method()` require type resolution and
are intentionally outside the current scope.

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
