# codeg: deterministic, persistent, incremental Go code graph

A Go CLI that indexes a Go repository into a compact on-disk index, keeps it in sync incrementally, and serves a local React UI to explore it. The goal is a reliable, deterministic representation of the code, built from Go's own tooling (`go/packages`, `go/types`). It is not AI, RAG or LLM-based, and it does not guess.

## 1. Locked decisions

| Area | Decision |
|---|---|
| Form | Go CLI: `codeg index`, `serve`, `status`, `query`; `serve --watch` later |
| Scale | No size limit: lazy per-package loading, never load the whole index |
| Index | Compact binary, one file per package, plus a manifest and summary, in `.codeg/` of the repo. One project per server. |
| Dependencies | Reference-only stubs: identity, signature, and use sites (file:line) |
| Build config | One config per index (host GOOS/GOARCH by default, flags to override). Files excluded by tags are listed. |
| Tests / generated | Indexed and flagged. Hidden in the UI by default, with toggles. |
| Server | Localhost only, backend for the React app, single binary with the UI embedded |
| UI | Package graph, then package internals, then function graph. Sidebar shows a plain snippet with line highlight (no clickable code viewer). Variables show as an ordered event list plus one-hop links. |
| Order of work | Indexer and CLI queries first, then the UI |

## 2. Architecture

```text
 fs + go.mod/go.work/env
        │
   [Planner] ──reads──▶ manifest (packages, file hashes, input_hash, api_hash)
        │  metadata-only load (go list, no typecheck)
        ▼
   dirty package set (topological worklist, early cutoff)
        │
   [Loader]  go/packages.Load(dirty import paths)   ← roots from source, deps from export data
        ▼
   [Extractor] per package: ast/inspector + types.Info → PackageFacts (pure, deterministic)
        ▼
   [Writer]  pkg/<id>.<inputhash>.bin  →  manifest swap (commit)
        ▼
   [Reader / Query layer]  lazy per-package loads + summary index
        ▼
   [HTTP server (localhost)]  →  React + React Flow UI
```

Rules:
- The extractor is a pure function from a type-checked package to `PackageFacts`. It never touches storage, so it can be golden-tested.
- **Ownership:** every fact is owned by exactly one package. Re-indexing a package replaces everything it owns, which prevents stale edges.
- **Central invariant:** the incremental result must equal a from-scratch result. This is tested in CI by editing fixtures and comparing the two indexes.
- Only `go/packages`, `go/types` and `go/ast` are needed for v1. `go/ssa` and `go/callgraph` are v2. `go/analysis` is not part of the indexer.

## 3. Library roles

| Package | Job |
|---|---|
| `go/packages` | Loads and type-checks. Handles modules, workspaces, vendoring, build tags, cgo and test variants through `go list`. Also the source of the package import graph. |
| `go/ast` + `x/tools/go/ast/inspector` | Positions and syntactic context (assignment LHS, call target, `go`/`defer`, return, composite-literal key). `ast.IsGenerated` flags generated files. |
| `go/types` | All semantic identity: `Info.Defs/Uses/Selections/Instances/Scopes`, `Implements`, method sets, `Unalias`, `Origin()`. |
| `x/tools/go/types/typeutil` | `Callee`/`StaticCallee` classify calls (excludes conversions and builtins). |
| `x/tools/go/types/objectpath` | Stable identity for hard-to-name objects (anonymous struct fields). |
| `x/tools/go/gcexportdata` | Serialize a package's exported API, hashed as `api_hash` for early cutoff. |
| `go/build/constraint` | Inspect excluded files' build tags. |
| `x/mod/modfile` | Read go.mod. |
| `x/tools/txtar` | Multi-file test fixtures. |
| v2 | `go/ssa`, `ssautil`, `buildssa`, `go/callgraph/{cha,rta,vta}`. |

## 4. Graph model

**Symbol kinds:** package, file, const, var (global), field, func, method, type (struct, interface, named, alias), type_param, local, param, result, func_lit.

**Occurrences are the primary fact**: (file, byte range, symbol, role, enclosing symbol, attributes). "Calls", "callers", "reads" and so on are queries over occurrences.

