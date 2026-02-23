# CLI Reference

Binary entrypoint: `go run ./cmd/fbx` (or build `./cmd/fbx`).

## Command Index
- `convert-zip` — import ZIP into FBX.
- `pack` — rebuild container from live entries.
- `pack-many` — parallel repack for multiple FBX files.
- `add` / `upsert` / `replace` — write one file into container.
- `rm` / `find` — filter/remove by path and size predicates.
- `stat` / `info` / `list` — inspect container and entries.
- `set-meta` — rewrite one entry with new metadata.
- `replace-text` — bulk byte-string replacement in matching entries.
- `extract` — stream one entry out.
- `verify` — integrity validation.

## Global Semantics
- Exit codes:
  - `0` success
  - `1` runtime/data error
  - `2` CLI usage/argument error
- Paths inside container are normalized to `/` and must be valid FBX paths.
- Size flags are bytes.
- `--max-entry-size` and `--max-chunk-size` are safety bounds (`0` = unlimited).
- `--codec` values: `store|zstd|lz4`.
- `--level` default is `0`:
  - for `zstd`, `0` maps to fast mode (not zstd default level 3)
  - for `store`, ignored
  - for `lz4`, `0` = fast mode, `1..9` = higher-compression HC profile (values above `9` are treated as `9`)
- Progress bar redraw is one line: `[done/total] ▓▓▓...`.

## `convert-zip`
Syntax:
```bash
fbx convert-zip [flags] <input.zip> <output.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--meta auto\|none` | `auto` | `auto` writes generated metadata per ZIP entry; `none` disables auto metadata. | Disable metadata for minimal containers, or keep it for traceability. |
| `--meta-file <file.json>` | `""` | JSON map `path -> metadata object`; merged over auto metadata. | Inject custom metadata (IDs, titles, source tags). |
| `--prefix <p>` | `""` | Prepends path prefix inside FBX. | Place imported files under `books/`, `images/`, etc. |
| `--codec store\|zstd\|lz4` | `store` | Codec for newly written chunks. | Trade compression ratio vs speed. |
| `--level <n>` | `0` | Compression level passed to writer. | Tune zstd or lz4 HC compression. |
| `--progress` | `true` | Show one-line progress bar. | Observe long imports. |
| `--overwrite` | `false` | Allow replacing existing output file. | Controlled overwrite in scripts. |
| `--max-entry-size <bytes>` | `0` | Reject too-large entries while reading/writing. | Protect RAM/CPU on untrusted ZIP. |
| `--max-chunk-size <bytes>` | `0` | Cap/read-check chunk sizes. | Prevent oversized chunk attacks. |

## `pack`
Syntax:
```bash
fbx pack [flags] <input.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `-o <output.fbx>` | in-place | Output path. If omitted, rewrites input in place via temp+rename. | Keep original as source or compact in place. |
| `--codec store\|zstd\|lz4` | `store` | Rewrites all live entries with selected codec. | Change container-wide codec policy. |
| `--level <n>` | `0` | Compression level for rewritten chunks. | Control zstd/lz4 speed vs ratio tradeoff. |
| `--chunk-text <bytes>` | `0` | Text chunk size override during repack. | Optimize text extraction throughput. |
| `--chunk-bin <bytes>` | `0` | Binary chunk size override during repack. | Optimize binary chunking layout. |
| `--workers <n>` | `0` | Parallel compression workers (`0` = library default). | Speed up heavy recompression. |
| `--verify-in` | `true` | Verify input container before repack. | Catch corruption early. |
| `--fast` | `false` | Unsafe speed profile: disable CRC read checks during repack and disable output fsync on commit. | Max throughput for trusted data and disposable runs. |
| `--progress` | `true` | One-line repack progress bar. | Visibility for long repacks. |
| `--max-entry-size <bytes>` | `0` | Safety limit during read/write. | Harden repack for untrusted files. |
| `--max-chunk-size <bytes>` | `0` | Safety chunk bound. | Avoid processing oversized chunks. |

Skip optimization:
- For in-place `pack`, if all chunks already match requested `--codec` and `--level`, no `--chunk-text/--chunk-bin` overrides are set, and `dead_bytes==0`, CLI prints `skip` and does not rewrite file.

## `pack-many`
Syntax:
```bash
fbx pack-many [flags] <input1.fbx> [input2.fbx ...]
```

Runs `pack` in parallel across many containers (always in-place per file).

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--jobs <n>` | `GOMAXPROCS` | Number of files processed in parallel. | Scale throughput on multi-core systems. |
| `--glob <pattern>` | empty | Adds inputs by glob (`*.fbx`, `data/*.fbx`). | Batch-select files without listing each one. |
| `--codec store\|zstd\|lz4` | `store` | Repack codec for all inputs. | Apply one codec policy to many files. |
| `--level <n>` | `0` | Compression level for all inputs. | Batch tune speed/ratio. |
| `--chunk-text <bytes>` | `0` | Text chunk size override. | Unified text chunking across files. |
| `--chunk-bin <bytes>` | `0` | Binary chunk size override. | Unified binary chunking across files. |
| `--workers <n>` | `0` | Per-file compression workers. | Tune per-job CPU usage. |
| `--verify-in` | `true` | Verify each input before repack. | Early corruption detection in batch runs. |
| `--fast` | `false` | Unsafe fast profile per file (`StrictVerify=false`, `SyncOnCommit=false`). | Maximum throughput on trusted input sets. |
| `--max-entry-size <bytes>` | `0` | Safety entry bound per file. | Bound resource usage in bulk mode. |
| `--max-chunk-size <bytes>` | `0` | Safety chunk bound per file. | Bound decompression/chunk processing. |

