# CLAUDE.md

Руководство для Claude Code по работе с этим репозиторием. Go-сервис на Clean Architecture + DDD:
агент, который отвечает на вопросы об инфраструктуре через LLM с инструментами MCP-сервера pulse
(`github.com/modelcontextprotocol/go-sdk`, клиент), и JSON-API для систем-клиентов (Telegram-бот
`pulse_bot`, service-desk, разбор алертов). LLM — за провайдер-независимой абстракцией, сейчас
OpenAI (`github.com/openai/openai-go/v3`, Responses API, `gpt-6-sol`).

**Контракт для клиентов — `docs/agent-api.md`**: поля запроса/ответа можно добавлять, удалять и
переименовывать нельзя (клиенты — чужие системы).

Выделен из `pulse_bot` (2026-09-25): агентный цикл, LLM, графики, история и эталонные вопросы
переехали сюда, бот — тонкий клиент этого API. Согласованные отклонения от шаблона gotemplate:
- gRPC/grpc-gateway/proto/swagger и трассировка убраны: транспорт — JSON поверх HTTP, история
  бесед — в памяти (одна реплика).
- Postgres — журнал вопросов (с 2026-09-25), с 2026-09-26 — ещё контекст бесед, приглушения и
  уведомления наблюдателя: отдельная база `pulse_agent` в `pulse-pg` (общий Postgres чарта pulse),
  срок хранения 90 дней. Без `PG_DSN` журнал — в памяти, наблюдателя, ленты и заметок нет.
- Проактивный режим (2026-09-26, решения заказчика): агент сам следит за выкатками и алертами и
  кладёт уведомления в ленту; куда слать — решает клиент (бот: `NOTIFY_CHAT_IDS`), удобное
  управление маршрутами — позже, через контекст беседы (подписки). Приглушение — у каждой беседы
  своё (на время или навсегда, видно, что скрыто, можно вернуть). Персональные данные в
  уведомлениях — как в ответах (доступ к боту только у сотрудников). Только сообщает — действий нет.
- Ответ синхронный (асинхронного режима пока нет — решение заказчика).

Конвенции этого стека вынесены в глобальные Claude Code скиллы (`golang-service`,
`golang-samber-lo`, `crud`, `mobone`) — они подхватываются автоматически по описанию.

---

## Структура проекта

### Верхний уровень
- `cmd/main.go` — entrypoint, поднимает `internal/app.App`; `cmd/eval` — эталонные вопросы.
- `internal/` — бизнес-логика и инфраструктура (закрытые пакеты).
- `docs/` — контракт API (`agent-api.md`), выдаётся через `/docs/*`.
- `migrations/` — SQL миграции (golang-migrate): `000001_init` — журнал, `000002_notify` — беседы,
  сигналы, уведомления, приглушения (сервис уже в проде — изменения схемы только новыми файлами).
- `evals/` — эталонные вопросы (`cases.yaml`) и эталонный прогон (`baseline.json`).
- `Dockerfile`, `Makefile` — сборка (`make build` подставляет версию через ldflags).
- `.env.example` — пример окружения.

