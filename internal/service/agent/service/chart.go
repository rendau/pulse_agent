package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	// база часовых поясов вшита в бинарник: время точек без пояса — по Алматы
	_ "time/tzdata"

	"github.com/samber/lo"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	localConstant "github.com/rendau/pulse_agent/internal/service/agent/service/constant"
	chartModel "github.com/rendau/pulse_agent/internal/service/chart/model"
)

// queryMetricsTool — инструмент pulse, на результаты которого ссылается render_chart.
const queryMetricsTool = "query_metrics"

// chartArgs — аргументы render_chart от модели.
type chartArgs struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Unit    string `json:"unit"`
	Metrics []struct {
		Service  string `json:"service"`
		MetricId string `json:"metric_id"`
	} `json:"metrics"`
	Series []struct {
		Name   string `json:"name"`
		Points []struct {
			X string  `json:"x"`
			Y float64 `json:"y"`
		} `json:"points"`
	} `json:"series"`
}

// queryMetricsArgs, queryMetricsRep — вход и ответ query_metrics pulse (только нужные поля).
type queryMetricsArgs struct {
	Service  string `json:"service"`
	MetricId string `json:"metric_id"`
}

type queryMetricsRep struct {
	Service  string         `json:"service"`
	MetricId string         `json:"metric_id"`
	Title    string         `json:"title"`
	Unit     string         `json:"unit"`
	Series   []metricSeries `json:"series"`
}

type metricSeries struct {
	Labels map[string]string `json:"labels"`
	Points []metricPoint     `json:"points"`
}

type metricPoint struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}

func decodeMetricPoint(p metricPoint, _ int) chartModel.Point {
	return chartModel.Point{Time: p.TS, Value: p.Value}
}

// errChart — ошибка в аргументах графика: текст уходит модели, чтобы она поправила вызов.
var errChart = errors.New("chart")

// renderChart строит график по аргументам модели. Ряды metrics берутся из ответов
// query_metrics этого разбора (prior) — точные данные без переписывания точек моделью.
func (s *Service) renderChart(arguments string, prior []agentModel.ToolTrace) (*agentModel.Chart, string, error) {
	var args chartArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, "", fmt.Errorf("%w: arguments are not valid JSON: %w", errChart, err)
	}

	spec := &chartModel.Spec{Type: args.Type, Title: strings.TrimSpace(args.Title), Unit: args.Unit}

	for _, ref := range args.Metrics {
		rep, err := findMetrics(prior, ref.Service, ref.MetricId)
		if err != nil {
			return nil, "", err
		}
		spec.Unit = lo.CoalesceOrEmpty(spec.Unit, rep.Unit)
		spec.Title = lo.CoalesceOrEmpty(spec.Title, rep.Title+" "+rep.Service)
		for _, series := range rep.Series {
			name := seriesName(series.Labels)
			if len(args.Metrics) > 1 || name == "" {
				name = strings.TrimSpace(rep.Service + " " + name)
			}
			spec.Series = append(spec.Series, chartModel.Series{Name: name, Points: lo.Map(series.Points, decodeMetricPoint)})
		}
	}

	for _, series := range args.Series {
		points := make([]chartModel.Point, 0, len(series.Points))
		for _, p := range series.Points {
			point := chartModel.Point{Label: strings.TrimSpace(p.X), Value: p.Y}
			if args.Type != chartModel.TypeBar {
				ts, err := parseTime(p.X)
				if err != nil {
					return nil, "", err
				}
				point.Time, point.Label = ts, ""
			}
			points = append(points, point)
		}
		spec.Series = append(spec.Series, chartModel.Series{Name: strings.TrimSpace(series.Name), Points: points})
	}

	png, err := s.chart.Render(spec)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", errChart, err)
	}

	points := lo.SumBy(spec.Series, func(r chartModel.Series) int { return len(r.Points) })
	return &agentModel.Chart{Title: spec.Title, Png: png, Spec: spec}, fmt.Sprintf(localConstant.ChartDone, spec.Title, len(spec.Series), points), nil
}

// findMetrics — последний успешный query_metrics сервиса (и метрики, если задана) в разборе.
func findMetrics(prior []agentModel.ToolTrace, service, metricId string) (*queryMetricsRep, error) {
	for i := len(prior) - 1; i >= 0; i-- {
		t := prior[i]
		if t.Name != queryMetricsTool || t.Status != agentModel.ToolStatusOk {
			continue
		}
		var args queryMetricsArgs
		if json.Unmarshal([]byte(t.Arguments), &args) != nil || args.Service != service {
			continue
		}
		var rep queryMetricsRep
		if err := json.Unmarshal([]byte(t.Output), &rep); err != nil {
			continue
		}
		if metricId != "" && args.MetricId != metricId && rep.MetricId != metricId {
			continue
		}
		return &rep, nil
	}
	return nil, fmt.Errorf("%w: query_metrics(service=%s, metric_id=%s) was not called in this conversation; call it first", errChart, service, metricId)
}

// seriesLabels — лейблы, по которым ряды одной метрики различаются (под, контейнер, статус…),
// в порядке важности; остальные лейблы — только если этих нет.
var seriesLabels = []string{"pod", "container", "workload", "status", "method", "route", "app", "instance"}

func seriesName(labels map[string]string) string {
	for _, key := range seriesLabels {
		if v := labels[key]; v != "" {
			return v
		}
	}
	keys := lo.Keys(labels)
	sort.Strings(keys)
	return strings.Join(lo.Map(keys, func(k string, _ int) string { return k + "=" + labels[k] }), ", ")
}

// timeLayouts — время точки от модели: RFC3339 или без пояса (тогда — по Алматы).
var timeLayouts = []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", time.DateOnly}

var almaty = mustLocation("Asia/Almaty")

func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, almaty); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: line point x %q: expected time RFC3339 (2026-09-25T10:00:00+05:00)", errChart, s)
}

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("agent: " + err.Error())
	}
	return loc
}