Skip optimization:
- For each file, when codec/level already match, no chunk-size overrides are set, and `dead_bytes==0`, worker prints `SKIP` and does not repack that file.

Examples:
```bash
fbx pack-many --jobs 4 --codec zstd --level 10 --verify-in /data/*.fbx
fbx pack-many --glob '/data/*.fbx' --jobs 6 --codec zstd --level 10 --fast
```

## `add`, `upsert`, `replace`
Syntax:
```bash
fbx add [flags] <container.fbx> <source-file>
fbx upsert [flags] <container.fbx> <source-file>
fbx replace [flags] <container.fbx> <source-file>
```

Semantics:
- `add`: fail if entry exists.
- `upsert`: add or replace.
- `replace`: fail if entry does not exist.

Shared flags (`add`/`upsert`/`replace`):

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--as <entry/path>` | source basename | Entry path inside FBX. | Place file exactly where needed. |
| `--meta-json <json>` | empty | Inline metadata JSON. | Quick metadata injection from CLI. |
| `--meta-file <file.json>` | empty | Metadata JSON from file. | Reuse structured metadata payload. |
| `--keep-meta` | `true` | For `upsert`: if entry exists and no `--meta-*` provided, preserve existing metadata. | Update body without dropping metadata. |
| `--codec store\|zstd\|lz4` | `store` | Codec for this write. | Per-entry compression control. |
| `--level <n>` | `0` | Compression level. | Tune zstd/lz4 quality vs speed. |
| `--chunk-size <bytes>` | `0` | Force chunk size for this entry. | Override text/binary defaults. |
| `--max-entry-size <bytes>` | `0` | Open-time safety limit. | Guard against oversized input. |
| `--max-chunk-size <bytes>` | `0` | Open/write safety chunk bound. | Bound chunk processing. |

Extra `replace` flag:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--keep-meta` | `true` | If no new meta is given, preserve current entry metadata. | Replace content without losing metadata. |

## `rm`
Syntax:
```bash
fbx rm [flags] <container.fbx> [entry ...]
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--prefix <p>` | empty | Remove entries by path prefix. | Bulk-delete directory subtree. |
| `--glob <pattern>` | empty | Remove entries matching glob. | Pattern-based cleanup. |
| `--contains <s>` | empty | Remove entries whose path contains substring. | Fast fuzzy filtering. |
| `--min-size <bytes>` | `0` | Remove entries with `size >= min-size` (inclusive). | Example: prune large blobs only. |
| `--max-size <bytes>` | `0` | Remove entries with `size <= max-size` (inclusive). | Example: remove tiny junk files. |

Selection logic:
- Explicit `[entry ...]`, `--prefix`, and `--glob` are unioned (removed if any matches).
- `--contains`, `--min-size`, `--max-size` form one predicate block (all specified conditions must pass).
- Predicate block is also unioned with other selectors.

Example from your question:
```bash
go run ./cmd/fbx rm --contains books/ --min-size 1024 books.fbx
```
Meaning: remove entries whose path contains `books/` **and** size is at least `1024` bytes.

## `find`
Syntax:
```bash
fbx find [flags] <container.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--prefix <p>` | empty | Keep paths starting with prefix. | Narrow to namespace/subtree. |
| `--glob <pattern>` | empty | Keep paths matching glob. | Filename pattern filtering. |
| `--contains <s>` | empty | Keep paths containing substring. | Quick fuzzy lookup. |

