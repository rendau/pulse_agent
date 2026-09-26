// Package db — сигналы, уведомления и приглушения в Postgres (таблицы signal, notification, mute).
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	commonRepoPg "github.com/rendau/pulse_agent/internal/domain/common/repo/pg"
	"github.com/rendau/pulse_agent/internal/domain/notify/model"
)

type Repo struct {
	*commonRepoPg.Base
}

func New(con *pgxpool.Pool) *Repo {
	return &Repo{Base: commonRepoPg.NewBase(con)}
}

const signalColumns = `key, kind, service, at, due_at, status, attempts, summary, details, outcome, notification_id, done_at`

func (r *Repo) TouchSignal(ctx context.Context, s *model.Signal, repeatAfter time.Duration, now time.Time) (bool, error) {
	rows, err := r.Con.Query(ctx, `
		insert into signal (key, kind, service, at, due_at, status, summary, details)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		on conflict (key) do update
		set at = excluded.at, due_at = excluded.due_at, status = excluded.status, attempts = 0,
		    summary = excluded.summary, details = excluded.details, outcome = '', notification_id = null, done_at = null
		where $9::bigint > 0 and signal.status = 'done' and signal.done_at < $10::timestamptz - $9::bigint * interval '1 microsecond'
		returning key`,
		s.Key, s.Kind, s.Service, s.At, s.DueAt, s.Status, s.Summary, s.Details, repeatAfter.Microseconds(), now)
	if err != nil {
		return false, fmt.Errorf("Con.Query: %w", err)
	}
	defer rows.Close()
	touched := rows.Next()
	if err = rows.Err(); err != nil {
		return false, fmt.Errorf("rows: %w", err)
	}
	return touched, nil
}

func (r *Repo) DueSignals(ctx context.Context, now time.Time, limit int) ([]*model.Signal, error) {
	rows, err := r.Con.Query(ctx, `select `+signalColumns+` from signal
		where status = 'pending' and due_at <= $1 order by due_at limit $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	signals, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*model.Signal, error) {
		s := &model.Signal{}
		err := row.Scan(&s.Key, &s.Kind, &s.Service, &s.At, &s.DueAt, &s.Status, &s.Attempts, &s.Summary, &s.Details,
			&s.Outcome, &s.NotificationId, &s.DoneAt)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("CollectRows: %w", err)
	}
	return signals, nil
}

func (r *Repo) NewerDeploy(ctx context.Context, service string, at time.Time) (bool, error) {
	var exists bool
	err := r.Con.QueryRow(ctx, `select exists(select 1 from signal where kind = $1 and service = $2 and at > $3)`,
		model.KindDeploy, service, at).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("Con.QueryRow: %w", err)
	}
	return exists, nil
}

func (r *Repo) FinishSignal(ctx context.Context, key, outcome string, notificationId *int64, at time.Time) error {
	_, err := r.Con.Exec(ctx, `update signal set status = $2, outcome = $3, notification_id = $4, done_at = $5 where key = $1`,
		key, model.SignalDone, outcome, notificationId, at)
	if err != nil {
		return fmt.Errorf("Con.Exec: %w", err)
	}
	return nil
}

func (r *Repo) RetrySignal(ctx context.Context, key string, dueAt time.Time) error {
	_, err := r.Con.Exec(ctx, `update signal set attempts = attempts + 1, due_at = $2 where key = $1`, key, dueAt)
	if err != nil {
		return fmt.Errorf("Con.Exec: %w", err)
	}
	return nil
}

func (r *Repo) DeleteSignalsBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Con.Exec(ctx, `delete from signal where status = $1 and done_at < $2`, model.SignalDone, before)
	if err != nil {
		return 0, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected(), nil
}

const notificationColumns = `id, at, kind, service, key, severity, title, text, investigated, signal_key`

func scanNotification(row pgx.CollectableRow) (*model.Notification, error) {
	n := &model.Notification{}
	err := row.Scan(&n.Id, &n.At, &n.Kind, &n.Service, &n.Key, &n.Severity, &n.Title, &n.Text, &n.Investigated, &n.SignalKey)
	return n, err
}

func (r *Repo) CreateNotification(ctx context.Context, n *model.Notification) error {
	err := r.Con.QueryRow(ctx, `
		insert into notification (at, kind, service, key, severity, title, text, investigated, signal_key)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning id`,
		n.At, n.Kind, n.Service, n.Key, n.Severity, n.Title, n.Text, n.Investigated, n.SignalKey).Scan(&n.Id)
	if err != nil {
		return fmt.Errorf("Con.QueryRow: %w", err)
	}
	return nil
}

func (r *Repo) GetNotification(ctx context.Context, id int64) (*model.Notification, error) {
	rows, err := r.Con.Query(ctx, `select `+notificationColumns+` from notification where id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	n, err := pgx.CollectOneRow(rows, scanNotification)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("CollectOneRow: %w", err)
	}
	return n, nil
}

