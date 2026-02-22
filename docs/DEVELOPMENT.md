# Development Guide

## Repository Layout
- `fbx/`: public API, transactions, import, pack, journal/recovery.
- `internal/format/`: binary format, codecs, CRC/hash helpers.
- `internal/pathutil/`: path normalization and validation.
- `cmd/fbx/`: CLI implementation.
- `tests/`: integration/compatibility/benchmark tests.
- `tests/testdata/vectors/`: canonical compatibility vectors.

## Build and Test
```bash
go build ./...
go test ./...
go test -run TestCanonicalVectors ./tests
go test -bench . ./tests
```

Regenerate vectors:
```bash
go run tests/scripts/gen_vectors.go
```

## Coding Conventions
- Run `gofmt -w` on changed files.
- Keep exported API in `fbx/`; keep implementation internals under `internal/`.
- Prefer streaming (`io.Reader`/`io.Writer`) over full-buffer processing for large inputs.
- Use typed errors and wrap with context only when it preserves `errors.Is` behavior.

## Testing Expectations
- Add regression tests for every bug fix.
- For format/compat changes, update vectors and manifests.
- If feature depends on codec (`zstd`/`lz4`), tests must skip when codec is unavailable.

## Commit and PR Checklist
- Small, focused commits (`feat:`, `fix:`, `test:`, `docs:`).
- Include RFC section references for behavior changes.
- Attach commands used for validation (at minimum `go test ./...`).
- For CLI changes, include a concrete command example and expected output snippet.
