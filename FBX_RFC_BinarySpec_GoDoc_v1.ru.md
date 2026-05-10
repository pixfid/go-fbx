# RFC: FBX v1 — FictionBook eXtended Container Format
**Категория:** Informational  
**Статус:** Draft  
**Версия:** 1.0  
**Последнее обновление:** 2026-03-09  
**Предполагаемое применение:** Хранение и обработка очень больших коллекций электронных книг (FB2 и ассеты) с быстрым произвольным доступом и обновлениями в append-only режиме.

---

## Аннотация

Этот документ определяет **FBX v1**, бинарный формат контейнера, разработанный как практичная замена хранению больших библиотек FB2 в ZIP-архивах, особенно когда архивы достигают **многогигабайтных размеров (5+ GB)** и частые обновления (add/replace/remove) становятся дорогими из-за перепаковки.

FBX v1 поддерживает:

- O(1) открытие: чтение фиксированного заголовка и blob-каталога без сканирования контейнера
- Произвольный доступ к отдельным файлам через дескрипторы чанков
- Append-only модификации: add/replace/remove реализуются дописыванием новых данных и нового каталога
- Эффективные массовые операции: применить много изменений и выполнить один commit
- Проверки целостности: CRC32 для каталога и сырых данных каждого чанка
- Офлайн-компактацию (GC): `pack` строит новый компактный контейнер из «живых» записей

FBX v1 намеренно избегает таких усложнений, как шифрование, дедупликация и полнотекстовая индексация, оставляя пространство для будущих версий.

Текущий `go-fbx` рассматривает бывший **профиль расширения v1**
(при том же `Header.version = 1`) как канонический поддерживаемый layout `v1`
для более строгой семантики commit/recovery и ленивого парсинга каталога. См.:
- `docs/V1_EXTENSION_SPEC.md`

---

## Оглавление

1. Введение  
2. Соглашения и терминология  
3. Цели проектирования и не-цели  
4. Обзор контейнера  
5. Типы данных и правила кодирования  
6. Формат файла FBX v1  
   - 6.1 Заголовок (фиксированный)  
   - 6.2 Запись чанка (блок данных)  
   - 6.3 Каталог (directory)  
7. Правила путей  
8. Формат метаданных  
9. Требования к Reader  
10. Требования к Writer  
11. Алгоритмы (Open, Extract, Add, Replace, Remove, Batch Commit)  
12. Массовые операции  
13. Компактация (`pack`)  
14. Верификация (`verify`)  
15. Обработка ошибок и надежность  
16. Вопросы безопасности  
17. Совместимость и расширяемость  
18. Референсная архитектура Go-библиотеки  
19. README (для пользователей репозитория)  
20. API-документация в стиле GoDoc  
21. Приложение A: Канонические тестовые векторы (рекомендуется)  
22. Приложение B: Рекомендуемые MIME-типы и соглашения  

---

## 1. Введение

ZIP — широко поддерживаемый формат архивов, но он становится дорогим для **очень больших библиотек**, когда изменения происходят часто. Типичные сценарии (обновить несколько книг, удалить подмножество, добавить новый контент) могут требовать переписывания больших частей архива или как минимум переписывания структур центрального каталога, что приводит к долгим операциям и высокой I/O-амплификации.

FBX v1 решает это за счет:

- **Хранения чанками:** каждый файл хранится как независимые сжатые чанки.
- **Append-only обновлений:** новые чанки и новый каталог дописываются; старые данные остаются до компактации.
- **Единственного указателя на активный каталог:** заголовок указывает на последний каталог, делая время открытия константным.

Этот документ определяет полную бинарную спецификацию, пригодную для построения совместимых реализаций.

---

## 2. Соглашения и терминология

Ключевые слова **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY** и **OPTIONAL** следует интерпретировать как описано в RFC 2119.

**Термины:**

- **Container:** FBX-файл, хранимый на диске.
- **Entry:** Логический файл внутри контейнера (например, `book.fb2`, `img/cover.jpg`).
- **Chunk:** Срез содержимого entry в сыром (несжатом) виде.
- **Chunk Record:** Запись на диске, содержащая один сжатый чанк.
- **ChunkRef:** Дескриптор в каталоге, который указывает на запись чанка.
- **Directory:** Каталог, перечисляющий все «живые» entries и ссылки на их чанки.
- **Active Directory:** Каталог, на который указывают `dir_offset` и `dir_size` в заголовке.

---

## 3. Цели проектирования и не-цели

### 3.1 Цели