func (r *Repo) ListNotifications(ctx context.Context, f model.NotificationFilter) ([]*model.Notification, error) {
	q := r.QB.Select(notificationColumns).From("notification").OrderBy("id")
	if f.AfterId > 0 {
		q = q.Where("id > ?", f.AfterId)
	}
	if !f.Since.IsZero() {
		q = q.Where("at >= ?", f.Since)
	}
	if f.Limit > 0 {
		q = q.Limit(uint64(f.Limit))
	}
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	rows, err := r.Con.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	items, err := pgx.CollectRows(rows, scanNotification)
	if err != nil {
		return nil, fmt.Errorf("CollectRows: %w", err)
	}
	return items, nil
}

func (r *Repo) LastNotificationId(ctx context.Context) (int64, error) {
	var id int64
	if err := r.Con.QueryRow(ctx, `select coalesce(max(id), 0) from notification`).Scan(&id); err != nil {
		return 0, fmt.Errorf("Con.QueryRow: %w", err)
	}
	return id, nil
}

func (r *Repo) CountInvestigatedSince(ctx context.Context, since time.Time) (int, error) {
	var n int
	if err := r.Con.QueryRow(ctx, `select count(*) from signal where done_at >= $1 and outcome in ($2, $3)`,
		since, model.OutcomeNotified, model.OutcomeQuiet).Scan(&n); err != nil {
		return 0, fmt.Errorf("Con.QueryRow: %w", err)
	}
	return n, nil
}

func (r *Repo) DeleteNotificationsBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Con.Exec(ctx, `delete from notification where at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected(), nil
}

const muteColumns = `id, client, conversation_id, service, kind, key, until, note, created_at, created_by`

func (r *Repo) CreateMute(ctx context.Context, m *model.Mute) error {
	err := r.Con.QueryRow(ctx, `
		insert into mute (client, conversation_id, service, kind, key, until, note, created_at, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9) returning id`,
		m.Client, m.ConversationId, m.Service, m.Kind, m.Key, m.Until, m.Note, m.CreatedAt, m.CreatedBy).Scan(&m.Id)
	if err != nil {
		return fmt.Errorf("Con.QueryRow: %w", err)
	}
	return nil
}

func (r *Repo) ListMutes(ctx context.Context, client, conversationId string) ([]*model.Mute, error) {
	rows, err := r.Con.Query(ctx, `select `+muteColumns+` from mute where client = $1 and conversation_id = $2 order by id`,
		client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*model.Mute, error) {
		m := &model.Mute{}
		err := row.Scan(&m.Id, &m.Client, &m.ConversationId, &m.Service, &m.Kind, &m.Key, &m.Until, &m.Note, &m.CreatedAt, &m.CreatedBy)
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("CollectRows: %w", err)
	}
	return items, nil
}

func (r *Repo) DeleteMute(ctx context.Context, client, conversationId string, id int64) (bool, error) {
	tag, err := r.Con.Exec(ctx, `delete from mute where id = $1 and client = $2 and conversation_id = $3`, id, client, conversationId)
	if err != nil {
		return false, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *Repo) DeleteMutesExpiredBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.Con.Exec(ctx, `delete from mute where until < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected(), nil
}

const subscriptionColumns = `id, client, conversation_id, service, kind, min_severity, note, created_at, created_by`

func (r *Repo) CreateSubscription(ctx context.Context, s *model.Subscription) error {
	err := r.Con.QueryRow(ctx, `
		insert into subscription (client, conversation_id, service, kind, min_severity, note, created_at, created_by)
		values ($1, $2, $3, $4, $5, $6, $7, $8) returning id`,
		s.Client, s.ConversationId, s.Service, s.Kind, s.MinSeverity, s.Note, s.CreatedAt, s.CreatedBy).Scan(&s.Id)
	if err != nil {
		return fmt.Errorf("Con.QueryRow: %w", err)
	}
	return nil
}

func (r *Repo) ListSubscriptions(ctx context.Context, client, conversationId string) ([]*model.Subscription, error) {
	rows, err := r.Con.Query(ctx, `select `+subscriptionColumns+` from subscription where client = $1 and conversation_id = $2 order by id`,
		client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("Con.Query: %w", err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*model.Subscription, error) {
		s := &model.Subscription{}
		err := row.Scan(&s.Id, &s.Client, &s.ConversationId, &s.Service, &s.Kind, &s.MinSeverity, &s.Note, &s.CreatedAt, &s.CreatedBy)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("CollectRows: %w", err)
	}
	return items, nil
}

func (r *Repo) DeleteSubscription(ctx context.Context, client, conversationId string, id int64) (bool, error) {
	tag, err := r.Con.Exec(ctx, `delete from subscription where id = $1 and client = $2 and conversation_id = $3`, id, client, conversationId)
	if err != nil {
		return false, fmt.Errorf("Con.Exec: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
