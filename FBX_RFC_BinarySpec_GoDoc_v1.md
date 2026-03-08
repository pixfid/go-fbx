# RFC: FBX v1 — FictionBook eXtended Container Format
**Category:** Informational  
**Status:** Draft  
**Version:** 1.0  
**Last-Updated:** 2026-03-09  
**Intended Use:** Storage and manipulation of very large e-book collections (FB2 and assets) with fast random access and append-only updates.

---

## Abstract

This document specifies **FBX v1**, a binary container format designed as a practical replacement for storing large FB2 libraries in ZIP archives, particularly when archives reach **multi‑gigabyte sizes (5+ GB)** and frequent updates (add/replace/remove) become costly due to repacking.

FBX v1 supports:

- O(1) open: read a fixed header plus a directory blob without scanning the container
- Random access to individual files via chunk descriptors
- Append-only modifications: add/replace/remove are implemented by appending new data and a new directory
- Efficient mass operations: apply many changes and commit once
- Integrity checks: CRC32 for directory and per-chunk raw data
- Offline compaction (GC): `pack` builds a new compact container from live entries

FBX v1 deliberately avoids complexities such as encryption, deduplication, and full-text indexing, reserving room for future versions.

Current `go-fbx` also defines an incompatible **v1 extension profile** (still
`Header.version = 1`) for stronger commit/recovery semantics and lazy directory
parsing. See:
- `docs/V1_EXTENSION_SPEC.md`
- `docs/MIGRATION.md`

---

## Table of Contents

1. Introduction  
2. Conventions and Terminology  
3. Design Goals and Non-goals  
4. Container Overview  
5. Data Types and Encoding Rules  
6. FBX v1 File Format  
   - 6.1 Header (fixed)  
   - 6.2 Chunk Record (data block)  
   - 6.3 Directory (catalog)  
7. Path Rules  
8. Metadata Format  
9. Reader Requirements  
10. Writer Requirements  
11. Algorithms (Open, Extract, Add, Replace, Remove, Batch Commit)  
12. Mass Operations  
13. Compaction (`pack`)  
14. Verification (`verify`)  
15. Error Handling and Robustness  
16. Security Considerations  
17. Compatibility and Extensibility  
18. Reference Go Library Architecture  
19. README (for repository users)  
20. GoDoc-Style API Documentation  
21. Appendix A: Canonical Test Vectors (recommended)  
22. Appendix B: Suggested MIME Types and Conventions  

---

## 1. Introduction

ZIP is a widely supported archive format but becomes expensive for **very large libraries** when changes are frequent. Typical workflows (updating a few books, removing a subset, adding new content) can require rewriting large portions of the archive, or at least rewriting central directory structures, resulting in long operations and high I/O amplification.

FBX v1 addresses this by using:

- **Chunked storage:** each file is stored as independent compressed chunks.
- **Append-only updates:** new chunks and a new directory are appended; old data remains until compaction.
- **Single active directory pointer:** the header points to the latest directory, making open time constant.

This document defines a complete binary specification suitable for building interoperable implementations.

---

## 2. Conventions and Terminology

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** are to be interpreted as described in RFC 2119.

**Terms:**

- **Container:** The FBX file as stored on disk.
- **Entry:** A logical file inside the container (e.g., `book.fb2`, `img/cover.jpg`).
- **Chunk:** A raw (uncompressed) slice of an entry’s content.
- **Chunk Record:** On-disk record storing one compressed chunk.
- **ChunkRef:** A directory descriptor that points to a chunk record.
- **Directory:** A catalog listing all live entries and their chunk references.
- **Active Directory:** The directory referenced by the header’s `dir_offset` and `dir_size`.

---

## 3. Design Goals and Non-goals

### 3.1 Goals

1. **Fast open:** O(1) open regardless of container size.
2. **Fast access:** Extract a single entry without processing unrelated data.
3. **Efficient updates:** Add/replace/remove without rewriting existing data.
4. **Mass operations:** Support thousands of operations with a single directory commit.
5. **Integrity:** Detect corruption via CRC32 checks.
6. **Simplicity:** Small set of structures; minimal parsing complexity.

### 3.2 Non-goals (v1)

- Encryption
- Deduplication
- Full-text search index
- Inline patching of compressed streams
- Cross-platform permissions fidelity (mode stored but optional semantics)

---

## 4. Container Overview

An FBX container is a single file with:

- A **fixed-size header** at offset 0.
- A variable-size **data region** containing chunk records and old directories.
- The **active directory** blob (typically appended to the end).

Append-only mutation means a container grows over time. Removing data is logical until a later compaction (`pack`) rebuilds a smaller container.

---

## 5. Data Types and Encoding Rules

### 5.1 Endianness

All integer fields are **little-endian**.

### 5.2 Primitive Types

| Name | Size | Description |
|---|---:|---|
| u8  | 1 | Unsigned 8-bit integer |
| u16 | 2 | Unsigned 16-bit integer (LE) |
| u32 | 4 | Unsigned 32-bit integer (LE) |
| u64 | 8 | Unsigned 64-bit integer (LE) |
| bytes[N] | N | N raw bytes |

