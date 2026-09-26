package service

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/rendau/pulse_agent/internal/infra/metrics"
)

// статусы шагов модели (метрики); статусы вызовов инструментов — agentModel.ToolStatus*
const (
	statusOk    = "ok"
	statusError = "error"
)

var (
	metricLlmRequests        *prometheus.CounterVec
	metricLlmRequestDuration *prometheus.HistogramVec
	metricLlmTokens          *prometheus.CounterVec
	metricToolCalls          *prometheus.CounterVec
	metricToolCallDuration   *prometheus.HistogramVec
	metricRunSteps           prometheus.Histogram
)

func init() {
	metricLlmRequests = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_request_total",
		Help: "Шаги модели по провайдеру и статусу.",
	}, []string{"provider", "status"})

	metricLlmRequestDuration = metrics.Factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "llm_request_duration_seconds",
		Help:    "Время одного шага модели.",
		Buckets: []float64{1, 3, 5, 10, 20, 40, 80, 160},
	}, []string{"provider"})

	metricLlmTokens = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_total",
		Help: "Токены по типу: input (включая cached), cached, output (включая reasoning), reasoning.",
	}, []string{"provider", "type"})

	metricToolCalls = metrics.Factory.NewCounterVec(prometheus.CounterOpts{
		Name: "tool_call_total",
		Help: "Вызовы инструментов pulse по статусу.",
	}, []string{"tool", "status"})

	metricToolCallDuration = metrics.Factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "tool_call_duration_seconds",
		Help:    "Время вызова инструмента pulse.",
		Buckets: []float64{0.1, 0.3, 1, 3, 10, 30},
	}, []string{"tool"})

	metricRunSteps = metrics.Factory.NewHistogram(prometheus.HistogramOpts{
		Name:    "agent_run_steps",
		Help:    "Шагов модели на один разбор.",
		Buckets: []float64{1, 2, 3, 5, 8, 13, 21},
	})
}
