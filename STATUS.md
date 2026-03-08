# FBX RFC Implementation Status

Last updated: 2026-03-09

## Overall
- Implemented: core FBX v1 library + CLI + ZIP->FBX conversion + pack + verify + compatibility vectors + journal recovery + v1 extension profile (`IDX1`) + legacy migration.
- Test status: `go test ./...` passes.

## RFC Coverage Matrix

| RFC area | Status | Notes |
|---|---|---|
| §6 File Format (Header/Chunk/Directory) | Implemented | Binary layout, CRC checks, path hash, chunk invariants are enforced. |
| §7 Path Rules | Implemented | Normalization and validation in `internal/pathutil/path.go`. |
| §8 Metadata | Implemented | Opaque metadata bytes; JSON recommended and used by tooling. |
| §9 Reader Requirements | Implemented | STORE/ZSTD/LZ4 supported in all builds; CRC checks configurable via `StrictVerify`. |
| §10 Writer Requirements | Implemented | Append-only semantics, path uniqueness, invariant checks. |
| §11 Algorithms (Open/Extract/Add/Replace/Remove/Commit) | Implemented | Includes transactional commit flow. |
| §12 Mass Operations | Implemented | `RemoveMany`, `RemovePrefix`, `RemoveGlob`, `RemoveWhere`. |
| §13 Compaction (`pack`) | Implemented | In-place and out-of-place pack via API/CLI. |
| §14 Verification (`verify`) | Implemented | `dir`, `sample`, `all` modes in API/CLI. |
| v1 Extension profile (`HAS_DIR_INDEX`, required-features) | Implemented | `IDX1` commit/open path and header extension fields are active in writer/reader. |
| Legacy migration (`migrate`) | Implemented | Append-only metadata migration without payload re-encode; API + CLI coverage. |
| §15 Error handling | Implemented | Public typed errors (`ErrNotFound`, `ErrCRCMismatch`, etc.). |
| §16 Security considerations | Mostly implemented | Path validation, streaming I/O, configurable read/write limits (`MaxEntrySize`, `MaxChunkSize`), ZSTD decoder memory/window limits. |
| §18 Reference architecture/API/CLI | Mostly implemented | Core API and major CLI commands are present. |
| Appendix A canonical vectors | Implemented | Added in `tests/testdata/vectors/*` with compat test runner. |

## Journal & Recovery
- Implemented journal record append on commit (`JNL1`) with header snapshot + CRC.
- Implemented backup header record append on commit (`BKP1`) with header snapshot + CRC.
- Implemented fixed backup header slot at offset `128` (mirrored header copy).
- `Open()` attempts recovery from journal if primary header/directory is invalid.
- Recovery order: fixed backup header -> latest journal/backup records.

## Performance
- Benchmark suite added in `tests/bench_test.go`:
  - chunk encode benchmarks for STORE/ZSTD/LZ4
  - end-to-end container `Upsert+Extract` benchmark

## Current Practical Limitations
- ZSTD is pure-Go (`klauspost/compress`) and available without cgo.
- LZ4 is pure-Go (`pierrec/lz4/v4`) and available without cgo.
- Text detection and “already-compressed” heuristics are best-effort and extension/magic-byte based.
- Fixed backup slot is guaranteed for containers created with current layout; legacy containers still recover via journal/backup records.
