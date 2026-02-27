# go-fbx

`go-fbx` is a Go library and CLI for the FBX v1 binary container described in [`FBX_RFC_BinarySpec_GoDoc_v1.md`](FBX_RFC_BinarySpec_GoDoc_v1.md).

The project targets large FB2/FictionBook datasets and operational pipelines where containers are updated incrementally, verified regularly, and compacted offline.

## Why FBX Container

FBX in this implementation is designed around practical properties needed for very large corpora:

- Append-only mutation model:
  - writes append new chunks and a new directory snapshot;
  - old data stays intact until explicit compaction.
- Transactional commits (`Begin`/`Commit`/`Rollback`) with durable commit stages.
- Streaming I/O for both read and write paths (`io.Reader` / `io.Writer`), avoiding full in-memory entry buffering in normal paths.
- Built-in integrity checks:
  - directory CRC validation;
  - chunk CRC validation (strict mode by default).
- Recovery strategy on open:
  - primary header;
  - fixed backup header slot;
  - journal/backup tail records;
  - directory-scan fallback (`DIR1 ... END1`) for heavily damaged headers.
- Operational metadata controls:
  - per-entry metadata;
  - metadata-only batch updates (`set-meta-many`, API `SetMetaMany`) without payload rewrite.
- Offline compaction (`pack`) and parallel compaction (`pack-many`) for maintenance and codec migration.

## Key Advantages in Production

- Predictable failure mode under crashes: latest fully committed snapshot remains readable.
- Fast metadata migration path (no payload re-encode) for hundreds of thousands of entries.
- Strong observability for maintenance decisions via `info` metrics:
  - codec/level distribution;
  - `dead_bytes` and `churn_ops` compaction hints;
  - file size and chunk counts.
- Safety knobs for untrusted input:
  - `--max-entry-size` and `--max-chunk-size` in CLI;
  - `Options.MaxEntrySize` and `Options.MaxChunkSize` in API.

## Requirements

- Go `1.23+`.

## Build and Test

```bash
go build ./...
go test ./...
```

## Quick CLI Tour

### 1. Convert ZIP to FBX

```bash
go run ./cmd/fbx convert-zip \
  --codec zstd --level 3 \
  --meta auto --progress \
  books.zip books.fbx
```

### 2. Inspect and verify

```bash
go run ./cmd/fbx info books.fbx
go run ./cmd/fbx verify --mode all books.fbx
go run ./cmd/fbx list books.fbx
go run ./cmd/fbx stat --json books.fbx books/123.fb2
```

### 3. Extract one entry (stdout or file)

```bash
# stream to stdout
go run ./cmd/fbx extract books.fbx books/123.fb2 > /tmp/123.fb2

# write directly to file
go run ./cmd/fbx extract -o /tmp/123.fb2 books.fbx books/123.fb2
```

### 4. Write/replace entries

```bash
go run ./cmd/fbx add --as books/new.fb2 books.fbx ./new.fb2
go run ./cmd/fbx upsert --as books/new.fb2 books.fbx ./new-v2.fb2
go run ./cmd/fbx replace --as books/new.fb2 books.fbx ./new-v3.fb2
```

### 5. Metadata updates

#### Single entry (rewrites payload)

```bash
go run ./cmd/fbx set-meta \
  --meta-json '{"source":"import-a","book_id":123}' \
  books.fbx books/123.fb2
```

#### Batch metadata-only update (one commit, no payload rewrite)

`meta-map.json` format:

```json
{
  "books/123.fb2": {"book_id": 123, "lang": "ru"},
  "books/124.fb2": {"book_id": 124, "lang": "en"}
}
```

Run:

```bash
go run ./cmd/fbx set-meta-many \
  --meta-file meta-map.json \
  --ignore-missing \
  books.fbx
```

Output example:

```text
entries_updated=2 missing=0
```

### 6. Repack / codec migration / metadata cleanup

```bash
# migrate container to zstd
go run ./cmd/fbx pack --codec zstd --level 6 books.fbx

# drop metadata for all entries during repack
go run ./cmd/fbx pack --codec zstd --clear-meta books.fbx
```

