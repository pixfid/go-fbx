# Migration to V1 Extension Layout

This document describes migration from legacy baseline `v1` containers to the
extended `v1` layout used by current `go-fbx` writers.

Related specs:
- [V1_EXTENSION_SPEC.md](./V1_EXTENSION_SPEC.md)
- [FORMAT_AND_RECOVERY.md](./FORMAT_AND_RECOVERY.md)

## Scope

Migration upgrades metadata layout and commit semantics while keeping the format
version (`Header.version = 1`).

Extension features enabled by migration:
- `HAS_JOURNAL`
- `HAS_BACKUP`
- `HAS_DIR_INDEX`
- `HAS_REQUIRED_FEATURES`

## Data Preservation Guarantees

Migration is append-only and lossless for user data:
- payload chunks are not recompressed and not rewritten;
- chunk references (`ChunkOffset`, `RawOffset`, `RawSize`, `CompSize`, `CRC32Raw`) are preserved;
- entry path, metadata, mode, flags, mtime are preserved.

## What Is Written

For legacy containers, migration writes one new commit snapshot:
1. appends a fresh `DIR1` snapshot from current live entries;
2. appends `IDX1` for that `DIR1` snapshot;
3. appends `JNL1` and `BKP1` header records;
4. updates fixed backup header slot (`offset=128`);
5. updates primary header (`offset=0`) with extension flags and reserved fields.

For already migrated containers, migration is idempotent and does not append new
bytes.

## CLI Usage

```bash
# preflight only (no write)
fbx migrate --dry-run --verify-source all library.fbx

# migrate with source preflight and target full verification
fbx migrate --verify-source dir --verify-target library.fbx
```

Exit behavior:
- success: `migration=ok`
- dry-run success: `migration_dry_run=ok`
- runtime/data failure: exit code `1`
- argument failure: exit code `2`

## API Usage

```go
err := fbx.Migrate("library.fbx", &fbx.MigrateOptions{
    VerifySource: fbx.VerifyDirectoryOnly,
    VerifyTarget: true,
})
```

Error classes:
- `ErrMigrationPreflightFailed`
- `ErrMigrationInterrupted`
- `ErrMigrationVerificationFailed`

Use `errors.Is` to branch on these classes.

## Failure Model

If migration is interrupted before the final primary-header write, the previous
committed snapshot remains authoritative and readable.

If migration completed but primary header later becomes corrupted, recovery may
use:
- fixed backup header (`offset=128`),
- `JNL1`/`BKP1` tail records,
- directory scan fallback.

## Recommended Rollout

1. Run `fbx migrate --dry-run --verify-source all` on a sample corpus.
2. Run real migration with `--verify-target` enabled.
3. Track `fbx info` metrics (`dead_bytes`, `churn_ops`, chunk/codec distribution).
4. Run offline compaction (`pack`) only when operationally required.