### 5.3 Variable-length byte arrays and strings

FBX v1 uses explicit lengths (no terminators):

- `varbytes`: `u32 length` followed by `length` bytes.
- `string`: `u32 length` followed by UTF‑8 bytes.

### 5.4 CRC32

CRC32 uses the IEEE polynomial (the same as Go’s `crc32.IEEE`).

### 5.5 Absolute offsets

All offsets are absolute from file start (byte 0). Implementations MUST use 64-bit math for offsets and sizes.

---

## 6. FBX v1 File Format (Normative)

This section is normative: compliant readers/writers MUST follow it.

### 6.0 Global constants

- **Magic (header):** `FBXC`
- **Header size:** 128 bytes
- **Version:** 1
- **Directory magic:** `DIR1`
- **Directory footer magic:** `END1`
- **Chunk record magic:** `CK`

### 6.1 Header (fixed, 128 bytes)

**Location:** offset 0  
**Size:** exactly 128 bytes

#### 6.1.1 Layout

```
struct HeaderV1 {
  u8   magic[4];        // "FBXC"
  u16  version;         // 0x0001
  u16  header_size;     // 0x0080 (128)
  u32  flags;           // bitmask
  u8   uuid[16];        // opaque
  u64  created_unix;    // seconds since Unix epoch
  u64  dir_offset;      // absolute offset of active Directory
  u64  dir_size;        // byte length of active Directory blob
  u32  dir_crc32;       // CRC32 of active Directory blob (same as footer.crc32)
  u64  journal_offset;  // 0 in v1 unless journal is implemented
  u64  journal_size;    // 0 in v1 unless journal is implemented
  u8   reserved[56];    // v1 baseline: zero; readers MUST ignore contents (see extension profile in §17)
}
```

#### 6.1.2 Validation rules

A reader MUST validate:

- `magic == "FBXC"`
- `version == 1`
- `header_size == 128`
- `dir_offset > 0`, `dir_size > 0` (unless empty container rules used by implementation)
- `dir_offset + dir_size` MUST be within file bounds
- It SHOULD validate `dir_crc32` against the directory blob.

#### 6.1.3 Flags

`flags` is a bitmask (u32). Unrecognized bits MUST be ignored by readers.

| Bit | Name | Meaning |
|---:|---|---|
| 0 | HAS_JOURNAL | Journal region is present and may be used for safer commits |
| 1 | HAS_BACKUP | Fixed backup header semantics are enabled (extension profile) |
| 2 | HAS_DIR_INDEX | `IDX1` directory index is required for open (extension profile) |
| 3 | HAS_REQUIRED_FEATURES | required-feature mask in `reserved[52:56]` is active (extension profile) |
| 4..31 | RESERVED | MUST be 0 unless an extension profile defines them |

> Note: The base append-only commit procedure in this document is safe without journal (see §11.6). Journal support is OPTIONAL.

---

### 6.2 Chunk Record (data block)

Chunk records store compressed or stored raw data for entry chunks.

**Location:** anywhere in file (typically appended)  
**Size:** variable (`16 + comp_size` bytes)

#### 6.2.1 Layout

```
struct ChunkRecordV1 {
  u8   magic[2];       // "CK"
  u8   codec;          // 0=STORE, 1=ZSTD, 2=LZ4
  u8   level;          // codec-specific; STORE=0
  u32  raw_size;       // size of uncompressed data
  u32  comp_size;      // payload length
  u32  crc32_raw;      // CRC32 of uncompressed data
  u8   payload[comp_size];
}
```

#### 6.2.2 Codec IDs

| codec | Name | Notes |
|---:|---|---|
| 0 | STORE | payload is raw bytes; `comp_size == raw_size` SHOULD hold |
| 1 | ZSTD | payload is Zstandard frame |
| 2 | LZ4 | payload is LZ4 block/frame as defined by implementation profile |

**Compliance:** Readers MUST support STORE. Support for ZSTD and LZ4 is RECOMMENDED.

#### 6.2.3 Validation rules

A reader MUST validate:

- `magic == "CK"`
- `raw_size > 0`
- `comp_size > 0`
- `chunk_offset + header+payload` within file bounds
- After decoding, computed CRC32(raw) MUST equal `crc32_raw`

A reader SHOULD validate that `raw_size`, `comp_size`, `crc32_raw` match the corresponding `ChunkRef` fields (see §6.3.3).

---

### 6.3 Directory (catalog)

The directory contains the list of all live entries and their chunk maps.

**Location:** `HeaderV1.dir_offset`  
**Size:** `HeaderV1.dir_size` bytes  
**Integrity:** CRC32 in both header and footer

#### 6.3.1 Directory Header layout

