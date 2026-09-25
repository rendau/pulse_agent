package http

import (
	"context"

	askModel "github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

type AskUsecaseI interface {
	Ask(ctx context.Context, q *askModel.Question) (*askModel.Answer, error)
	Reset(ctx context.Context, client, conversationId string) error
}