### Внутренние пакеты (`internal/`)
- `internal/app/` — сборка приложения: DI, серверы.
  - `app.go` — граф зависимостей, выбор LLM-провайдера по `LLM_PROVIDER`, ключи клиентов
    (`API_KEYS`), жизненный цикл.
  - `http_server.go` — сервер API (`HTTP_PORT`, дефолт 80): `WriteTimeout` = `AGENT_TIMEOUT` + запас,
    запросы в контексте приложения (остановка отменяет разборы).
  - `system_http_server.go` — системный HTTP-сервер (`SYSTEM_HTTP_PORT`, дефолт 3003):
    /healthcheck, /docs/*, /metrics.
  - `migration.go` — `ensureDatabase` (базы из `PG_DSN` нет — создаёт её, нужно право CREATEDB)
    и миграции из `migrations/`; `pgpool.go` — пул на 3 соединения (pulse-pg общий).
- `internal/config/` — конфигурация через env (`config.go`).
- `internal/handler/http/` — JSON-API: `POST /v1/ask`, `POST /v1/reset`, `POST /v1/eval` (прогон
  эталонов — системам из `EVAL_CLIENTS`); bearer-ключ → имя системы (`withClient`, сравнение за
  постоянное время); `dto/` — контракт (`docs/agent-api.md`), неизвестные поля запроса — 400; коды
  ошибок `invalid_request`/`unauthorized`/`forbidden`/`busy`/`canceled`/`timeout`/`internal`.
  `debug.go` — ручки разработчика (только `DEBUG_TOKEN`; он же годится для `/v1/*` как система
  `debug`): `POST /debug/eval`, `GET /debug/eval/last`,
  `GET /debug/recent?client=&outcome=&limit=&before_id=` (список без ответа и хода разбора, новые —
  первыми, `before_id` — листать), `GET /debug/journal/{id}` (вопрос целиком: ответ, вызовы
  инструментов с аргументами и ответами pulse; нет — 404 `not_found`), `GET /debug/stats?window=7d`
  (сводка за окно: `Nd` или длительность Go, по умолчанию 7d, не больше срока хранения),
  `GET /debug/info`. Прогон в сервисе идёт тем же путём, что вопрос API
  (`answer`), от системы `eval`.
- `internal/usecase/ask/` — вопрос: формат, «один вопрос за раз на беседу», история беседы (ключ —
  `клиент/conversation_id`: беседы систем не пересекаются; без conversation_id — без истории),
  агент, метрики по клиентам.
- `internal/usecase/notify/` — лента уведомлений для беседы (курсор беседы: первый вызов подписывает
  с текущего места, `Ack` — получено), приглушения, заметки беседы (`/v1/notifications`, `/v1/mutes`,
  `/v1/chat`; `handler/http/notify.go`, без хранилища — 503 `unavailable`).
- `internal/usecase/monitor/` — мониторинг для `/debug/*`: последние вопросы, вопрос целиком,
  сводка по журналу за окно, сведения об агенте и доступность pulse (каталог инструментов).
- `internal/domain/journal/` — журнал вопросов (кто, что, исход, время, инструменты, токены,
  итоговый ответ и ход разбора — вызовы с аргументами и ответами pulse целиком): таблица `journal`
  (`repo/db`; ход разбора — jsonb `trace` байтовым способом через repo-локальную `traceJSON`,
  аргументы — JSON-объектом, `answer`/`trace` сжаты lz4; список читает только лёгкие колонки
  `BriefColumns`), без `PG_DSN` — кольцо последних `JOURNAL_SIZE` в памяти (`repo/mem`); сводка по
  системам и инструментам; пишет usecase `ask` на каждый вопрос, включая отказы.
- `internal/domain/chat/` — контекст беседы (таблица `chat`, ключ — клиент и conversation_id): заметки
  (до 2000 символов — идут модели первым сообщением перед историей, общий промпт не меняется) и курсор
  ленты (`notify_cursor`, null — беседа ещё не читала ленту; не откатывается).
- `internal/domain/notify/` — сигналы наблюдателя (`signal`: ключ — дедупликация; выкатка — один раз,
  алерт сервиса — снова, только если прошлый закрыт раньше `WATCH_ALERT_REPEAT`), уведомления
  (`notification`, общие для бесед) и приглушения (`mute`: пустое поле — любое, `until` null — навсегда;
  `Covers`/`Active`); лента беседы помечает приглушённые (`MutedBy`), `Muted` — что скрыто. Чистка:
  уведомления — по сроку журнала, закрытые сигналы — неделя, истёкшие приглушения — сутки.
- `internal/domain/dialog/` — история беседы: пары «вопрос — итоговый ответ» (без вызовов
  инструментов), последние N, сброс после тишины; `repo/mem` — в памяти процесса.
- `internal/service/` — сервисные модули (раскладка — скилл `golang-service`):
  - `agent` — агентный цикл: шаги модели, параллельные вызовы pulse (`errgroup`), лимит вызовов
    и времени (последняя минута — только на финальный ответ, без инструментов), системный промпт
    (`service/constant/prompts.go`), метрики LLM и инструментов; ход разбора — `Result.Trace`.
    - Формат ответа (`Req.Format`: `telegram` по умолчанию, `markdown`, `plain`, `json`) — блок
      «Оформление ответа» в конце системного промпта: общий префикс у всех форматов один (кэш
      провайдера). `json` — структурированный вывод по строгой схеме `constant/result.go`
      (`llm.Request.Output`): поля разбираются в `Result.Structured` (`structured.go`), `Answer`
      собирается из полей; не разобрался (оборванный ответ) — текст как есть, `Structured` nil.
    - Своя схема системы (RPC, `Req.ResponseSchema`): `schema.go` приводит её к strict (все поля в
      required, необязательные — nullable, `additionalProperties: false`, oneOf → anyOf) — клиент
      пишет обычную JSON Schema; в промпте — блок `clientSchemaRules`; ответ — `Result.Json` как
      есть. 400 провайдера (схема не принята) → `errs.InvalidRequest` → клиенту `400`.
    - Свой инструмент `render_chart` (`service/chart.go`, описание и схема — `constant/chart.go`),
      предлагается, только если клиент принимает графики (`Req.Charts`). Временные ряды —
      ссылкой `metrics` (service + metric_id) на ответ `query_metrics` этого разбора: точки не
      переписываются моделью; `series` — свои точки для небольших данных. До 3 графиков на ответ;
      `Chart.Spec` — данные графика (клиент может нарисовать сам).
  - `llm` — провайдер-независимый контракт: фасад `Provider` (`interface.go`: шаг, `Ping` для ручки
    состояния), модели шага
    (`model/`, `Request.Output` — JSON Schema итогового ответа). Адаптеры —
    `llm/<provider>/service`; сейчас `openai` (`text.format: json_schema`, strict).
  - `pii` — персональные данные токенами на границе с моделью (стандарт pulse, «Персональные данные —
    токенами»; pulse отдаёт данные как есть, прячет их только агент): `pii:<вид>:<12 букв a–p>` —
    HMAC с `PII_TOKEN_KEY` от значения, приведённого к одному виду (телефон — цифры с кодом страны
    `PII_PHONE_COUNTRY_CODE`); память «токен → значение» 24 ч. `Mask` — вопрос и история (телефоны,
    email, карты — маской); `MaskToolOutput` — ответ pulse (плюс `personal_fields` ручек — по виду);
    `RevealArgs` — токены видов с поиском в аргументах вызова pulse (телефон — `+цифры`); `Reveal` —
    итоговый ответ и подписи графиков. В журнал и историю беседы — то, что видела модель
    (`Result.ModelAnswer`, вопрос через `Mask`); клиенты токенов не видят (кроме `trace`).
  - `watch` — наблюдатель (`WATCH_ENABLED`, нужен `PG_DSN`): раз в `WATCH_INTERVAL` — `get_timeline`
    scope=cluster (окно 30m) → сигналы `deploy`/`alert_firing` (алерты severity none — нет). Выкатка
    через `WATCH_DEPLOY_DELAY`: сменилась следующей — `superseded`; снапшот healthy — `healthy` (без
    модели); иначе разбор. Алерт — разбор сразу. Разбор — `agent.Run` со схемой `verdictSchema`
    (notify, severity, title, text); notify=false — `quiet`. Сверх `WATCH_MAX_RUNS_PER_HOUR` разборов
    или после 3 неудачных попыток (повтор через 5 мин) — уведомление без разбора (`raw`). Разборы — в
    журнале как система `watch` (беседа — ключ сигнала). Метрика `watch_signal_total{kind,outcome}`.
  - `chattools` — инструменты беседы для модели (только в вопросе с conversation_id и с хранилищем;
    выполняет агент, не pulse): `chat_notes_save`, `notifications_mute`, `notifications_unmute`,
    `notifications_mutes`. Аргументы — настоящими значениями (`pii.Reveal`), ответ — модели токенами.
  - `pulse` — MCP-клиент pulse: ленивое подключение, переподключение при потере сессии,
    bearer-токен, каталог инструментов перечитывается на каждый разбор.
  - `retention` — фоновая чистка журнала: раз в час удаляет записи старше
    `JOURNAL_RETENTION_DAYS` (первый проход — на старте); только при журнале в Postgres.
  - `chart` — графики (`gonum.org/v1/plot`, PNG в памяти, шрифты с кириллицей вшиты): `line` —
    ряды во времени (ось по Алматы, круглые метки), `bar` — горизонтальные столбцы с подписями
    значений; единицы (`bytes`, `ratio`, `seconds`, `cores`, `rps`) — в привычные (МБ десятичные,
    как в тексте ответа; %, мс, запросы в минуту). Ось линий от нуля, если ряд опускается ниже
    половины максимума, иначе — по данным с полями. 1152×648; тема `CHART_THEME` (`dark` по
    умолчанию, `light`).
- `internal/eval/` — эталонные вопросы (см. ниже): проверки, прогон (`Runner` с `AskFunc` — по HTTP
  или в сервисе), отчёт, `Keeper` — прогоны в сервисе (один за раз, последний — в памяти).
- `evals/` — `cases.yaml` и `baseline.json`, вшиты в образ (`embed.go`).
- `internal/infra/httpx/` — единая фабрика http-клиентов (таймауты, лимиты; все клиенты только через неё).
- `internal/infra/pulsekit/` — манифест сервиса по стандарту pulse (`pulse/docs/service-manifest.md`; копия
  эталон — gotemplate `internal/infra/pulsekit`; копии в pulse и здесь совпадают с ним файл в файл). Манифест агента —
  `app/manifest.go`: команда platform, критичность low, зависимости pulse и llm (критичные; `Provider.Ping` —
  без генерации) и журнал в Postgres, ручка `question_stats` (агрегаты журнала за 1h/24h/7d, без текстов и
  имён). На системном порту: `/.well-known/pulse`, `/.well-known/pulse/status`, `/diag/questions`.
  Коммит сборки — `constant.Commit` (Makefile, ldflags).
- `internal/infra/metrics/` — реестр Prometheus.
- `internal/errs/` и `internal/constant/` — общие коды ошибок и константы (`Timezone` — Asia/Almaty).

---

## Архитектура: слои и зависимости

- **Transport** (`internal/handler/http`):
  - Работает только с usecase-интерфейсами и моделями usecase; DTO — только в `dto/`.
- **Usecase** (`internal/usecase/*`):
  - Входной слой от транспортного слоя (запросы от внешних систем).
  - Валидация входных параметров.
  - Оркестрация доменных сервисов и сервисов `internal/service/*`.
  - Вход/выход — доменные модели и модели usecase.
  - Желательно не обращается в соседние usecases.
- **Domain** (`internal/domain/*`):
  - `model/` — структуры данных, сущности (entity).
    - **Запрещены теги сериализации** (`json:"..."`, `yaml:"..."` и т.п.) на полях доменной
      модели — она не знает про транспорт и про формат хранения.
  - `service/` — доменные операции и инварианты. Может использовать только репозиторий.
  - `repo/` — доступ к хранилищам. Может содержать подпапки для разных типов хранилищ или моков.
- **Service** (Infrastructure/background/external integrations, `internal/service/*`):
  - Фоновые процессы, интеграции с внешними системами.
  - Выделенные или переиспользуемые логики.
  - Может использовать другие сервисы `internal/service/*` и доменные сервисы `internal/domain/*/service`.
  - Не обращается в usecase слой.
- **Composition** (`internal/app/`):
  - Сборка зависимостей, запуск серверов.

### Правило зависимостей
```
handler        → usecase
usecase        → domain service
usecase        → service
service        → service
service        → domain service
domain service → repo
```
- Обратные зависимости **запрещены**.
- К `repo` слою доступ только из `domain service`.

### LLM-провайдеры
- Агентный цикл знает только контракт `internal/service/llm/model` (шаг: запрос → текст и/или
  вызовы инструментов). Служебное состояние провайдера внутри разбора — `Response.State`
  (непрозрачно для цикла); между вопросами в историю идёт только текст.
- Новый провайдер = новый адаптер `internal/service/llm/<provider>/service` + ветка в `switch` по
  `LLM_PROVIDER` в `app.go` + константа `constant.LlmProvider*`. Цикл не трогаем. Адаптер обязан
  поддержать `Request.Output` (структурированный вывод) — на нём формат `json`.
- OpenAI: Responses API **без хранения** (`store=false`): в запросы уходят логи и конфигурация
  сервисов из pulse. Контекст разбора (вход + выходные элементы, включая
  `reasoning.encrypted_content`) накапливается в `State` и отправляется целиком.
- Модель и параметры — только из конфига (`LLM_MODEL`, `LLM_REASONING_EFFORT`), не в коде.
- Системный промпт стабилен между разборами (на нём держится кэш префикса у провайдера): всё
  переменное — время, вопрос — в сообщении пользователя; оформление под формат — в самом конце.

### Ошибки и валидация
- Семантические ошибки — через `internal/errs` (`InvalidRequest`, `Busy`); handler переводит их
  в HTTP-статус и `code`.
- Ошибка вызова инструмента не роняет разбор: уходит модели текстом с префиксом `ERROR: `.
- Нельзя пробрасывать ошибки наружу без wrapping (оборачивать в `fmt.Errorf("...: %w")`).
- Для работы с ошибками всегда используй `errors.Is` и `errors.AsType`. Избегай прямого
  сравнения ошибок (`==`) и type assertion (`err.(*MyError)`), чтобы корректно обрабатывать
  обёрнутые ошибки.

### Правила изменения кода
- Модели usecase и сервисов не протекают в транспорт дальше handler'а; контракт — только `dto/`.
- В тестах всегда предпочитай `testify`: `require` для проверок, прерывающих тест,
  и `assert` для остальных утверждений.
- При реализации worker pool / параллельной обработки используй `errgroup`
  (golang.org/x/sync/errgroup), а не ручное управление горутинами через `sync.WaitGroup` + каналы.
- http-клиенты — только через `internal/infra/httpx`. Клиенты внешних API с ключами (OpenAI) — с
  проверкой TLS (`VerifyTLS: true`); у клиента LLM `ResponseHeaderTimeout` = таймаут разбора (ответ
  без стриминга приходит целиком после генерации).

---

## Композиционный корень (`internal/app/app.go`)

`app.go` — единственная точка композиции приложения: здесь собирается граф зависимостей
(`repo → service → usecase → handler`) и описывается жизненный цикл. Бизнес-логики тут нет —
только связывание компонентов и управление их запуском/остановкой.

### Тип `App`
- В поля выносится **только то, чем нужно управлять после `Init`**: серверы (останавливать),
  сессия pulse и pgx pool (закрывать), чистка журнала (ждать), корневой `ctx` с его `ctxCancel` и
  `exitCode`.
- Локальные звенья графа (repo, service, usecase, handler) — локальные переменные внутри `Init`.

### Импорты Композиционного корня
- Группируются блоками с пустой строкой между группами: стандартная библиотека → внешние
  зависимости → внутренние пакеты проекта.
- Внутренние пакеты-конструкторы импортируются с суффиксом-алиасом `P`, и в алиасе прописывается весь путь в camel-case
  (например, `internal/service/pulse/service` -> `servicePulseServiceP`, `internal/handler/http` -> `handlerHttpP`).

### Методы-фазы жизненного цикла
Фиксированный набор методов, каждый делает ровно одно:
- `Init` — создание и связывание всех зависимостей.
- `PreStartHook` — действия перед стартом.
- `Start` — запуск чистки журнала и серверов.
- `Listen` — блокировка до сигнала ОС (`SIGINT`/`SIGTERM`).
- `Stop` — отмена контекста (отменяет идущие разборы — клиенты получают 503 `canceled`) и
  graceful-остановка серверов.
- `WaitJobs` — ожидание фоновых задач (чистка журнала).
- `Exit` — закрытие ресурсов (сессия pulse, pgx pool) и выход с `exitCode`.

### Стиль `Init`
- Сборка идёт **сверху вниз в порядке зависимостей**: инфраструктура (логгер, база журнала и
  миграции) → сервисы (llm,
  pulse, chart, agent) → доменные блоки → usecase → транспорт → серверы.
- Каждый логический блок предваряется коротким комментарием-меткой в нижнем регистре.
- Блоки, которым не нужны внешние переменные, оборачиваются в анонимный блок `{ ... }`.

### Обработка ошибок при инициализации
- Используется хелпер `errCheck(err, msg)`: на этапе сборки любая ошибка фатальна
  (лог + `os.Exit(1)`). Невалидный `API_KEYS` — фатально; пустой — предупреждение (все запросы 401).
- Недоступный pulse — **не** ошибка старта: MCP-сессия поднимается лениво.

### Конфигурация
- Все параметры берутся из единого глобального конфига (`config.Conf.*`) прямо в месте
  использования. Весь env сервиса — в kusec (app `pulse`, configmap/secret `agent`), у деплоймента
  своих env нет.

---

## Runtime и конфигурация

### Переменные окружения
- Описаны в `internal/config/config.go`, пример — `.env.example`.
- Обязательные: `PULSE_MCP_URL`; для OpenAI — `OPENAI_API_KEY`; `API_KEYS` — `имя:ключ` через запятую
  (секрет; имя системы — в журнале и метриках). `DEBUG_TOKEN` — ключ разработчика (секрет; пусто —
  `/debug` закрыт); `EVAL_CLIENTS` — кому можно `/v1/eval` (бот); `EVAL_PARALLEL` (3),
  `EVAL_TIMEOUT` (20m). Журнал: `PG_DSN` (секрет; `postgres://…@pulse-pg.default:5432/pulse_agent`,
  пусто — в памяти), `JOURNAL_RETENTION_DAYS` (90), `JOURNAL_SIZE` (500, только в памяти). Персональные данные:
  `PII_TOKEN_KEY` (секрет, ≥32 случайных символа; пусто — токены меняются после рестарта),
  `PII_PHONE_COUNTRY_CODE` (7). Наблюдатель: `WATCH_ENABLED` (true), `WATCH_INTERVAL` (1m),
  `WATCH_DEPLOY_DELAY` (15m), `WATCH_ALERT_REPEAT` (6h), `WATCH_MAX_RUNS_PER_HOUR` (20).

