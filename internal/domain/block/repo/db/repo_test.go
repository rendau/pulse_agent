package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_agent/internal/domain/block/model"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	notifyRepoDb "github.com/rendau/pulse_agent/internal/domain/notify/repo/db"
)

// TestLive — решения по уведомлениям в живом Postgres (база должна существовать, миграции
// применяются тестом):
//
//	PG_LIVE_DSN='postgres://postgres:postgres@localhost:5440/pulse_agent?sslmode=disable' go test ./internal/domain/block/repo/db/ -run TestLive -v
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
		_, _ = pool.Exec(ctx, "delete from notification where signal_key = 'live:block'")
	}
	cleanup()
	defer cleanup()

	notifyRepo := notifyRepoDb.New(pool)
	n := &notifyModel.Notification{At: time.Now(), Kind: notifyModel.KindSecurity, Key: "203.0.113.7", Severity: "critical",
		Title: "атака", Text: "перебор", Investigated: true, SignalKey: "live:block"}
	require.NoError(t, notifyRepo.CreateNotification(ctx, n))

	repo := New(pool)
	now := time.Now().Truncate(time.Microsecond)
	decision := func(action, refused string) *model.Decision {
		return &model.Decision{NotificationId: n.Id, Action: action, Duration: lo.Ternary(action == model.ActionBlock, "24h", ""),
			Ip: "203.0.113.7", Refused: refused, ById: "42", ByName: "Иван", Client: "live-test", ConversationId: "tg:-100", At: now}
	}

	skip := decision(model.ActionSkip, "")
	ok, err := repo.CreateDecision(ctx, skip)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Positive(t, skip.Id)
	ok, err = repo.CreateDecision(ctx, decision(model.ActionSkip, ""))
	require.NoError(t, err)
	assert.False(t, ok, "второе «не блокировать» не пишется")

	for range 2 {
		ok, err = repo.CreateDecision(ctx, decision(model.ActionBlock, "private network"))
		require.NoError(t, err)
		assert.True(t, ok, "отказов может быть несколько")
	}

	// одновременные итоговые блоки — записывается один
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0
	for range 5 {
		wg.Go(func() {
			d := decision(model.ActionBlock, "")
			d.Ip, d.Until, d.RutoBlockId, d.RutoSource = "203.0.113.7/32", new(now.Add(24*time.Hour)), "b1", "agent"
			ok, err := repo.CreateDecision(ctx, d)
			assert.NoError(t, err)
			mu.Lock()
			created += lo.Ternary(ok, 1, 0)
			mu.Unlock()
		})
	}
	wg.Wait()
	assert.Equal(t, 1, created)

	decisions, err := repo.ListDecisions(ctx, n.Id)
	require.NoError(t, err)
	require.Len(t, decisions, 4)
	assert.Equal(t, skip, decisions[0])
	final := model.Final(decisions)
	require.NotNil(t, final)
	assert.Equal(t, "203.0.113.7/32", final.Ip)
	assert.True(t, now.Add(24*time.Hour).Equal(*final.Until))
	assert.Equal(t, "agent", final.RutoSource)

	// уведомление удалено чисткой — решения вместе с ним
	_, err = pool.Exec(ctx, "delete from notification where id = $1", n.Id)
	require.NoError(t, err)
	decisions, err = repo.ListDecisions(ctx, n.Id)
	require.NoError(t, err)
	assert.Empty(t, decisions)
}