### 7. Parallel repack across many containers

```bash
# explicit list
go run ./cmd/fbx pack-many --jobs 8 --codec zstd --level 6 /data/a.fbx /data/b.fbx

# by glob
go run ./cmd/fbx pack-many --glob '/data/*.fbx' --jobs 8 --codec zstd --level 6
```

### 8. Filter/remove and text patch workflows

```bash
# find by prefix/glob/substring
go run ./cmd/fbx find --prefix books/ --contains fb2 books.fbx

# remove selected entries
go run ./cmd/fbx rm --prefix books/old/ books.fbx

# bulk byte replacement in matching entries
go run ./cmd/fbx replace-text \
  --find old-domain.example --replace new-domain.example \
  --prefix books/ \
  books.fbx
```

## High-Volume Metadata Workflow (Recommended)

For scenarios like hundreds of containers and hundreds of thousands of entries:

1. Prepare one metadata map JSON per container (`path -> metadata JSON`).
2. Apply with `set-meta-many` per container.
3. Optionally run `verify --mode dir` or `verify --mode all` based on SLA.
4. Repack only when needed (`dead_bytes` growth, codec migration, or policy change).

This avoids per-entry payload rewrite and is typically much faster than repeated `set-meta`.

## Library Examples

### Basic write/read

```go
package main

import (
	"bytes"
	"os"

	"github.com/pixfid/go-fbx/fbx"
)

func main() {
	c, err := fbx.Create("books.fbx", &fbx.Options{MaxWorkers: 4})
	if err != nil {
		panic(err)
	}
	defer c.Close()

	if err := c.Upsert("books/a.fb2", bytes.NewReader([]byte("payload")), nil, &fbx.WriteOptions{Codec: fbx.CodecZstd, Level: 3}); err != nil {
		panic(err)
	}

	if err := c.Extract("books/a.fb2", os.Stdout); err != nil {
		panic(err)
	}
}
```

### Explicit transaction with multiple operations

```go
tx, err := c.Begin()
if err != nil {
	panic(err)
}
if err := tx.Add("books/a.fb2", srcA, metaA, nil); err != nil {
	tx.Rollback()
	panic(err)
}
if err := tx.Upsert("books/b.fb2", srcB, metaB, nil); err != nil {
	tx.Rollback()
	panic(err)
}
if err := tx.Commit(); err != nil {
	panic(err)
}
```

### Metadata-only batch update from API

```go
updated, missing, err := c.SetMetaMany(map[string][]byte{
	"books/123.fb2": []byte(`{"book_id":123}`),
	"books/124.fb2": []byte(`{"book_id":124}`),
}, true)
if err != nil {
	panic(err)
}
_ = updated
_ = missing
```

### Verification and compaction

```go
if _, err := c.Verify(&fbx.VerifyOptions{Mode: fbx.VerifyAllChunks}); err != nil {
	panic(err)
}
if err := fbx.Pack("books.fbx", "books-compacted.fbx", &fbx.PackOptions{Codec: fbx.CodecZstd, Level: 6, VerifyIn: true}); err != nil {
	panic(err)
}
```

## Safety and Integrity Notes

- `Options.StrictVerify` is enabled by default; chunk CRC mismatch returns an error during extraction.
- `FastUnsafe` modes in pack paths are intended only for trusted datasets and disposable runs.
- Metadata-only updates (`SetMeta`, `SetMetaMany`, `set-meta-many`) do not rewrite payload chunks.
- `set-meta` CLI rewrites entry payload by design; use `set-meta-many` for large-scale metadata migration.

## Documentation Map

- [docs/CLI.md](docs/CLI.md) - full command reference and examples.
- [docs/API.md](docs/API.md) - public Go API and options.
- [docs/FORMAT_AND_RECOVERY.md](docs/FORMAT_AND_RECOVERY.md) - on-disk model and recovery flow.
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) - tests, vectors, benchmarks, contribution workflow.