1. **Быстрое открытие:** O(1) open независимо от размера контейнера.
2. **Быстрый доступ:** Извлечение одной entry без обработки несвязанных данных.
3. **Эффективные обновления:** Add/replace/remove без переписывания существующих данных.
4. **Массовые операции:** Поддержка тысяч операций с одним commit каталога.
5. **Целостность:** Обнаружение повреждений через проверки CRC32.
6. **Простота:** Малый набор структур; минимальная сложность парсинга.

### 3.2 Не-цели (v1)

- Шифрование
- Дедупликация
- Полнотекстовый поисковый индекс
- Встраиваемое patching сжатых потоков
- Точная кроссплатформенная передача прав доступа (mode хранится, но семантика опциональна)

---

## 4. Обзор контейнера

Контейнер FBX — это один файл, содержащий:

- **Заголовок фиксированного размера** по смещению 0.
- Переменный по размеру **регион данных**, содержащий записи чанков и старые каталоги.
- Blob **активного каталога** (обычно дописывается в конец).

Append-only мутация означает, что контейнер растет со временем. Удаление данных является логическим до тех пор, пока последующая компактация (`pack`) не перестроит контейнер меньшего размера.

---

## 5. Типы данных и правила кодирования

### 5.1 Порядок байтов

Все целочисленные поля имеют порядок байтов **little-endian**.

### 5.2 Примитивные типы

| Name | Size | Description |
|---|---:|---|
| u8  | 1 | Беззнаковое 8-битное целое |
| u16 | 2 | Беззнаковое 16-битное целое (LE) |
| u32 | 4 | Беззнаковое 32-битное целое (LE) |
| u64 | 8 | Беззнаковое 64-битное целое (LE) |
| bytes[N] | N | N сырых байтов |

### 5.3 Байтовые массивы и строки переменной длины

FBX v1 использует явные длины (без терминаторов):

- `varbytes`: `u32 length`, затем `length` байтов.
- `string`: `u32 length`, затем UTF‑8 байты.

### 5.4 CRC32

CRC32 использует полином IEEE (тот же, что Go `crc32.IEEE`).

### 5.5 Абсолютные смещения

Все смещения абсолютны относительно начала файла (байт 0). Реализации MUST использовать 64-битную арифметику для смещений и размеров.

---

## 6. Формат файла FBX v1 (нормативный)

Этот раздел нормативный: совместимые readers/writers MUST следовать ему.

### 6.0 Глобальные константы

- **Magic (header):** `FBXC`
- **Размер заголовка:** 128 байт
- **Версия:** 1
- **Magic каталога:** `DIR1`
- **Magic footer каталога:** `END1`
- **Magic записи чанка:** `CK`

### 6.1 Header (фиксированный, 128 байт)

