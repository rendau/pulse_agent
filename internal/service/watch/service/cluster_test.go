package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pulseModel "github.com/rendau/pulse_agent/internal/service/pulse/model"
)

func TestLogBaseline(t *testing.T) {
	var b logBaseline
	// прогрев: обычный уровень копится, сигналов нет даже при большом числе
	for range logWarmup {
		assert.Empty(t, b.observe(map[string]int{"noisy": 400, "calm": 2}, 50, 5))
	}

	assert.Empty(t, b.observe(map[string]int{"noisy": 450, "calm": 3}, 50, 5), "шумный как обычно — не всплеск")

	spikes := b.observe(map[string]int{"noisy": 500, "calm": 120, "new": 80}, 50, 5)
	assert.Equal(t, map[string]int{"calm": 2, "new": 0}, spikes, "рост в 5+ раз и не меньше 50; новый в топе — от нуля")

	assert.Empty(t, b.observe(map[string]int{"calm": 40}, 50, 5), "меньше минимума — не всплеск")
	assert.Len(t, b.history["noisy"], 6, "выпал из топа — считаем 0")
	assert.Equal(t, 0, b.history["noisy"][5])
}

// fakeClusterPulse — get_cluster_health с заданным ответом.
type fakeClusterPulse struct{ rep string }

func (p *fakeClusterPulse) Call(_ context.Context, name, _ string) (*pulseModel.CallResult, error) {
	if name == "get_cluster_health" {
		return &pulseModel.CallResult{Text: p.rep}, nil
	}
	return &pulseModel.CallResult{Text: `{"events":[]}`}, nil
}

func TestCollectCluster(t *testing.T) {
	pulse := &fakeClusterPulse{rep: `{"log_errors":{"services":[{"service":"caravan","count":5,"top_error":"x"},{"namespace":"kube-system","count":900}]},
		"self_reported":[{"service":"caravan","status":"degraded","hints":["сервис сообщает: зависимость onec — down; ломает: уведомления 1С"]}]}`}
	notify := &fakeNotify{}
	s := New(Config{AlertRepeat: 6 * time.Hour, ClusterInterval: 5 * time.Minute, LogErrorsMin: 50, LogErrorsFactor: 5}, pulse, &fakeAgent{}, notify, &fakeJournal{})
	s.now = func() time.Time { return now }

	for range logWarmup {
		require.NoError(t, s.collectCluster(context.Background()))
	}
	assert.Equal(t, []string{"self:caravan", "self:caravan", "self:caravan"}, notify.observed, "самоотчёт — сразу; логи — после прогрева")

	pulse.rep = `{"log_errors":{"services":[{"service":"caravan","namespace":"default","count":340,"top_error":"dial tcp onec-proxy: connection refused"}]},"self_reported":[]}`
	notify.observed = nil
	require.NoError(t, s.collectCluster(context.Background()))
	assert.Equal(t, []string{"logs:caravan"}, notify.observed)
}
