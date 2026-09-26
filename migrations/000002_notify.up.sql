-- контекст беседы системы-клиента (чат Telegram, тикет): заметки, которые попросили запомнить
-- (идут модели с каждым вопросом беседы), и докуда беседа получила уведомления
create table if not exists chat (
    client           text not null,
    conversation_id  text not null,
    notes            text not null default '',
    notes_updated_at timestamptz,
    notes_updated_by text not null default '',
    -- id последнего уведомления, которое беседа получила (null — ещё не читала ленту)
    notify_cursor    bigint,
    primary key (client, conversation_id)
);

-- сигналы наблюдателя: выкатки и алерты из pulse; ключ — дедупликация (выкатка — один раз,
-- алерт — не чаще раза в WATCH_ALERT_REPEAT)
create table if not exists signal (
    key             text primary key,
    kind            text not null,
    service         text not null,
    at              timestamptz not null,
    due_at          timestamptz not null,
    status          text not null,
    attempts        integer not null default 0,
    summary         text not null default '',
    details         jsonb,
    outcome         text not null default '',
    notification_id bigint,
    done_at         timestamptz
);

create index if not exists signal_due_idx on signal (due_at) where status = 'pending';

-- уведомления: разбор сигнала, одинаковый для всех бесед (приглушение — при выдаче)
create table if not exists notification (
    id           bigserial primary key,
    at           timestamptz not null,
    kind         text not null,
    service      text not null,
    key          text not null default '',
    severity     text not null default '',
    title        text not null,
    text         text not null,
    investigated boolean not null default false,
    signal_key   text not null default ''
);

create index if not exists notification_at_idx on notification (at);

-- приглушения беседы: что не присылать (пустое поле — любое) и до какого времени (null — навсегда)
create table if not exists mute (
    id              bigserial primary key,
    client          text not null,
    conversation_id text not null,
    service         text not null default '',
    kind            text not null default '',
    key             text not null default '',
    until           timestamptz,
    note            text not null default '',
    created_at      timestamptz not null,
    created_by      text not null default ''
);

create index if not exists mute_chat_idx on mute (client, conversation_id);