**Occurrence roles**
- `definition`
- `read`
- `write` (whole overwrite: `x = ...`, `x := ...`)
- `read_write` (`x++`, `x += 1`)
- `write_partial` (`x.f = ...`, `x[i] = ...`): a write on the field or element and a partial mutation of the root variable
- `address_taken` (`&x`, or an implicit `&x` for a pointer-receiver call)
- `call`, with `dispatch` = `static` | `interface` | `dynamic`, plus `go` and `defer` flags
- `value_ref` (function used as a value, e.g. `handler := CreateUser`)
- `construct` (`T{}`, `&T{}`, `new(T)`)
- `type_use`
- `import`
- `captured` (variable declared outside the current closure)

Every occurrence also carries: `in_loop`, `in_branch`, `in_closure`, `in_defer`, `in_go`; call-site ID and argument index for arguments; and statement-level source and target for `a := b`, `x.f = y`, `return z`.

**Structural edges:** `contains`, `has_param`, `has_result`, `has_field`, `has_method`, `embeds`, `type_param`, `alias_of`, package `imports`.

**Derived, on demand:** `implements` (with `via_pointer`, `via_embedding`), and in v2 `may_call`.

**Dropped from the original proposal:** separate `Defines` and `Declares` edges (one `definition` role plus structural containment), and separate `Writes` and `Modifies` (the roles above are more precise).

## 5. Symbol identity

Keys never include the module version.

| Entity | Key |
|---|---|
| Package-level func/type/var/const | `pkgpath.Name` |
| Method | `pkgpath.RecvType.Method` (pointer-ness is not part of the key; generic receivers use the base name) |
| Field | `pkgpath.Type.Field` (embedded promotion resolves to the declaring field via `Selection.Obj()`; anonymous structs use `objectpath`) |
| Multiple `init`, blank `_` | `pkgpath.init#N`, `pkgpath._#N` (source-order ordinal) |
| Generic instantiation | Origin symbol (`Origin()`), with type arguments as occurrence attributes |
| Alias | Own symbol with an `alias_of` edge |
| Type parameter | `owner_key[T]` |
| Function-local type/const | `owner_key.T@N` |
| Closure | `parent_key$N` (ordinal by position) |
| Parameter / result | `func_key#param{i}` / `#result{i}` (index-based; names can be `_` or absent) |
| Local variable | `func_key::name@N` (nth declaration of that name in the function; resolves shadowing) |

Cross-package references only target package-level symbols, methods and fields, all keyed by name and structure. Locals, params and closures are never referenced from outside their function, so their ordinal keys only need to be stable within one indexing generation. Do not key by byte offset.

The owning package is derived from the key's `pkgpath` prefix, so there is no global symbol table.

## 6. Index file layout

```text
.codeg/
  manifest.bin                 config, schema version, package table, file hashes, input/api hashes
  summary.bin                  import graph, per-package "packages I reference", method-name index
  pkg/<id>.<inputhash>.bin     everything owned by one package
  lock                         prevents two indexers running at once
```

**Package file** (columnar, varint and delta encoded, optional zstd):
- string table
- symbol table, sorted by key
- occurrences in position order, plus a second sorted array for lookup by symbol
- structural edges
- call-edge pairs for fast traversal
- each named type's full method set, flattened including promoted methods, as (name, canonical signature string, pointer-receiver flag)
- external symbol stubs, with module@version and signature
- header with format version and checksum

**Format choice:** a hand-defined binary layout, not protobuf, because protobuf allocates per message and is slow for millions of occurrences. Benchmark against protobuf in milestone 4 and keep the format versioned.

**Why it scales**
- Reverse lookups use the summary. "Callers of X" loads only packages whose referenced-packages set contains X's package. That is not just importers, because embedded-method promotion lets a caller reach a symbol without importing its package.
- `implements` is computed on demand with a cache. Method sets are stored as canonical strings, so implementing an interface is a set-inclusion check, prefiltered through the method-name index. Nothing global goes stale.
- Stubs live inside the package that references them, so rewriting or deleting a package file discards them. A dependency version bump changes `input_hash` only for importers.
- Files include the input hash in their name. New files are written first, and the manifest swap is the commit. The server keeps reading old files until then. Old files are garbage-collected afterwards. Crashes leave the last consistent state.

