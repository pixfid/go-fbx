# go-fbx

`go-fbx` — библиотека и CLI на Go для бинарного контейнера FBX v1, описанного в `FBX_RFC_BinarySpec_GoDoc_v1.md`.
Проект рассчитан на большие архивы FB2/FictionBook и поддерживает потоковые чтение/запись, транзакции, верификацию и repack.

## Возможности
- Append-only запись контейнера с транзакционным commit (`Begin`/`Commit`/`Rollback`).
- Кодеки:
  - `store`: всегда доступен
  - `zstd`: pure Go (`github.com/klauspost/compress/zstd`), работает с cgo и без
  - `lz4`: pure Go (`github.com/pierrec/lz4/v4`), работает с cgo и без
- Конвертация ZIP -> FBX с опциональными метаданными и однострочным progress redraw.
- Режимы проверки: только directory, выборка чанков, все чанки.
- Массовые операции: удаление по prefix/glob/predicate и уплотнение `pack`.
- CLI-команды `add/upsert/replace`, `set-meta`, `stat`, `find/rm/replace-text`.
- CLI-режим `interactive` для просмотра/чтения/удаления записей.
  - Bubble Tea UI с навигацией по панелям (`Tab`) и подтверждением удаления (`Backspace`).
- Восстановление при повреждении primary header (fixed backup + journal/backup records).
- Ограничения безопасности чтения/записи: `MaxEntrySize`, `MaxChunkSize`.

## Требования
- Go `1.23+`.

## Быстрый старт (CLI)
```bash
go run ./cmd/fbx convert-zip --progress \
  --codec zstd --meta auto \
  f.fb2-712242-720343.zip books.fbx

go run ./cmd/fbx list books.fbx
go run ./cmd/fbx interactive books.fbx
go run ./cmd/fbx stat --json books.fbx books/123.fb2
go run ./cmd/fbx info books.fbx
go run ./cmd/fbx verify --mode all books.fbx
go run ./cmd/fbx extract -o sample.fb2 books.fbx books/123.fb2
go run ./cmd/fbx set-meta --meta-json '{"source":"flibusta"}' books.fbx books/123.fb2
go run ./cmd/fbx pack --codec zstd books.fbx
```

## Быстрый старт (Library)
```go
c, _ := fbx.Create("books.fbx", &fbx.Options{MaxWorkers: 4})
_ = c.Add("book.fb2", srcReader, nil, &fbx.WriteOptions{Codec: fbx.CodecZstd})
_ = c.Extract("book.fb2", dstWriter)
_ = c.Close()
```

## Документация
- `docs/CLI.md` — справочник CLI и примеры.
- `docs/API.md` — публичный Go API и семантика опций.
- `docs/FORMAT_AND_RECOVERY.md` — модель формата на диске, journal и recovery.
- `docs/DEVELOPMENT.md` — тесты, векторы, бенчмарки и workflow разработки.
- `AGENTS.md` — правила для контрибьюторов (стиль кода и PR-гигиена).
