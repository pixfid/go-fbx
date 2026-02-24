# Формат и восстановление

Эта реализация следует `FBX_RFC_BinarySpec_GoDoc_v1.md`.

## Модель на диске
- Header (`HDR1`) по смещению `0`.
- Зарезервированный фиксированный backup header по смещению `128` (`HeaderSize`).
- Append-only chunk records (`CHK1`) с метаданными codec/raw/CRC.
- Directory blob (`DIR1`) дописывается на `commit` и указывается в header (`DirOffset`, `DirSize`, `DirCRC32`).
- Подсказки компактации в reserved-байтах заголовка:
  - `reserved[8:16]` (`u64 LE`) = `dead_bytes` (оценка объема устаревших байтов чанков).
  - `reserved[16:24]` (`u64 LE`) = `churn_ops` (число replace/remove-подобных операций).

Commit никогда не переписывает существующие payload-чанков. Актуальный header всегда указывает на последний snapshot directory.

## Поток commit в транзакции
1. Кодирование и дописывание нового directory blob.
2. Построение обновлённого header.
3. Дозапись `JNL1` journal record (snapshot header + CRC).
4. Дозапись `BKP1` backup record (snapshot header + CRC).
5. Запись fixed backup header (offset `128`), если слот включён.
6. Запись primary header в offset `0`.

При `SyncOnCommit=true` (по умолчанию) между критическими этапами выполняется `fsync`.

## Восстановление при `Open`
Если валидация primary header/directory не проходит:
1. Попытка чтения fixed backup header.
2. Сканирование хвоста файла (`JNL1`/`BKP1`) и выбор последнего валидного snapshot по timestamp.
3. Сканирование файла на валидные snapshot'ы `DIR1 ... END1` и синтез header по самому новому валидному directory.
4. Перезапись восстановленного header в offset `0`.

Если все источники восстановления невалидны, `Open` возвращает `ErrInvalidFormat`.

## Доступность кодеков
- `store`: поддерживается всегда.
- `zstd`: поддерживается во всех сборках через `github.com/klauspost/compress/zstd` (pure Go).
- `lz4`: поддерживается во всех сборках через `github.com/pierrec/lz4/v4` (pure Go).

## Механизмы безопасности
- Нормализация путей и запрет traversal (`internal/pathutil`).
- CRC-проверка directory и chunk payload.
- Настраиваемые лимиты чтения/записи: `MaxEntrySize`, `MaxChunkSize`.
