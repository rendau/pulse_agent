// Package service — наблюдатель: раз в Interval берёт у pulse изменения по кластеру
// (get_timeline scope=cluster), раз в ClusterInterval — здоровье кластера (get_cluster_health:
// всплеск ошибок в логах против обычного уровня сервиса, самоотчёты не ok, проблемы публичных
// приложений API-gateway), заводит сигналы
// (выкатка — один раз, остальное по сервису — не чаще AlertRepeat) и разбирает созревшие:
// выкатку — через DeployDelay, только если снапшот сервиса не healthy; остальное — сразу. Разбор — агент со схемой ответа (сообщать ли и что); сверх
// MaxRunsPerHour разборов или после неудачных попыток — уведомление без разбора.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	journalModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/infra/metrics"
	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
)

const (
	// Client — система в журнале для разборов наблюдателя
	Client = "watch"

	// timelineWindow — окно ленты за опрос: с запасом на пропущенные опросы (рестарт, сбой pulse)
	timelineWindow = "30m"
	// perTick — сколько сигналов разбирать за опрос: разбор идёт минуту-две
	perTick = 3
	// maxAttempts — после стольких неудачных разборов — уведомление без разбора
	maxAttempts = 3
	// retryAfter — через сколько повторить неудачный разбор
	retryAfter = 5 * time.Minute
	// cleanupEvery — как часто чистить старые уведомления и сигналы
	cleanupEvery = time.Hour
	// healthy — health снапшота, при котором выкатку не разбираем
	healthy = "healthy"
)

var metricSignals *prometheus.CounterVec

func init() {
	metricSignals = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "watch_signal_total",
		Help: "Сигналы наблюдателя по виду и исходу: notified, raw, quiet, healthy, superseded.",
	}, []string{"kind", "outcome"})
}

type Config struct {
	Interval    time.Duration // опрос ленты изменений pulse
	DeployDelay time.Duration // через сколько после выкатки её проверять
	AlertRepeat time.Duration // один сигнал сервиса (алерт, логи, самоотчёт) — не чаще
	// ClusterInterval — опрос здоровья кластера (логи, самоотчёты); 0 — не опрашивать
	ClusterInterval time.Duration
	// всплеск ошибок в логах: за 15 мин не меньше LogErrorsMin и в LogErrorsFactor раз выше обычного
	LogErrorsMin    int
	LogErrorsFactor int
	MaxRunsPerHour  int           // разборов агентом в час (дальше — без разбора)
	Keep            time.Duration // срок хранения уведомлений
}

type Service struct {
	cfg     Config
	pulse   pulseI
	agent   agentI
	notify  notifyI
	journal journalI
	now     func() time.Time
	wg      sync.WaitGroup

	logs          logBaseline // обычный уровень ошибок сервисов (только из цикла наблюдателя)
	clusterPolled time.Time
}

func New(cfg Config, pulse pulseI, agent agentI, notify notifyI, journal journalI) *Service {
	return &Service{cfg: cfg, pulse: pulse, agent: agent, notify: notify, journal: journal, now: time.Now}
}

