// Package db — решения по уведомлениям security в Postgres (таблица block_decision).
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rendau/pulse_agent/internal/domain/block/model"
	commonRepoPg "github.com/rendau/pulse_agent/internal/domain/common/repo/pg"
)

type Repo struct {
	*commonRepoPg.Base
}

func New(con *pgxpool.Pool) *Repo {
	return &Repo{Base: commonRepoPg.NewBase(con)}
}

const decisionColumns = `id, notification_id, action, duration, ip, until, ruto_block_id, ruto_source, refused,
	by_id, by_name, client, conversation_id, at`

func (r *Repo) ListDecisions(ctx context.Context, notificationId int64) ([]*model.Decision, error) {
	rows, err := r.Con.Query(ctx, `select `+decisionColumns+` from block_decision where notification_id = $1 order by id`, notificationId)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*model.Decision, error) {
		d := &model.Decision{}
		err := row.Scan(&d.Id, &d.NotificationId, &d.Action, &d.Duration, &d.Ip, &d.Until, &d.RutoBlockId, &d.RutoSource,
			&d.Refused, &d.ById, &d.ByName, &d.Client, &d.ConversationId, &d.At)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("CollectRows: %w", err)
	}
	return items, nil
}

// CreateDecision — on conflict do nothing: частичные уникальные индексы держат одно итоговое
// решение и одно «не блокировать» на уведомление, в том числе при одновременных вызовах.
func (r *Repo) CreateDecision(ctx context.Context, d *model.Decision) (bool, error) {
	err := r.Con.QueryRow(ctx, `
		insert into block_decision (notification_id, action, duration, ip, until, ruto_block_id, ruto_source, refused,
		                            by_id, by_name, client, conversation_id, at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		on conflict do nothing
		returning id`,
		d.NotificationId, d.Action, d.Duration, d.Ip, d.Until, d.RutoBlockId, d.RutoSource, d.Refused,
		d.ById, d.ByName, d.Client, d.ConversationId, d.At).Scan(&d.Id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("Con.QueryRow: %w", err)
	}
	return true, nil
}
