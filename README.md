# goast

GoAST helps a solo architect-builder and a coding agent increase **the size of
a system one person can change confidently**.

A coding agent makes implementation faster. Your bottleneck becomes directing
it: finding the right code, explaining relationships, checking consequences,
and catching locally correct changes that violate the larger design. GoAST
reduces that coordination work by giving the agent a queryable map of your Go
workspace and tools for reviewing changes against architectural intent.

For a solo builder, that means:

- **Larger changes per working session.** An interface change spanning several
  packages or services becomes easier to trace, delegate to the agent, and
  review.
- **Less preparation for each task.** You spend less time assembling file lists
  and explaining where things live. The agent can query the system itself.
- **More viable maintenance work.** Config consolidation, implementation
  consistency, dependency cleanup, and test discovery become cheaper to
  investigate.
- **Faster return to dormant projects.** You can reconstruct the relevant
  architecture from the code instead of rebuilding all that context from
  memory.
- **More attention available for design.** You can spend a larger share of your
  time deciding what the system should do and assessing tradeoffs.

For example, changing an authorization interface across three services involves
substantial reconnaissance and follow-through. With GoAST, the agent can locate
implementations and callers, make coordinated edits, refresh the index, inspect
the reported impact, and check declared enforcement paths. You still own the
design and acceptance criteria, but less of your day goes into navigating the
consequences.

The intended productivity gain is **more completed, coherent changes per unit
of your attention**. These are workflow benefits, not a benchmarked speedup:
the benefit will depend on workspace size, familiarity, and how often changes
cross package or repository boundaries. Impact analysis and structural
witnesses provide bounded evidence; they support your review and judgment.

`goast` is a Go MCP server for indexing Go source across one or more
repositories. It builds a fast in-memory index from Go ASTs and exposes tools
that let AI coding agents discover packages, symbols, source ranges, references,
interfaces, config structs, gRPC services, and cross-repository dependencies
without reading an entire workspace into context. It also compares Git snapshots
for change-impact review and offers bounded structural enforcement analysis.

The default setup runs as a stdio MCP server. Point it at local Go repositories
with a small YAML file or the `GOAST_REPOS` environment variable, connect it to
an MCP-capable coding agent, and use the tool surface as a live code map during
implementation, review, and refactoring work.

It is designed for engineers and teams that need AI assistants to work inside
large Go systems with better grounding: architecture discovery, interface
mapping, dependency review, generated API inspection, configuration analysis,
and reindexing after code changes.

## Quick Start

Building or installing from source requires Go 1.24 or newer. Each configured
repository path should point to a Go module root containing `go.mod`; configure
multiple module roots separately for a monorepo. Git is required for source
snapshots, impact analysis, structural witnesses, and worktree switching.

Install the command:

```bash
go install github.com/helsingin/goast/cmd/goast@latest
```

Or build from a local checkout:

```bash
make build
```

This writes `bin/goast`; use `./bin/goast` in place of `goast` below, or run
`make install` to install this checkout into `GOBIN` (normally `GOPATH/bin`).
`@latest` installs the latest published release, which can lag behind this
checkout's features.

From a local checkout, create a config file:

```bash
cp config.example.yaml config.yaml
```

Alternatively, create `config.yaml` directly using the following example.
Set the paths to one or more local Go modules:

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

In stdio mode, the server waits for MCP input; an MCP client normally launches
it as a subprocess. Startup logs go to stderr.

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
- Compares a Git base revision with the indexed worktree to identify changed
  declarations, affected callers, and candidate tests.