**Расположение:** смещение 0  
**Размер:** ровно 128 байт

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
  u8   reserved[56];    // MUST be zero for v1 writers; readers MUST ignore contents
}
```

#### 6.1.2 Правила валидации

Reader MUST проверять:

- `magic == "FBXC"`
- `version == 1`
- `header_size == 128`
- `dir_offset > 0`, `dir_size > 0` (если реализация не использует правила пустого контейнера)
- `dir_offset + dir_size` MUST находиться в границах файла
- Reader SHOULD проверять `dir_crc32` по blob-каталогу.

#### 6.1.3 Flags

`flags` — это bitmask (u32). Неизвестные биты MUST игнорироваться readers.

| Bit | Name | Meaning |
|---:|---|---|
| 0 | HAS_JOURNAL | Регион журнала присутствует и может использоваться для более безопасных commit |
| 1 | HAS_BACKUP | В текущем профиле `go-fbx` включена семантика fixed backup header |
| 2 | HAS_DIR_INDEX | В текущем профиле `go-fbx` для открытия обязателен индекс каталога `IDX1` |
| 3 | HAS_REQUIRED_FEATURES | В текущем профиле `go-fbx` активна маска required-features в `reserved[52:56]` |
| 4..31 | RESERVED | MUST быть 0, если более поздний профиль не определяет иное |

> Примечание: базовая append-only процедура commit в этом документе безопасна и без журнала (см. §11.6). Поддержка журнала OPTIONAL.

---

### 6.2 Chunk Record (блок данных)

Записи чанков хранят сжатые или сохраненные сырые данные чанков entry.

**Расположение:** в любом месте файла (обычно дописываются)  
**Размер:** переменный (`16 + comp_size` байт)

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

#### 6.2.2 Идентификаторы кодеков

| codec | Name | Notes |
|---:|---|---|
| 0 | STORE | payload — это сырые байты; SHOULD выполняться `comp_size == raw_size` |
| 1 | ZSTD | payload — Zstandard frame |
| 2 | LZ4 | payload — LZ4 block/frame, как определено профилем реализации |

**Совместимость:** Readers MUST поддерживать STORE. Поддержка ZSTD и LZ4 RECOMMENDED.

#### 6.2.3 Правила валидации

Reader MUST проверять:

- `magic == "CK"`
- `raw_size > 0`
- `comp_size > 0`
- `chunk_offset + header+payload` в границах файла
- После декодирования вычисленный CRC32(raw) MUST быть равен `crc32_raw`

Reader SHOULD проверять, что `raw_size`, `comp_size`, `crc32_raw` совпадают с соответствующими полями `ChunkRef` (см. §6.3.3).

---

### 6.3 Directory (каталог)

Каталог содержит список всех «живых» entries и их карты чанков.

**Расположение:** `HeaderV1.dir_offset`  
**Размер:** `HeaderV1.dir_size` байт  
**Целостность:** CRC32 в header и footer

#### 6.3.1 Layout заголовка каталога

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

Readers MUST проверять:

- `magic == "DIR1"`
- `flags == 0` (или игнорировать неизвестные биты для будущего; v1 writers MUST писать 0)
- `entry_count` согласован с парсингом и границами файла

#### 6.3.2 Layout EntryV1 (переменный размер)

Каждая entry описывает файл, хранящийся в контейнере.

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

##### 6.3.2.1 Флаги entry

`entry_flags` — bitmask u32. В v1 определено:

| Bit | Name | Meaning |
|---:|---|---|
| 0 | IS_BINARY | Содержимое бинарное |
| 1 | IS_TEXT | Содержимое текстовое |
| 2..31 | RESERVED | Для v1 writers MUST быть 0; readers игнорируют неизвестные биты |

> `IS_BINARY` и `IS_TEXT` — только подсказки. Файл может не выставлять ни один или выставлять оба, хотя writers SHOULD выставлять ровно один, когда это возможно.

##### 6.3.2.2 Meta blob

`meta` — непрозрачный blob байтов. В v1 RECOMMENDED UTF-8 JSON, но это не обязательно. Readers MUST NOT падать, если meta неизвестного формата.

##### 6.3.2.3 Строка path

`path` — UTF-8 байты длиной `path_size` (без NUL). Он MUST удовлетворять правилам путей (§7).

#### 6.3.3 Layout ChunkRefV1

Ссылки на чанки отображают несжатое пространство файла entry на сохраненные чанки.

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

##### 6.3.3.1 Обязательные инварианты

Для каждой entry:

- `chunk_count >= 1` для непустых файлов.
- `raw_offset` MUST быть неубывающим между чанками.
- Чанки MUST NOT пересекаться в сыром пространстве:
  - Для соседних чанков i и i+1: `raw_offset[i] + raw_size[i] <= raw_offset[i+1]`
- Последний чанк MUST удовлетворять: `raw_offset_last + raw_size_last == file_size`
- Каждый ChunkRef SHOULD соответствовать значениям ChunkRecord (`raw_size`, `comp_size`, `crc32_raw`)

Reader MUST отклонять entry, если ее карта чанков нарушает инварианты, так как это может приводить к выходу за границы или чтению поврежденных данных.

#### 6.3.4 Layout footer каталога

```
struct DirectoryFooterV1 {
  u8   magic[4];        // "END1"
  u32  crc32;           // CRC32 over directory bytes from "DIR1" up to just before this footer
  u64  total_size;      // total directory size including footer
}
```

Readers MUST проверять:

- `magic == "END1"`
- `total_size == HeaderV1.dir_size`
- `crc32 == HeaderV1.dir_crc32`
- вычисленный CRC32 совпадает с `crc32`

> Область вычисления CRC: все байты начиная с magic каталога `"DIR1"` и заканчивая непосредственно перед footer.magic `"END1"`.

---

## 7. Правила путей (нормативно)

Пути entry MUST удовлетворять:

- UTF‑8
- Разделитель — `/`
- MUST NOT начинаться с `/`
- MUST NOT содержать сегменты обхода пути `..`
- MUST NOT содержать NUL-байт
- SHOULD NOT содержать обратный слеш `\`
- Чувствительность к регистру
- Пустой путь недопустим

Writers MUST нормализовать пути к канонической форме. Readers SHOULD отклонять невалидные пути.

---

## 8. Формат метаданных (информативно)

FBX v1 не навязывает схему метаданных. Рекомендуемые JSON-поля для FB2-библиотек:

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

Рекомендуемые соглашения:

- `mime` должен присутствовать для классификации контента.
- Для изображений: `mime` вроде `image/jpeg`, `image/png`.
- Для индексов или внутренних данных: пути под `__index__/` или `__meta__/` (см. Приложение B).

---

## 9. Требования к Reader (нормативно)

Совместимый reader:

1. MUST валидировать magic/version/size заголовка.
2. MUST загружать активный каталог из `(dir_offset, dir_size)`.
3. MUST валидировать CRC каталога и footer.
4. MUST реализовывать кодек STORE (codec=0).
5. MUST отклонять entries с невалидными картами чанков (пересечения, выход за диапазон, mismatch `file_size`).
6. MUST проверять chunk CRC32 при чтении данных чанка (если нет явно включенного «unsafe» режима).
7. SHOULD игнорировать неизвестные флаги и неизвестные метаданные.
8. SHOULD поддерживать ZSTD (codec=1) и LZ4 (codec=2).

---

## 10. Требования к Writer (нормативно)

Совместимый writer:

1. MUST записывать HeaderV1 с корректными magic/version/header_size.
2. MUST записывать каталог и footer с валидным CRC.
3. MUST гарантировать, что все offsets/sizes корректны и в границах файла.
4. MUST обеспечивать правила путей и уникальность:
   - в активном каталоге каждый path MUST быть уникальным.
5. MUST обеспечивать инварианты чанков:
   - без пересечений, полное покрытие до `file_size`.
6. SHOULD использовать append-only семантику commit (см. §11.6).
7. MUST записывать зарезервированные поля как нули в v1.

---

## 11. Алгоритмы (нормативно)

### 11.1 Open

1. Прочитать 128 байт по смещению 0 → распарсить HeaderV1.
2. Валидировать поля заголовка.
3. Прочитать blob-каталог через `ReadAt(dir_offset, dir_size)`.
4. Валидировать DirectoryV1 + DirectoryFooterV1 и CRC.
5. Построить индекс в памяти:
   - map path → ссылка на entry
   - опционально map hash → список кандидатов (для ускорения поиска)

### 11.2 Extract(entryPath)

1. Найти EntryV1 по path.
2. Для каждого ChunkRef (по возрастанию `raw_offset`):
   - Прочитать ChunkRecordV1 по `chunk_offset`.
   - Валидировать и декодировать payload по кодеку.
   - Проверить CRC32(raw).
   - Последовательно записать raw в output.

### 11.3 Add(path, reader)

Add MUST завершаться ошибкой, если path уже существует в активном каталоге.

1. Нормализовать и валидировать path.
2. Выбрать стратегию чанкинга (размеры для text/bin).
3. Читать из reader и делить на чанки.
4. Для каждого raw-чанка:
   - Сжать/сохранить, вычислить CRC32(raw).
   - Дописать ChunkRecordV1 в конец файла.
   - Зафиксировать ChunkRefV1 с offsets и sizes.
5. Сформировать EntryV1 с картой чанков, meta и `file_size`.
6. Добавить EntryV1 в состояние каталога в памяти.
7. Выполнить один commit каталога (см. §11.6).

### 11.4 Replace(path, reader)

Replace MUST завершаться ошибкой, если path не существует. Семантика эквивалентна: удалить старую entry из каталога + добавить новую entry. Старые чанки становятся мусором до выполнения pack.

### 11.5 Remove(path)

Remove является логическим: удалить entry из следующего snapshot-каталога и выполнить commit. Данные чанков не изменяются.

### 11.6 Batch commit (append-only, без журнала)

Это стандартный безопасный подход обновления для очень больших файлов.

**Процедура commit:**
1. Сериализовать новый blob-каталог (entries DirectoryV1 + footer) в буфер памяти.
2. Дописать blob-каталог в конец файла контейнера → зафиксировать `(new_dir_offset, new_dir_size)`.
3. Если настроено, выполнить `fsync` файла.
4. Обновить поля HeaderV1 (`dir_offset`, `dir_size`, `dir_crc32`) и записать заголовок по смещению 0 через `WriteAt`.
5. Если настроено, снова выполнить `fsync`.

**Безопасность при сбое:**
- Если сбой происходит до шага 4, заголовок все еще указывает на предыдущий каталог: контейнер остается консистентным (последняя операция просто не видна).
- Если сбой происходит во время шага 4, заголовок может быть записан частично на некоторых системах; для максимальной безопасности реализации MAY добавить журнал или резервный заголовок в будущей версии. Для практического использования двойной sync снижает риск, а запись только 128 байт обычно безопасна на современных файловых системах, но это не гарантируется спецификацией.

---

## 12. Массовые операции (информативно, но настоятельно рекомендуется)

Выполняйте тысячи операций через объект транзакции:

1. Один раз загрузить каталог.
2. Применить все операции в памяти:
   - RemoveMany: удалить entries по path/prefix/glob/filter
   - AddMany/UpsertMany: дописать чанки для каждого нового файла
3. Сделать один commit (каталог сериализуется один раз, заголовок обновляется один раз).

Это минимизирует накладные расходы на переписывание каталога и обеспечивает предсказуемую производительность независимо от размера контейнера.

---

## 13. Компактация (`pack`) (нормативно для tooling)

`pack` создает новый контейнер, который включает только «живые» entries, на которые ссылается активный каталог.

**Алгоритм:**
1. Открыть исходный контейнер и прочитать активный каталог.
2. Создать новый файл контейнера со свежим заголовком.
3. Для каждой entry:
   - Потоково извлечь данные entry из старого контейнера.
   - Записать в новый контейнер обычным пайплайном Add/Upsert.
4. Выполнить commit каталога в новом контейнере.
5. Атомарно заменить старый файл (зависит от реализации; обычно temp+rename).

Когда используются счетчики-подсказки компактации (см. §17), `pack` SHOULD сбрасывать их в ноль в новом компактном контейнере.

---

## 14. Верификация (`verify`) (нормативно для tooling)

Реализации SHOULD предоставлять верификацию:

- Только каталог: валидация header + CRC каталога + структурные инварианты.
- Полная: дополнительно валидация всех чанков через чтение и проверку CRC.

Reader MUST всегда валидировать CRC каталога и SHOULD валидировать CRC чанков при извлечении.

---

## 15. Обработка ошибок и надежность

### 15.1 Обязательные проверки

Readers MUST проверять:
- границы для всех offsets и sizes
- инварианты карты чанков
- CRC32 для каталога и сырых данных чанков

### 15.2 Рекомендуемое поведение
- Предоставлять типы ошибок, различающие:
  - not found
  - already exists
  - invalid format
  - CRC mismatch
  - unsupported codec
  - invalid path
- Разрешать «unsafe fast mode» ТОЛЬКО при явном включении и документировать, что он может возвращать поврежденные данные.

---

## 16. Вопросы безопасности

Контейнеры FBX могут быть недоверенным входом. Реализации MUST защищаться от:

- Path traversal: отклонять невалидные пути, никогда не писать вне целевой директории при extract.
- Zip-bomb-подобного раздувания: `raw_size` может быть большим; применять настраиваемые лимиты.
- Исчерпания памяти: избегать загрузки целых entries в память; использовать потоковую обработку чанков.
- CPU-бомб: фреймы сжатия могут быть вредоносными; по возможности задавать лимиты/таймауты декомпрессора.
- Переполнения целых: использовать 64-битные проверки для вычислений offset+size.

FBX v1 обеспечивает целостность (CRC32), но не криптографическую подлинность.

---

## 17. Совместимость и расширяемость

- Неизвестные флаги заголовка MUST игнорироваться readers.
- Базовые v1 writers SHOULD оставлять зарезервированные поля нулевыми.
- Реализации MAY определять приватные подсказки в reserved-байтах без повышения версии, если readers, которые их игнорируют, остаются полностью совместимыми.
- Подсказки профиля `go-fbx` в `HeaderV1.reserved`:
  - `reserved[8:16]` (`u64`, little-endian): `dead_bytes` (оценка объема устаревших байтов чанков).
  - `reserved[16:24]` (`u64`, little-endian): `churn_ops` (операции replace/remove-подобного типа, создавшие устаревшие данные).
  - эти значения advisory; они MUST NOT влиять на корректность парсинга/извлечения.
- Канонический профиль `v1` в `go-fbx` (с тем же `Header.version = 1`) дополнительно определяет:
  - строгую семантику commit через журнал + backup (`HAS_JOURNAL`, `HAS_BACKUP`);
  - таблицу смещений/хэшей каталога `IDX1` для ленивого парсинга (`HAS_DIR_INDEX`);
  - контракт required-features (`HAS_REQUIRED_FEATURES`);
- Детали профиля описаны в:
  - `docs/V1_EXTENSION_SPEC.md`
- Будущие версии по-прежнему могут добавить шифрование/дедупликацию/индексацию.
- Изменение версии MUST увеличивать `HeaderV1.version`.

---

# 18. Референсная архитектура Go-библиотеки (ненормативно)

Следующие разделы определяют полную архитектуру Go-библиотеки, реализующей FBX v1.

---

## 18.1 Структура репозитория

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

## 18.2 Базовый дизайн

### 18.2.1 Ответственность `Container`
- Владеет `*os.File`
- Загружает и валидирует header + directory
- Предоставляет операции чтения (`List`, `Stat`, `OpenReader`, `Extract`)
- Предоставляет операции записи через `Tx` или «обертки одиночных операций», которые внутри создают Tx и выполняют commit

### 18.2.2 Ответственность `Tx`
- Держит copy-on-write представление состояния каталога
- Выполняет add/replace/remove операции в памяти
- Дописывает записи чанков в тот же файл
- Один раз сериализует и коммитит новый blob-каталог

### 18.2.3 `internal/format`
- Чистый парсинг/кодирование структур на диске
- Валидация инвариантов
- Без доступа к файловой системе

### 18.2.4 `internal/chunk`
- Кодирование/декодирование записей чанков
- Кодеки сжатия

---

## 18.3 Публичная поверхность API (рекомендуется)

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

## 18.4 Внутренние пайплайны (производительность)

### 18.4.1 Потоковое чтение (`OpenReader`)
- Использует `ReadAt` для получения записей чанков
- Декодирует по одному чанку за раз
- Поддерживает небольшой буфер для активного чанка
- По умолчанию проверяет CRC32

### 18.4.2 Потоковая запись с параллельным сжатием
Рекомендуемый пайплайн:

1. Chunker: читает вход и эмитит raw-чанки `(index, raw_offset, data)`
2. Workers: сжимают/сохраняют и вычисляют CRC
3. Appender: пишет записи чанков по порядку; возвращает offsets чанков
4. Entry builder: строит `EntryV1` с `ChunkRefV1[]`

Порядок сохраняется за счет буферизации результатов сжатия, пришедших не по порядку, пока не прибудет следующий ожидаемый index.

---

## 18.5 Commit и безопасность при сбое
Стандартная последовательность commit в v1:

- Дописать новый каталог
- Sync
- Обновить заголовок по смещению 0
- Sync

Старый каталог остается валидным до обновления заголовка. Это делает запись безопасной при частичных операциях (последний commit может быть потерян, но контейнер остается открываемым).

---

# 19. README (готово для репозитория)

Этот раздел предназначен для дословного копирования в `README.md`.

---

## FBX — Быстрый контейнер для огромных FB2-библиотек (Go)

FBX — чанковый контейнер append-only, разработанный как замена ZIP для очень больших библиотек электронных книг (5+ GB). Он обеспечивает быстрый произвольный доступ к отдельным файлам и эффективные обновления без перепаковки всего архива.

### Возможности

- **Быстрое открытие**: чтение только заголовка + каталога (без сканирования)
- **Произвольный доступ**: извлечение одного файла чтением только его чанков
- **Append-only обновления**: add/replace/remove через дописывание новых данных + нового каталога
- **Пакетные транзакции**: массовые обновления коммитятся один раз
- **Целостность**: проверки CRC32 на каталоге и чанках
- **Компактация**: `pack` перестраивает минимальный контейнер

### Установка

```bash
go get github.com/you/fbx@latest
```

### Быстрый старт

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

### CLI (опционально)

Если вы собрали CLI:

```bash
go install github.com/you/fbx/cmd/fbx@latest
```

Примеры:

```bash
fbx list library.fbx
fbx extract library.fbx books/book.fb2 -o book.fb2
fbx rm library.fbx --prefix trash/
fbx upsert library.fbx new/book.fb2 --as books/book.fb2
fbx pack library.fbx -o library.packed.fbx
fbx verify library.fbx --mode dir
```

### Заметки по дизайну

- Обновления append-only: старые данные остаются до `pack`.
- Для лучшей производительности на огромных контейнерах используйте транзакции (batch operations).
- CRC32 обеспечивает целостность, но не криптографическую подлинность.

### Лицензия
MIT (рекомендуется) или любая предпочтительная лицензия.

---

# 20. API-документация в стиле GoDoc (готово для пакета)

Этот раздел предоставляет комментарии в формате GoDoc и рекомендуемые экспортируемые символы.

> Помещайте эти комментарии непосредственно над объявлениями package/type/function.

---

## 20.1 Комментарий пакета (`fbx/doc.go`)

```go
// Package fbx реализует формат контейнера FBX v1: чанковый append-only архив,
// рассчитанный на очень большие библиотеки (multi-GB) с быстрым произвольным доступом.
//
// FBX открывается за O(1) за счет чтения фиксированного заголовка и blob-каталога.
// Обновления append-only: add/replace/remove дописывают новые записи чанков
// и новый каталог, после чего активный каталог атомарно переключается через заголовок.
//
// Типичный сценарий использования — транзакции (Tx) для массовых обновлений.
//
// Пакет предоставляет потоковые операции extract и add, проверку целостности
// по CRC и офлайн-компактацию (pack) через функцию Pack.
package fbx
```

---

## 20.2 Ошибки (`fbx/errors.go`)

```go
// ErrNotFound возвращается, когда path entry не существует в активном каталоге.
var ErrNotFound = errors.New("fbx: entry not found")

