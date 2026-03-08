# V1 Extension Spec (Journal Region, Backup Header, Directory Index)

Status: Implemented in `go-fbx` main branch (2026-03-09)

This document defines an incompatible extension of the existing `Version=1` container layout.  
The base header stays `HeaderV1` (`HeaderSize=128`, `Version=1`).

## Scope

The extension adds three mandatory capabilities:

1. Journal region with strict commit/recovery semantics (`HAS_JOURNAL`).
2. Reserved backup header semantics (primary + fixed backup slot).
3. Directory offset/hash index for lazy directory parsing.

This spec also defines append-only conversion without payload rewrite.

Operational migration runbook: [MIGRATION.md](./MIGRATION.md).

## Goals

- Keep append-only writes and multi-GB streaming behavior.
- Preserve user data losslessly during conversion:
  - chunk payload bytes are never recompressed/reencoded;
  - chunk references (`ChunkOffset`, `RawOffset`, `RawSize`, `CompSize`, `CRC32Raw`) are preserved.
- Fail fast on unsupported required extension features.

## Non-Goals

- Backward compatibility with pre-extension readers.
- Introducing a new format version.

## HeaderV1 Extension Contract

`HeaderV1.Flags` adds:

- `bit 0`: `HAS_JOURNAL` (already used in baseline).
- `bit 1`: `HAS_BACKUP` (already used in baseline).
- `bit 2`: `HAS_DIR_INDEX` (new; `IDX1` is required for open).
- `bit 3`: `HAS_REQUIRED_FEATURES` (new; required feature mask must be validated).

`HeaderV1.Reserved[56]` layout (little-endian):

- `reserved[0]` (`u8`): layout marker, value `0x02` for this spec.
- `reserved[1]` (`u8`): layout minor version, starts from `0`.
- `reserved[8:16]` (`u64`): `dead_bytes` (kept from baseline compaction hints).
- `reserved[16:24]` (`u64`): `churn_ops` (kept from baseline compaction hints).
- `reserved[24:32]` (`u64`): `generation` (monotonic commit generation).
- `reserved[32:40]` (`u64`): `idx_offset` (file offset of active `IDX1` blob).
- `reserved[40:48]` (`u64`): `idx_size` (byte size of active `IDX1` blob).
- `reserved[48:52]` (`u32`): `idx_crc32` (CRC32 of full `IDX1` blob).
- `reserved[52:56]` (`u32`): `required_features_low` (required feature bits 0..31).

`required_features_low` bits:

- `bit 0`: journal region semantics required.
- `bit 1`: fixed backup header semantics required.
- `bit 2`: directory index required.
- `bit 3`: lazy directory parsing semantics required.

Open MUST return `ErrUnsupportedFeature` if any required bit is unknown to the implementation.

## Directory Index Blob (`IDX1`)

`IDX1` is an append-only sidecar index for the active `DIR1` snapshot.

Header (`64` bytes):

- `0:4` magic: `"IDX1"`.
- `4:6` version (`u16`): `1`.
- `6:8` header size (`u16`): `64`.
- `8:12` flags (`u32`): currently `0`.
- `12:16` entry record size (`u32`): `24`.
- `16:20` hash range record size (`u32`): `16`.
- `24:32` generation (`u64`).
- `32:40` `dir_offset` (`u64`) of indexed `DIR1`.
- `40:48` `dir_size` (`u64`) of indexed `DIR1`.
- `48:52` `dir_crc32` (`u32`) of indexed `DIR1`.
- `52:56` `entry_count` (`u32`).
- `56:60` `hash_count` (`u32`).
- `60:64` header CRC32 (`u32`) over bytes `[0:60]`.

Body sections:

1. Entry offset table (`entry_count * 24`), sorted by `dir_entry_off`:
   - `0:4` `dir_entry_off` (`u32`) relative to start of `DIR1`.
   - `4:8` `dir_entry_size` (`u32`) bytes for this encoded entry.
   - `8:16` `path_hash64` (`u64`) (FNV-1a 64).
   - `16:20` `path_off` (`u32`) relative to start of `DIR1`.
   - `20:24` `path_size` (`u32`).
