# Руководство по API (`go-fbx/fbx`)

## Основные типы
- `Container`: открытый или созданный FBX-файл.
- `Tx`: явная транзакция (`Begin`, `Commit`, `Rollback`).
- `Options`: поведение контейнера (размер чанков, лимиты, проверка, воркеры).
- `WriteOptions`: параметры записи конкретной entry (кодек, размер чанка, поля метаданных).
- `PackOptions`: параметры уплотнения `Pack`.
- `ZIPImportOptions`: параметры ZIP-конвертации.

## Жизненный цикл
1. `Create(path, opts)` для нового контейнера или `Open(path, opts)` для существующего.
2. Используйте высокоуровневые методы (`Add`, `Upsert`, `Replace`, `Remove`) или ручную транзакцию через `Begin()`.
3. Закрывайте контейнер через `Close()`.

## Минимальный пример
```go
c, err := fbx.Create("books.fbx", &fbx.Options{MaxWorkers: 4})
if err != nil { panic(err) }
defer c.Close()

if err := c.Upsert("books/a.fb2", src, nil, &fbx.WriteOptions{Codec: fbx.CodecZstd}); err != nil {
    panic(err)
}

var out bytes.Buffer
if err := c.Extract("books/a.fb2", &out); err != nil {
    panic(err)
}
```

## Проверка и уплотнение
- `Verify(&fbx.VerifyOptions{Mode: ...})`:
  - `VerifyDirectoryOnly`
  - `VerifySampledChunks`
  - `VerifyAllChunks`
- `Pack(inPath, outPath, opts)` пересобирает компактный контейнер.

## Обновление только метаданных
- `SetMeta(path, meta)` обновляет метаданные одной записи без перепаковки payload-чанков.
- `SetMetaMany(metaByPath, ignoreMissing)` массово обновляет метаданные в одном commit (возвращает `updated`, `missing`).
- Также доступны транзакционные варианты: `tx.SetMeta(...)`, `tx.SetMetaMany(...)`.

## Лимиты и безопасность
- `Options.MaxEntrySize`: отклонение чтения/записи выше лимита размера записи.
- `Options.MaxChunkSize`: отклонение oversized-чанков при чтении; ограничение размера чанков при записи.
- `Options.StrictVerify` (по умолчанию `true`): если `false`, несовпадение CRC payload допускается при извлечении.

## Обработка ошибок
Используйте `errors.Is` с экспортируемыми ошибками:
- `ErrNotFound`, `ErrAlreadyExists`, `ErrPathInvalid`
- `ErrInvalidFormat`, `ErrCRCMismatch`, `ErrUnsupportedCodec`, `ErrLimitExceeded`

## ZIP-конвертация
Используйте `ConvertZIPToFBX(zipPath, fbxPath, opts)` для импорта больших ZIP-архивов с опциональными метаданными (`--meta`, `MetaFile`) и callback прогресса.
