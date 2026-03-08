# Миграция к расширенному layout V1

Документ описывает миграцию legacy-контейнеров baseline `v1` к расширенному
`v1` layout, используемому текущими writer'ами `go-fbx`.

Связанные документы:
- [V1_EXTENSION_SPEC.md](./V1_EXTENSION_SPEC.md)
- [FORMAT_AND_RECOVERY.md](./FORMAT_AND_RECOVERY.md)

## Область

Миграция обновляет layout метаданных и семантику commit без изменения версии
формата (`Header.version = 1`).

После миграции включаются возможности:
- `HAS_JOURNAL`
- `HAS_BACKUP`
- `HAS_DIR_INDEX`
- `HAS_REQUIRED_FEATURES`

## Гарантии сохранения данных

Миграция append-only и без потери пользовательских данных:
- payload-чанки не перекодируются и не переписываются;
- ссылки на чанки (`ChunkOffset`, `RawOffset`, `RawSize`, `CompSize`, `CRC32Raw`) сохраняются;
- path, metadata, mode, flags, mtime записей сохраняются.

## Что записывается

Для legacy-контейнера миграция пишет один новый commit-снимок:
1. дописывает новый `DIR1` snapshot из текущих live entries;
2. дописывает `IDX1` для этого `DIR1`;
3. дописывает `JNL1` и `BKP1` header records;
4. обновляет fixed backup header slot (`offset=128`);
5. обновляет primary header (`offset=0`) с extension-флагами и reserved-полями.

Для уже мигрированного контейнера операция идемпотентна и не дописывает
новые байты.

## Использование CLI

```bash
# только preflight (без записи)
fbx migrate --dry-run --verify-source all library.fbx

# миграция с preflight и полной verify целевого snapshot
fbx migrate --verify-source dir --verify-target library.fbx
```

Поведение выхода:
- успех: `migration=ok`
- dry-run успех: `migration_dry_run=ok`
- ошибка выполнения/данных: код `1`
- ошибка аргументов: код `2`

## Использование API

```go
err := fbx.Migrate("library.fbx", &fbx.MigrateOptions{
    VerifySource: fbx.VerifyDirectoryOnly,
    VerifyTarget: true,
})
```

Классы ошибок:
- `ErrMigrationPreflightFailed`
- `ErrMigrationInterrupted`
- `ErrMigrationVerificationFailed`

Для ветвления используйте `errors.Is`.

## Модель отказов

Если миграция прервана до финальной записи primary header, предыдущий committed
snapshot остается authoritative и читаемым.

Если миграция завершена, но позже поврежден primary header, recovery может
использовать:
- fixed backup header (`offset=128`),
- tail records `JNL1`/`BKP1`,
- directory scan fallback.

## Рекомендуемый rollout

1. Запустить `fbx migrate --dry-run --verify-source all` на выборке.
2. Выполнить реальную миграцию с `--verify-target`.
3. Отслеживать метрики `fbx info` (`dead_bytes`, `churn_ops`, распределение чанков/кодеков).
4. Делать offline compaction (`pack`) только при операционной необходимости.
