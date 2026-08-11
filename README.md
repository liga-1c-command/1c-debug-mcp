# 1C Debug MCP Server

MCP (Model Context Protocol) сервер для отладки 1С:Предприятие через HTTP Debug Protocol (dbgs.exe).

Позволяет AI-ассистентам (Claude, Kiro и др.) управлять отладкой 1С: устанавливать точки останова, выполнять пошаговую отладку, просматривать переменные и вычислять выражения.

**Версия 2.0** — переписан на Go. Единый бинарник без зависимостей (Node.js не нужен).

## Возможности

- ✅ Подключение к серверу отладки 1С (dbgs.exe)
- ✅ Установка точек останова (основная конфигурация, расширения, формы)
- ✅ Авторезолвинг objectID из метаданных по имени модуля
- ✅ **Автоматическое кэширование метаданных** — быстрый старт (<100мс для больших конфигураций)
- ✅ Пошаговое выполнение (step-in, step-out, continue)
- ✅ Пауза на следующей строке (pause через initSettings)
- ✅ Просмотр локальных переменных
- ✅ Вычисление BSL выражений в контексте остановки
- ✅ Стек вызовов
- ✅ Поддержка конфигураций, расширений и внешних обработок
- ✅ Автоматическое переподключение при сбое dbgs.exe
- ✅ Логирование с уровнями (error/info/debug) и записью в файл

## Установка

### Требования

- 1С:Предприятие 8.3 с запущенным сервером отладки (dbgs.exe)
- Go 1.23+ (только для сборки из исходников)

### Сборка из исходников

```bash
cd 1c-debug-mcp/go
go build -o dist/1c-debug-mcp.exe ./cmd/1c-debug-mcp/
```

## Настройка

### 1. Запуск сервера отладки 1С

```bash
dbgs.exe --port=1550 --addr=localhost
```

### 2. Настройка MCP сервера

Добавьте конфигурацию в `.kiro/settings/mcp.json`:

```json
{
  "mcpServers": {
    "1c-debug": {
      "command": "C:\\path\\to\\1c-debug-mcp\\go\\dist\\1c-debug-mcp.exe",
      "env": {
        "ONEC_DEBUG_URL": "http://localhost:1550",
        "ONEC_INFOBASE_ALIAS": "DefAlias",
        "ONEC_CF_PATH": "C:\\path\\to\\src\\cf",
        "ONEC_CFE_PATHS": "C:\\path\\to\\src\\cfe",
        "ONEC_EPF_PATHS": "C:\\path\\to\\src\\epf",
        "ONEC_LOG_LEVEL": "info",
        "ONEC_LOG_FILE": "C:\\Logs\\1c-debug.log"
      },
      "type": "stdio",
      "disabled": false,
      "autoApprove": ["get_targets", "wait_for_stop", "get_variables", "evaluate", "get_call_stack"]
    }
  }
}
```

#### Параметры конфигурации

| Параметр | Описание | Обязательный |
|---|---|---|
| `ONEC_DEBUG_URL` | URL сервера отладки | Да |
| `ONEC_INFOBASE_ALIAS` | Алиас базы (`DefAlias` для локальной, имя базы для серверной) | Да |
| `ONEC_DEBUG_PASSWORD` | Пароль сервера отладки | Нет |
| `ONEC_CF_PATH` | Путь к выгруженной конфигурации | Нет |
| `ONEC_CFE_PATHS` | Пути к расширениям (через `;`) | Нет |
| `ONEC_EPF_PATHS` | Пути к внешним обработкам (через `;`) | Нет |
| `ONEC_LOG_LEVEL` | Уровень логов: `error`/`info`/`debug` | Нет |
| `ONEC_LOG_FILE` | Путь к файлу логов (перезапись при старте) | Нет |
| `ONEC_DISABLE_CACHE` | Отключить кэш метаданных: `true`/`false` (по умолчанию `false`) | Нет |
| `ONEC_CACHE_PATH` | Путь к файлу кэша метаданных или к каталогу для него. По умолчанию файл `.1c-debug-metadata-cache.json` кладётся рядом с выгруженной конфигурацией (`ONEC_CF_PATH`) | Нет |