```
struct DirectoryV1 {
  u8   magic[4];        // "DIR1"
  u32  entry_count;     // number of entries following
  u32  flags;           // MUST be 0 in v1
  u64  build_unix;      // directory build time (unix seconds)
  // followed immediately by entry_count EntryV1 records (variable sized)
  // followed by DirectoryFooterV1
}
```

Readers MUST validate:

- `magic == "DIR1"`
- `flags == 0` (or ignore unknown bits if future use; v1 writers MUST write 0)
- `entry_count` is consistent with parsing and file bounds

#### 6.3.2 EntryV1 layout (variable size)

Each entry describes a file stored in the container.

```
struct EntryV1 {
  u64  path_hash64;     // FNV-1a 64 of UTF-8 path
  u64  mtime_unix;      // modification time (unix seconds)
  u32  mode;            // unix mode; MAY be 0
  u32  entry_flags;     // bitmask
  u64  file_size;       // total uncompressed size
  u32  chunk_count;     // number of ChunkRefV1 records
  u32  meta_size;       // length of meta blob
  u32  path_size;       // length of UTF-8 path bytes
  ChunkRefV1 chunks[chunk_count];
  u8   meta[meta_size];
  u8   path[path_size];
}
```

##### 6.3.2.1 Entry flags

`entry_flags` is a u32 bitmask. v1 defines:

| Bit | Name | Meaning |
|---:|---|---|
| 0 | IS_BINARY | Content is binary |
| 1 | IS_TEXT | Content is text |
| 2..31 | RESERVED | MUST be 0 for v1 writers; readers ignore unknown bits |

> `IS_BINARY` and `IS_TEXT` are hints only. A file may set neither or both, though writers SHOULD set exactly one when possible.

##### 6.3.2.2 Meta blob

`meta` is an opaque byte blob. v1 RECOMMENDS UTF-8 JSON but does not require it. Readers MUST NOT fail if meta is unknown.

##### 6.3.2.3 Path string

`path` is UTF-8 bytes of length `path_size` (no NUL). It MUST satisfy path rules (§7).

#### 6.3.3 ChunkRefV1 layout

Chunk references map an entry’s uncompressed file space to stored chunks.

```
struct ChunkRefV1 {
  u64  chunk_offset;    // absolute offset to 'C' of "CK"
  u64  raw_offset;      // offset within uncompressed entry
  u32  raw_size;        // expected uncompressed size of this chunk
  u32  comp_size;       // expected payload size
  u32  crc32_raw;       // expected CRC32 of raw
  u32  reserved;        // MUST be 0 in v1
}
```

##### 6.3.3.1 Required invariants

For each entry:

- `chunk_count >= 1` for non-empty files.
- `raw_offset` MUST be non-decreasing across chunks.
- Chunks MUST NOT overlap in raw space:
  - For consecutive chunks i and i+1: `raw_offset[i] + raw_size[i] <= raw_offset[i+1]`
- The last chunk MUST satisfy: `raw_offset_last + raw_size_last == file_size`
- Each ChunkRef SHOULD match ChunkRecord values (`raw_size`, `comp_size`, `crc32_raw`)

A reader MUST reject an entry whose chunk map violates invariants, as it may cause out-of-bounds or corrupted reads.

#### 6.3.4 Directory footer layout

```
struct DirectoryFooterV1 {
  u8   magic[4];        // "END1"
  u32  crc32;           // CRC32 over directory bytes from "DIR1" up to just before this footer
  u64  total_size;      // total directory size including footer
}
```

Readers MUST validate:

- `magic == "END1"`
- `total_size == HeaderV1.dir_size`
- `crc32 == HeaderV1.dir_crc32`
- computed CRC32 matches `crc32`

> CRC calculation region: all bytes starting at Directory magic `"DIR1"` and ending immediately before footer.magic `"END1"`.

---

## 7. Path Rules (Normative)

Entry paths MUST satisfy:

