package pulsekit

import (
	"context"
	"log/slog"
	"slices"
	"sort"
	"time"
)

// EntityCounts — объекты сейчас: сколько в каждом статусе и сколько из них застряло; Created и
// Finished — поток за последний час (nil — сервис его не считает).
type EntityCounts struct {
	Statuses map[string]StatusCount
	Created  *int
	Finished *int
}

// StatusCount — объекты в одном статусе. Stuck — дольше порога StuckAfter этого статуса из
// Domain; Oldest — сколько в статусе самый старый объект (0 — не считается).
type StatusCount struct {
	Count  int
	Stuck  int
	Oldest time.Duration
}

// Activity объявляет отчёт по объекту из Domain (имя — Entity.Name) в ручке состояния: как
// работает сервис по сути, а не только «живы ли зависимости». read вызывается в фоне вместе с
// проверками (не на каждый запрос pulse) и получает пороги StuckAfter по статусам из Domain —
// порог объявлен один раз. Застрявших всего не меньше degradedAtStuck — объект и сервис
// degraded; 0 — только показывать.
func (k *Kit) Activity(entity string, degradedAtStuck int, read func(ctx context.Context, stuckAfter map[string]time.Duration) (EntityCounts, error)) {
	defer k.catch()
	var declared *Entity
	if k.service.Domain != nil {
		for i := range k.service.Domain.Entities {
			if k.service.Domain.Entities[i].Name == entity {
				declared = &k.service.Domain.Entities[i]
			}
		}
	}
	if declared == nil {
		fail("activity %q: entity is not declared in Service.Domain — not published", entity)
	}
	stuckAfter := map[string]time.Duration{}
	for _, s := range declared.Statuses {
		if s.StuckAfter > 0 {
			stuckAfter[s.Name] = s.StuckAfter
		}
	}
	order := make([]string, 0, len(declared.Statuses))
	for _, s := range declared.Statuses {
		order = append(order, s.Name)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.activities = append(k.activities, &activity{
		entity: entity, degradedAtStuck: degradedAtStuck, stuckAfter: stuckAfter, order: order, readFn: read,
	})
}

type activity struct {
	entity          string
	degradedAtStuck int
	stuckAfter      map[string]time.Duration
	order           []string // статусы в порядке Domain
	readFn          func(ctx context.Context, stuckAfter map[string]time.Duration) (EntityCounts, error)
	last            *EntityRep
}

// read — отчёт по объекту; ошибка — объекта нет в ответе (в лог — без текста ошибки).
func (a *activity) read(ctx context.Context) *EntityRep {
	counts, err := a.readFn(ctx, a.stuckAfter)
	if err != nil {
		slog.Warn("pulsekit: activity read failed", "entity", a.entity, "reason", Describe(err))
		return nil
	}
	rep := &EntityRep{Name: a.entity, Status: "ok", Created1h: counts.Created, Finished1h: counts.Finished}
	names := make([]string, 0, len(counts.Statuses))
	for name := range counts.Statuses {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		pi, pj := slices.Index(a.order, names[i]), slices.Index(a.order, names[j])
		switch {
		case pi >= 0 && pj >= 0:
			return pi < pj
		case pi >= 0 || pj >= 0:
			return pi >= 0 // объявленные в Domain — первыми
		default:
			return names[i] < names[j]
		}
	})
	stuck := 0
	for _, name := range names {
		c := counts.Statuses[name]
		rep.Statuses = append(rep.Statuses, EntityStatusRep{Name: name, Count: c.Count, Stuck: c.Stuck, OldestS: int64(c.Oldest / time.Second)})
		stuck += c.Stuck
	}
	if a.degradedAtStuck > 0 && stuck >= a.degradedAtStuck {
		rep.Status = "degraded"
	}
	return rep
}

// EntityRep — отчёт по объекту в ручке состояния.
type EntityRep struct {
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Statuses   []EntityStatusRep `json:"statuses,omitempty"`
	Created1h  *int              `json:"created_1h,omitempty"`
	Finished1h *int              `json:"finished_1h,omitempty"`
}

type EntityStatusRep struct {
	Name    string `json:"name"`
	Count   int    `json:"count"`
	Stuck   int    `json:"stuck,omitempty"`
	OldestS int64  `json:"oldest_s,omitempty"`
}
