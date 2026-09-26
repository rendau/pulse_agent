package pulsekit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Passive — пассивная проверка зависимости по исходам настоящих вызовов: для платных API, API
// с лимитами и API без health-ручки, где проверочный запрос стоит денег или квоты. Клиент
// зовёт Observe после каждого вызова; Check (передаётся в Depend) смотрит вызовы за окно:
// все последние неудачны — down, неудачных больше половины — degraded, вызовов не было — ok с
// пометкой «вызовов не было» (не с чем сравнить: проверка честна только про реальный трафик).
// Редкий критичный вызов — Idle(): вызовов не было — проверьте хотя бы сеть (см. ExamplePassive_Idle).
//
//	bank := pulsekit.NewPassive(5 * time.Minute)
//	a.pulsekit.Depend("bank", "http", pulsekit.Host(config.Conf.BankUrl), false, bank.Check).Affects("онлайн-оплата")
//	…
//	rep, err := bankClient.Pay(ctx, req)
//	bank.Observe(err)
type Passive struct {
	window   time.Duration
	minCalls int

	mu    sync.Mutex
	calls []passiveCall
}

type passiveCall struct {
	at  time.Time
	err error
}

// passiveMaxCalls — сколько последних вызовов помнить.
const passiveMaxCalls = 100

// NewPassive — проверка по вызовам за window (по умолчанию 5 мин). down — только если неудачны
// не меньше трёх последних вызовов подряд: одна ошибка — ещё не авария.
func NewPassive(window time.Duration) *Passive {
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &Passive{window: window, minCalls: 3}
}

// Observe — исход вызова. Сбой — зависимость не сделала свою работу для сервиса: сеть, 5xx, а
// также отказы, которые значат поломку интеграции (401/403 — ключ, 429 — квота, 400 на запрос,
// который сервис должен формировать правильно). Штатный ответ системы, даже отказ по делу (404
// геокодера на плохой адрес, повторный capture), — nil: зависимость работает. Решает сервис.
func (p *Passive) Observe(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, passiveCall{at: time.Now(), err: err})
	if len(p.calls) > passiveMaxCalls {
		p.calls = p.calls[len(p.calls)-passiveMaxCalls:]
	}
}

// Idle — вызовов за окно не было: Check судить не по чему.
func (p *Passive) Idle() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls) == 0 || !p.calls[len(p.calls)-1].at.After(time.Now().Add(-p.window))
}

// Check — проверка для Depend: Problem с числом неудачных вызовов и причиной последнего
// (своими словами — Describe); вызовов не было — ok с пометкой.
func (p *Passive) Check(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	since := time.Now().Add(-p.window)
	var total, failed, failedInRow int
	var last error
	streak := true // серия неудач с последнего вызова ещё не прервана
	for i := len(p.calls) - 1; i >= 0 && p.calls[i].at.After(since); i-- {
		total++
		if p.calls[i].err == nil {
			streak = false
			continue
		}
		failed++
		if streak {
			failedInRow++
		}
		if last == nil {
			last = p.calls[i].err
		}
	}

	switch {
	case total == 0:
		return Problem{Status: "ok", Message: "вызовов не было за " + humanWindow(p.window)}
	case failedInRow >= p.minCalls:
		return Problem{Status: "down", Message: fmt.Sprintf("последние %d вызовов неудачны: %s", failedInRow, Describe(last))}
	case total > 0 && failed*2 > total:
		return Problem{Status: "degraded", Message: fmt.Sprintf("неудачных вызовов %d из %d за %s: %s", failed, total, humanWindow(p.window), Describe(last))}
	default:
		return nil
	}
}

// humanWindow — окно для сообщения: «5 мин», «1 ч», иначе как есть.
func humanWindow(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%d ч", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%d мин", d/time.Minute)
	default:
		return d.String()
	}
}