// ErrAlreadyExists возвращается при добавлении entry, которая уже существует.
var ErrAlreadyExists = errors.New("fbx: entry already exists")

// ErrInvalidFormat указывает на структурное нарушение формата FBX.
var ErrInvalidFormat = errors.New("fbx: invalid format")

// ErrCRCMismatch указывает на сбой проверки целостности каталога или чанка.
var ErrCRCMismatch = errors.New("fbx: crc mismatch")

// ErrUnsupportedCodec возвращается, когда контейнер использует кодек,
// который reader не поддерживает.
var ErrUnsupportedCodec = errors.New("fbx: unsupported codec")

// ErrPathInvalid возвращается, когда path entry нарушает правила путей FBX.
var ErrPathInvalid = errors.New("fbx: invalid path")
```

---

## 20.3 Типы и функции

### Codec

```go
// Codec определяет алгоритм сжатия, используемый для записей чанков.
type Codec uint8

const (
  // CodecStore хранит сырые байты без сжатия.
  CodecStore Codec = 0

  // CodecZstd хранит байты, сжатые Zstandard.
  CodecZstd Codec = 1

  // CodecLZ4 хранит байты, сжатые LZ4.
  CodecLZ4 Codec = 2
)
```

### Options

```go
// Options настраивает поведение контейнера при чтении и записи.
// nil *Options означает значения по умолчанию.
type Options struct {
  // ChunkSizeText — целевой размер несжатого чанка для текстоподобных entries.
  // По умолчанию: 1 MiB.
  ChunkSizeText int

  // ChunkSizeBin — целевой размер несжатого чанка для бинарных entries.
  // По умолчанию: 4 MiB.
  ChunkSizeBin int

  // DetectText включает эвристическое определение text/binary при записи.
  DetectText bool

  // DefaultCodec используется, когда WriteOptions не переопределяет codec.
  DefaultCodec Codec

  // DefaultLevel — codec-specific уровень сжатия (например, zstd 1..22).
  DefaultLevel int

  // StoreIfAlreadyCompressed избегает повторного сжатия форматов,
  // которые обычно уже сжаты (например, JPEG/PNG), используя CodecStore.
  StoreIfAlreadyCompressed bool

  // MaxWorkers управляет числом параллельных worker'ов сжатия.
  // По умолчанию: GOMAXPROCS.
  MaxWorkers int

  // SyncOnCommit выполняет fsync после записи каталога и после обновления заголовка.
  SyncOnCommit bool

  // StrictVerify управляет тем, проверяют ли readers CRC32 чанка по умолчанию при чтении.
  StrictVerify bool
}
```

### EntryInfo

```go
// EntryInfo описывает «живую» entry в активном каталоге.
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
// Container представляет открытый контейнер FBX.
// Он безопасен для конкурентного чтения; для записи требуется транзакция.
type Container struct {
  // unexported
}

