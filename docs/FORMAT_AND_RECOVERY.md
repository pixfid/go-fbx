# Format and Recovery

This implementation follows `FBX_RFC_BinarySpec_GoDoc_v1.md`.

Note: baseline `v1` is still documented here, but current `go-fbx` writers use the
incompatible `v1` extension profile (`HAS_JOURNAL` semantics, backup-header contract,
`IDX1` lazy directory index, required features contract).

See:
- [V1_EXTENSION_SPEC.md](./V1_EXTENSION_SPEC.md)
- [MIGRATION.md](./MIGRATION.md)

## On-Disk Model
- Header (`HDR1`) at offset `0`.
- Reserved fixed backup header slot at offset `128` (`HeaderSize`).
- Append-only chunk records (`CHK1`) with codec/raw/CRC metadata.
- Directory blob (`DIR1`) appended on commit and referenced by header (`DirOffset`, `DirSize`, `DirCRC32`).
- Compaction hints in header reserved bytes:
  - `reserved[8:16]` (`u64 LE`) = `dead_bytes` (estimated obsolete chunk bytes).
  - `reserved[16:24]` (`u64 LE`) = `churn_ops` (count of replace/remove-like operations).

Commits never rewrite existing chunk payloads. The latest header points to the newest directory snapshot.

## Transaction Commit Flow
1. Encode and append new directory blob.
2. Build and append `IDX1` for the active directory.
3. Build updated header (extension flags + reserved pointer fields).
4. Append `JNL1` journal record (header snapshot + CRC).
5. Append `BKP1` backup record (header snapshot + CRC).
6. Write fixed backup header (offset `128`) when slot is enabled.
7. Write primary header at offset `0`.

With `SyncOnCommit=true` (default), `fsync` is executed between critical stages.

## Recovery on Open
If primary header/directory validation fails:
1. Try fixed backup header slot.
2. Try scanning tail records (`JNL1`/`BKP1`) and pick latest valid by timestamp.
3. Scan file for valid `DIR1 ... END1` snapshots and synthesize a header from the newest valid directory.
4. Re-write recovered header to offset `0`.

If all recovery sources fail, `Open` returns `ErrInvalidFormat`.

## Codec Availability
- `store`: always supported.
- `zstd`: supported in all builds via `github.com/klauspost/compress/zstd` (pure Go).
- `lz4`: supported in all builds via `github.com/pierrec/lz4/v4` (pure Go).

## Security Controls
- Path normalization and traversal rejection (`internal/pathutil`).
- CRC verification for directory and chunk payloads.
- Configurable read/write limits: `MaxEntrySize`, `MaxChunkSize`.
