# Architecture

## Overview

`goast` is a standalone MCP server that builds an in-memory index of Go symbols
from one or more repositories. It exposes tool handlers that let AI agents
progressively discover packages, symbols, APIs, dependencies, references, and
implementation relationships.

```text
MCP client
   |
   | stdio or streamable HTTP
   v
goast
   |
   +-- MCP server and tool registry
   |
   +-- in-memory index
   |     +-- symbols
   |     +-- packages
   |     +-- struct fields
   |     +-- references
   |     +-- cross-repo dependencies
   |     +-- interface implementation maps
   |
   +-- source file reads for read-symbol
```

The structural index is AST-based and does not run `go list` or require
dependencies to compile. An opt-in, non-fatal `go/types` pass resolves method
selections for the reference index within coherent declared build contexts.
Type errors do not stop structural indexing, which keeps the server usable
against incomplete or partially checked-out workspaces.

## Startup Flow

1. Load config from `GOAST_CONFIG`, `GOAST_REPOS`, or `config.yaml`.
2. Parse each repository's `go.mod` to get its module path.
3. Walk Go source files, applying each repository's effective test-indexing
   setting and then exclude patterns.
4. Parse files with `go/parser` and `go/ast` using comments.
5. Extract packages, symbols, struct fields, interface methods, imports, and
   syntax-derived body references.
6. For opted-in repositories, use `go/types` over coherent build-context
   package groups to collect proved receiver selections.
7. Build lookup maps and derived indexes.
8. Wrap the index in `IndexHolder` so `reindex` can atomically refresh it.
9. Start the MCP server on stdio or HTTP.

## Package Layout

```text
cmd/goast/main.go        config -> index -> server -> transport
internal/
  config/                YAML/env configuration loading
  index/                 AST parsing, index construction, search, references
  tools/                 MCP tool handlers
  server/                MCP server setup and tool registration
testdata/                small Go module used by unit tests
```

## Index Model

### Symbol

One entry is created for each function, method, type, const, or var.

Important fields:

- `Name`, `Kind`, `Exported`
- `Repo`, `ImportPath`, `PkgName`
- `FilePath`, `Line`, `EndLine`
- `Signature`
- `Receiver` for methods
- `TypeKind` for types: `struct`, `interface`, `alias`, or `other`
- `Fields` for struct fields
- `MethodDescriptors` for interface methods
- `Doc` and `DocSummary`
- `Generated` for generated protobuf files

### Package

One entry is created for each import path.

Important fields:

- `ImportPath`, `Name`, `Repo`, `Dir`
- `Doc` and `DocSummary`
- `FileCount`, `SymbolCount`
- `Internal`

### Lookup Maps

`NewIndex` builds maps for efficient tool handling:

- lowercase symbol name to symbols
- import path to symbols
- repository name to symbols
- import path to package
- interface symbol to implementing concrete types
- concrete type to satisfied interfaces
- target symbol to references

## AST Extraction

`goast` uses `go/parser.ParseFile` with `parser.ParseComments`.

- Functions and methods come from `*ast.FuncDecl`.
- Types, consts, and vars come from `*ast.GenDecl`.
- Struct fields are extracted from `*ast.StructType`.
- Interface methods are extracted from `*ast.InterfaceType`.
- Signatures are rendered with `go/printer`.
- Package import paths are derived from module path plus relative file path.
- Package-qualified references are collected from function bodies.
- Test files are parsed only when enabled for their repository. Internal tests
  share the production package import path; an external `package foo_test` is
  indexed under a non-importable synthetic path such as
  `example.com/project/foo [foo_test]`, which cannot collide with a real import
  path ending in `_test`.

Default struct-field values are inferred from same-file composite literals when
they are easy to recover syntactically. This is best-effort and intentionally
limited; it is meant to improve configuration discovery, not replace program
analysis.

## Reindexing

The initial index is built at startup. During an agent session, source can
change. The `reindex` tool rebuilds the index from disk and swaps it into the
server through `IndexHolder`.

