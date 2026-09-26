package service

import (
	"context"
	"time"

	"github.com/mechta-market/pulse_agent/internal/domain/chat/model"
)

type RepoI interface {
	// Get — беседа; nil — о ней ещё ничего не записано.
	Get(ctx context.Context, client, conversationId string) (*model.Chat, error)
	SaveNotes(ctx context.Context, client, conversationId, notes, by string, at time.Time) error
	SetCursor(ctx context.Context, client, conversationId string, cursor int64) error
}