- UTF‑8
- Separator is `/`
- MUST NOT start with `/`
- MUST NOT contain `..` path traversal segments
- MUST NOT contain NUL byte
- SHOULD NOT contain backslash `\`
- Case-sensitive
- Empty path is invalid

Writers MUST normalize paths to a canonical form. Readers SHOULD reject invalid paths.

---

## 8. Metadata Format (Informative)

FBX v1 does not enforce a metadata schema. Recommended JSON fields for FB2 libraries:

```json
{
  "mime": "application/fb2+xml",
  "title": "…",
  "authors": [{"first":"…","last":"…"}],
  "lang": "ru",
  "series": {"name":"…","number": 1},
  "id": "…",
  "source": "…"
}
```

Recommended conventions:

- `mime` should be present for content classification.
- For images: `mime` such as `image/jpeg`, `image/png`.
- For indexes or internals: paths under `__index__/` or `__meta__/` (see Appendix B).

---

## 9. Reader Requirements (Normative)

A compliant reader:

1. MUST validate header magic/version/size.
2. MUST load the active directory from `(dir_offset, dir_size)`.
3. MUST validate directory CRC and footer.
4. MUST implement STORE codec (codec=0).
5. MUST reject entries with invalid chunk maps (overlap, out-of-range, file_size mismatch).
6. MUST verify chunk CRC32 when reading chunk data (unless an explicit “unsafe” mode exists).
7. SHOULD ignore unknown flags and unknown metadata.
8. SHOULD support ZSTD (codec=1) and LZ4 (codec=2).

---

## 10. Writer Requirements (Normative)

A compliant writer:

1. MUST write HeaderV1 with correct magic/version/header_size.
2. MUST write directory and footer with valid CRC.
3. MUST ensure all offsets/sizes are correct and within file bounds.
4. MUST enforce path rules and uniqueness:
   - within an active directory, each path MUST be unique.
5. MUST enforce chunk invariants:
   - non-overlapping, complete coverage to file_size.
6. SHOULD use append-only commit semantics (see §11.6).
7. SHOULD write reserved fields as zero in baseline v1.
8. MAY use reserved bytes for implementation-private hints if:
   - core format semantics are unchanged,
   - readers that ignore hints still work correctly,
   - and hint bytes are treated as advisory only.

---

## 11. Algorithms (Normative)

### 11.1 Open

1. Read 128 bytes at offset 0 → parse HeaderV1.
2. Validate header fields.
3. Read directory blob using `ReadAt(dir_offset, dir_size)`.
4. Validate DirectoryV1 + DirectoryFooterV1 and CRC.
5. Build in-memory index:
   - map path → entry reference
   - optionally map hash → candidate list (to speed lookups)

### 11.2 Extract(entryPath)

1. Locate EntryV1 by path.
2. For each ChunkRef (in ascending raw_offset):
   - Read ChunkRecordV1 at chunk_offset.
   - Validate and decode payload using codec.
   - Validate CRC32(raw).
   - Write raw to output sequentially.

### 11.3 Add(path, reader)

Add MUST fail if path already exists in active directory.

1. Normalize and validate path.
2. Choose chunking strategy (text/bin sizes).
3. Read from reader, split into chunks.
4. For each raw chunk:
   - Compress/store, compute CRC32(raw).
   - Append ChunkRecordV1 to end of file.
   - Record ChunkRefV1 with offsets and sizes.
5. Construct EntryV1 with chunk map, meta, and file_size.
6. Add EntryV1 to in-memory directory state.
7. Commit directory once (see §11.6).

### 11.4 Replace(path, reader)

Replace MUST fail if path does not exist. Semantics are equivalent to: remove old entry from directory + add new entry. Old chunks become garbage until pack.

### 11.5 Remove(path)

Remove is logical: remove the entry from the next directory snapshot and commit. No chunk data is modified.

### 11.6 Batch commit (append-only, journal-less)

This is the default safe update approach for very large files.

**Commit procedure:**
1. Serialize the new directory blob (DirectoryV1 entries + footer) to memory buffer.
2. Append the directory blob to end of container file → record `(new_dir_offset, new_dir_size)`.
3. If configured, `fsync` the file.
4. Update HeaderV1 fields (`dir_offset`, `dir_size`, `dir_crc32`) and write header at offset 0 using `WriteAt`.
5. If configured, `fsync` again.

**Crash safety:**
- If crash happens before step 4, header still points to previous directory: container remains consistent (last operation is simply not visible).
- If crash happens during step 4, header might be torn on some systems; for maximum safety, implementations MAY add a journal or a backup header in a future version. For practical use, the double-sync reduces risk, and writing only 128 bytes is typically safe on modern filesystems but not guaranteed by spec.

---

## 12. Mass Operations (Informative but strongly recommended)

Perform thousands of operations via a transaction object:

1. Load directory once.
2. Apply all operations in memory:
   - RemoveMany: delete entries by path/prefix/glob/filter
   - AddMany/UpsertMany: append chunks for each new file
3. Commit once (directory serialized once, header updated once).

This minimizes directory rewrite overhead and ensures predictable performance regardless of container size.

---

## 13. Compaction (`pack`) (Normative for tooling)

`pack` creates a new container that includes only “live” entries referenced in the active directory.

**Algorithm:**
1. Open source container and read active directory.
2. Create new container file with fresh header.
3. For each entry:
   - Stream-extract entry data from old container.
   - Write to new container using normal Add/Upsert pipeline.
4. Commit directory in new container.
5. Atomically replace old file (implementation-specific; usually temp+rename).

When compaction-hint counters are used (see §17), `pack` SHOULD reset them to zero in the newly created compacted container.

---

## 14. Verification (`verify`) (Normative for tooling)

Implementations SHOULD provide verification:

- Directory-only: validate header + directory CRC + structural invariants.
- Full: additionally validate all chunks by reading and checking CRC.

A reader MUST always validate directory CRC and SHOULD validate chunk CRC upon extraction.

---

## 15. Error Handling and Robustness

### 15.1 Mandatory checks

Readers MUST check:
- bounds for all offsets and sizes
- chunk map invariants
- CRC32 for directory and chunk raw data

### 15.2 Recommended behavior
- Provide error types that distinguish:
  - not found
  - already exists
  - invalid format
  - CRC mismatch
  - unsupported codec
  - invalid path
- Allow an “unsafe fast mode” ONLY if explicitly enabled, and document that it may return corrupted data.

---

## 16. Security Considerations

FBX containers may be untrusted input. Implementations MUST protect against:

- Path traversal: reject invalid paths, never write outside intended directory during extract.
- Zip-bomb-like expansion: raw_size may be large; enforce configurable limits.
- Memory exhaustion: avoid loading full entries to memory; stream chunks.
- CPU bombs: compression frames may be malicious; set decompressor limits/timeouts where possible.
- Integer overflow: use 64-bit checks for offset+size computations.

FBX v1 provides integrity (CRC32) but not cryptographic authenticity.

---

## 17. Compatibility and Extensibility

- Unknown header flags MUST be ignored by readers.
- Baseline v1 writers SHOULD keep reserved fields zero.
- Implementations MAY define private reserved-byte hints without version bump, as long as readers that ignore them remain fully compatible.
- `go-fbx` profile reserved-byte hints (HeaderV1.reserved):
  - `reserved[8:16]` (`u64`, little-endian): `dead_bytes` (estimated obsolete chunk bytes).
  - `reserved[16:24]` (`u64`, little-endian): `churn_ops` (replace/remove-like operations that created obsolete data).
  - these values are advisory; they MUST NOT affect correctness of parsing/extraction.
- `go-fbx` extension profile (same `Header.version = 1`) additionally defines:
  - strict journal + backup commit semantics (`HAS_JOURNAL`, `HAS_BACKUP`);
  - `IDX1` directory offsets/hash table for lazy parse (`HAS_DIR_INDEX`);
  - required feature bitmask contract (`HAS_REQUIRED_FEATURES`);
  - conversion/migration rules without payload rewrite.
- Extension profile details are specified in:
  - `docs/V1_EXTENSION_SPEC.md`
  - `docs/MIGRATION.md`
- Future versions may still add encryption/deduplication/indexing.
- Version changes MUST increment `HeaderV1.version`.

---

# 18. Reference Go Library Architecture (Non-normative)

The following sections define a complete architecture for a Go library implementing FBX v1.

---

## 18.1 Repository layout

```
github.com/you/fbx
  go.mod
  README.md
  fbx/
    container.go
    options.go
    errors.go
    verify.go
    pack.go
    tx.go
    iter.go
    textops.go
  internal/
    format/
      header.go
      directory.go
      entry.go
      chunkref.go
      crc.go
      fnv.go
      iohelpers.go
      validate.go
    chunk/
      record.go
      codecs.go
      zstd.go
      lz4.go
      store.go
    index/
      index.go
      normalize.go
    writer/
      appender.go
      pipeline.go
      fsync.go
    reader/
      file_reader.go
      chunk_reader.go
      stream.go
    pathutil/
      path.go
      glob.go
  cmd/
    fbx/
      main.go
      commands/
        list.go
        extract.go
        add.go
        upsert.go
        rm.go
        pack.go
        verify.go
        find.go
        replace_text.go
  tests/
    compat_test.go
    fuzz_test.go
