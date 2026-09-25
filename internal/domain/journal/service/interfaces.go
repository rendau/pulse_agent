package service

import (
	"context"

	"github.com/mechta-market/pulse_agent/internal/domain/journal/model"
)

type RepoI interface {
	Append(ctx context.Context, e *model.Entry) error
	List(ctx context.Context) ([]*model.Entry, error)
}
