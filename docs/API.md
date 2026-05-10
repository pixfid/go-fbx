# API Guide (`go-fbx/fbx`)

## Core Types
- `Container`: opened or created FBX file.
- `Tx`: explicit transaction (`Begin`, `Commit`, `Rollback`).
- `Options`: container behavior (chunk sizing, limits, verification, workers).
- `WriteOptions`: per-entry write settings (codec, chunk size, metadata fields).
- `PackOptions`: options for `Pack` compaction.
- `ZIPImportOptions`: ZIP conversion options.

## Lifecycle
1. `Create(path, opts)` for new container, or `Open(path, opts)` for existing one.
2. Use high-level methods (`Add`, `Upsert`, `Replace`, `Remove`) or manual `Begin()` transaction.
3. Close with `Close()`.

## Minimal Example
```go
c, err := fbx.Create("books.fbx", &fbx.Options{MaxWorkers: 4})
if err != nil { panic(err) }
defer c.Close()

if err := c.Upsert("books/a.fb2", src, nil, &fbx.WriteOptions{Codec: fbx.CodecZstd}); err != nil {
    panic(err)
}

var out bytes.Buffer
if err := c.Extract("books/a.fb2", &out); err != nil {
    panic(err)
}
```

## Verification and Compaction
- `Verify(&fbx.VerifyOptions{Mode: ...})`:
  - `VerifyDirectoryOnly`
  - `VerifySampledChunks`
  - `VerifyAllChunks`
- `Pack(inPath, outPath, opts)` rebuilds a compact container.

## Metadata-Only Updates
- `SetMeta(path, meta)` updates one entry metadata payload without rewriting entry chunks.
- `SetMetaMany(metaByPath, ignoreMissing)` updates many entries in one commit (returns `updated`, `missing`).
- Transaction form is also available: `tx.SetMeta(...)`, `tx.SetMetaMany(...)`.

## Limits and Safety
- `Options.MaxEntrySize`: reject reads/writes above entry byte limit.
- `Options.MaxChunkSize`: reject oversized chunks on read; cap chunk size on write.
- `Options.StrictVerify` (default true): if false, payload CRC mismatch is tolerated during extraction.

## Error Handling
Use `errors.Is` against exported errors:
- `ErrNotFound`, `ErrAlreadyExists`, `ErrPathInvalid`
- `ErrInvalidFormat`, `ErrCRCMismatch`, `ErrUnsupportedFeature`, `ErrUnsupportedCodec`, `ErrLimitExceeded`

## ZIP Conversion
Use `ConvertZIPToFBX(zipPath, fbxPath, opts)` to import large ZIP archives with optional metadata (`--meta`, `MetaFile`) and progress callback.
