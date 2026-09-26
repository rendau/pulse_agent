package notify

import (
	"context"

	chatModel "github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
)

type ChatServiceI interface {
	Get(ctx context.Context, client, conversationId string) (*chatModel.Chat, error)
	SaveNotes(ctx context.Context, client, conversationId, notes, by string) error
	SetCursor(ctx context.Context, client, conversationId string, cursor int64) error
}

type NotifyServiceI interface {
	LastId(ctx context.Context) (int64, error)
	Feed(ctx context.Context, client, conversationId string, afterId int64, limit int) ([]*notifyModel.FeedItem, error)
	Mute(ctx context.Context, spec *notifyModel.MuteSpec) (*notifyModel.Mute, error)
	Unmute(ctx context.Context, client, conversationId string, id int64) error
	Mutes(ctx context.Context, client, conversationId string) ([]*notifyModel.MuteState, error)
	Muted(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.FeedItem, error)
	Subscribe(ctx context.Context, spec *notifyModel.SubscriptionSpec) (*notifyModel.Subscription, error)
	Unsubscribe(ctx context.Context, client, conversationId string, id int64) error
	Subscriptions(ctx context.Context, client, conversationId string) ([]*notifyModel.Subscription, error)
}