### Деплой
- Чарт — `helm-zeon/charts/pulse` (`templates/agent.yaml`, Deployment `pulse-agent`, одна реплика:
  история бесед в памяти). Образ `ghcr.io/mechta-market/pulse_agent:latest` собирает CI на push в
  master, keel перекатывает под.
- Внутри кластера: `http://pulse-agent.default/v1/ask`; наружу — через ruto.

### Эталонные вопросы (`make eval`)
- `evals/cases.yaml` — вопросы с проверками поведения (данные живые, числа не сверяются): какие
  инструменты вызваны и с какими аргументами (`calls`, `no_calls`, `tool_limits`, `first_call`),
  регэкспы по ответу (общие запреты единиц — `defaults`: мCPU, МиБ, сырые байты), графики,
  лимиты времени/вызовов/токенов; `format: json` — дополнительно проверяется `result`.
  `\b` в Go-регэкспах — только латиница: границы кириллицы — через `\P{L}`.
- Три способа прогона: `make eval` с машины (`cmd/eval`, вопросы из файла — новые вопросы до
  деплоя; через `POST /v1/ask`, `trace: true`); `POST /debug/eval` (вшитые вопросы, в сервисе);
  `/eval` в Telegram для админов бота (`POST /v1/eval`). Отчёт сравнивается с
  `evals/baseline.json` (что сломалось/починилось, время, токены — по тем же вопросам).
