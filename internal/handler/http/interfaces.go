package http

import (
	"context"

	chatModel "github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
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
	Entry(ctx context.Context, id int64) (*journalModel.Entry, error)
	Stats(ctx context.Context, window string) (*journalModel.Stats, error)
	Info(ctx context.Context) *monitorModel.Info
}

// EvalKeeperI — прогоны эталонных вопросов на стороне сервиса (internal/eval.Keeper).
type EvalKeeperI interface {
	Run(ctx context.Context, ask eval.AskFunc, source string, only []string) (*eval.Report, error)
	Last() (*eval.Report, bool)
	Baseline() *eval.Report
}

// NotifyUsecaseI — лента уведомлений, приглушения и заметки беседы; nil — без хранилища (503).
type NotifyUsecaseI interface {
	Feed(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.FeedItem, error)
	Ack(ctx context.Context, client, conversationId string, lastId int64) error
	Mute(ctx context.Context, client, conversationId string, spec *notifyModel.MuteSpec) (*notifyModel.Mute, error)
	Unmute(ctx context.Context, client, conversationId string, id int64) error
	Mutes(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.MuteState, []*notifyModel.FeedItem, error)
	Chat(ctx context.Context, client, conversationId string) (*chatModel.Chat, error)
	SaveNotes(ctx context.Context, client, conversationId, notes, by string) error
}
