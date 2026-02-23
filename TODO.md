# Performance TODO

Цель: ускорить библиотеку `go-fbx` без потери совместимости формата.

- [x] 1. Базовая метрика производительности
  - зафиксировать baseline: `go test -bench . ./tests -benchmem`
  - добавить/уточнить bench-сценарии для `pack` (малый/средний/большой контейнер)
  - статус: выполнено, добавлены `BenchmarkPackSmall/Medium/Large` в `tests/bench_test.go`
  - baseline (2026-02-23): `PackSmall ~39.1ms`, `PackMedium ~94.0ms`, `PackLarge ~177.8ms`

- [x] 2. Пуллинг ZSTD encoder/decoder
  - убрать `NewWriter/NewReader` на каждый чанк
  - внедрить переиспользование encoder/decoder через кэш/пулы
  - статус: выполнено через кэш-пулы encoder/decoder с бакетизацией лимитов decoder

- [x] 3. Снижение аллокаций в chunk pipeline
  - пулы буферов для записи чанков
  - уменьшить лишние копирования `[]byte`
  - статус: выполнено (`writeChunksSequential` без лишнего copy, `writeChunksParallel` с `sync.Pool` буферов)
  - наблюдение: `BenchmarkContainerAddExtract1MiB` ~`520486 ns/op`, `110 allocs/op`

- [x] 4. LZ4: поддержка уровней сжатия
  - использовать `--level`/`WriteOptions.Level` для LZ4
  - реализовать маппинг уровня в профиль LZ4 (Fast/HC)
  - покрыть тестами

- [x] 5. Оптимизация `pack` по I/O
  - рассмотреть fast-профиль для доверенных данных
  - проверить стратегию sync/durability для bulk-режима
  - статус: выполнено, добавлен `PackOptions.FastUnsafe` и CLI-флаг `pack --fast`
  - наблюдение: `BenchmarkPackMedium` ~`26.6ms/op` vs `BenchmarkPackMediumFastUnsafe` ~`5.4ms/op`

- [x] 6. Параллелизм на уровне задач
  - рекомендации/инструменты для параллельной упаковки множества файлов
  - статус: выполнено, добавлена CLI-команда `pack-many` (`--jobs`, `--glob`, общие флаги pack)

- [ ] 7. Регресс-контроль производительности
  - зафиксировать метрики до/после
  - добавить guard от performance regression