**Dependency stubs:** package path, module@version, kind, name and signature (free from `go/types`), plus every file:line in your code that references it.

## 7. Incremental indexing

Per-package `input_hash`:

```text
H( sorted(file path + sha256)        // GoFiles, CgoFiles, CFiles, SFiles, EmbedFiles
 , go version, GOOS, GOARCH, tags, CGO_ENABLED
 , module path + version, go.mod/go.sum/go.work hashes
 , indexer schema version
 , sorted(direct imports' api_hash) )
```

Algorithm:
1. **Detect:** walk the tree with an mtime+size fast path and a hash fallback. Optionally use `git diff --name-status` to narrow candidates. Git is a shortcut, never the source of truth. Changes to go.mod, go.work, go.sum, `vendor/modules.txt` or the build config dirty the affected module (or everything).
2. **Metadata load:** `go/packages` without types gives the current package graph. Delete files for packages that vanished.
3. **Seed:** packages whose recomputed `input_hash` differs from the stored one.
4. **Process in topological order.** For each dirty package: load it (only the root from source), extract, write the package file, and compute the new `api_hash`. Unchanged `api_hash` means dependents are not dirtied (early cutoff). Changed means direct importers are dirtied.
5. **Commit:** update the summary, then swap the manifest.
6. **Garbage collect** superseded package files.

A v1 shortcut, if needed first, is to invalidate the full reverse-transitive closure. It is correct but over-indexes. The early cutoff is its own milestone.

Special cases:
- **Test variants** (`p`, `p [p.test]`, `p_test`, `p.test`): index `p [p.test]` as canonical, skip the synthesized `p.test`, own each file once, and strip the suffix from keys.
- **Type errors:** the package is indexed with whatever `types.Info` has and flagged `has_errors`, with diagnostics recorded.
- **Generated and cgo files:** flagged via `ast.IsGenerated`; use `fset.PositionFor(pos, false)` to bypass `//line` adjustments.
- **Renames:** no rename detection. A rename is delete plus add, and dependents are re-resolved through the `api_hash` change.
- **Excluded files:** files excluded by build tags are recorded with `excluded_by_build_tags` so the UI can say they are not indexed.

## 8. Server and API

- Binds to `127.0.0.1` only. **Validates Host and Origin headers** to block DNS-rebinding reads of your source. Serves only files listed in the manifest.
- Single binary with the React build embedded via `go:embed`. In development, the Vite dev server proxies to the Go server.

| Endpoint | Purpose |
|---|---|
| `/api/graph/packages` | Level 0 slice |
| `/api/graph/package?key=` | Level 1 slice |
| `/api/graph/function?key=` | Level 2 slice |
| `/api/symbol?key=` | Sidebar data |
| `/api/source?file=&range=` | Snippet with line highlight, plus a staleness flag (file hash vs manifest) |

Symbol search across a large repo needs its own sorted name index. It comes after the first server milestone.

## 9. UI (Vite, React, TypeScript, React Flow, elkjs)

- **Level 0:** project packages with import edges, grouped by directory. Externals are collapsed by default.
- **Interaction:** click fills the sidebar. Double-click drills in. Breadcrumbs go back up.
- **Level 1:** "this package" (functions, types, variables) and "imported" (grouped per package, showing only what is used). Edges are aggregated, with per-kind filters.
- **Level 2:** the function view.
  - Signature types
  - Locals with types
  - Called functions, labeled `static`, `interface` or `dynamic`
  - Constructed types
  - Globals and fields touched
  - Nested closures
  - Edges carry a source-order number, and the UI says this is source order, not execution order.
- **Variable panel:** ordered event list (declaration, reads, writes, argument uses) with loop, branch and closure flags, plus one-hop links such as `x -> f(x)`.
- **Layout:** elkjs in a web worker, with slices loaded lazily. Never send the whole codebase.
- Tests, generated code and externals have toggles and are visually marked when shown.

