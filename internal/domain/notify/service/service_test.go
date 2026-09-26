package service

import (
	"context"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/domain/notify/model"
	"github.com/mechta-market/pulse_agent/internal/errs"
)

// fakeRepo — уведомления и приглушения в памяти (сигналы тестам сервиса не нужны).
type fakeRepo struct {
	RepoI
	notifications []*model.Notification
	mutes         []*model.Mute
	subscriptions []*model.Subscription
}

func (r *fakeRepo) CreateSubscription(_ context.Context, s *model.Subscription) error {
	s.Id = int64(len(r.subscriptions) + 1)
	r.subscriptions = append(r.subscriptions, s)
	return nil
}

func (r *fakeRepo) ListSubscriptions(_ context.Context, client, conversationId string) ([]*model.Subscription, error) {
	return lo.Filter(r.subscriptions, func(s *model.Subscription, _ int) bool {
		return s.Client == client && s.ConversationId == conversationId
	}), nil
}

func (r *fakeRepo) DeleteSubscription(_ context.Context, client, conversationId string, id int64) (bool, error) {
	n := len(r.subscriptions)
	r.subscriptions = lo.Reject(r.subscriptions, func(s *model.Subscription, _ int) bool {
		return s.Id == id && s.Client == client && s.ConversationId == conversationId
	})
	return len(r.subscriptions) < n, nil
}

func (r *fakeRepo) CreateNotification(_ context.Context, n *model.Notification) error {
	n.Id = int64(len(r.notifications) + 1)
	r.notifications = append(r.notifications, n)
	return nil
}

func (r *fakeRepo) GetNotification(_ context.Context, id int64) (*model.Notification, error) {
	n, _ := lo.Find(r.notifications, func(n *model.Notification) bool { return n.Id == id })
	return n, nil
}

func (r *fakeRepo) ListNotifications(_ context.Context, f model.NotificationFilter) ([]*model.Notification, error) {
	items := lo.Filter(r.notifications, func(n *model.Notification, _ int) bool {
		return n.Id > f.AfterId && !n.At.Before(f.Since)
	})
	if f.Limit > 0 && len(items) > f.Limit {
		items = items[:f.Limit]
	}
	return items, nil
}

func (r *fakeRepo) CreateMute(_ context.Context, m *model.Mute) error {
	m.Id = int64(len(r.mutes) + 1)
	r.mutes = append(r.mutes, m)
	return nil
}

func (r *fakeRepo) ListMutes(_ context.Context, client, conversationId string) ([]*model.Mute, error) {
	return lo.Filter(r.mutes, func(m *model.Mute, _ int) bool { return m.Client == client && m.ConversationId == conversationId }), nil
}

func (r *fakeRepo) DeleteMute(_ context.Context, client, conversationId string, id int64) (bool, error) {
	n := len(r.mutes)
	r.mutes = lo.Reject(r.mutes, func(m *model.Mute, _ int) bool {
		return m.Id == id && m.Client == client && m.ConversationId == conversationId
	})
	return len(r.mutes) < n, nil
}

func TestMuteAndFeed(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{}
	s := New(repo)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	require.NoError(t, s.Notify(ctx, &model.Notification{Kind: model.KindDeploy, Service: "caravan", Title: "caravan после выкатки"}))

	// кнопка под уведомлением: его сервис, на час
	now = now.Add(time.Minute)
	m, err := s.Mute(ctx, &model.MuteSpec{Client: "bot", ConversationId: "-100", NotificationId: 1, For: time.Hour, By: "Иван"})
	require.NoError(t, err)
	assert.Equal(t, "caravan", m.Service)
	assert.Equal(t, now.Add(time.Hour), *m.Until)

	now = now.Add(time.Minute)
	require.NoError(t, s.Notify(ctx, &model.Notification{Kind: model.KindAlert, Service: "caravan", Key: "HighErrors", Title: "caravan: алерт"}))
	require.NoError(t, s.Notify(ctx, &model.Notification{Kind: model.KindAlert, Service: "notifire", Title: "notifire: алерт"}))

	feed, err := s.Feed(ctx, "bot", "-100", 1, 10)
	require.NoError(t, err)
	require.Len(t, feed, 2)
	assert.Equal(t, m.Id, feed[0].MutedBy.Id, "caravan приглушён")
	assert.Nil(t, feed[1].MutedBy, "notifire — нет")

	other, err := s.Feed(ctx, "bot", "-200", 1, 10)
	require.NoError(t, err)
	assert.Nil(t, other[0].MutedBy, "приглушение — только в своей беседе")

	mutes, err := s.Mutes(ctx, "bot", "-100")
	require.NoError(t, err)
	require.Len(t, mutes, 1)
	assert.Equal(t, 1, mutes[0].Suppressed, "скрыто одно — после приглушения")

	muted, err := s.Muted(ctx, "bot", "-100", 10)
	require.NoError(t, err)
	require.Len(t, muted, 1)
	assert.Equal(t, "caravan: алерт", muted[0].Notification.Title)

	// срок вышел — снова приходит, в списке приглушений его нет
	now = now.Add(2 * time.Hour)
	feed, err = s.Feed(ctx, "bot", "-100", 1, 10)
	require.NoError(t, err)
	assert.Nil(t, feed[0].MutedBy)
	mutes, err = s.Mutes(ctx, "bot", "-100")
	require.NoError(t, err)
	assert.Empty(t, mutes)
}

