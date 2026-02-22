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

Key flags: `-o`, `--codec`, `--level`, `--chunk-text`, `--chunk-bin`, `--verify-in`, `--max-entry-size`, `--max-chunk-size`.

### `add` / `upsert`
Write file content into an entry.

Example:
```bash
go run ./cmd/fbx add --as books/1.fb2 --codec lz4 books.fbx ./1.fb2
```

Key flags: `--as`, `--meta-json`, `--meta-file`, `--codec`, `--level`, `--chunk-size`, `--max-entry-size`, `--max-chunk-size`.

### `rm`, `find`, `replace-text`
- `rm`: delete entries by path/prefix/glob.
- `find`: filter paths by prefix/glob/substring.
- `replace-text`: bulk text replacement in matching entries.

Example:
```bash
go run ./cmd/fbx replace-text --find old --replace new --glob 'books/*.fb2' books.fbx
```

### `list`, `extract`, `verify`
- `list`: print `size path` for all entries.
- `extract`: stream one entry to stdout or `-o` file.
- `verify`: `dir|sample|all` integrity check.

Examples:
```bash
go run ./cmd/fbx list books.fbx
go run ./cmd/fbx extract -o out.fb2 books.fbx books/1.fb2
go run ./cmd/fbx verify --mode all books.fbx
```
