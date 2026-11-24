// Package model — решения админов по уведомлениям security: заблокировать адрес атаки в ruto
// (кнопка в боте) или нет.
package model

import (
	"time"

	"github.com/samber/lo"
)

// решения
const (
	ActionBlock = "block" // заблокировать адрес в ruto
	ActionSkip  = "skip"  // не блокировать
)

// Decision — решение админа по уведомлению. Итоговое — успешная блокировка (Final): одна на
// уведомление, дальше ничего не делается. «Не блокировать» — не итоговое: заблокировать после
// него ещё можно; записывается первое. Отказ ruto (Refused не пуст) — попытка: можно ещё раз.
type Decision struct {
	Id             int64
	NotificationId int64

	Action string
	// Duration — срок блокировки как его выбрали (24h, 168h); у skip — пусто
	Duration string
	// Ip — сеть, как её записал ruto (203.0.113.7/32); у skip и отказа — ключ уведомления
	Ip string
	// Until — конец блокировки; nil — без конца (сеть уже заблокировал человек) или не блок
	Until *time.Time

	RutoBlockId string
	// RutoSource — agent | admin: admin — сеть уже заблокировал человек, ruto вернул его блок как есть
	RutoSource string
	// Refused — отказ ruto словами; не пусто — не заблокировано
	Refused string

	ById           string
	ByName         string
	Client         string
	ConversationId string
	At             time.Time
}

// Final — итоговое решение: адрес заблокирован.
func (d *Decision) Final() bool {
	return d.Action == ActionBlock && d.Refused == ""
}

// Final — итоговое решение среди решений уведомления; нет — nil.
func Final(decisions []*Decision) *Decision {
	d, _ := lo.Find(decisions, func(d *Decision) bool { return d.Final() })
	return d
}

// FirstSkip — первое «не блокировать» среди решений уведомления; нет — nil.
func FirstSkip(decisions []*Decision) *Decision {
	d, _ := lo.Find(decisions, func(d *Decision) bool { return d.Action == ActionSkip })
	return d
}
