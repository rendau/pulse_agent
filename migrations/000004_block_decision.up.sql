-- решения админов по уведомлениям security (кнопки в боте): заблокировать адрес атаки в ruto или
-- нет. Итоговое — успешная блокировка, одна на уведомление; «не блокировать» — не итоговое,
-- пишется первое; отказ ruto (refused не пуст) — попытка, их может быть несколько.
create table if not exists block_decision (
    id              bigserial primary key,
    notification_id bigint not null references notification (id) on delete cascade,
    -- block | skip
    action          text not null,
    -- срок блокировки как его выбрали: 24h | 168h; у skip — пусто
    duration        text not null default '',
    -- сеть, как её записал ruto (203.0.113.7/32); у skip и отказа — ключ уведомления
    ip              text not null,
    -- конец блокировки; null — без конца (сеть уже заблокировал человек) или не блок
    until           timestamptz,
    ruto_block_id   text not null default '',
    -- agent | admin (ruto вернул блок человека как есть)
    ruto_source     text not null default '',
    -- отказ ruto словами; не пусто — не заблокировано
    refused         text not null default '',
    by_id           text not null default '',
    by_name         text not null default '',
    client          text not null,
    conversation_id text not null,
    at              timestamptz not null
);

create index if not exists block_decision_notification_idx on block_decision (notification_id);

-- одно итоговое решение на уведомление и одно «не блокировать» (при одновременных нажатиях)
create unique index if not exists block_decision_final_uniq on block_decision (notification_id)
    where action = 'block' and refused = '';
create unique index if not exists block_decision_skip_uniq on block_decision (notification_id)
    where action = 'skip';
