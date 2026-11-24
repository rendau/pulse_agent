// Package model — решение админа по уведомлению security (POST /v1/notifications/{id}/block) и
// его итог.
package model

import (
	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
)

// сроки блокировки, которые можно выбрать (кнопки в боте)
const (
	Duration24h = "24h"
	Duration7d  = "168h"
)

// Request — решение: Action — block | skip, Duration — только у block (24h | 168h).
type Request struct {
	Client         string
	ConversationId string
	NotificationId int64
	Action         string
	Duration       string
	User           User
}

// User — кто решил в системе клиента (админ в Telegram).
type User struct {
	Id   string
	Name string
}

// Result — решение. Repeat — итоговое решение (или первое «не блокировать» на повторный skip)
// уже было: это оно, ничего нового не сделано.
type Result struct {
	Decision *blockModel.Decision
	Repeat   bool
}