// Open открывает существующий контейнер FBX по пути файловой системы.
func Open(path string, opts *Options) (*Container, error)

// Create создает новый контейнер FBX по path, обрезая файл, если он уже существует.
// Реализации SHOULD использовать temp+rename для атомарного create в tooling.
func Create(path string, opts *Options) (*Container, error)

// Close закрывает базовый файл.
func (c *Container) Close() error

// List возвращает итератор по живым entries.
func (c *Container) List() Iterator[EntryInfo]

// Stat возвращает метаданные для указанного path entry.
func (c *Container) Stat(path string) (EntryInfo, error)

// OpenReader возвращает потоковый reader для содержимого entry.
func (c *Container) OpenReader(path string) (io.ReadCloser, error)

// Extract потоково записывает содержимое entry в w.
func (c *Container) Extract(path string, w io.Writer) error

// Begin запускает новую транзакцию для пакетных обновлений.
// Возвращенный Tx MUST NOT использоваться конкурентно.
func (c *Container) Begin() *Tx
```

### Tx

```go
// Tx — транзакция пакетного обновления. Она дописывает новые записи чанков в контейнер
// и фиксирует изменения записью одного нового каталога и обновлением заголовка.
//
// Tx не безопасен для конкурентного использования.
type Tx struct {
  // unexported
}