func TestMuteValidation(t *testing.T) {
	ctx := context.Background()
	repo := &fakeRepo{}
	s := New(repo)

	_, err := s.Mute(ctx, &model.MuteSpec{Client: "bot", ConversationId: "1", Kind: "metrics"})
	require.ErrorIs(t, err, errs.InvalidRequest)
	_, err = s.Mute(ctx, &model.MuteSpec{Client: "bot", ConversationId: "1", NotificationId: 42})
	require.ErrorIs(t, err, errs.ObjectNotFound)

	m, err := s.Mute(ctx, &model.MuteSpec{Client: "bot", ConversationId: "1", Kind: model.KindDeploy})
	require.NoError(t, err)
	assert.Nil(t, m.Until, "без срока — навсегда")

	require.ErrorIs(t, s.Unmute(ctx, "bot", "2", m.Id), errs.ObjectNotFound, "чужая беседа")
	require.NoError(t, s.Unmute(ctx, "bot", "1", m.Id))
}

func TestSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := New(&fakeRepo{})
	for _, n := range []*model.Notification{
		{Kind: model.KindDeploy, Service: "caravan", Severity: "warning"},
		{Kind: model.KindAlert, Service: "caravan", Severity: "critical"},
		{Kind: model.KindAlert, Service: "notifire", Severity: "info"},
		{Kind: model.KindAlert, Service: "receipt", Severity: "critical"},
	} {
		require.NoError(t, s.Notify(ctx, n))
	}
	delivered := func() []int64 {
		feed, err := s.Feed(ctx, "bot", "-100", 0, 10)
		require.NoError(t, err)
		return lo.FilterMap(feed, func(item *model.FeedItem, _ int) (int64, bool) {
			return item.Notification.Id, !item.NotSubscribed && item.MutedBy == nil
		})
	}

	assert.Empty(t, delivered(), "без подписок — ничего")

	all, err := s.Subscribe(ctx, &model.SubscriptionSpec{Client: "bot", ConversationId: "-100"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 3, 4}, delivered(), "подписка без полей — всё")
	require.NoError(t, s.Unsubscribe(ctx, "bot", "-100", all.Id))

	_, err = s.Subscribe(ctx, &model.SubscriptionSpec{Client: "bot", ConversationId: "-100", Service: "caravan"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, delivered(), "только caravan")

	critical, err := s.Subscribe(ctx, &model.SubscriptionSpec{Client: "bot", ConversationId: "-100", Kind: model.KindAlert, MinSeverity: "critical"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2, 4}, delivered(), "плюс критичные алерты любого сервиса")

	_, err = s.Mute(ctx, &model.MuteSpec{Client: "bot", ConversationId: "-100", Service: "receipt"})
	require.NoError(t, err)
	assert.Equal(t, []int64{1, 2}, delivered(), "приглушение — поверх подписок")

	require.NoError(t, s.Unsubscribe(ctx, "bot", "-100", critical.Id))
	require.ErrorIs(t, s.Unsubscribe(ctx, "bot", "-200", 1), errs.ObjectNotFound, "чужая беседа")

	_, err = s.Subscribe(ctx, &model.SubscriptionSpec{Client: "bot", ConversationId: "-100", MinSeverity: "high"})
	require.ErrorIs(t, err, errs.InvalidRequest)
}
