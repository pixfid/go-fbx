# Статус реализации FBX RFC

Последнее обновление: 2026-03-09

## Общее
- Реализовано: ядро библиотеки FBX v1 + CLI + ZIP->FBX + pack + verify + векторы совместимости + journal recovery + канонический профиль `v1` (`IDX1`, required-features).
- Статус тестов: `go test ./...` проходит.

## Матрица покрытия RFC

| Область RFC | Статус | Примечание |
|---|---|---|
| §6 Формат файла (Header/Chunk/Directory) | Реализовано | Бинарный layout, CRC-проверки, path hash и инварианты чанков валидируются. |
| §7 Правила путей | Реализовано | Нормализация и валидация в `internal/pathutil/path.go`. |
| §8 Метаданные | Реализовано | Непрозрачные байты метаданных; рекомендованный формат JSON используется в tooling. |
| §9 Требования к Reader | Реализовано | STORE/ZSTD/LZ4 доступны во всех сборках; CRC-проверка управляется `StrictVerify`. |
| §10 Требования к Writer | Реализовано | Append-only семантика, уникальность путей, проверки инвариантов. |
| §11 Алгоритмы (Open/Extract/Add/Replace/Remove/Commit) | Реализовано | Включая транзакционный commit flow. |
| §12 Массовые операции | Реализовано | `RemoveMany`, `RemovePrefix`, `RemoveGlob`, `RemoveWhere`. |
| §13 Уплотнение (`pack`) | Реализовано | In-place и out-of-place pack через API/CLI. |
| §14 Верификация (`verify`) | Реализовано | Режимы `dir`, `sample`, `all` в API/CLI. |
| Канонический профиль `v1` (`HAS_DIR_INDEX`, required-features) | Реализовано | `IDX1` и обязательные поля заголовка требуются для поддерживаемых snapshot'ов. |
| §15 Обработка ошибок | Реализовано | Публичные типизированные ошибки (`ErrNotFound`, `ErrCRCMismatch` и т.д.). |
| §16 Безопасность | В основном реализовано | Валидация путей, потоковый I/O, лимиты (`MaxEntrySize`, `MaxChunkSize`), лимиты памяти/окна декодера ZSTD. |
| §18 Референсная архитектура/API/CLI | В основном реализовано | Реализованы базовый API и основные CLI-команды. |
| Приложение A (канонические векторы) | Реализовано | Добавлены в `tests/testdata/vectors/*` с compat test runner. |

## Journal и recovery
- Реализована запись journal record (`JNL1`) на commit со snapshot header + CRC.
- Реализована запись backup header record (`BKP1`) на commit со snapshot header + CRC.
- Реализован fixed backup header slot в offset `128` (зеркальная копия header).
- `Open()` выполняет recovery при невалидном primary header/directory.
- Порядок recovery: fixed backup header -> последние journal/backup records.

## Производительность
- Добавлены бенчмарки в `tests/bench_test.go`:
  - benchmark chunk encode для STORE/ZSTD/LZ4
  - end-to-end benchmark контейнера `Upsert+Extract`

## Текущие практические ограничения
- ZSTD — pure-Go (`klauspost/compress`), доступен без cgo.
- LZ4 — pure-Go (`pierrec/lz4/v4`), доступен без cgo.
- Детект текста и heuristic "already-compressed" — best-effort, основаны на расширении и magic bytes.
- Fixed backup slot гарантирован для контейнеров, созданных текущим layout.
