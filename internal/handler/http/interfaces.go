package http

import (
	"context"

	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	"github.com/mechta-market/pulse_agent/internal/eval"
	askModel "github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
	monitorModel "github.com/mechta-market/pulse_agent/internal/usecase/monitor/model"
)

type AskUsecaseI interface {
	Ask(ctx context.Context, q *askModel.Question) (*askModel.Answer, error)
	Reset(ctx context.Context, client, conversationId string) error
}

type MonitorUsecaseI interface {
	Recent(ctx context.Context, f journalModel.Filter) ([]*journalModel.Entry, error)
	Stats(ctx context.Context) (*journalModel.Stats, error)
	Info(ctx context.Context) *monitorModel.Info
}

// EvalKeeperI — прогоны эталонных вопросов на стороне сервиса (internal/eval.Keeper).
type EvalKeeperI interface {
	Run(ctx context.Context, ask eval.AskFunc, source string, only []string) (*eval.Report, error)
	Last() (*eval.Report, bool)
	Baseline() *eval.Report
}