- Ключ разработчика (агент Claude Code) — `DEBUG_TOKEN` в `~/.config/pulse_agent/debug_token`
  (подставлять через `$(tr -d '\n' < …)`, не печатать); адрес — `EVAL_URL` (по умолчанию прод
  через ruto: `https://api.mdev.kz/pulse_agent`).
- Правка промпта/агента → `make eval` → если лучше, обновить baseline (без текстов ответов:
  `jq '.cases |= map(del(.answer, .result))'`). Вопрос о данных, которые уходят из хранения
  логов, — с `skip_after`.

### Метрики
- Prometheus на `/metrics` (системный сервер) при `WITH_METRICS=true`, реестр `metrics.Registry`.
- `question_total{client,outcome}`, `answer_duration_seconds{client}`, `llm_request_total{provider,status}`,
  `llm_request_duration_seconds`, `llm_tokens_total{provider,type}`, `tool_call_total{tool,status}`,
  `tool_call_duration_seconds`, `agent_run_steps`.

### Сборка
- `make build` создаёт бинарник `cmd/build/svc`.
- Dockerfile копирует бинарник, `docs/` и `migrations/` в `/app`.

### Flow проверки изменений
```
gofmt  →  go vet ./...  →  go test ./...  →  make eval (после деплоя)
```

### Тестовый стенд
- Postgres — контейнер `pulse-pg` из стенда pulse (`localhost:5440`, `postgres/postgres`); база
  журнала `pulse_agent` создаётся агентом сама: `PG_DSN=postgres://postgres:postgres@localhost:5440/pulse_agent?sslmode=disable`.
  Живой тест репозиториев: `PG_LIVE_DSN=<тот же DSN> go test ./internal/domain/journal/repo/db/ ./internal/domain/notify/repo/db/ -run TestLive -v`
  (базу `pulse_agent` создать заранее: `docker exec pulse-pg psql -U postgres -c "create database pulse_agent"`).
- Локально: pulse — по стенду pulse (`localhost:9091/mcp`, `devtoken`), агент —
  `HTTP_PORT=9092 SYSTEM_HTTP_PORT=3014 API_KEYS=dev:devkey`, вопрос:
  ```
  curl -s -X POST localhost:9092/v1/ask -H "Authorization: Bearer devkey" \
    -H "Content-Type: application/json" -d '{"question":"что с caravan?","format":"json"}'
  ```
