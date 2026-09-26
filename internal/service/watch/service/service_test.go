package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	journalModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
)

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

const timeline = `{"events":[
 {"ts":"2026-09-26T11:50:00Z","type":"deploy","service":"caravan","summary":"caravan: деплой c43985f → cac1a18",
  "details":{"commit":"cac1a189932047af15cccb6d26a209d8bf36c785","workload":"Deployment/default/caravan"}},
 {"ts":"2026-09-26T04:10:05Z","type":"alert_firing","service":"airflow-dags","summary":"airflow-dags: алерт KubeJobFailed",
  "details":{"last_at":"2026-09-26T11:58:00Z","labels":{"alertname":"KubeJobFailed","severity":"warning"}}},
 {"ts":"2026-09-26T11:59:00Z","type":"alert_firing","service":"x","summary":"без имени","details":{"labels":{}}},
 {"ts":"2026-09-26T11:59:00Z","type":"alert_firing","service":"x","summary":"none","details":{"labels":{"alertname":"Info","severity":"none"}}},
 {"ts":"2026-09-26T11:59:00Z","type":"warning","service":"x","summary":"BackoffLimitExceeded"}
]}`

func TestSignals(t *testing.T) {
	var rep timelineRep
	require.NoError(t, json.Unmarshal([]byte(timeline), &rep))

	got := signals(rep.Events, now, 15*time.Minute)
	require.Len(t, got, 2)

	assert.Equal(t, "deploy:Deployment/default/caravan:cac1a189932047af15cccb6d26a209d8bf36c785", got[0].Key)
	assert.Equal(t, time.Date(2026, 9, 26, 12, 5, 0, 0, time.UTC), got[0].DueAt, "выкатка — через 15 минут")
	assert.Equal(t, "cac1a18", signalRef(got[0]))

	assert.Equal(t, "alert:airflow-dags:KubeJobFailed", got[1].Key)
	assert.Equal(t, now, got[1].DueAt, "алерт — сразу")
	assert.Equal(t, time.Date(2026, 9, 26, 11, 58, 0, 0, time.UTC), got[1].At, "время — последнее срабатывание")
	assert.Equal(t, "KubeJobFailed", signalRef(got[1]))
}

type fakePulse struct {
	health string
	err    error
	calls  []string
}

func (p *fakePulse) Call(_ context.Context, name, _ string) (*pulseModel.CallResult, error) {
	p.calls = append(p.calls, name)
	if p.err != nil {
		return nil, p.err
	}
	switch name {
	case "get_service_snapshot":
		return &pulseModel.CallResult{Text: `{"health":"` + p.health + `","summary_hints":["error_rate вырос"]}`}, nil
	case "get_timeline":
		return &pulseModel.CallResult{Text: timeline}, nil
	}
	return &pulseModel.CallResult{IsError: true, Text: "unknown tool"}, nil
}

type fakeAgent struct {
	verdict string
	err     error
	runs    int
}

func (a *fakeAgent) Run(_ context.Context, req *agentModel.Req) (*agentModel.Result, error) {
	a.runs++
	if req.ResponseSchema == nil {
		return nil, errors.New("schema expected")
	}
	if a.err != nil {
		return nil, a.err
	}
	return &agentModel.Result{Json: []byte(a.verdict), ModelAnswer: a.verdict, Steps: 2}, nil
}

type fakeNotify struct {
	notifyI
	superseded    bool
	runs          int
	notifications []*notifyModel.Notification
	finished      map[string]string
	retried       []string
	observed      []string
}

func (n *fakeNotify) Observe(_ context.Context, s *notifyModel.Signal, _ time.Duration) (bool, error) {
	n.observed = append(n.observed, s.Key)
	return true, nil
}

func (n *fakeNotify) Superseded(context.Context, *notifyModel.Signal) (bool, error) {
	return n.superseded, nil
}

func (n *fakeNotify) InvestigatedSince(context.Context, time.Time) (int, error) { return n.runs, nil }

func (n *fakeNotify) Notify(_ context.Context, x *notifyModel.Notification) error {
	x.Id = int64(len(n.notifications) + 1)
	n.notifications = append(n.notifications, x)
	return nil
}

func (n *fakeNotify) Finish(_ context.Context, key, outcome string, _ *int64) error {
	if n.finished == nil {
		n.finished = map[string]string{}
	}
	n.finished[key] = outcome
	return nil
}

func (n *fakeNotify) Retry(_ context.Context, key string, _ time.Duration) error {
	n.retried = append(n.retried, key)
	return nil
}

type fakeJournal struct{ entries []*journalModel.Entry }

func (j *fakeJournal) Append(_ context.Context, e *journalModel.Entry) error {
	j.entries = append(j.entries, e)
	return nil
}

func newTestService(pulse *fakePulse, agent *fakeAgent, notify *fakeNotify, journal *fakeJournal) *Service {
	s := New(Config{Interval: time.Minute, DeployDelay: 15 * time.Minute, AlertRepeat: 6 * time.Hour, MaxRunsPerHour: 20}, pulse, agent, notify, journal)
	s.now = func() time.Time { return now }
	return s
}

