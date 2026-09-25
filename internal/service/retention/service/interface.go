package service

import (
	"context"
	"time"
)

// JournalI — журнал вопросов (domain/journal/service).
type JournalI interface {
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)
}
