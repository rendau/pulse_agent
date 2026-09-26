// Package db — контекст бесед в Postgres (таблица chat).
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rendau/pulse_agent/internal/domain/chat/model"
	commonRepoPg "github.com/rendau/pulse_agent/internal/domain/common/repo/pg"
)

type Repo struct {
	*commonRepoPg.Base
}

func New(con *pgxpool.Pool) *Repo {
	return &Repo{Base: commonRepoPg.NewBase(con)}
}

func (r *Repo) Get(ctx context.Context, client, conversationId string) (*model.Chat, error) {
	chat := &model.Chat{Client: client, ConversationId: conversationId}
	err := r.Con.QueryRow(ctx, `
		select notes, notes_updated_at, notes_updated_by, notify_cursor
		from chat where client = $1 and conversation_id = $2`, client, conversationId,
	).Scan(&chat.Notes, &chat.NotesUpdatedAt, &chat.NotesUpdatedBy, &chat.NotifyCursor)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("Con.QueryRow: %w", err)
	}
	return chat, nil
}

func (r *Repo) SaveNotes(ctx context.Context, client, conversationId, notes, by string, at time.Time) error {
	_, err := r.Con.Exec(ctx, `
		insert into chat (client, conversation_id, notes, notes_updated_at, notes_updated_by)
		values ($1, $2, $3, $4, $5)
		on conflict (client, conversation_id) do update
		set notes = excluded.notes, notes_updated_at = excluded.notes_updated_at, notes_updated_by = excluded.notes_updated_by`,
		client, conversationId, notes, at, by)
	if err != nil {
		return fmt.Errorf("Con.Exec: %w", err)
	}
	return nil
}

func (r *Repo) SetCursor(ctx context.Context, client, conversationId string, cursor int64) error {
	_, err := r.Con.Exec(ctx, `
		insert into chat (client, conversation_id, notify_cursor) values ($1, $2, $3)
		on conflict (client, conversation_id) do update set notify_cursor = greatest(coalesce(chat.notify_cursor, 0), excluded.notify_cursor)`,
		client, conversationId, cursor)
	if err != nil {
		return fmt.Errorf("Con.Exec: %w", err)
	}
	return nil
}
