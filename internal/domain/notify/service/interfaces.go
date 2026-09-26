package service

import (
	"context"
	"time"

	"github.com/rendau/pulse_agent/internal/domain/notify/model"
)

type RepoI interface {
	// TouchSignal заводит сигнал; уже есть — повторяет его, только если repeatAfter > 0 и сигнал
	// закончен раньше, чем now-repeatAfter. true — сигнал заведён или повторён.
	TouchSignal(ctx context.Context, s *model.Signal, repeatAfter time.Duration, now time.Time) (bool, error)
	// DueSignals — сигналы, которые пора разбирать, по времени.
	DueSignals(ctx context.Context, now time.Time, limit int) ([]*model.Signal, error)
	// NewerDeploy — у сервиса есть выкатка новее at.
	NewerDeploy(ctx context.Context, service string, at time.Time) (bool, error)
	FinishSignal(ctx context.Context, key, outcome string, notificationId *int64, at time.Time) error
	RetrySignal(ctx context.Context, key string, dueAt time.Time) error
	DeleteSignalsBefore(ctx context.Context, before time.Time) (int64, error)

	// CreateNotification сохраняет уведомление и проставляет Id.
	CreateNotification(ctx context.Context, n *model.Notification) error
	GetNotification(ctx context.Context, id int64) (*model.Notification, error)
	ListNotifications(ctx context.Context, f model.NotificationFilter) ([]*model.Notification, error)
	LastNotificationId(ctx context.Context) (int64, error)
	// CountInvestigatedSince — сколько сигналов разобрал агент с since (исходы notified и quiet).
	CountInvestigatedSince(ctx context.Context, since time.Time) (int, error)
	DeleteNotificationsBefore(ctx context.Context, before time.Time) (int64, error)

	// CreateMute сохраняет приглушение и проставляет Id.
	CreateMute(ctx context.Context, m *model.Mute) error
	ListMutes(ctx context.Context, client, conversationId string) ([]*model.Mute, error)
	// DeleteMute — false: у беседы нет такого приглушения.
	DeleteMute(ctx context.Context, client, conversationId string, id int64) (bool, error)
	DeleteMutesExpiredBefore(ctx context.Context, before time.Time) (int64, error)

	// CreateSubscription сохраняет подписку и проставляет Id.
	CreateSubscription(ctx context.Context, s *model.Subscription) error
	ListSubscriptions(ctx context.Context, client, conversationId string) ([]*model.Subscription, error)
	// DeleteSubscription — false: у беседы нет такой подписки.
	DeleteSubscription(ctx context.Context, client, conversationId string, id int64) (bool, error)
}