- Produces bounded SSA witnesses and counterexamples for declared enforcement
  rules, with explicit [analysis limitations](#structural-analysis-limits).
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

The separate `analyze-structural-witness` tool loads and type-checks packages
on demand to build SSA (static single assignment form). It requires the `go`
command on the server's `PATH`, available dependencies, and packages that load
successfully in each requested context. This stronger requirement applies to
that tool, not ordinary AST indexing.

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
        +-- Git snapshot and change-impact analysis
        +-- on-demand structural witnesses (Go packages + SSA)
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

`search-symbols` uses substring queries, or `exact:Name` for an exact,
case-sensitive declaration name. Generated `.pb.go` symbols are hidden unless
`include_generated: true` is supplied; the default result limit is 100.
Query-free searches return exported declarations only. To find an unexported
declaration, provide a nonempty query and leave `exported_only` false.

### Map an Interface

Use `find-implementations` on an interface to find concrete types that satisfy
it. Use `direction=interfaces` on a concrete type to see which indexed
interfaces it appears to implement.

This uses AST method-descriptor matching, not a compiler-verified assignability
check. Confirm semantic compatibility with Go's compiler when it matters.

### Review API Surface

Use `list-services` to inspect generated gRPC service interfaces and method
signatures without manually opening protobuf-generated files. Discovery
currently recognizes interfaces ending in `ServiceServer` in `.pb.go` files,
excluding `Unsafe*` stubs; other generated naming conventions are not included
automatically.

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
numbers match the edited workspace. There is no automatic file watcher. A
reindex also reloads index configuration from YAML/environment; changing the
transport or listening port requires a server restart.

When the agent moves to another linked Git worktree, pass its absolute root as
`worktree_root`. A successful selection persists across later argument-free
reindexes. Use `reset_worktrees: true` to return to the configured roots, and
use `index-status` whenever the active source tree needs to be proved.

### Review Changes Against a Base Commit

After `reindex`, call `impact-since` with the stable configured repository name
and a locally resolvable Git revision:

```json
{
  "repository": "service-a",
  "base_commit": "main"
}
```

The comparison includes committed changes since the base, uncommitted tracked
changes, and non-ignored untracked files. It compares the current worktree
directly with that revision; it does not choose a merge base or fetch remote
refs. To review a branch against its common ancestor, resolve the desired
commit with Git first and pass that SHA as `base_commit`.

The result contains `snapshot`, `changed_files`, `changed_symbols`,
`affected_symbols`, `interfaces`, `config_structs`, `services`,
`candidate_tests`, `affected_repositories`, and `impact_digest`. Complete
declaration contents are compared, so body-only edits count. Removed
declarations remain in `changed_symbols`.

Impact analysis uses the current index's direct references and interface
relationships, not a transitive whole-program call graph. Deleted declarations
have no current symbol from which to expand callers. Config classification
uses struct names containing `config`; service classification uses generated
interfaces ending in `ServiceServer`. Candidate tests are indexed `Test*`
functions in affected packages; enable `include_tests` for useful coverage.
Enable `typed_method_references` in caller repositories for method references.
Cross-repository dependants are direct importers of the selected repository.

Both analysis tools require a captured Git snapshot and reject source drift
since publication with an error asking for `reindex`. Keep the worktree stable
during analysis. Snapshot digests cover the whole Git worktree, including
non-Go files, even when the configured module is a subdirectory. Git-ignored
untracked files are outside that snapshot identity.

### Inspect an Enforcement Path

`analyze-structural-witness` checks a caller-supplied `must-pass-through` rule:
which entry points should reach which sensitive operations through which
enforcement functions. The rule selects exactly one configured repository;
other repositories can remain configured on the server.

This example uses the names in
[`testdata/structural_witness`](testdata/structural_witness). For an isolated
trial, copy that module to its own Git repository, commit it, and configure its
root with `name: fixture`. Replace the identities for your own code:

```json
{
  "version": 1,
  "invariant_id": "INV-RELEASE-001",
  "relation": "must-pass-through",
  "scope": {
    "repositories": ["fixture"],
    "build_contexts": ["default"]
  },
  "entry_symbols": [
    {"repository": "fixture", "language": "go", "package": "example.test/structural/release", "name": "Valid", "kind": "func"}
  ],
  "enforcement_symbols": [
    {"repository": "fixture", "language": "go", "package": "example.test/structural/release", "name": "Authorize", "kind": "func"}
  ],
  "sink_symbols": [
    {"repository": "fixture", "language": "go", "package": "example.test/structural/release", "name": "Sink.Send", "kind": "method"}
  ],
  "permit_types": [
    {"repository": "fixture", "language": "go", "package": "example.test/structural/release", "name": "Permit", "kind": "type"}
  ],
  "binding_requirements": ["payload-digest", "destination"],
  "failure_policy": "fail-closed"
}
```

Version `1`, a nonempty invariant ID, relation `must-pass-through`, nonempty
entry/enforcement/sink/context lists, and `failure_policy: "fail-closed"` are
required. Every symbol identity, including optional permit types, must include
`language: "go"`. Methods use `Receiver.Method`; structs and interfaces use
`kind: "type"`. `permit_types` and `binding_requirements` are optional.

Structural `scope.build_contexts` is a list of strings, separate from the YAML
build-context objects used for typed references. `default` loads production
packages in the server's Go environment; `test` or `tests` also loads tests.
Other strings are passed to Go as `-tags=<value>`, such as `production` or
`production,purego`. The tool loads `./...` from the active module using Go's
package selection, independently of AST `exclude_patterns`, `include_tests`,
and `typed_method_references` settings.

The report provides declared and candidate sinks, witnessed paths,
counterexamples (`BYPASS`, `IGNORED_ENFORCEMENT`, `BINDING_DRIFT`, `FAILURE_OPEN`),
unresolved edges, limitations, `rule_digest`, and `report_digest`. Current
status selection is:

| Status | Meaning in the current implementation |
| --- | --- |
| `counterexample` | At least one counterexample was found. |
| `indeterminate` | No counterexample, but at least one unresolved edge was recorded. |
| `no-witness-discovered` | Neither of the above, and at least one declared sink lacks a witness in a requested context. |
| `complete` | Every declared sink has a witness in every requested context, with no recorded counterexample or unresolved edge. |

Missing sink/context pairs are listed in `limitations` and the text response.
Partial witnesses remain in `witnessed_sinks`; evidence in one context cannot
satisfy another context's coverage requirement. Missing coverage is not itself
reported as a bypass counterexample.

Source drift currently returns a tool error, not a report with `status: "stale"`.
Invalid rules and package-loading failures also return errors.

#### Structural Analysis Limits

Treat this as bounded review evidence. `complete` establishes coverage of the
declared sinks across requested contexts within the analysis below; it is not
a whole-program or runtime guarantee.

The traversal follows selected static calls, but enforcement dominance and
binding checks operate within the function containing a sink. Unmapped
interface calls are recorded as unresolved; other dynamic calls, reflection,
goroutines, and deferred calls are not comprehensively modeled. A missing
unresolved-edge report does not establish that all execution paths were checked.

Binding checks match argument names/types and require the same SSA value at
enforcement and sink. Supported labels include `payload-digest`, `destination`,
`classification`, `releasability`, `policy-identity`, and `principal-identity`.
These checks do not prove cryptographic digest correctness or absence of
mutation through aliases. `permit_types` is included in the rule digest but
does not currently enforce permit provenance or one-time use. Candidate egress
discovery uses operation names such as `Send`, `Publish`, and `Write`; it is
not an exhaustive inventory of external effects.

### Source Identity and Report Digests

Impact and structural reports include a `snapshot` containing repository,
HEAD, branch, tracked/untracked/worktree digests, and toolchain identity.
Impact snapshots also resolve `base_commit`; structural snapshots have no
comparison base. Symbol identities include `repository`, `language`, `package`,
`name`, and `kind`.

- `goast_generation` is the process-local numeric index generation, starting
  at 1 and increasing after successful reindexes.
- `structural_provider` is `"goast"`.
- `structural_generation` is a 64-character hexadecimal digest derived from
  the numeric generation, toolchain identity, and worktree digest.
- `impact_digest` and `report_digest` hash normalized report data with their
  own digest field cleared. `rule_digest` covers the normalized structural rule.

Use the returned digests as opaque identifiers. Reproducing them requires the
exact serialization contract, including field order and empty/null arrays;
arbitrarily re-encoding JSON can change the hash. They identify analysis inputs
and results, not a signature or a proof of analysis completeness.

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
  captured Git source provenance, including the combined worktree digest.
- `impact-since`: compare one current indexed Git worktree with a resolved base
  commit and return changed declarations, callers, implicit interfaces,
  configuration types, generated services, candidate tests, cross-repository
  dependants, an exact source snapshot, and a deterministic impact digest.
- `analyze-structural-witness`: evaluate one frozen `must-pass-through` rule
  against the exact published Git snapshot with bounded interprocedural SSA.
  The deterministic report includes declared and candidate sinks, witnessed
  paths, bypass/binding/failure-open counterexamples, unresolved dynamic edges,
  limitations, complete-rule digest, generation, status, and canonical report
  digest.

The tools are intentionally small and composable. A coding agent can combine
them during a refactor instead of relying on one large, lossy codebase summary.

See [Inspect an Enforcement Path](#inspect-an-enforcement-path) for the rule
format, status meanings, and current analysis limits.

## Configuration

Configuration is loaded in this order:

1. `GOAST_CONFIG`
2. `GOAST_REPOS`
3. `config.yaml` in the working directory

The first selected source is used without merging the others. An invalid
`GOAST_CONFIG` file fails loading instead of falling back to `GOAST_REPOS`.
Use absolute repository and config paths for client-launched processes;
relative repository paths resolve from the server's working directory, not
the YAML file's directory.

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
file basename. If `exclude_patterns` is omitted or empty, the defaults are
`vendor/**` and `testdata/**`. Directories named `vendor`, `testdata`, or `.git`
are always skipped by AST indexing, regardless of custom patterns.

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

These contexts filter typed method selections, not the base AST symbol list,
which can include declarations from mutually exclusive build variants.

When typed references are enabled, build and tool tags are normalized and
invalid contexts fail indexing instead of silently producing an empty graph.
When an explicit target differs from the
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
configured Git repository by passing these arguments to `reindex`:

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
YAML/environment roots by calling `reindex` with:

```json
{
  "reset_worktrees": true
}
```

`worktree_root` and `reset_worktrees: true` are mutually exclusive.

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
names, symbol/package counts, generation, branch/HEAD selection provenance, and
the combined worktree digest. The separate tracked and untracked digests are
included in the analysis tools' structured snapshots.
Override provenance comes from the selection verified for that published
generation; Git metadata for ordinary configured roots is best-effort. HEAD
identifies the selected Git revision; the worktree digests additionally bind
uncommitted tracked changes and non-ignored untracked content. Live switching
requires the `git` executable at runtime with support for
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

If `go env GOBIN` is nonempty, use that directory instead. For a local build,
use the absolute path to `bin/goast` in the checkout.

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

See the [official Codex MCP documentation](https://developers.openai.com/codex/mcp)
for client configuration details.

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

See the [Claude Code MCP documentation](https://code.claude.com/docs/en/mcp)
for client configuration details.

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

Clients can connect locally at:

```text
http://127.0.0.1:7400/mcp
```

The listener binds `:7400` on all interfaces, not just loopback. The MCP handler
is mounted directly, so `/mcp` is a usable client URL rather than an exclusive
route. GoAST configures no authentication or TLS; restrict network access or
provide those controls through a reverse proxy when sharing the server. Every
client of that process shares its configured repositories and worktree
selection. The `port` setting is unused in stdio mode.

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
- A readable `go.mod` at each configured module root. Ordinary indexing logs
  and skips modules whose `go.mod` cannot be read, so check startup counts.
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
- For structural witnesses, a working `go` command, resolvable dependencies,
  and packages that type-check in the requested contexts.

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
full-text search service. Structural witnesses supplement review with bounded
static evidence; they do not establish runtime enforcement or whole-program
security. See [Structural Analysis Limits](#structural-analysis-limits).

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
- [`docs/architecture.md`](docs/architecture.md): index internals and analysis
  design.
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

`make build` and `make install` embed a version from `git describe`; use
`make build VERSION=vX.Y.Z` to set it explicitly. The version is advertised
in MCP initialization and startup logs; there is no `--version` CLI flag.

Run the optional integration test against a real local Go repository:

```bash
GOAST_INTEGRATION_REPO=/path/to/go/repo go test ./internal/tools
```

Set `GOAST_INTEGRATION_INCLUDE_TESTS=1` to exercise test indexing and
`GOAST_INTEGRATION_TYPED_METHOD_REFERENCES=1` for typed receiver selections. To
require a specific reference target, also set
`GOAST_INTEGRATION_REFERENCE_PACKAGE` and
`GOAST_INTEGRATION_REFERENCE_NAME` (methods use `Receiver.Method`).

For the structural integration check, set `GOAST_INTEGRATION_STRUCTURAL_RULE`
to a JSON rule in the format above; its repository name must match the
configured name (the module directory's basename in this test harness).
`GOAST_INTEGRATION_EXPECT_STATUS` optionally asserts a specific result status.
Tests requiring these environment variables are skipped when they are absent.
