package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
)

// maxMutedScan — сколько уведомлений просматривать, считая приглушённые.
const maxMutedScan = 1000

type Service struct {
	repo RepoI
	now  func() time.Time
}

func New(repo RepoI) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Observe заводит сигнал (или повторяет алерт, см. RepoI.TouchSignal); true — его надо разобрать.
func (s *Service) Observe(ctx context.Context, signal *model.Signal, repeatAfter time.Duration) (bool, error) {
	signal.Status = model.SignalPending
	ok, err := s.repo.TouchSignal(ctx, signal, repeatAfter, s.now())
	if err != nil {
		return false, fmt.Errorf("repo.TouchSignal: %w", err)
	}
	return ok, nil
}

// Due — сигналы, которые пора разбирать (не больше limit), старые — первыми.
func (s *Service) Due(ctx context.Context, limit int) ([]*model.Signal, error) {
	signals, err := s.repo.DueSignals(ctx, s.now(), limit)
	if err != nil {
		return nil, fmt.Errorf("repo.DueSignals: %w", err)
	}
	return signals, nil
}

// Superseded — после этой выкатки сервиса была ещё одна: проверять надо последнюю.
func (s *Service) Superseded(ctx context.Context, signal *model.Signal) (bool, error) {
	if signal.Kind != model.KindDeploy {
		return false, nil
	}
	newer, err := s.repo.NewerDeploy(ctx, signal.Service, signal.At)
	if err != nil {
		return false, fmt.Errorf("repo.NewerDeploy: %w", err)
	}
	return newer, nil
}

// Finish закрывает сигнал с исходом; notificationId — уведомление по нему (если есть).
func (s *Service) Finish(ctx context.Context, key, outcome string, notificationId *int64) error {
	if err := s.repo.FinishSignal(ctx, key, outcome, notificationId, s.now()); err != nil {
		return fmt.Errorf("repo.FinishSignal: %w", err)
	}
	return nil
}

// Retry откладывает разбор сигнала на after (сбой pulse или модели).
func (s *Service) Retry(ctx context.Context, key string, after time.Duration) error {
	if err := s.repo.RetrySignal(ctx, key, s.now().Add(after)); err != nil {
		return fmt.Errorf("repo.RetrySignal: %w", err)
	}
	return nil
}

// Notify сохраняет уведомление (Id проставляется).
func (s *Service) Notify(ctx context.Context, n *model.Notification) error {
	n.At = s.now()
	if err := s.repo.CreateNotification(ctx, n); err != nil {
		return fmt.Errorf("repo.CreateNotification: %w", err)
	}
	return nil
}

// InvestigatedSince — сколько сигналов разобрал агент с since (лимит разборов в час).
func (s *Service) InvestigatedSince(ctx context.Context, since time.Time) (int, error) {
	n, err := s.repo.CountInvestigatedSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("repo.CountInvestigatedSince: %w", err)
	}
	return n, nil
}

// LastId — id последнего уведомления (0 — их ещё не было).
func (s *Service) LastId(ctx context.Context) (int64, error) {
	id, err := s.repo.LastNotificationId(ctx)
	if err != nil {
		return 0, fmt.Errorf("repo.LastNotificationId: %w", err)
	}
	return id, nil
}

