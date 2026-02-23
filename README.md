# go-fbx

`go-fbx` is a Go library and CLI for the FBX v1 binary container described in `FBX_RFC_BinarySpec_GoDoc_v1.md`.
It is designed for large FB2/FictionBook archives and supports streaming reads/writes, transactions, verification, and repacking.

## Features
- Append-only container writes with transactional commit (`Begin`/`Commit`/`Rollback`).
- Codecs:
  - `store`: always available
  - `zstd`: pure Go (`github.com/klauspost/compress/zstd`), available with and without cgo
  - `lz4`: pure Go (`github.com/pierrec/lz4/v4`), available with and without cgo
- ZIP -> FBX conversion with optional metadata and one-line progress redraw.
- Verification modes: directory-only, sampled chunks, all chunks.
- Mass operations: remove by prefix/glob/predicate and `pack` compaction.
- CLI includes `add/upsert/replace`, `set-meta`, `stat`, `find/rm/replace-text`.
- Recovery path for broken primary header (fixed backup + journal/backup records).
- Read/write safety limits: `MaxEntrySize`, `MaxChunkSize`.

## Requirements
- Go `1.23+`.

## Quick Start (CLI)
```bash
go run ./cmd/fbx convert-zip --progress \
  --codec zstd --meta auto \
  f.fb2-712242-720343.zip books.fbx

go run ./cmd/fbx list books.fbx
go run ./cmd/fbx stat --json books.fbx books/123.fb2
go run ./cmd/fbx info books.fbx
go run ./cmd/fbx verify --mode all books.fbx
go run ./cmd/fbx extract -o sample.fb2 books.fbx books/123.fb2
go run ./cmd/fbx set-meta --meta-json '{"source":"flibusta"}' books.fbx books/123.fb2
go run ./cmd/fbx pack --codec zstd books.fbx
```

## Quick Start (Library)
```go
c, _ := fbx.Create("books.fbx", &fbx.Options{MaxWorkers: 4})
_ = c.Add("book.fb2", srcReader, nil, &fbx.WriteOptions{Codec: fbx.CodecZstd})
_ = c.Extract("book.fb2", dstWriter)
_ = c.Close()
```

## Documentation
- `docs/CLI.md` - command reference and examples.
- `docs/API.md` - public Go API and option semantics.
- `docs/FORMAT_AND_RECOVERY.md` - on-disk model, journal, and recovery flow.
- `docs/DEVELOPMENT.md` - tests, vectors, benchmarks, and contribution workflow.
- `AGENTS.md` - contributor guide for coding style and PR hygiene.
