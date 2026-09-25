-- журнал вопросов агенту: кто спросил, что ответил, как к этому пришёл (мониторинг и разбор
-- ответов). Записи не меняются; старше JOURNAL_RETENTION_DAYS удаляются фоном.
create table if not exists journal (
    id              bigserial primary key,
    at              timestamptz not null,
    client          text not null,
    conversation_id text not null default '',
    user_id         text not null default '',
    user_name       text not null default '',
    question        text not null,
    format          text not null default '',
    client_schema   boolean not null default false,
    outcome         text not null,
    error           text not null default '',
    incomplete      text not null default '',
    duration_ms     bigint not null default 0,
    steps           integer not null default 0,
    tool_calls      integer not null default 0,
    tools           text[] not null default '{}',
    input_tokens    bigint not null default 0,
    cached_tokens   bigint not null default 0,
    output_tokens   bigint not null default 0,
    charts          integer not null default 0,
    answer          text not null default '',
    -- вызовы инструментов с аргументами и ответами pulse целиком (до ~100 KB на вызов)
    trace           jsonb
);

-- ответы pulse — JSON с повторами: lz4 сжимает их в разы и быстрее pglz
alter table journal alter column trace set compression lz4;
alter table journal alter column answer set compression lz4;

create index if not exists journal_at_idx on journal (at);
create index if not exists journal_client_id_idx on journal (client, id desc);