2. Hash range table (`hash_count * 16`), sorted by `path_hash64`:
   - `0:8` `path_hash64` (`u64`).
   - `8:12` `first_entry_idx` (`u32`) in entry table.
   - `12:16` `entry_span` (`u32`).

Validation rules:

- `IDX1` CRC from header pointer (`reserved[48:52]`) MUST match the full blob.
- `IDX1.(dir_offset, dir_size, dir_crc32)` MUST equal active header directory tuple.
- Every hash range MUST be in-bounds and non-overlapping.
- `path_off/path_size` and `dir_entry_off/dir_entry_size` MUST point inside the active `DIR1` blob.

## Journal Region Semantics (`HAS_JOURNAL`)

Record payload format remains the baseline fixed record (`JNL1`/`BKP1`) carrying full header bytes plus CRCs.

Semantics:

- A commit writes a pair with identical header bytes:
  - `JNL1`: intent record;
  - `BKP1`: commit record.
- A generation is considered committed only if a valid `BKP1` exists for it.
- On recovery, incomplete tail state (only `JNL1` without valid `BKP1`) MUST be ignored.
- `Header.JournalOffset` points to last `JNL1` offset for the active generation.
- `Header.JournalSize` stores the fixed record size.

## Backup Header Semantics (`HAS_BACKUP`)

- Fixed backup header slot is at offset `128` (one `HeaderSize` after primary).
- Writers MUST keep backup header byte-compatible with the active primary snapshot (same generation and pointers).
- Recovery order:
  1. validate primary header (`offset=0`);
  2. validate backup header (`offset=128`);
  3. validate journal commit candidates (`BKP1`) from tail scan;
  4. choose highest valid generation.

## Commit Protocol (Normative)

For generation `g+1`:

1. Append new `DIR1` snapshot.
2. Build and append `IDX1` for that `DIR1`.
3. Build new `HeaderV1`:
   - set `HAS_JOURNAL|HAS_BACKUP|HAS_DIR_INDEX|HAS_REQUIRED_FEATURES`;
   - update directory tuple;
   - update reserved generation/index/required-feature fields.
4. Append `JNL1` with the new header bytes.
5. Append `BKP1` with the same header bytes.
6. `fsync` (when `SyncOnCommit=true`).
7. Write backup header at offset `128`.
8. `fsync` (when `SyncOnCommit=true`).
9. Write primary header at offset `0`.
10. `fsync` (when `SyncOnCommit=true`).

## Open/Lazy Parse Behavior

When `HAS_DIR_INDEX` is set and `IDX1` validates:

- Open MAY skip full `DIR1` decode.
- Path lookup flow:
  1. hash path (`FNV-1a 64`);
  2. find hash range in `IDX1`;
  3. parse only candidate directory entries by offsets.

If `IDX1` is missing/corrupt while `HAS_DIR_INDEX` is set, open MUST fail with `ErrInvalidFormat` (or `ErrCRCMismatch` when applicable).

## Append-Only Conversion (Lossless User Data)

Conversion from legacy `v1` to this extension:

1. Preflight verify source snapshot (`dir|sample|all`).
2. Read active `DIR1` snapshot and build `IDX1` from it.
3. Append `IDX1` (no chunk rewrite).
4. Commit new header using the protocol above.
5. Optional post-verify (`all`).

Data-preservation guarantees:

- No payload chunk re-encoding.
- Entry path/meta/mtime/mode/flags preserved.
- Chunk refs preserved byte-for-byte in meaning.

Failure model:

- If conversion stops before primary-header write, previous snapshot remains authoritative.
- Torn conversion tails are recoverable by backup header + journal semantics.

## Implementation Mapping (`go-fbx`)

- API:
  - `fbx.Migrate(path string, opts *MigrateOptions) error`
- CLI:
  - `fbx migrate [--verify-source dir|sample|all] [--verify-target] [--dry-run] <container.fbx>`
- Writer behavior:
  - new commits append `DIR1` + `IDX1` + `JNL1` + `BKP1`, then update backup/primary headers.