```

---

## 18.2 Core design

### 18.2.1 `Container` responsibilities
- Owns `*os.File`
- Loads and validates header + directory
- Provides read operations (`List`, `Stat`, `OpenReader`, `Extract`)
- Provides write operations via `Tx` or “single-op wrappers” that internally create a Tx and commit

### 18.2.2 `Tx` responsibilities
- Holds a copy-on-write view of directory state
- Performs add/replace/remove operations in memory
- Appends chunk records to the same file
- Serializes and commits a new directory blob once

### 18.2.3 `internal/format`
- Pure parsing/encoding of on-disk structures
- Validation of invariants
- No filesystem access

### 18.2.4 `internal/chunk`
- Encoding/decoding chunk records
- Compression codecs

---

## 18.3 Public API surface (recommended)

### 18.3.1 Options

```go
package fbx

type Codec uint8

const (
  CodecStore Codec = 0
  CodecZstd  Codec = 1
  CodecLZ4   Codec = 2
)

type Options struct {
  // Chunking
  ChunkSizeText  int // default: 1<<20  (1 MiB)
  ChunkSizeBin   int // default: 4<<20  (4 MiB)
  DetectText     bool // heuristic for fb2/xml/json

  // Compression defaults
  DefaultCodec   Codec // default: CodecZstd
  DefaultLevel   int   // default: 1
  StoreIfAlreadyCompressed bool // true => store JPEG/PNG etc
  MaxWorkers     int   // default: runtime.GOMAXPROCS(0)

  // Safety/performance
  SyncOnCommit   bool // default: true for durability
  StrictVerify   bool // default: true
}
```

### 18.3.2 EntryInfo

```go
package fbx

type EntryInfo struct {
  Path      string
  Size      uint64
  MTimeUnix uint64
  Mode      uint32
  Flags     uint32
  Meta      []byte // recommended JSON, but opaque
}
```

### 18.3.3 Container API

```go
package fbx

