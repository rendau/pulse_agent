package service

import (
	"context"

	"github.com/rendau/pulse_agent/internal/domain/dialog/model"
)

type RepoI interface {
	List(ctx context.Context, conversationId string) ([]*model.Turn, error)
	Replace(ctx context.Context, conversationId string, turns []*model.Turn) error
}
