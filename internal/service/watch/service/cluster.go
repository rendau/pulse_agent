package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"

	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
)

const (
	// clusterWindow — окно ошибок в логах за опрос кластера
	clusterWindow = "15m"
	// logHistory — сколько прошлых опросов помнить на сервис (обычный уровень — медиана)
	logHistory = 12
	// logWarmup — опросов после старта без сигналов: копим обычный уровень
	logWarmup = 3
)

// clusterRep — нужная наблюдателю часть ответа get_cluster_health.
type clusterRep struct {
	LogErrors *struct {
		Services []serviceLogErrors `json:"services"`
	} `json:"log_errors"`
	SelfReported []struct {
		Service string   `json:"service"`
		Status  string   `json:"status"`
		Stale   bool     `json:"stale"`
		Hints   []string `json:"hints"`
	} `json:"self_reported"`
}

// serviceLogErrors — ошибки в логах сервиса за окно.
type serviceLogErrors struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Count     int    `json:"count"`
	TopError  string `json:"top_error"`
}

// logBaseline — обычный уровень ошибок сервиса по прошлым опросам (в памяти: после рестарта
// копится заново, первые logWarmup опросов — без сигналов).
type logBaseline struct {
	polls   int
	history map[string][]int
}

// observe — счётчики опроса (сервисы не из топа — 0); сервисы со всплеском: сейчас не меньше
// minCount и в factor раз выше обычного (медиана прошлых опросов). Обычный уровень — до этого опроса.
func (b *logBaseline) observe(counts map[string]int, minCount, factor int) map[string]int {
	if b.history == nil {
		b.history = map[string][]int{}
	}
	b.polls++
	spikes := map[string]int{}
	for service := range lo.Assign(lo.MapValues(b.history, func([]int, string) int { return 0 }), counts) {
		count := counts[service]
		usual := median(b.history[service])
		if b.polls > logWarmup && count >= minCount && count >= factor*max(usual, 1) {
			spikes[service] = usual
		}
		b.history[service] = lo.Subset(append(b.history[service], count), -logHistory, logHistory)
	}
	return spikes
}

func median(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(values))
	return sorted[len(sorted)/2]
}

// collectCluster — сигналы из здоровья кластера: всплеск ошибок в логах и самоотчёты не ok.
func (s *Service) collectCluster(ctx context.Context) error {
	var rep clusterRep
	if err := s.call(ctx, "get_cluster_health", map[string]any{"window": clusterWindow}, &rep); err != nil {
		return err
	}
	now := s.now()
	var signals []*notifyModel.Signal

	if rep.LogErrors != nil {
		services := lo.Filter(rep.LogErrors.Services, func(v serviceLogErrors, _ int) bool {
			return v.Service != ""
		})
		counts := lo.SliceToMap(services, func(v serviceLogErrors) (string, int) {
			return v.Service, v.Count
		})
		spikes := s.logs.observe(counts, s.cfg.LogErrorsMin, s.cfg.LogErrorsFactor)
		for _, v := range services {
			usual, ok := spikes[v.Service]
			if !ok {
				continue
			}
			details, _ := json.Marshal(map[string]any{"count": v.Count, "usual": usual, "window": clusterWindow, "namespace": v.Namespace, "top_error": v.TopError})
			summary := fmt.Sprintf("%s: %d ошибок в логах за 15 мин (обычно ~%d)", v.Service, v.Count, usual)
			if v.TopError != "" {
				summary += fmt.Sprintf("; чаще всего: «%s»", lo.Ellipsis(v.TopError, 200))
			}
			signals = append(signals, &notifyModel.Signal{
				Key: "logs:" + v.Service, Kind: notifyModel.KindLogs, Service: v.Service, At: now, DueAt: now,
				Summary: summary, Details: details,
			})
		}
	}

	for _, v := range rep.SelfReported {
		details, _ := json.Marshal(v)
		status := lo.Ternary(v.Stale && v.Status == "ok", "отчёт устарел", v.Status)
		summary := fmt.Sprintf("%s сообщает о себе: %s", v.Service, status)
		if len(v.Hints) > 0 {
			summary += " — " + strings.Join(lo.Slice(v.Hints, 0, 3), "; ")
		}
		signals = append(signals, &notifyModel.Signal{
			Key: "self:" + v.Service, Kind: notifyModel.KindSelf, Service: v.Service, At: now, DueAt: now,
			Summary: summary, Details: details,
		})
	}

	for _, signal := range signals {
		added, err := s.notify.Observe(ctx, signal, s.cfg.AlertRepeat)
		if err != nil {
			return fmt.Errorf("notify.Observe: %w", err)
		}
		if added {
			slogSignal(signal)
		}
	}
	return nil
}

// dueCluster — пора опрашивать кластер.
func (s *Service) dueCluster(now time.Time) bool {
	return s.cfg.ClusterInterval > 0 && now.Sub(s.clusterPolled) >= s.cfg.ClusterInterval
}
