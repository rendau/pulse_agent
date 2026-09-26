package pulsekit

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// статусы показателя: пусто — ok
var gaugeStatuses = []string{"", "ok", "degraded", "down"}

// Gauge объявляет числовой показатель ручки состояния — то, чего нет в метриках и что
// объясняет состояние: глубина outbox, размер очереди. read вызывается в фоне вместе с
// проверками зависимостей и возвращает значение и статус (ok, degraded, down; пусто — ok).
// Не ok делает состояние сервиса degraded. unit — count, seconds, bytes… (свободно).
func (k *Kit) Gauge(id, title, unit string, read func(ctx context.Context) (float64, string, error)) {
	k.addGauge(id, title, unit, func(ctx context.Context) (any, string, error) {
		v, status, err := read(ctx)
		return v, status, err
	})
}

// GaugeTime — показатель-время: «последняя выгрузка», «последнее успешное событие».
func (k *Kit) GaugeTime(id, title string, read func(ctx context.Context) (time.Time, string, error)) {
	k.addGauge(id, title, "time", func(ctx context.Context) (any, string, error) {
		t, status, err := read(ctx)
		if err != nil || t.IsZero() {
			return nil, status, err
		}
		return t.Format(time.RFC3339), status, nil
	})
}

type gauge struct {
	id, title, unit string
	readFn          func(ctx context.Context) (any, string, error)
	last            gaugeReading
}

type gaugeReading struct {
	value  any // nil — не прочитан
	status string
}

func (k *Kit) addGauge(id, title, unit string, read func(ctx context.Context) (any, string, error)) {
	if !idRe.MatchString(id) || title == "" {
		panic(fmt.Sprintf("pulsekit: gauge %q: Id (%s) and title are required", id, idRe))
	}
	checkText("gauge "+id+" title", title, maxTitleChars)
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.gauges) >= maxGauges {
		panic(fmt.Sprintf("pulsekit: more than %d gauges", maxGauges))
	}
	k.gauges = append(k.gauges, &gauge{id: id, title: title, unit: unit, readFn: read})
}

// read — значение показателя; ошибка — показателя нет в ответе (в лог — без текста ошибки).
func (g *gauge) read(ctx context.Context) gaugeReading {
	value, status, err := g.readFn(ctx)
	if err != nil {
		slog.Warn("pulsekit: gauge read failed", "gauge", g.id, "reason", Describe(err))
		return gaugeReading{}
	}
	if !slices.Contains(gaugeStatuses, status) {
		slog.Warn("pulsekit: gauge status is not ok, degraded or down", "gauge", g.id, "status", status)
		status = ""
	}
	return gaugeReading{value: value, status: status}
}
