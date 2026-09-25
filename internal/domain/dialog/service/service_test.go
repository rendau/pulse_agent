package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/domain/dialog/repo/mem"
)

func TestDialog(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

	svc := New(Config{MaxTurns: 2, Ttl: time.Hour}, mem.New())
	svc.now = func() time.Time { return now }

	require.NoError(t, svc.Append(ctx, "c1", "q1", "a1"))
	require.NoError(t, svc.Append(ctx, "c1", "q2", "a2"))
	require.NoError(t, svc.Append(ctx, "c1", "q3", "a3"))
	require.NoError(t, svc.Append(ctx, "c2", "other", "chat"))

	// последние MaxTurns пар
	turns, err := svc.History(ctx, "c1")
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, "q2", turns[0].Question)
	assert.Equal(t, "a3", turns[1].Answer)

	// тишина дольше Ttl — история беседы забыта, другой чат не задет
	now = now.Add(time.Hour + time.Minute)
	turns, err = svc.History(ctx, "c1")
	require.NoError(t, err)
	assert.Empty(t, turns)

	// /reset
	now = now.Add(-time.Hour)
	require.NoError(t, svc.Append(ctx, "c2", "q", "a"))
	require.NoError(t, svc.Reset(ctx, "c2"))
	turns, err = svc.History(ctx, "c2")
	require.NoError(t, err)
	assert.Empty(t, turns)
}
