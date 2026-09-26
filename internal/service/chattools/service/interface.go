package service

import (
	"context"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
)

// chatI — заметки беседы (доменный сервис chat).
type chatI interface {
	SaveNotes(ctx context.Context, client, conversationId, notes, by string) error
}

// notifyI — приглушения беседы (доменный сервис notify).
type notifyI interface {
	Mute(ctx context.Context, spec *notifyModel.MuteSpec) (*notifyModel.Mute, error)
	Unmute(ctx context.Context, client, conversationId string, id int64) error
	Mutes(ctx context.Context, client, conversationId string) ([]*notifyModel.MuteState, error)
	Muted(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.FeedItem, error)
	Subscribe(ctx context.Context, spec *notifyModel.SubscriptionSpec) (*notifyModel.Subscription, error)
	Unsubscribe(ctx context.Context, client, conversationId string, id int64) error
	Subscriptions(ctx context.Context, client, conversationId string) ([]*notifyModel.Subscription, error)
}
