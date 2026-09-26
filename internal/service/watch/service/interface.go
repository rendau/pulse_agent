package service

import (
	"context"
	"time"

	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
)

type pulseI interface {
	Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error)
}

type agentI interface {
	Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error)
}

// notifyI — сигналы и уведомления (доменный сервис notify).
type notifyI interface {
	Observe(ctx context.Context, signal *notifyModel.Signal, repeatAfter time.Duration) (bool, error)
	Due(ctx context.Context, limit int) ([]*notifyModel.Signal, error)
	Superseded(ctx context.Context, signal *notifyModel.Signal) (bool, error)
	Finish(ctx context.Context, key, outcome string, notificationId *int64) error
	Retry(ctx context.Context, key string, after time.Duration) error
	Notify(ctx context.Context, n *notifyModel.Notification) error
	InvestigatedSince(ctx context.Context, since time.Time) (int, error)
	Cleanup(ctx context.Context, keep time.Duration) error
}

// journalI — разборы наблюдателя пишутся в журнал, как вопросы (система watch): видно в /debug.
type journalI interface {
	Append(ctx context.Context, e *journalModel.Entry) error
}
