// Package notify — лента уведомлений наблюдателя для бесед систем-клиентов, приглушения и
// заметки беседы. Беседа — conversation_id клиента (чат Telegram): у каждой свой курсор ленты,
// свои приглушения и заметки. Лента общая: приглушение применяется при выдаче.
package notify

import (
	"context"
	"fmt"
	"strings"

	chatModel "github.com/rendau/pulse_agent/internal/domain/chat/model"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

type Usecase struct {
	chat   ChatServiceI
	notify NotifyServiceI
}

func New(chat ChatServiceI, notify NotifyServiceI) *Usecase {
	return &Usecase{chat: chat, notify: notify}
}

// Feed — новые уведомления беседы (после подтверждённых Ack), по порядку; приглушённые — с
// MutedBy, не по подпискам беседы — с NotSubscribed (беседе их не присылают, но подтверждают). Беседа, впервые читающая ленту,
// подписывается с текущего места: старые уведомления ей не приходят.
func (u *Usecase) Feed(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.FeedItem, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, err
	}
	chat, err := u.chat.Get(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("chat.Get: %w", err)
	}
	if chat.NotifyCursor == nil {
		last, err := u.notify.LastId(ctx)
		if err != nil {
			return nil, fmt.Errorf("notify.LastId: %w", err)
		}
		if err = u.chat.SetCursor(ctx, client, conversationId, last); err != nil {
			return nil, fmt.Errorf("chat.SetCursor: %w", err)
		}
		return []*notifyModel.FeedItem{}, nil
	}

	limit = min(max(limit, 0), maxLimit)
	if limit == 0 {
		limit = defaultLimit
	}
	items, err := u.notify.Feed(ctx, client, conversationId, *chat.NotifyCursor, limit)
	if err != nil {
		return nil, fmt.Errorf("notify.Feed: %w", err)
	}
	return items, nil
}

// Ack — беседа получила уведомления до lastId включительно (приглушённые — тоже).
func (u *Usecase) Ack(ctx context.Context, client, conversationId string, lastId int64) error {
	conversationId, err := required(conversationId)
	if err != nil {
		return err
	}
	if lastId <= 0 {
		return fmt.Errorf("%w: last_id must be positive", errs.InvalidRequest)
	}
	if err = u.chat.SetCursor(ctx, client, conversationId, lastId); err != nil {
		return fmt.Errorf("chat.SetCursor: %w", err)
	}
	return nil
}

// Mute — приглушение в беседе (spec.Client и ConversationId проставляются здесь).
func (u *Usecase) Mute(ctx context.Context, client, conversationId string, spec *notifyModel.MuteSpec) (*notifyModel.Mute, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, err
	}
	spec.Client, spec.ConversationId = client, conversationId
	m, err := u.notify.Mute(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("notify.Mute: %w", err)
	}
	return m, nil
}

func (u *Usecase) Unmute(ctx context.Context, client, conversationId string, id int64) error {
	conversationId, err := required(conversationId)
	if err != nil {
		return err
	}
	if err = u.notify.Unmute(ctx, client, conversationId, id); err != nil {
		return fmt.Errorf("notify.Unmute: %w", err)
	}
	return nil
}

// Mutes — действующие приглушения беседы и последние скрытые ими уведомления (не больше limit).
func (u *Usecase) Mutes(ctx context.Context, client, conversationId string, limit int) ([]*notifyModel.MuteState, []*notifyModel.FeedItem, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, nil, err
	}
	mutes, err := u.notify.Mutes(ctx, client, conversationId)
	if err != nil {
		return nil, nil, fmt.Errorf("notify.Mutes: %w", err)
	}
	muted, err := u.notify.Muted(ctx, client, conversationId, min(max(limit, 1), maxLimit))
	if err != nil {
		return nil, nil, fmt.Errorf("notify.Muted: %w", err)
	}
	return mutes, muted, nil
}

// Subscribe — подписка беседы (spec.Client и ConversationId проставляются здесь).
func (u *Usecase) Subscribe(ctx context.Context, client, conversationId string, spec *notifyModel.SubscriptionSpec) (*notifyModel.Subscription, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, err
	}
	spec.Client, spec.ConversationId = client, conversationId
	sub, err := u.notify.Subscribe(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("notify.Subscribe: %w", err)
	}
	return sub, nil
}

func (u *Usecase) Unsubscribe(ctx context.Context, client, conversationId string, id int64) error {
	conversationId, err := required(conversationId)
	if err != nil {
		return err
	}
	if err = u.notify.Unsubscribe(ctx, client, conversationId, id); err != nil {
		return fmt.Errorf("notify.Unsubscribe: %w", err)
	}
	return nil
}

// Subscriptions — подписки беседы (пусто — не приходит ничего).
func (u *Usecase) Subscriptions(ctx context.Context, client, conversationId string) ([]*notifyModel.Subscription, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, err
	}
	subs, err := u.notify.Subscriptions(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("notify.Subscriptions: %w", err)
	}
	return subs, nil
}

// Chat — контекст беседы (заметки).
func (u *Usecase) Chat(ctx context.Context, client, conversationId string) (*chatModel.Chat, error) {
	conversationId, err := required(conversationId)
	if err != nil {
		return nil, err
	}
	chat, err := u.chat.Get(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("chat.Get: %w", err)
	}
	return chat, nil
}

// SaveNotes перезаписывает заметки беседы.
func (u *Usecase) SaveNotes(ctx context.Context, client, conversationId, notes, by string) error {
	conversationId, err := required(conversationId)
	if err != nil {
		return err
	}
	if err = u.chat.SaveNotes(ctx, client, conversationId, notes, by); err != nil {
		return fmt.Errorf("chat.SaveNotes: %w", err)
	}
	return nil
}

func required(conversationId string) (string, error) {
	conversationId = strings.TrimSpace(conversationId)
	if conversationId == "" {
		return "", fmt.Errorf("%w: conversation_id is required", errs.InvalidRequest)
	}
	return conversationId, nil
}