Filter logic: all specified filters are AND-combined.

## `stat`
Syntax:
```bash
fbx stat [flags] <container.fbx> <entry-path>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--json` | `false` | Emit JSON object instead of key-value lines. | Script-friendly output. |
| `--max-entry-size <bytes>` | `0` | Safety limit while opening container. | Defensive reads. |
| `--max-chunk-size <bytes>` | `0` | Safety chunk bound while opening. | Defensive reads for untrusted data. |

Output fields: `path`, `size`, `mtime_unix`, `mode`, `flags`, `meta_size` (+ `meta` when valid JSON and `--json`).

## `info`
Syntax:
```bash
fbx info [flags] <container.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--json` | `false` | Emit full JSON report. | Parse with scripts/tools. |

What it reports:
- Entry/chunk totals.
- Codec summary: `store`, `zstd`, `lz4`, or `mixed(...)`.
- Level summary: single level (`0`, `3`, ...) or `mixed(...)`.
- Per-codec chunk counters.
- Per-level chunk counters (`chunks_level_<n>`).
- `dead_bytes`: estimated obsolete bytes left after replacements/removals.
- `churn_ops`: number of replace/remove-like operations that created obsolete bytes.
- `file_size`: current container size in bytes.

## `set-meta`
Syntax:
```bash
fbx set-meta [flags] <container.fbx> <entry-path>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--meta-json <json>` | empty | New metadata JSON payload. | Quick inline metadata update. |
| `--meta-file <file.json>` | empty | New metadata from file. | Reuse larger metadata blobs. |
| `--codec store\|zstd\|lz4` | `store` | Codec for rewritten entry body. | Re-encode while changing metadata. |
| `--level <n>` | `0` | Compression level for rewrite. | Tune zstd/lz4 rewrite ratio/speed. |
| `--chunk-size <bytes>` | `0` | Chunk size for rewritten entry. | Control rewrite chunking. |
| `--max-entry-size <bytes>` | `0` | Open-time safety limit. | Defensive operation on unknown files. |
| `--max-chunk-size <bytes>` | `0` | Open/write chunk safety bound. | Prevent oversized chunk processing. |

Note: `set-meta` rewrites the entry (content is read and written back), not just an in-place metadata patch.

## `replace-text`
Syntax:
```bash
fbx replace-text --find <old> --replace <new> [flags] <container.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--find <text>` | required | Byte sequence to search for. | Define replacement source. |
| `--replace <text>` | empty string | Replacement byte sequence. | Define replacement target. |
| `--prefix <p>` | empty | Limit by entry path prefix. | Restrict rewrite scope. |
| `--glob <pattern>` | empty | Limit by glob pattern. | Pattern-targeted rewrite. |
| `--dry-run` | `false` | Report matches without writing changes. | Safe preview before rewrite. |
| `--codec store\|zstd\|lz4` | `store` | Codec for rewritten entries. | Re-encode while replacing. |
| `--level <n>` | `0` | Compression level for rewritten entries. | zstd/lz4 tuning. |
| `--chunk-size <bytes>` | `0` | Chunk size for rewritten entries. | Control rewritten layout. |
| `--max-entry-size <bytes>` | `0` | Open-time safety limit. | Defensive processing. |
| `--max-chunk-size <bytes>` | `0` | Chunk safety bound. | Defensive processing. |

Output: `entries_changed=<n> replacements=<m>`.

Important: replacement is raw byte replacement; command currently buffers each matched entry in memory.

## `list`
Syntax:
```bash
fbx list <container.fbx>
```

Prints one line per entry: `<size>  <path>`.

## `extract`
Syntax:
```bash
fbx extract [flags] <container.fbx> <entry-path>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `-o <output>` | stdout | Write extracted data to file instead of stdout. | Save directly to disk. |
| `--max-entry-size <bytes>` | `0` | Open-time safety limit. | Defensive extraction. |
| `--max-chunk-size <bytes>` | `0` | Chunk safety bound. | Defensive extraction. |

## `verify`
Syntax:
```bash
fbx verify [flags] <container.fbx>
```

Flags:

| Flag | Default | What it does | Why use it |
|---|---:|---|---|
| `--mode dir\|sample\|all` | `dir` | Verification depth. | Trade speed vs confidence. |

Modes:
- `dir`: header + directory checks only.
- `sample`: directory + sampled chunks.
- `all`: directory + all chunks.

Output: `entries_checked=<n> chunks_checked=<n> errors=<n>`.
