package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chatRepoDb "github.com/rendau/pulse_agent/internal/domain/chat/repo/db"
	"github.com/rendau/pulse_agent/internal/domain/notify/model"
)

// TestLive — сигналы, уведомления, приглушения и контекст бесед в живом Postgres (база должна
// существовать, миграции применяются тестом):
//
//	PG_LIVE_DSN='postgres://postgres:postgres@localhost:5440/pulse_agent?sslmode=disable' go test ./internal/domain/notify/repo/db/ -run TestLive -v
func TestLive(t *testing.T) {
	dsn := os.Getenv("PG_LIVE_DSN")
	if dsn == "" {
		t.Skip("PG_LIVE_DSN is empty")
	}
	ctx := context.Background()

	migrations, err := filepath.Abs("../../../../../migrations")
	require.NoError(t, err)
	m, err := migrate.New("file://"+migrations, dsn)
	require.NoError(t, err)
	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		require.NoError(t, err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	cleanup := func() {
		_, _ = pool.Exec(ctx, "delete from signal where key like 'live:%'")
		_, _ = pool.Exec(ctx, "delete from notification where signal_key like 'live:%'")
		_, _ = pool.Exec(ctx, "delete from mute where client = 'live-test'")
		_, _ = pool.Exec(ctx, "delete from subscription where client = 'live-test'")
		_, _ = pool.Exec(ctx, "delete from chat where client = 'live-test'")
	}
	cleanup()
	defer cleanup()
	repo := New(pool)
	now := time.Now().Truncate(time.Microsecond)

	t.Run("signals", func(t *testing.T) {
		alert := &model.Signal{Key: "live:alert", Kind: model.KindAlert, Service: "live-svc", At: now, DueAt: now,
			Status: model.SignalPending, Summary: "алерт", Details: []byte(`{"labels":{"alertname":"X"}}`)}
		ok, err := repo.TouchSignal(ctx, alert, 6*time.Hour, now)
		require.NoError(t, err)
		assert.True(t, ok, "новый")
		ok, err = repo.TouchSignal(ctx, alert, 6*time.Hour, now)
		require.NoError(t, err)
		assert.False(t, ok, "ещё не разобран — не повторяется")

		due, err := repo.DueSignals(ctx, now, 100)
		require.NoError(t, err)
		got := findSignal(due, "live:alert")
		require.NotNil(t, got)
		assert.JSONEq(t, `{"labels":{"alertname":"X"}}`, string(got.Details))

		require.NoError(t, repo.RetrySignal(ctx, "live:alert", now.Add(time.Hour)))
		due, err = repo.DueSignals(ctx, now, 100)
		require.NoError(t, err)
		assert.Nil(t, findSignal(due, "live:alert"), "отложен")

		require.NoError(t, repo.FinishSignal(ctx, "live:alert", model.OutcomeQuiet, nil, now))
		ok, err = repo.TouchSignal(ctx, alert, 6*time.Hour, now.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, ok, "разобран час назад — рано повторять")
		ok, err = repo.TouchSignal(ctx, alert, 6*time.Hour, now.Add(7*time.Hour))
		require.NoError(t, err)
		assert.True(t, ok, "прошло больше AlertRepeat — снова")

		n, err := repo.CountInvestigatedSince(ctx, now.Add(-time.Minute))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, 0)

		deploy := &model.Signal{Key: "live:deploy:1", Kind: model.KindDeploy, Service: "live-svc", At: now, DueAt: now, Status: model.SignalPending}
		_, err = repo.TouchSignal(ctx, deploy, 0, now)
		require.NoError(t, err)
		require.NoError(t, repo.FinishSignal(ctx, deploy.Key, model.OutcomeHealthy, nil, now))
		ok, err = repo.TouchSignal(ctx, deploy, 0, now.Add(48*time.Hour))
		require.NoError(t, err)
		assert.False(t, ok, "выкатка — один раз")

		newer, err := repo.NewerDeploy(ctx, "live-svc", now.Add(-time.Minute))
		require.NoError(t, err)
		assert.True(t, newer)
		newer, err = repo.NewerDeploy(ctx, "live-svc", now)
		require.NoError(t, err)
		assert.False(t, newer)
	})

	t.Run("notifications and mutes", func(t *testing.T) {
		n := &model.Notification{At: now, Kind: model.KindAlert, Service: "live-svc", Key: "X", Severity: "warning",
			Title: "live-svc: алерт", Text: "- **разбор**", Investigated: true, SignalKey: "live:alert"}
		require.NoError(t, repo.CreateNotification(ctx, n))
		require.Positive(t, n.Id)

		got, err := repo.GetNotification(ctx, n.Id)
		require.NoError(t, err)
		assert.Equal(t, n.Text, got.Text)
		missing, err := repo.GetNotification(ctx, -1)
		require.NoError(t, err)
		assert.Nil(t, missing)

		list, err := repo.ListNotifications(ctx, model.NotificationFilter{AfterId: n.Id - 1, Limit: 10})
		require.NoError(t, err)
		require.NotEmpty(t, list)
		assert.Equal(t, n.Id, list[0].Id)
		last, err := repo.LastNotificationId(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, last, n.Id)

		mute := &model.Mute{Client: "live-test", ConversationId: "-100", Service: "live-svc", Until: new(now.Add(time.Hour)), CreatedAt: now, CreatedBy: "Иван"}
		require.NoError(t, repo.CreateMute(ctx, mute))
		mutes, err := repo.ListMutes(ctx, "live-test", "-100")
		require.NoError(t, err)
		require.Len(t, mutes, 1)
		assert.Equal(t, now.Add(time.Hour), mutes[0].Until.In(now.Location()))

		ok, err := repo.DeleteMute(ctx, "live-test", "-200", mute.Id)
		require.NoError(t, err)
		assert.False(t, ok, "чужая беседа")
		ok, err = repo.DeleteMute(ctx, "live-test", "-100", mute.Id)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("subscriptions", func(t *testing.T) {
		sub := &model.Subscription{Client: "live-test", ConversationId: "-100", Service: "caravan", MinSeverity: "warning", CreatedAt: now, CreatedBy: "Иван"}
		require.NoError(t, repo.CreateSubscription(ctx, sub))
		require.Positive(t, sub.Id)
		subs, err := repo.ListSubscriptions(ctx, "live-test", "-100")
		require.NoError(t, err)
		require.Len(t, subs, 1)
		assert.Equal(t, "warning", subs[0].MinSeverity)
		ok, err := repo.DeleteSubscription(ctx, "live-test", "-200", sub.Id)
		require.NoError(t, err)
		assert.False(t, ok, "чужая беседа")
		ok, err = repo.DeleteSubscription(ctx, "live-test", "-100", sub.Id)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("chat", func(t *testing.T) {
		chats := chatRepoDb.New(pool)
		c, err := chats.Get(ctx, "live-test", "-100")
		require.NoError(t, err)
		assert.Nil(t, c)

		require.NoError(t, chats.SetCursor(ctx, "live-test", "-100", 5))
		require.NoError(t, chats.SetCursor(ctx, "live-test", "-100", 3))
		require.NoError(t, chats.SaveNotes(ctx, "live-test", "-100", "мы — доставка", "Иван", now))
		c, err = chats.Get(ctx, "live-test", "-100")
		require.NoError(t, err)
		require.NotNil(t, c.NotifyCursor)
		assert.Equal(t, int64(5), *c.NotifyCursor, "курсор не откатывается")
		assert.Equal(t, "мы — доставка", c.Notes)
		assert.Equal(t, "Иван", c.NotesUpdatedBy)

		require.NoError(t, chats.SaveNotes(ctx, "live-test", "-200", "x", "", now))
		c, err = chats.Get(ctx, "live-test", "-200")
		require.NoError(t, err)
		assert.Nil(t, c.NotifyCursor, "заметки без ленты — не подписана")
	})
}

func findSignal(signals []*model.Signal, key string) *model.Signal {
	for _, s := range signals {
		if s.Key == key {
			return s
		}
	}
	return nil
}