func (s *Service) Start(ctx context.Context) {
	s.wg.Go(func() {
		ticker := time.NewTicker(s.cfg.Interval)
		defer ticker.Stop()

		var cleaned time.Time
		for {
			s.tick(ctx)
			if s.now().Sub(cleaned) >= cleanupEvery {
				if err := s.notify.Cleanup(ctx, s.cfg.Keep); err != nil && ctx.Err() == nil {
					slog.Error("watch: cleanup", "error", err)
				}
				cleaned = s.now()
			}

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (s *Service) Wait() {
	s.wg.Wait()
}

// tick — один опрос: новые сигналы, затем разбор созревших.
func (s *Service) tick(ctx context.Context) {
	if err := s.collect(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("watch: collect", "error", err)
	}
	if s.dueCluster(s.now()) {
		s.clusterPolled = s.now()
		if err := s.collectCluster(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("watch: collect cluster", "error", err)
		}
	}

	due, err := s.notify.Due(ctx, perTick)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("watch: due signals", "error", err)
		}
		return
	}
	for _, signal := range due {
		if ctx.Err() != nil {
			return
		}
		if err = s.handle(ctx, signal); err != nil && ctx.Err() == nil {
			slog.Error("watch: handle signal", "key", signal.Key, "error", err)
		}
	}
}

// collect — сигналы из ленты изменений pulse.
func (s *Service) collect(ctx context.Context) error {
	var rep timelineRep
	if err := s.call(ctx, "get_timeline", map[string]any{"scope": "cluster", "window": timelineWindow}, &rep); err != nil {
		return err
	}
	for _, signal := range signals(rep.Events, s.now(), s.cfg.DeployDelay) {
		repeat := lo.Ternary(signal.Kind == notifyModel.KindAlert, s.cfg.AlertRepeat, 0)
		added, err := s.notify.Observe(ctx, signal, repeat)
		if err != nil {
			return fmt.Errorf("notify.Observe: %w", err)
		}
		if added {
			slogSignal(signal)
		}
	}
	return nil
}

// handle — разбор созревшего сигнала.
func (s *Service) handle(ctx context.Context, signal *notifyModel.Signal) error {
	superseded, err := s.notify.Superseded(ctx, signal)
	if err != nil {
		return fmt.Errorf("notify.Superseded: %w", err)
	}
	if superseded {
		return s.finish(ctx, signal, notifyModel.OutcomeSuperseded, nil)
	}

	var question string
	switch signal.Kind {
	case notifyModel.KindDeploy:
		var snap snapshotRep
		if err = s.call(ctx, "get_service_snapshot", map[string]any{"service": signal.Service}, &snap); err != nil {
			return s.failed(ctx, signal, err)
		}
		if snap.Health == healthy {
			return s.finish(ctx, signal, notifyModel.OutcomeHealthy, nil)
		}
		question = fmt.Sprintf(deployQuestion, signal.Service, signal.Summary, signal.At.UTC().Format(time.RFC3339),
			s.now().Sub(signal.At).Round(time.Minute), snap.Health, lo.CoalesceOrEmpty(strings.Join(snap.SummaryHints, "; "), "нет"))
	case notifyModel.KindLogs:
		question = fmt.Sprintf(logsQuestion, signal.Service, signal.Summary, string(signal.Details))
	case notifyModel.KindSelf:
		question = fmt.Sprintf(selfQuestion, signal.Service, signal.Summary, string(signal.Details))
	case notifyModel.KindPublic:
		question = fmt.Sprintf(publicQuestion, signal.Summary, string(signal.Details))
	default:
		question = fmt.Sprintf(alertQuestion, signal.Summary, string(signal.Details))
	}

	runs, err := s.notify.InvestigatedSince(ctx, s.now().Add(-time.Hour))
	if err != nil {
		return fmt.Errorf("notify.InvestigatedSince: %w", err)
	}
	if runs >= s.cfg.MaxRunsPerHour {
		slog.Warn("watch: investigation limit reached, notifying without investigation", "key", signal.Key, "runs_per_hour", runs)
		return s.notifyRaw(ctx, signal, "разбор пропущен: исчерпан лимит разборов в час")
	}

	v, err := s.investigate(ctx, signal, question)
	if err != nil {
		return s.failed(ctx, signal, err)
	}
	if !v.Notify {
		return s.finish(ctx, signal, notifyModel.OutcomeQuiet, nil)
	}

	n := &notifyModel.Notification{
		Kind: signal.Kind, Service: signal.Service, Key: signalRef(signal), Severity: v.Severity,
		Title: v.Title, Text: v.Text, Investigated: true, SignalKey: signal.Key,
	}
	if err = s.notify.Notify(ctx, n); err != nil {
		return fmt.Errorf("notify.Notify: %w", err)
	}
	return s.finish(ctx, signal, notifyModel.OutcomeNotified, &n.Id)
}

// verdict — ответ разбора по verdictSchema.
type verdict struct {
	Notify   bool   `json:"notify"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Text     string `json:"text"`
}

// investigate — разбор агентом; запись в журнал (система watch, беседа — ключ сигнала).
func (s *Service) investigate(ctx context.Context, signal *notifyModel.Signal, question string) (*verdict, error) {
	started := s.now()
	result, err := s.agent.Run(ctx, &agentModel.Req{Question: question, Format: "json", ResponseSchema: verdictSchema})

	entry := &journalModel.Entry{
		At: started, Client: Client, ConversationId: signal.Key, Question: question, Format: "json", ClientSchema: true,
		Duration: s.now().Sub(started), Outcome: journalModel.OutcomeAnswered,
	}
	var v verdict
	switch {
	case err != nil:
		entry.Outcome, entry.Error = journalModel.OutcomeError, err.Error()
		err = fmt.Errorf("agent.Run: %w", err)
	case result.Json == nil || json.Unmarshal(result.Json, &v) != nil || strings.TrimSpace(v.Title) == "":
		entry.Outcome, entry.Incomplete = journalModel.OutcomeIncomplete, lo.CoalesceOrEmpty(result.Incomplete, "no verdict")
		err = fmt.Errorf("no verdict (incomplete %q)", result.Incomplete)
	}
	if result != nil {
		entry.Steps, entry.ToolCalls, entry.Answer = result.Steps, result.ToolCalls, result.ModelAnswer
		entry.InputTokens, entry.CachedTokens, entry.OutputTokens = result.Usage.InputTokens, result.Usage.CachedTokens, result.Usage.OutputTokens
		entry.Tools = lo.Map(result.Trace, func(t agentModel.ToolTrace, _ int) string { return t.Name })
		entry.Trace = lo.Map(result.Trace, func(t agentModel.ToolTrace, _ int) journalModel.ToolCall {
			return journalModel.ToolCall{Step: t.Step, Name: t.Name, Arguments: t.Arguments, Status: t.Status, Output: t.Output, Duration: t.Duration}
		})
	}
	if jerr := s.journal.Append(context.WithoutCancel(ctx), entry); jerr != nil {
		slog.Warn("watch: journal append", "error", jerr)
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// failed — разбор не удался: повторить позже, после maxAttempts — уведомление без разбора.
func (s *Service) failed(ctx context.Context, signal *notifyModel.Signal, cause error) error {
	slog.Warn("watch: investigation failed", "key", signal.Key, "attempt", signal.Attempts+1, "error", cause)
	if signal.Attempts+1 < maxAttempts {
		if err := s.notify.Retry(ctx, signal.Key, retryAfter); err != nil {
			return fmt.Errorf("notify.Retry: %w", err)
		}
		return nil
	}
	return s.notifyRaw(ctx, signal, "разобрать не получилось: pulse или модель не ответили")
}

// notifyRaw — уведомление без разбора: событие, как его отдал pulse, и почему без разбора.
func (s *Service) notifyRaw(ctx context.Context, signal *notifyModel.Signal, why string) error {
	n := &notifyModel.Notification{
		Kind: signal.Kind, Service: signal.Service, Key: signalRef(signal),
		Severity: lo.Ternary(signal.Kind == notifyModel.KindDeploy, "info", "warning"),
		Title:    signal.Summary, Text: signal.Summary + "\n\n_" + why + "_", SignalKey: signal.Key,
	}
	if err := s.notify.Notify(ctx, n); err != nil {
		return fmt.Errorf("notify.Notify: %w", err)
	}
	return s.finish(ctx, signal, notifyModel.OutcomeRaw, &n.Id)
}

func slogSignal(signal *notifyModel.Signal) {
	slog.Info("watch: signal", "key", signal.Key, "due_at", signal.DueAt)
}

func (s *Service) finish(ctx context.Context, signal *notifyModel.Signal, outcome string, notificationId *int64) error {
	metricSignals.WithLabelValues(signal.Kind, outcome).Inc()
	slog.Info("watch: signal done", "key", signal.Key, "outcome", outcome)
	if err := s.notify.Finish(ctx, signal.Key, outcome, notificationId); err != nil {
		return fmt.Errorf("notify.Finish: %w", err)
	}
	return nil
}

// call — вызов инструмента pulse с разбором ответа в out.
func (s *Service) call(ctx context.Context, tool string, args map[string]any, out any) error {
	raw, _ := json.Marshal(args)
	res, err := s.pulse.Call(ctx, tool, string(raw))
	switch {
	case err != nil:
		return fmt.Errorf("pulse %s: %w", tool, err)
	case res.IsError:
		return fmt.Errorf("pulse %s: %s", tool, res.Text)
	}
	if err = json.Unmarshal([]byte(res.Text), out); err != nil {
		return fmt.Errorf("pulse %s: decode: %w", tool, err)
	}
	return nil
}