type Container struct {
  // unexported fields
}

func Open(path string, opts *Options) (*Container, error)
func Create(path string, opts *Options) (*Container, error)
func (c *Container) Close() error

// Read
func (c *Container) List() Iterator[EntryInfo]
func (c *Container) Stat(path string) (EntryInfo, error)
func (c *Container) OpenReader(path string) (io.ReadCloser, error)
func (c *Container) Extract(path string, w io.Writer) error

// Convenience write ops (single op => Tx under the hood)
func (c *Container) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (c *Container) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (c *Container) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (c *Container) Remove(path string) error

// Batch
func (c *Container) Begin() *Tx

// Maintenance
func (c *Container) Verify(vopts *VerifyOptions) (*VerifyReport, error)
```

### 18.3.4 Tx API

```go
package fbx

type Tx struct {
  // unexported
}

func (tx *Tx) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (tx *Tx) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (tx *Tx) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error
func (tx *Tx) Remove(path string) error

func (tx *Tx) RemoveMany(paths []string) (removed int, err error)
func (tx *Tx) RemovePrefix(prefix string) (removed int, err error)
// optional: RemoveGlob(glob string), RemoveWhere(predicate)

func (tx *Tx) Commit() error
func (tx *Tx) Rollback()
```

### 18.3.5 Iterator

```go
package fbx

type Iterator[T any] interface {
  Next() bool
  Value() T
  Err() error
}
```

---

## 18.4 Internal pipelines (performance)

### 18.4.1 Streaming read (`OpenReader`)
- Uses `ReadAt` to fetch chunk records
- Decodes one chunk at a time
- Maintains a small buffer for the active chunk
- Verifies CRC32 by default

### 18.4.2 Streaming write with parallel compression
Recommended pipeline:

1. Chunker: reads input and emits raw chunks `(index, raw_offset, data)`
2. Workers: compress/store and compute CRC
3. Appender: writes chunk records in order; returns chunk offsets
4. Entry builder: constructs `EntryV1` with `ChunkRefV1[]`

Ordering is preserved by buffering out-of-order compressed results until the next expected index arrives.

---

## 18.5 Commit and crash safety
Default v1 commit sequence:

- Append new directory
- Sync
- Update header at offset 0
- Sync

Old directory remains valid until header update. This makes writes safe against partial operations (last commit may be lost, but container stays openable).

---

# 19. README (Repository-ready)

This section is intended to be copied verbatim into `README.md`.

---

## FBX — Fast container for huge FB2 libraries (Go)

FBX is a chunked, append-only container format designed to replace ZIP for very large e-book libraries (5+ GB). It enables fast random access to individual files and efficient updates without repacking the entire archive.

### Features

- **Fast open**: read header + directory only (no scanning)
- **Random access**: extract a single file by reading only its chunks
- **Append-only updates**: add/replace/remove by appending new data + new directory
- **Batch transactions**: mass updates commit once
- **Integrity**: CRC32 checks on directory and chunks
- **Compaction**: `pack` rebuilds a minimal container

### Install

```bash
go get github.com/you/fbx@latest
```

### Quick start

```go
package main

import (
  "os"
  "github.com/you/fbx/fbx"
)

func main() {
  c, err := fbx.Open("library.fbx", nil)
  if err != nil { panic(err) }
  defer c.Close()

  // read
  it := c.List()
  for it.Next() {
    e := it.Value()
    _ = e
  }
  if err := it.Err(); err != nil { panic(err) }

  // batch update
  tx := c.Begin()
  _ = tx.Remove("trash/old.fb2")

  f, _ := os.Open("new/book.fb2")
  defer f.Close()
  _ = tx.Upsert("books/book.fb2", f, []byte(`{"mime":"application/fb2+xml"}`), nil)

  if err := tx.Commit(); err != nil { panic(err) }
}
```

### CLI (optional)

If you build the CLI:

```bash
go install github.com/you/fbx/cmd/fbx@latest
```

Examples:

```bash
fbx list library.fbx
fbx extract library.fbx books/book.fb2 -o book.fb2
fbx rm library.fbx --prefix trash/
fbx upsert library.fbx new/book.fb2 --as books/book.fb2
fbx pack library.fbx -o library.packed.fbx
fbx verify library.fbx --mode dir
```

### Design notes

- Updates are append-only: old data remains until `pack`.
- For best performance on huge containers, use transactions (batch operations).
- CRC32 provides integrity but not cryptographic authenticity.

### License
MIT (recommended) or your preferred license.

---

# 20. GoDoc-Style API Documentation (Package-ready)

This section provides GoDoc-ready comments and recommended exported symbols.

> Put these comments directly above package/type/function declarations.

---

## 20.1 Package comment (`fbx/doc.go`)

```go
// Package fbx implements the FBX v1 container format: a chunked, append-only
// archive designed for very large libraries (multi-GB) with fast random access.
//
// FBX opens in O(1) time by reading a fixed header and a directory blob.
// Updates are append-only: add/replace/remove append new chunk records and
// a new directory, then atomically switch the active directory via header.
//
// Typical usage is through transactions (Tx) for mass updates.
//
// This package provides streaming extract and add operations, CRC integrity
// verification, and offline compaction (pack) via the Pack function.
package fbx
```

---

## 20.2 Errors (`fbx/errors.go`)

```go
// ErrNotFound is returned when an entry path does not exist in the active directory.
var ErrNotFound = errors.New("fbx: entry not found")

