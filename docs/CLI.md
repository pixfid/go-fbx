# CLI Reference

Binary entrypoint: `go run ./cmd/fbx` (or build `./cmd/fbx`).

## Global Notes
- Paths inside container are normalized to `/` separators.
- `--max-entry-size` and `--max-chunk-size` are byte limits (`0` = unlimited).
- Progress for ZIP conversion is redrawn in one line: `[done/total] ▓▓▓...`.

## Commands

### `convert-zip`
Convert ZIP archive into a new FBX container.

Example:
```bash
go run ./cmd/fbx convert-zip --meta auto --codec zstd --progress input.zip out.fbx
```

Key flags: `--meta auto|none`, `--meta-file`, `--prefix`, `--codec`, `--level`, `--overwrite`, `--max-entry-size`, `--max-chunk-size`.

### `pack`
Rebuild container with only live entries (in-place by default).

Example:
```bash
go run ./cmd/fbx pack --codec zstd --chunk-text 262144 --verify-in books.fbx
```

Key flags: `-o`, `--codec`, `--level`, `--chunk-text`, `--chunk-bin`, `--workers`, `--verify-in`, `--max-entry-size`, `--max-chunk-size`.

### `add` / `upsert` / `replace`
Write file content into an entry.

Example:
```bash
go run ./cmd/fbx add --as books/1.fb2 --codec lz4 books.fbx ./1.fb2
go run ./cmd/fbx replace --as books/1.fb2 --keep-meta books.fbx ./1.fb2
```

Key flags: `--as`, `--meta-json`, `--meta-file`, `--codec`, `--level`, `--chunk-size`, `--max-entry-size`, `--max-chunk-size`.
`replace` uses strict semantics: entry must already exist.

### `set-meta`
Update metadata of an existing entry while keeping content (entry is rewritten internally).

Example:
```bash
go run ./cmd/fbx set-meta --meta-json '{"author":"A"}' books.fbx books/1.fb2
```

Key flags: `--meta-json|--meta-file`, `--codec`, `--level`, `--chunk-size`, `--max-entry-size`, `--max-chunk-size`.

### `rm`, `find`, `replace-text`
- `rm`: delete entries by path/prefix/glob and predicate-like filters.
- `find`: filter paths by prefix/glob/substring.
- `replace-text`: bulk text replacement in matching entries.

Example:
```bash
go run ./cmd/fbx rm --contains books/ --min-size 1024 books.fbx
go run ./cmd/fbx replace-text --find old --replace new --glob 'books/*.fb2' books.fbx
```

### `list`, `stat`, `extract`, `verify`
- `list`: print `size path` for all entries.
- `stat`: print metadata for one entry (`--json` optional).
- `extract`: stream one entry to stdout or `-o` file.
- `verify`: `dir|sample|all` integrity check.

Examples:
```bash
go run ./cmd/fbx list books.fbx
go run ./cmd/fbx stat --json books.fbx books/1.fb2
go run ./cmd/fbx extract -o out.fb2 books.fbx books/1.fb2
go run ./cmd/fbx verify --mode all books.fbx
```