> **Важно:** Если используется глобальный `~/.kiro/settings/mcp.json` совместно с локальным `.kiro/settings/mcp.json` — параметры `env` из глобального конфига **не мержатся** с локальным. Локальный `env` полностью переопределяет глобальный. Указывайте все нужные параметры в локальном конфиге.

CLI-флаги (`--url`, `--alias`, `--password`, `--cf-path`, `--cache-path`) имеют приоритет над env.

### 3. Перезапуск MCP сервера

После изменения конфигурации перезапустите MCP сервер через панель "MCP Servers" в Kiro.

## Доступные инструменты

### attach

Подключение к серверу отладки 1С.

**Параметры:**
- `url` (optional) — URL сервера (по умолчанию из `ONEC_DEBUG_URL`)
- `infobaseAlias` (optional) — алиас базы (по умолчанию из `ONEC_INFOBASE_ALIAS`)
- `autoAttach` (optional) — автоподключение ко всем целям (по умолчанию `true`)
- `password` (optional) — пароль

### detach

Отключение от сервера отладки.

### force_detach

Принудительная остановка ping-цикла и очистка сессии. Используйте при ошибке `ibInDebug` или зависшей сессии.

### get_targets

Список подключённых целей отладки + статус метаданных.

**Возвращает:**
```json
{
  "targets": [
    { "targetID": { "id": "uuid" }, "targetType": "ManagedClient", "state": "Worked" }
  ],
  "metadata": { "ready": true, "moduleCount": 1975 }
}
```

### set_breakpoints

Установка точек останова в BSL-модуле.

**Параметры:**
- `moduleName` (required) — имя модуля
- `moduleType` (optional) — тип: `CommonModule`, `ObjectModule`, `FormModule`, `ManagerModule`, `RecordSetModule`
- `lines` (required) — массив номеров строк
- `objectID` (optional) — GUID объекта (авторезолвится из метаданных если не указан)
- `extensionName` (optional) — имя расширения (пустая строка = основная конфигурация)
- `targetId` (optional) — ID цели отладки

**Важно:** Для расширений всегда указывайте `extensionName` — иначе авторезолв ищет только в основной конфигурации.

### clear_breakpoints

Удаление всех точек останова.

### continue

Продолжение выполнения остановленной цели.

**Параметры:** `targetId` (required)

### step_in

Шаг с заходом в процедуры/функции.

**Параметры:** `targetId` (required)

### step_out

Выход из текущей процедуры/функции.

**Параметры:** `targetId` (required)

### pause

Остановка на следующей выполняемой строке. Глобальная — не требует `targetId`.

Реализован через `initSettings(breakOnNextLine=true)`. После остановки флаг сбрасывается автоматически.

**Параметры:** `targetId` (optional, для совместимости)

### wait_for_stop

Ожидание остановки цели отладки.

**Параметры:** `timeout` (optional, мс, по умолчанию 30000)

**Возвращает:**
```json
{
  "targetId": "uuid",
  "moduleName": "CommonModule.ОбщегоНазначения",
  "lineNo": 42,
  "callStack": [{ "moduleID": { "name": "...", "objectID": "..." }, "lineNo": 42 }]
}
```

### get_call_stack

Стек вызовов из последнего события остановки. Не потребляет очередь событий.

**Параметры:** `targetId` (required)

### get_variables

Локальные переменные остановленной цели.

**Параметры:** `targetId` (required)

**Возвращает:**
```json
{ "variables": [{ "name": "Отказ", "typeName": "Булево", "value": "Ложь" }] }
```

### evaluate

Вычисление BSL-выражения в контексте остановленной цели.

**Параметры:** `targetId` (required), `expression` (required)

**Возвращает:**
```json
{ "expression": "ТекущаяДата()", "result": { "typeName": "Дата", "value": "19.04.2026" } }
```

### raw_request

Отправка произвольного XML-запроса к dbgs.exe.

**Параметры:** `cmd` (required), `xml` (required), `dbgui` (optional)

### reload_metadata

Перезагрузка метаданных из исходных файлов без перезапуска сервера.

**Параметры:**
- `skipCache` (optional) — пропустить кэш и выполнить полное пересканирование (по умолчанию `false`)

**Возвращает:** `{ "success": true, "moduleCount": 1975, "skipCache": false, "message": "..." }`