## 10. Version 1 scope

**Included:** everything in §4–§9, one build config, tests included, external stubs, `implements` for non-generic interfaces and named types, diagnostics recorded, and CLI queries for:
- definition
- references
- callers
- callees
- reads and writes
- types used
- implements
- call chain
- variable events

**Not in v1:** SSA and control-flow ordering, whole-program call graph, interprocedural data flow, multiple build configs, framework route detection (e.g. "POST /users handler"), watch mode, symbol-level invalidation, generic interfaces in `implements`.

## 11. Static-analysis limits

The UI and CLI must show these as unknown, never as guesses:
- reflection, `unsafe`, `go:linkname`, plugins, cgo boundaries
- which concrete method an interface call reaches (candidates come from `implements` only)
- which function a variable holds when it comes from a map, channel or field
- aliasing through pointers, slices and maps (`*p = v` writes to something unknown)
- whether a pointer-receiver call mutates its receiver (only `address_taken` is recorded)
- real execution order (v1 is source order), goroutine interleaving, actual object lifetime (only lexical scope is recorded)
- anything from `go:generate` that has not been run

## 12. Version 2

- Intraprocedural SSA via `buildssa`/`ssautil` per package, with `ssa.GlobalDebug` to keep variable names. Gives CFG-based ordering, reaching definitions and real def-use chains.
- Call graph via `go/callgraph` (`vta` best, `rta` with entry points, `cha` baseline), stored as `may_call` edges with `algorithm` and `confidence`. This is whole-program and batch, not incremental.
- Interprocedural value flow over static callees, then field sensitivity.
- Symbol-level invalidation, multiple build configs, watch mode with `packages.Config.Overlay`, generic interfaces in `implements`.
- Framework plugins (route registration, DI). Optional `go/analysis` findings.
- Only after that, a semantic or AI layer.

## 13. Milestones

Indexer and CLI first, then the UI.

1. **Spike:** load a module with `go/packages` (`Tests: true`), confirm the test variants and partial-reload behaviour. *Done when* the package IDs, files, errors and deps are printed for a fixture module.
2. **Model and keys:** `Key(types.Object)` table-tested for generics, shadowing, closures, aliases and multiple `init`.
3. **Extractor:** structure first, then each role, driven by txtar golden fixtures, including the `CreateUser` and `foo` examples from the original brief.
4. **File format:** writer, reader, checksum, version; benchmark against protobuf; round-trip tests.
5. **Incremental engine:** manifest, planner, atomic commit, `api_hash` cutoff. Write the invariant test (incremental equals from-scratch after a sequence of edits: body change, signature change, add/delete/rename file, build-tag flip, go.mod edit) **before** optimizing.
6. **CLI queries:** the list in §10.
7. **Hardening:** type errors, generated files, cgo, workspaces, vendoring, excluded files; cross-check definitions and references against `scip-go` and investigate every difference.
8. **Server:** API, snippet with staleness check, embedded assets, Host/Origin validation.
9. **React app:** level 0, level 1, level 2, then the sidebar and variable panel.
10. **Search index and `serve --watch`.**
11. **Performance pass** on large real repositories. Set measurable targets (no-op re-index, single-file change) after a baseline measurement.

Suggested layout: `cmd/codeg`, `internal/loader`, `internal/extract`, `internal/model`, `internal/index` (format, reader, writer), `internal/plan`, `internal/query`, `internal/server`, `web/` (React app).

## 14. Risks and defaults

- **`go` must be on PATH and dependency sources must be in the module cache.** Type-checking needs them, and cgo needs a C compiler. `codeg index` must report clear errors for both.
- **Go version:** target a current release (alias and `Origin()` behaviour depend on 1.23+ semantics).
- **Assumed defaults, not yet confirmed:** binary name `codeg`, Windows paths normalized to forward slashes in keys, Vite dev server proxying to the Go server during UI development.
- **Open:** symbol-search index design (after the first server milestone), and the final binary layout (decided by the milestone 4 benchmark).