// Add добавляет новую entry. Возвращает ошибку, если path уже существует.
func (tx *Tx) Add(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Upsert добавляет или заменяет entry.
func (tx *Tx) Upsert(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Replace заменяет существующую entry. Возвращает ошибку, если path не существует.
func (tx *Tx) Replace(path string, r io.Reader, meta []byte, wopts *WriteOptions) error

// Remove удаляет entry логически (она не появится в следующем snapshot каталога).
func (tx *Tx) Remove(path string) error

// RemoveMany удаляет несколько entries по точным путям.
func (tx *Tx) RemoveMany(paths []string) (removed int, err error)

// RemovePrefix удаляет все entries, пути которых имеют заданный префикс.
func (tx *Tx) RemovePrefix(prefix string) (removed int, err error)

// Commit записывает новый snapshot каталога и обновляет заголовок.
func (tx *Tx) Commit() error

// Rollback отбрасывает изменения в памяти. Он не откатывает уже дописанные данные.
func (tx *Tx) Rollback()
```

### Maintenance

```go
// VerifyOptions управляет областью проверки.
type VerifyOptions struct {
  Mode VerifyMode
}

// VerifyMode выбирает глубину проверки.
type VerifyMode int

const (
  VerifyDirectoryOnly VerifyMode = iota
  VerifySampledChunks
  VerifyAllChunks
)

// VerifyReport суммирует результаты проверки.
type VerifyReport struct {
  EntriesChecked uint64
  ChunksChecked  uint64
  Errors         []error
}

// Verify валидирует CRC и структуру каталога и, опционально, CRC чанков.
func (c *Container) Verify(vopts *VerifyOptions) (*VerifyReport, error)

// PackOptions настраивает компактацию.
type PackOptions struct {
  Codec     Codec
  Level     int
  ChunkText int
  ChunkBin  int
  Workers   int
  VerifyIn  bool
}

// Pack перестраивает компактный контейнер, содержащий только живые entries.
func Pack(inPath, outPath string, opts *PackOptions) error
```

---

# 21. Приложение A: Канонические тестовые векторы (рекомендуется)

Для обеспечения совместимости проект SHOULD поставлять тестовые векторы:

1. Минимальный контейнер с одной entry `book.fb2`, сохраненной как STORE одним чанком.
2. Контейнер с двумя entries и чанками ZSTD.
3. Контейнер с операцией replace (два каталога, header указывает на последний).
4. Контейнер с операцией remove (entry отсутствует в последнем каталоге).
5. Чанк с поврежденным CRC, чтобы убедиться, что reader обнаруживает `ErrCRCMismatch`.

Каждый тестовый вектор должен включать:
- файл контейнера
- ожидаемый listing каталога
- ожидаемое извлеченное содержимое

---

# 22. Приложение B: Рекомендуемые MIME-типы и соглашения

Рекомендуемые значения `mime` для meta JSON:

- `application/fb2+xml` для FB2
- `image/jpeg`, `image/png`
- `application/json` для мета-данных или индексов

Предлагаемые внутренние пространства имен:

- `__meta__/` для snapshot-метаданных контейнера
- `__index__/` для опциональных поисковых индексов (будущее)
- `__tmp__/` для временных артефактов, создаваемых инструментами (не коммитить)

---

# 23. Бинарная структура контейнера (псевдографика)

```text
FBX файл (append-only)

+0x00000000  Primary HeaderV1 (128 bytes)
| magic[4]         = "FBXC"
| version          (u16)
| header_size      (u16, должен быть 128)
| flags            (u32)
| uuid[16]
| created_unix     (u64)
| dir_offset       (u64)
| dir_size         (u64)
| dir_crc32        (u32)
| journal_offset   (u64)
| journal_size     (u64)
| reserved[56]

+0x00000080  Fixed Backup Header slot (128 bytes, используется текущим layout writer)
| Зеркало HeaderV1 (те же поля)

+0x00000100..EOF   Append-only регион
| [ChunkRecordV1]...
| [старые DirectoryV1 blob]...
| [новый активный DirectoryV1 blob]
| [IDX1 directory index blob]          (профиль расширения)
| [JNL1 header-snapshot record] (во время commit)
| [BKP1 header-snapshot record] (во время commit)

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
+0x08  flags       (u32, в v1 записывается 0)
+0x0C  build_unix  (u64)
+...   EntryV1[entry_count]
+...   Footer:
       magic[4]    = "END1"
       crc32       (u32)  // от DIR1 до байта перед END1
       total_size  (u64)  // полный размер blob-каталога, включая footer

EntryV1 (переменный размер)
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
+0x1C  reserved     (u32, должен быть 0)

JNL1/BKP1 header snapshot record (148 bytes каждый)
+0x00  magic[4]        = "JNL1" или "BKP1"
+0x04  ts_unix         (u64)
+0x0C  header_bytes[128] (HeaderV1)
+0x8C  header_crc32    (u32)
+0x90  record_crc32    (u32) // CRC по всем предыдущим байтам записи
```


## Конец документа