**Использование:**
```javascript
// Обычное обновление - использует кэш если валиден
mcp_1c_debug_reload_metadata()

// Принудительное пересканирование - игнорирует кэш
mcp_1c_debug_reload_metadata({ skipCache: true })
```

## Типичные сценарии

### Отладка общего модуля

```
mcp_1c_debug_attach()
mcp_1c_debug_set_breakpoints(moduleName="ОбщегоНазначения", moduleType="CommonModule", lines=[42])
// выполнить код в 1С
stop = mcp_1c_debug_wait_for_stop()
mcp_1c_debug_get_variables(targetId=stop.targetId)
mcp_1c_debug_continue(targetId=stop.targetId)
mcp_1c_debug_detach()
```

### Отладка расширения

```
mcp_1c_debug_attach()
mcp_1c_debug_set_breakpoints(
  moduleName="_ДемоЗаказПокупателя",
  moduleType="ObjectModule",
  extensionName="_МоёРасширение",
  lines=[4]
)
// записать документ
stop = mcp_1c_debug_wait_for_stop()
mcp_1c_debug_continue(targetId=stop.targetId)
```

### Отладка внешней обработки (EPF)

Точки для EPF не работают — используйте pause:

```
mcp_1c_debug_attach()
mcp_1c_debug_pause()
// выполнить действие в обработке
stop = mcp_1c_debug_wait_for_stop()
mcp_1c_debug_step_in(targetId=stop.targetId)
stop2 = mcp_1c_debug_wait_for_stop()
mcp_1c_debug_get_variables(targetId=stop2.targetId)
mcp_1c_debug_continue(targetId=stop2.targetId)
```

## Резолвинг модулей

Если настроены `ONEC_CF_PATH`, `ONEC_CFE_PATHS`, `ONEC_EPF_PATHS` — сервер автоматически:

- Резолвит `objectID` → `CommonModule.ОбщегоНазначения` в ответах
- Авторезолвит `objectID` при установке точек по имени модуля

Метаданные загружаются **асинхронно в фоне** — MCP подключается мгновенно.

### Кэширование метаданных

Для ускорения старта метаданные автоматически кэшируются:

- **Первый запуск:** полное сканирование XML файлов (500 модулей ~3с, 1000 модулей ~10с)
- **Последующие запуски:** загрузка из кэша (<100мс)
- **Автоматическая инвалидация:** при изменении `Configuration.xml` или папок метаданных

Кэш хранится в `.1c-debug-metadata-cache.json` рядом с конфигурацией.

Подробнее: [PERFORMANCE.md](PERFORMANCE.md), [CACHE.md](CACHE.md)

### Проверка статуса

Статус в `get_targets`:

```json
{ "metadata": { "ready": true, "moduleCount": 1975 } }
```

После обновления исходников: `mcp_1c_debug_reload_metadata()`

## Известные ограничения

### Внешние обработки (EPF)

Точки останова не работают — ограничение протокола 1С. Используйте `pause` + пошаговое выполнение.

### extensionName обязателен для расширений

Без `extensionName` авторезолв ищет только в основной конфигурации.

### Типы целей отладки

| Тип | Описание |
|---|---|
| `ManagedClient` | Тонкий клиент (`&НаКлиенте`) |
| `Server` | Серверный контекст, серверная база |
| `ServerEmulation` | Серверный контекст, файловая база |
| `BackgroundJob` | Фоновые задания |
| `WebClient` | Веб-клиент |
| `MobileClient` | Мобильный клиент |

## Документация

- 📖 [Quick Start](QUICKSTART.md) — быстрый старт
- 📚 [Examples](EXAMPLES.md) — примеры использования
- ⚡ [Performance](PERFORMANCE.md) — оптимизация производительности и кэширование
- 💾 [Cache](CACHE.md) — как работает кэш метаданных
- ❓ [FAQ](FAQ.md) — часто задаваемые вопросы
- 🏗️ [Architecture](ARCHITECTURE.md) — архитектура проекта
- 📝 [Changelog](CHANGELOG.md) — история изменений
- 🎯 [Kiro Steering](examples/kiro-steering/) — готовые steering файлы

## Лицензия

MIT License

## Связанные проекты

- [onec-debug-adapter](https://github.com/akpaevj/onec-debug-adapter) — C# DAP адаптер для 1С
- [Model Context Protocol](https://modelcontextprotocol.io/) — спецификация MCP
