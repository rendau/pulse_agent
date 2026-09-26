-- подписки беседы: что присылать (пустое поле — любое); нет подписок — приходит всё, есть — только
-- подходящее под одну из них. Приглушения действуют поверх подписок.
create table if not exists subscription (
    id              bigserial primary key,
    client          text not null,
    conversation_id text not null,
    service         text not null default '',
    kind            text not null default '',
    -- минимальная важность: info | warning | critical; пусто — любая
    min_severity    text not null default '',
    note            text not null default '',
    created_at      timestamptz not null,
    created_by      text not null default ''
);

create index if not exists subscription_chat_idx on subscription (client, conversation_id);
