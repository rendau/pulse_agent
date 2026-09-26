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

	"github.com/rendau/pulse_agent/internal/domain/journal/model"
)

// TestLive — журнал в живом Postgres (база должна существовать, миграции применяются тестом):
//
//	PG_LIVE_DSN='postgres://postgres:postgres@localhost:5440/pulse_agent?sslmode=disable' go test ./internal/domain/journal/repo/db/ -run TestLive -v
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
	repo := New(pool)

	at := time.Now().Truncate(time.Microsecond)
	full := &model.Entry{
		At: at, Client: "live-test", ConversationId: "c1", UserId: "7", UserName: "Иван",
		Question: "что по заказу 234115?", Format: "telegram", Outcome: model.OutcomeAnswered,
		Duration: 1500 * time.Millisecond, Steps: 2, ToolCalls: 2, Tools: []string{"query_logs", "get_service_snapshot"},
		InputTokens: 1000, CachedTokens: 800, OutputTokens: 100, Answer: "Заказ найден в orders-center.",
		Trace: []model.ToolCall{
			{Step: 1, Name: "query_logs", Arguments: `{"pattern":"234115"}`, Status: "ok", Output: `{"lines":[]}`, Duration: time.Second},
			{Step: 2, Name: "get_service_snapshot", Arguments: `{broken`, Status: "tool_error", Output: "ERROR: unknown service", Duration: 200 * time.Millisecond},
		},
	}
	require.NoError(t, repo.Append(ctx, full))
	require.Positive(t, full.Id)

	refused := &model.Entry{At: at.Add(time.Second), Client: "live-test", Question: "q", Outcome: model.OutcomeBusy}
	require.NoError(t, repo.Append(ctx, refused))
	defer func() {
		_, _ = pool.Exec(ctx, "delete from journal where client = 'live-test'")
	}()

	list, err := repo.List(ctx, model.Filter{Client: "live-test"})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, refused.Id, list[0].Id, "новые — первыми")
	assert.Empty(t, list[1].Answer, "в списке — без ответа")
	assert.Nil(t, list[1].Trace, "в списке — без хода разбора")
	assert.Equal(t, []string{"query_logs", "get_service_snapshot"}, list[1].Tools)
	assert.Equal(t, 1500*time.Millisecond, list[1].Duration)

	list, err = repo.List(ctx, model.Filter{Client: "live-test", BeforeId: refused.Id, Outcome: model.OutcomeAnswered, Since: at, Limit: 5})
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, full.Id, list[0].Id)

	got, err := repo.Get(ctx, full.Id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, at.Equal(got.At))
	got.At = full.At
	// jsonb нормализует пробелы в аргументах: сравниваем по смыслу
	require.Len(t, got.Trace, 2)
	assert.JSONEq(t, full.Trace[0].Arguments, got.Trace[0].Arguments)
	got.Trace[0].Arguments = full.Trace[0].Arguments
	assert.Equal(t, full, got, "запись целиком — как записали")

	var argsService string
	require.NoError(t, pool.QueryRow(ctx, "select trace->0->'arguments'->>'pattern' from journal where id = $1", full.Id).Scan(&argsService))
	assert.Equal(t, "234115", argsService, "аргументы в jsonb — объектом")

	missing, err := repo.Get(ctx, -1)
	require.NoError(t, err)
	assert.Nil(t, missing)

	deleted, err := repo.DeleteBefore(ctx, at.Add(500*time.Millisecond))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, deleted, int64(1))
	got, err = repo.Get(ctx, full.Id)
	require.NoError(t, err)
	assert.Nil(t, got, "старше срока — удалена")
	got, err = repo.Get(ctx, refused.Id)
	require.NoError(t, err)
	assert.NotNil(t, got, "моложе срока — на месте")
}
