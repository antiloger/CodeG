# Package file format

Decision (milestone 4): a **hand-defined binary layout**, not protobuf.
Section layouts are documented in `encode.go`; the container is in `coding.go`.
Format version: see `index.Version`. Bump it on any layout change; readers
reject other versions and the index engine re-indexes those packages.

## Benchmark (AMD Ryzen 3 7320U)

Largest real package, `net/http` (36,845 occurrences), against a protobuf
schema with the same string/key tables and index references
(`internal/pbbench`, real `protoc-gen-go` output). Both sides are fair: the
protobuf schema is not naive, and the protobuf encode figure includes building
its tables.

| | ours | protobuf |
|---|---|---|
| size, all 20 test packages | 3.17 MB | 4.94 MB (ours = 64%) |
| size with zstd on both | 1.33 MB | 2.06 MB |
| encode (incl. table building) | 27.6 ms, 29k allocs | 29.2 ms, 114k allocs |
| full decode into Go structs | 10.8 ms, 289 allocs | 16.6 ms, 127k allocs (messages only, strings unresolved) |
| open + occurrences of one symbol | 8.9 ms | 17.2 ms (must decode everything) |

Rerun: `go test ./internal/index -run XXX -bench .` and
`go test ./internal/index -run TestFormatSize -v`.

## Why not protobuf

Protobuf allocates one message per element (127k allocations to decode one
package) and has no way to read one symbol's occurrences without decoding all
of them. The hand-written layout is columnar, delta-coded, and carries the
by-symbol and caller/callee lookup arrays the query layer needs.

## Not done yet

- **Compression.** zstd would shrink the file a further 58%. The header has a
  reserved flags field for it; it is not enabled because it costs decode time
  and adds a dependency. Decide in the milestone 11 performance pass.
- **Partial occurrence decode.** A symbol query still decodes the whole
  occurrence section (about 9 ms for a package the size of net/http). If
  that matters, add block-level offsets to the occurrence columns rather
  than caching decoded readers first.