Tool handlers always read the current index from the holder, so they see the
new index immediately after `reindex` completes.

## Interface Matching

`find-implementations` uses normalized method descriptors, such as:

```text
Authorize(context.Context,Subject,Action,*Resource)(Decision)
```

Parameter names are stripped so interface and implementation methods can match
when names differ. Embedded interface and struct method sets are flattened to a
fixpoint before matching.

This is still AST-based, not full Go type checking. Known tradeoffs:

- package-qualified types inside method signatures are compared textually
- type aliases are not fully resolved
- pointer and value receivers are treated equally
- ambiguous embedded references fall back to best-effort name matching

## Reference Index

`find-references` indexes:

- same-package unqualified calls like `NewClient()`
- package-qualified calls like `auth.NewClient()`
- package-qualified value references like `auth.ErrDenied`
- opt-in `go/types`-proved method calls like `client.Do()`
- opt-in method values like `do := client.Do`
- opt-in method expressions like `Client.Do`
- opt-in promoted methods selected through embedded fields or types

Method targets are addressed as `Receiver.Method`. For promoted selections the
target is the receiver that actually declares the method, not the outer type
through which it was selected. Type checking is deliberately non-fatal;
unresolved or ill-typed selections are omitted rather than guessed. Structural
references are always collected; receiver-aware analysis requires
`typed_method_references: true` and is unioned across configured coherent build
contexts.

## Dependency Graph

`list-dependencies` builds cross-repository edges from import statements. If
repository A imports a path under repository B's module path, `goast` records
an edge from A to B and stores the imported package paths.

## Configuration

Configuration priority:

1. `GOAST_CONFIG`
2. `GOAST_REPOS`
3. `config.yaml` in the working directory

Defaults:

| Field | Default |
| --- | --- |
| `include_tests` | `false` |
| `typed_method_references` | `false` |
| `build_contexts` | host Go build context |
| `exclude_patterns` | `vendor/**`, `testdata/**` |
| `transport` | `stdio` |
| `port` | `7400` |

The top-level `include_tests` value supplies the default for every configured
repository. An `include_tests` value on an individual `repos` entry overrides
that default. This supports a production-only fleet index with tests enabled
for one repository, or a test-inclusive default with selected large
repositories opted out.

Exclude patterns take precedence over test inclusion. An explicit
`**/*_test.go` therefore suppresses tests even for a repository whose effective
`include_tests` value is `true`. Patterns are matched as slash-separated paths;
the `**` segment matches zero or more directories, and patterns without a slash
apply to basenames in every directory.

Typed method analysis has the same top-level-default and per-repository
override shape as test indexing. Explicit build contexts select GOOS, GOARCH,
cgo, build tags, and optional tool tags. Each context is checked separately and
the exact references are unioned; files shared by contexts are deduplicated by
file, line, and column. Invalid targets or tags fail indexing. Alternate-target
standard-library packages are imported from source under that context rather
than from host export data, and `go/types` uses the selected compiler/GOARCH
size model. Host tool tags survive same-architecture OS changes; architecture
changes require explicit `tool_tags` when target-specific tool tags matter.

## Testing

Unit tests use `testdata/sample_repo`, a small Go module containing exported
and unexported symbols, internal and external test packages, method calls and
values, promoted methods, structs, interfaces, consts, vars, docs, internal
packages, and generated protobuf-shaped files.

Optional integration tests can run against a real local repository by setting:

```sh
GOAST_INTEGRATION_REPO=/path/to/go/repo go test ./internal/tools
```

`GOAST_INTEGRATION_INCLUDE_TESTS=1` enables test files for that run and
`GOAST_INTEGRATION_TYPED_METHOD_REFERENCES=1` enables typed receiver analysis.
A concrete reference can be required with
`GOAST_INTEGRATION_REFERENCE_PACKAGE` and
`GOAST_INTEGRATION_REFERENCE_NAME`.
