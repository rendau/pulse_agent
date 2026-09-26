package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_agent/internal/domain/journal/model"
	"github.com/rendau/pulse_agent/internal/domain/journal/repo/mem"
)

func TestJournal(t *testing.T) {
	ctx := context.Background()
	svc := New(mem.New(4))
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	add := func(i int, client, outcome string, d time.Duration, tools ...string) {
		require.NoError(t, svc.Append(ctx, &model.Entry{At: at.Add(time.Duration(i) * time.Minute), Client: client, Outcome: outcome,
			Duration: d, Tools: tools, InputTokens: 1000, Question: "q", Answer: "a",
			Trace: []model.ToolCall{{Name: "ping"}}}))
	}
	add(0, "pulse_bot", model.OutcomeAnswered, 5*time.Second, "query_logs")
	add(1, "pulse_bot", model.OutcomeAnswered, 10*time.Second, "resolve_service", "get_service_snapshot")
	add(2, "service-desk", model.OutcomeError, time.Second)
	add(3, "pulse_bot", model.OutcomeIncomplete, 30*time.Second, "query_logs", "query_logs")
	add(4, "eval", model.OutcomeAnswered, 20*time.Second, "get_cluster_health")

	// кольцо на 4: самая старая запись вытеснена
	recent, err := svc.Recent(ctx, model.Filter{})
	require.NoError(t, err)
	require.Len(t, recent, 4)
	assert.Equal(t, "eval", recent[0].Client, "новые — первыми")
	assert.Equal(t, int64(5), recent[0].Id)
	assert.Empty(t, recent[0].Answer, "в списке — без ответа")
	assert.Nil(t, recent[0].Trace, "в списке — без хода разбора")

	recent, err = svc.Recent(ctx, model.Filter{Client: "pulse_bot", Limit: 1})
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, model.OutcomeIncomplete, recent[0].Outcome)

	// листание: старше записи 4
	recent, err = svc.Recent(ctx, model.Filter{BeforeId: 4})
	require.NoError(t, err)
	require.Len(t, recent, 2)
	assert.Equal(t, int64(3), recent[0].Id)

	full, err := svc.Get(ctx, 5)
	require.NoError(t, err)
	require.NotNil(t, full)
	assert.Equal(t, "a", full.Answer)
	assert.Len(t, full.Trace, 1)

	missing, err := svc.Get(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, missing, "вытесненной записи нет")

	stats, err := svc.Stats(ctx, at.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Questions, "окно — с since")
	assert.Equal(t, at.Add(2*time.Minute), stats.Since)
	require.Len(t, stats.Clients, 3)
	bot := stats.Clients[1]
	assert.Equal(t, "pulse_bot", bot.Client)
	assert.Equal(t, map[string]int{"incomplete": 1}, bot.Outcomes)
	assert.Equal(t, 30*time.Second, bot.AvgDuration)
	assert.Equal(t, "query_logs", stats.Tools[0].Tool)
	assert.Equal(t, 2, stats.Tools[0].Calls)

	deleted, err := svc.DeleteBefore(ctx, at.Add(3*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	recent, err = svc.Recent(ctx, model.Filter{})
	require.NoError(t, err)
	assert.Len(t, recent, 2)
}
