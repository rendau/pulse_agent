package service

import (
	"context"
	"time"

	"github.com/rendau/pulse_agent/internal/domain/journal/model"
)

type RepoI interface {
	// Append сохраняет запись и проставляет ей Id.
	Append(ctx context.Context, e *model.Entry) error
	// List — записи по фильтру, новые — первыми, без Answer и Trace.
	List(ctx context.Context, f model.Filter) ([]*model.Entry, error)
	// Get — запись целиком; nil — нет такой.
	Get(ctx context.Context, id int64) (*model.Entry, error)
	// DeleteBefore удаляет записи старше before, возвращает сколько удалено.
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}