var deploySignal = &notifyModel.Signal{
	Key: "deploy:Deployment/default/caravan:cac1a18", Kind: notifyModel.KindDeploy, Service: "caravan",
	At: now.Add(-15 * time.Minute), Summary: "caravan: деплой c43985f → cac1a18", Details: []byte(`{"commit":"cac1a18"}`),
}

var alertSignal = &notifyModel.Signal{
	Key: "alert:caravan:HighErrors", Kind: notifyModel.KindAlert, Service: "caravan", At: now,
	Summary: "caravan: алерт HighErrors", Details: []byte(`{"labels":{"alertname":"HighErrors"}}`),
}

func TestCollect(t *testing.T) {
	notify := &fakeNotify{}
	require.NoError(t, newTestService(&fakePulse{}, &fakeAgent{}, notify, &fakeJournal{}).collect(context.Background()))
	assert.Equal(t, []string{"deploy:Deployment/default/caravan:cac1a189932047af15cccb6d26a209d8bf36c785", "alert:airflow-dags:KubeJobFailed"}, notify.observed)
}

func TestDeployHealthy(t *testing.T) {
	agent, notify := &fakeAgent{}, &fakeNotify{}
	require.NoError(t, newTestService(&fakePulse{health: "healthy"}, agent, notify, &fakeJournal{}).handle(context.Background(), deploySignal))

	assert.Equal(t, notifyModel.OutcomeHealthy, notify.finished[deploySignal.Key])
	assert.Zero(t, agent.runs, "здоровая выкатка — без разбора")
	assert.Empty(t, notify.notifications)
}

func TestDeployDegradedNotified(t *testing.T) {
	agent := &fakeAgent{verdict: `{"notify":true,"severity":"warning","title":"caravan: после выкатки 8% сбоев","text":"- сбои с 0 до 8%"}`}
	notify, journal := &fakeNotify{}, &fakeJournal{}
	require.NoError(t, newTestService(&fakePulse{health: "degraded"}, agent, notify, journal).handle(context.Background(), deploySignal))

	require.Len(t, notify.notifications, 1)
	n := notify.notifications[0]
	assert.Equal(t, "caravan: после выкатки 8% сбоев", n.Title)
	assert.Equal(t, "cac1a18", n.Key)
	assert.True(t, n.Investigated)
	assert.Equal(t, notifyModel.OutcomeNotified, notify.finished[deploySignal.Key])

	require.Len(t, journal.entries, 1, "разбор — в журнале")
	assert.Equal(t, Client, journal.entries[0].Client)
	assert.Contains(t, journal.entries[0].Question, "error_rate вырос", "подсказки снапшота — в вопросе")
}

func TestAlertQuiet(t *testing.T) {
	notify := &fakeNotify{}
	agent := &fakeAgent{verdict: `{"notify":false,"severity":"info","title":"уже прошло","text":""}`}
	require.NoError(t, newTestService(&fakePulse{}, agent, notify, &fakeJournal{}).handle(context.Background(), alertSignal))

	assert.Equal(t, notifyModel.OutcomeQuiet, notify.finished[alertSignal.Key])
	assert.Empty(t, notify.notifications)
}

func TestLimitRaw(t *testing.T) {
	agent, notify := &fakeAgent{}, &fakeNotify{runs: 20}
	require.NoError(t, newTestService(&fakePulse{}, agent, notify, &fakeJournal{}).handle(context.Background(), alertSignal))

	assert.Zero(t, agent.runs, "лимит — без разбора")
	require.Len(t, notify.notifications, 1)
	assert.False(t, notify.notifications[0].Investigated)
	assert.Equal(t, "HighErrors", notify.notifications[0].Key)
	assert.Contains(t, notify.notifications[0].Text, "лимит")
	assert.Equal(t, notifyModel.OutcomeRaw, notify.finished[alertSignal.Key])
}

func TestFailureRetryThenRaw(t *testing.T) {
	agent, notify := &fakeAgent{err: errors.New("llm down")}, &fakeNotify{}
	s := newTestService(&fakePulse{}, agent, notify, &fakeJournal{})

	require.NoError(t, s.handle(context.Background(), alertSignal))
	assert.Equal(t, []string{alertSignal.Key}, notify.retried, "первая неудача — повтор позже")
	assert.Empty(t, notify.notifications)

	last := *alertSignal
	last.Attempts = maxAttempts - 1
	require.NoError(t, s.handle(context.Background(), &last))
	require.Len(t, notify.notifications, 1, "последняя попытка — без разбора")
	assert.Equal(t, notifyModel.OutcomeRaw, notify.finished[alertSignal.Key])
}

func TestSuperseded(t *testing.T) {
	pulse, notify := &fakePulse{}, &fakeNotify{superseded: true}
	require.NoError(t, newTestService(pulse, &fakeAgent{}, notify, &fakeJournal{}).handle(context.Background(), deploySignal))
	assert.Equal(t, notifyModel.OutcomeSuperseded, notify.finished[deploySignal.Key])
	assert.Empty(t, pulse.calls, "проверяется только последняя выкатка")
}