// ErrAlreadyExists is returned when adding an entry that already exists.
var ErrAlreadyExists = errors.New("fbx: entry already exists")

// ErrInvalidFormat indicates a structural violation of the FBX format.
var ErrInvalidFormat = errors.New("fbx: invalid format")

// ErrCRCMismatch indicates integrity check failure for a directory or chunk.
var ErrCRCMismatch = errors.New("fbx: crc mismatch")

// ErrUnsupportedCodec is returned when the container uses a codec not supported by the reader.
var ErrUnsupportedCodec = errors.New("fbx: unsupported codec")

// ErrPathInvalid is returned when an entry path violates FBX path rules.
var ErrPathInvalid = errors.New("fbx: invalid path")
```

---

## 20.3 Types and functions

### Codec

```go
// Codec identifies the compression algorithm used for chunk records.
type Codec uint8

const (
  // CodecStore stores raw bytes without compression.
  CodecStore Codec = 0

  // CodecZstd stores Zstandard-compressed bytes.
  CodecZstd Codec = 1

  // CodecLZ4 stores LZ4-compressed bytes.
  CodecLZ4 Codec = 2
)
```

### Options

```go
// Options configure container behavior for reading and writing.
// A nil *Options means defaults.
type Options struct {
  // ChunkSizeText is the target uncompressed chunk size for text-like entries.
  // Default: 1 MiB.
  ChunkSizeText int

  // ChunkSizeBin is the target uncompressed chunk size for binary entries.
  // Default: 4 MiB.
  ChunkSizeBin int

  // DetectText enables heuristic detection of text vs binary when writing.
  DetectText bool

  // DefaultCodec is used when WriteOptions does not override codec.
  DefaultCodec Codec

  // DefaultLevel is codec-specific compression level (e.g., zstd 1..22).
  DefaultLevel int

  // StoreIfAlreadyCompressed avoids recompressing likely-compressed formats
  // such as JPEG/PNG by using CodecStore.
  StoreIfAlreadyCompressed bool

  // MaxWorkers controls parallel compression workers. Default: GOMAXPROCS.
  MaxWorkers int

  // SyncOnCommit performs fsync after writing directory and after updating header.
  SyncOnCommit bool

  // StrictVerify controls whether readers verify chunk CRC32 on read by default.
  StrictVerify bool
}
```

### EntryInfo

```go
// EntryInfo describes a live entry in the active directory.
type EntryInfo struct {
  Path      string
  Size      uint64
  MTimeUnix uint64
  Mode      uint32
  Flags     uint32
  Meta      []byte
}
```

### Container

```go
// Container represents an opened FBX container.
// It is safe for concurrent reading operations; writing requires a transaction.
type Container struct {
  // unexported
}

// Open opens an existing FBX container from a filesystem path.
func Open(path string, opts *Options) (*Container, error)

// Create creates a new FBX container at path, truncating if it already exists.
// Implementations SHOULD use temp+rename for atomic create in tooling.
func Create(path string, opts *Options) (*Container, error)

// Close closes the underlying file.
func (c *Container) Close() error

// List returns an iterator over live entries.
func (c *Container) List() Iterator[EntryInfo]

// Stat returns metadata for the given entry path.
func (c *Container) Stat(path string) (EntryInfo, error)

// OpenReader returns a streaming reader for the entry content.
func (c *Container) OpenReader(path string) (io.ReadCloser, error)

// Extract streams the entry content into w.
func (c *Container) Extract(path string, w io.Writer) error

// Begin starts a new transaction for batch updates.
// The returned Tx MUST NOT be used concurrently.
func (c *Container) Begin() *Tx
```

### Tx

```go
// Tx is a batch update transaction. It appends new chunk records to the container
// and commits changes by writing a single new directory and updating the header.
//
// Tx is not safe for concurrent use.
type Tx struct {
  // unexported
}

// Add adds a new entry. It fails if the path already exists.
func (tx *Tx) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Upsert adds or replaces an entry.
func (tx *Tx) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Replace replaces an existing entry. It fails if the path does not exist.
func (tx *Tx) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Remove deletes an entry logically (it will not appear in the next directory snapshot).
func (tx *Tx) Remove(path string) error

// RemoveMany deletes multiple entries by exact paths.
func (tx *Tx) RemoveMany(paths []string) (removed int, err error)

// RemovePrefix deletes all entries whose paths have the given prefix.
func (tx *Tx) RemovePrefix(prefix string) (removed int, err error)

