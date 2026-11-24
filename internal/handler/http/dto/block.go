package dto

import (
	"time"

	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
	usecaseBlockModel "github.com/rendau/pulse_agent/internal/usecase/block/model"
)

// BlockReq — решение админа по уведомлению security: action — block | skip, duration — только у
// block: 24h | 168h.
type BlockReq struct {
	ConversationId string  `json:"conversation_id"`
	Action         string  `json:"action"`
	Duration       string  `json:"duration,omitempty"`
	User           UserReq `json:"user,omitzero"`
}

// BlockRep — решение. Ip — сеть, как её записал ruto (у skip и отказа — ключ уведомления); Until —
// конец блокировки RFC3339 (пусто — skip, отказ или блок человека без конца); Repeat — решение уже
// было, это оно, ничего нового не сделано; Refused — отказ ruto словами: не пусто — не заблокировано.
type BlockRep struct {
	NotificationId int64     `json:"notification_id"`
	Ip             string    `json:"ip"`
	Action         string    `json:"action"`
	Until          string    `json:"until"`
	By             UserReq   `json:"by"`
	At             time.Time `json:"at"`
	Repeat         bool      `json:"repeat"`
	Refused        string    `json:"refused"`
}

func EncodeBlock(v *usecaseBlockModel.Result, loc *time.Location) *BlockRep {
	d := v.Decision
	rep := &BlockRep{
		NotificationId: d.NotificationId, Ip: d.Ip, Action: d.Action, By: UserReq{Id: d.ById, Name: d.ByName},
		At: d.At.In(loc), Repeat: v.Repeat, Refused: d.Refused,
	}
	if d.Action == blockModel.ActionBlock && d.Until != nil {
		rep.Until = d.Until.In(loc).Format(time.RFC3339)
	}
	return rep
}