// Feed — уведомления беседы после afterId (не больше limit), по порядку; приглушённые — с
// MutedBy (действующее сейчас приглушение).
func (s *Service) Feed(ctx context.Context, client, conversationId string, afterId int64, limit int) ([]*model.FeedItem, error) {
	notifications, err := s.repo.ListNotifications(ctx, model.NotificationFilter{AfterId: afterId, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("repo.ListNotifications: %w", err)
	}
	mutes, err := s.repo.ListMutes(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.ListMutes: %w", err)
	}
	subscriptions, err := s.repo.ListSubscriptions(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.ListSubscriptions: %w", err)
	}
	now := s.now()
	active := lo.Filter(mutes, func(m *model.Mute, _ int) bool { return m.Active(now) })

	return lo.Map(notifications, func(n *model.Notification, _ int) *model.FeedItem {
		mutedBy, _ := lo.Find(active, func(m *model.Mute) bool { return m.Covers(n) })
		subscribed := lo.ContainsBy(subscriptions, func(sub *model.Subscription) bool { return sub.Covers(n) })
		return &model.FeedItem{Notification: n, MutedBy: mutedBy, NotSubscribed: !subscribed}
	}), nil
}

// Subscribe подписывает беседу. Ошибки: errs.InvalidRequest — неизвестный вид или важность.
func (s *Service) Subscribe(ctx context.Context, spec *model.SubscriptionSpec) (*model.Subscription, error) {
	sub := &model.Subscription{
		Client: spec.Client, ConversationId: spec.ConversationId,
		Service: strings.TrimSpace(spec.Service), Kind: strings.TrimSpace(spec.Kind), MinSeverity: strings.TrimSpace(spec.MinSeverity),
		Note: strings.TrimSpace(spec.Note), CreatedAt: s.now(), CreatedBy: spec.By,
	}
	switch {
	case !model.KindKnown(sub.Kind):
		return nil, fmt.Errorf("%w: kind %q; expected alert, deploy, logs, self or empty (any)", errs.InvalidRequest, sub.Kind)
	case !model.SeverityKnown(sub.MinSeverity):
		return nil, fmt.Errorf("%w: min_severity %q; expected info, warning, critical or empty (any)", errs.InvalidRequest, sub.MinSeverity)
	}
	if err := s.repo.CreateSubscription(ctx, sub); err != nil {
		return nil, fmt.Errorf("repo.CreateSubscription: %w", err)
	}
	return sub, nil
}

// Unsubscribe убирает подписку беседы. Ошибки: errs.ObjectNotFound — у беседы нет такой.
func (s *Service) Unsubscribe(ctx context.Context, client, conversationId string, id int64) error {
	ok, err := s.repo.DeleteSubscription(ctx, client, conversationId, id)
	if err != nil {
		return fmt.Errorf("repo.DeleteSubscription: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: subscription %d", errs.ObjectNotFound, id)
	}
	return nil
}

// Subscriptions — подписки беседы (пусто — не приходит ничего).
func (s *Service) Subscriptions(ctx context.Context, client, conversationId string) ([]*model.Subscription, error) {
	subs, err := s.repo.ListSubscriptions(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.ListSubscriptions: %w", err)
	}
	return subs, nil
}

// Mute заводит приглушение в беседе. Ошибки: errs.InvalidRequest — неизвестный вид, отрицательный
// срок; errs.ObjectNotFound — нет уведомления NotificationId.
func (s *Service) Mute(ctx context.Context, spec *model.MuteSpec) (*model.Mute, error) {
	m := &model.Mute{
		Client: spec.Client, ConversationId: spec.ConversationId,
		Service: strings.TrimSpace(spec.Service), Kind: strings.TrimSpace(spec.Kind), Key: strings.TrimSpace(spec.Key),
		Note: strings.TrimSpace(spec.Note), CreatedAt: s.now(), CreatedBy: spec.By,
	}
	switch {
	case spec.For < 0:
		return nil, fmt.Errorf("%w: negative mute duration", errs.InvalidRequest)
	case !model.KindKnown(m.Kind):
		return nil, fmt.Errorf("%w: kind %q; expected alert, deploy, logs, self or empty (any)", errs.InvalidRequest, m.Kind)
	}
	if spec.NotificationId > 0 {
		n, err := s.repo.GetNotification(ctx, spec.NotificationId)
		if err != nil {
			return nil, fmt.Errorf("repo.GetNotification: %w", err)
		}
		if n == nil {
			return nil, fmt.Errorf("%w: notification %d", errs.ObjectNotFound, spec.NotificationId)
		}
		m.Service = lo.CoalesceOrEmpty(m.Service, n.Service)
	}
	if spec.For > 0 {
		m.Until = new(m.CreatedAt.Add(spec.For))
	}

	if err := s.repo.CreateMute(ctx, m); err != nil {
		return nil, fmt.Errorf("repo.CreateMute: %w", err)
	}
	return m, nil
}

// Unmute снимает приглушение беседы. Ошибки: errs.ObjectNotFound — у беседы нет такого.
func (s *Service) Unmute(ctx context.Context, client, conversationId string, id int64) error {
	ok, err := s.repo.DeleteMute(ctx, client, conversationId, id)
	if err != nil {
		return fmt.Errorf("repo.DeleteMute: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: mute %d", errs.ObjectNotFound, id)
	}
	return nil
}

// Mutes — действующие приглушения беседы и сколько уведомлений каждое уже скрыло.
func (s *Service) Mutes(ctx context.Context, client, conversationId string) ([]*model.MuteState, error) {
	active, err := s.activeMutes(ctx, client, conversationId)
	if err != nil {
		return nil, err
	}
	suppressed, err := s.suppressed(ctx, active, 0)
	if err != nil {
		return nil, err
	}
	return lo.Map(active, func(m *model.Mute, _ int) *model.MuteState {
		return &model.MuteState{Mute: m, Suppressed: lo.CountBy(suppressed, func(item *model.FeedItem) bool { return item.MutedBy.Id == m.Id })}
	}), nil
}

// Muted — последние уведомления (не больше limit), которые скрыли действующие приглушения
// беседы, новые — первыми.
func (s *Service) Muted(ctx context.Context, client, conversationId string, limit int) ([]*model.FeedItem, error) {
	active, err := s.activeMutes(ctx, client, conversationId)
	if err != nil {
		return nil, err
	}
	items, err := s.suppressed(ctx, active, limit)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) activeMutes(ctx context.Context, client, conversationId string) ([]*model.Mute, error) {
	mutes, err := s.repo.ListMutes(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.ListMutes: %w", err)
	}
	now := s.now()
	return lo.Filter(mutes, func(m *model.Mute, _ int) bool { return m.Until == nil || now.Before(*m.Until) }), nil
}

// suppressed — уведомления, пришедшие, пока действовало приглушение, под которое они подходят;
// новые — первыми; limit 0 — все просмотренные.
func (s *Service) suppressed(ctx context.Context, mutes []*model.Mute, limit int) ([]*model.FeedItem, error) {
	if len(mutes) == 0 {
		return nil, nil
	}
	since := lo.MinBy(mutes, func(a, b *model.Mute) bool { return a.CreatedAt.Before(b.CreatedAt) }).CreatedAt
	notifications, err := s.repo.ListNotifications(ctx, model.NotificationFilter{Since: since, Limit: maxMutedScan})
	if err != nil {
		return nil, fmt.Errorf("repo.ListNotifications: %w", err)
	}

	items := make([]*model.FeedItem, 0)
	for i := len(notifications) - 1; i >= 0; i-- {
		n := notifications[i]
		if m, ok := lo.Find(mutes, func(m *model.Mute) bool { return m.Active(n.At) && m.Covers(n) }); ok {
			items = append(items, &model.FeedItem{Notification: n, MutedBy: m})
			if limit > 0 && len(items) >= limit {
				break
			}
		}
	}
	return items, nil
}

// Cleanup удаляет уведомления старше keep, закрытые сигналы старше недели и истёкшие
// приглушения старше суток.
func (s *Service) Cleanup(ctx context.Context, keep time.Duration) error {
	now := s.now()
	if _, err := s.repo.DeleteNotificationsBefore(ctx, now.Add(-keep)); err != nil {
		return fmt.Errorf("repo.DeleteNotificationsBefore: %w", err)
	}
	if _, err := s.repo.DeleteSignalsBefore(ctx, now.Add(-7*24*time.Hour)); err != nil {
		return fmt.Errorf("repo.DeleteSignalsBefore: %w", err)
	}
	if _, err := s.repo.DeleteMutesExpiredBefore(ctx, now.Add(-24*time.Hour)); err != nil {
		return fmt.Errorf("repo.DeleteMutesExpiredBefore: %w", err)
	}
	return nil
}