// Commit writes a new directory snapshot and updates the header.
func (tx *Tx) Commit() error

// Rollback discards in-memory changes. It does not rewind appended data.
func (tx *Tx) Rollback()
```

### Maintenance

```go
// VerifyOptions controls verification scope.
type VerifyOptions struct {
  Mode VerifyMode
}

// VerifyMode selects verification depth.
type VerifyMode int

const (
  VerifyDirectoryOnly VerifyMode = iota
  VerifySampledChunks
  VerifyAllChunks
)

// VerifyReport summarizes verification results.
type VerifyReport struct {
  EntriesChecked uint64
  ChunksChecked  uint64
  Errors         []error
}

// Verify validates directory CRC and structure, and optionally chunk CRCs.
func (c *Container) Verify(vopts *VerifyOptions) (*VerifyReport, error)

// PackOptions configures compaction.
type PackOptions struct {
  Codec     Codec
  Level     int
  ChunkText int
  ChunkBin  int
  Workers   int
  VerifyIn  bool
}

// Pack rebuilds a compact container containing only live entries.
func Pack(inPath, outPath string, opts *PackOptions) error
```

---

# 21. Appendix A: Canonical Test Vectors (Recommended)

To ensure interoperability, a project SHOULD ship test vectors:

1. Minimal container with one entry `book.fb2` stored as STORE with a single chunk.
2. Container with two entries and ZSTD chunks.
3. Container with replace operation (two directories, header points to latest).
4. Container with remove operation (entry absent from latest directory).
5. Corrupted CRC chunk to ensure reader detects `ErrCRCMismatch`.

Each test vector should include:
- container file
- expected directory listing
- expected extracted contents

---

# 22. Appendix B: Suggested MIME Types and Conventions

Recommended `mime` values for meta JSON:

- `application/fb2+xml` for FB2
- `image/jpeg`, `image/png`
- `application/json` for meta or indexes

Suggested internal namespaces:

- `__meta__/` for container metadata snapshots
- `__index__/` for optional search indexes (future)
- `__tmp__/` for tool-created temporary artifacts (avoid committing)

---

# 23. Appendix C: Container Binary Layout (Pseudo-graphics)

```text
FBX file (append-only)

+0x00000000  Primary HeaderV1 (128 bytes)
| magic[4]         = "FBXC"
| version          (u16)
| header_size      (u16, must be 128)
| flags            (u32)
| uuid[16]
| created_unix     (u64)
| dir_offset       (u64)
| dir_size         (u64)
| dir_crc32        (u32)
| journal_offset   (u64)
| journal_size     (u64)
| reserved[56]

+0x00000080  Fixed Backup Header slot (128 bytes, used by current writer layout)
| HeaderV1 mirror (same field layout)

+0x00000100..EOF   Append-only region
| [ChunkRecordV1]...
| [old DirectoryV1 blobs]...
| [new active DirectoryV1 blob]
| [IDX1 directory index blob]          (extension profile)
| [JNL1 header-snapshot record] (commit-time)
| [BKP1 header-snapshot record] (commit-time)

ChunkRecordV1 (16 + comp_size bytes)
+0x00  magic[2]    = "CK"
+0x02  codec       (u8: 0=store,1=zstd,2=lz4)
+0x03  level       (u8)
+0x04  raw_size    (u32)
+0x08  comp_size   (u32)
+0x0C  crc32_raw   (u32)
+0x10  payload[comp_size]

DirectoryV1 blob
+0x00  magic[4]    = "DIR1"
+0x04  entry_count (u32)
+0x08  flags       (u32, v1 writes 0)
+0x0C  build_unix  (u64)
+...   EntryV1[entry_count]
+...   Footer:
       magic[4]    = "END1"
       crc32       (u32)  // over bytes from DIR1 up to before END1
       total_size  (u64)  // full directory blob size including footer

EntryV1 (variable)
+0x00  path_hash64 (u64, FNV-1a)
+0x08  mtime_unix  (u64)
+0x10  mode        (u32)
+0x14  entry_flags (u32)
+0x18  file_size   (u64)
+0x20  chunk_count (u32)
+0x24  meta_size   (u32)
+0x28  path_size   (u32)
+0x2C  chunks[chunk_count] (ChunkRefV1)
+...   meta[meta_size]
+...   path[path_size] (UTF-8)

ChunkRefV1 (32 bytes)
+0x00  chunk_offset (u64)
+0x08  raw_offset   (u64)
+0x10  raw_size     (u32)
+0x14  comp_size    (u32)
+0x18  crc32_raw    (u32)
+0x1C  reserved     (u32, must be 0)

JNL1/BKP1 header snapshot record (148 bytes each)
+0x00  magic[4]        = "JNL1" or "BKP1"
+0x04  ts_unix         (u64)
+0x0C  header_bytes[128] (HeaderV1)
+0x8C  header_crc32    (u32)
+0x90  record_crc32    (u32) // over all previous record bytes
```

## End of document
