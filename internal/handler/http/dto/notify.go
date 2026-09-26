package dto

import (
	"time"

	"github.com/samber/lo"

	chatModel "github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
)

// NotificationRep — уведомление наблюдателя. MutedBy — id приглушения беседы, под которое оно
// попало: не показывать, но подтвердить (Ack).
type NotificationRep struct {
	Id           int64     `json:"id"`
	At           time.Time `json:"at"`
	Kind         string    `json:"kind"`
	Service      string    `json:"service"`
	Key          string    `json:"key,omitempty"`
	Severity     string    `json:"severity"`
	Title        string    `json:"title"`
	Text         string    `json:"text"`
	Investigated bool      `json:"investigated"`
	MutedBy      *int64    `json:"muted_by"`
}

type NotificationsRep struct {
	Items []NotificationRep `json:"items"`
}

type AckReq struct {
	ConversationId string `json:"conversation_id"`
	LastId         int64  `json:"last_id"`
}

type AckRep struct {
	Acked bool `json:"acked"`
}

// MuteReq — приглушить в беседе: по уведомлению (кнопка под ним — его сервис) и/или по полям;
// пустое поле — любое. Duration — 30m, 2h, 1d, 7d; пусто — навсегда.
type MuteReq struct {
	ConversationId string  `json:"conversation_id"`
	NotificationId int64   `json:"notification_id,omitempty"`
	Service        string  `json:"service,omitempty"`
	Kind           string  `json:"kind,omitempty"`
	Key            string  `json:"key,omitempty"`
	Duration       string  `json:"duration,omitempty"`
	Note           string  `json:"note,omitempty"`
	User           UserReq `json:"user,omitzero"`
}

type MuteRep struct {
	Id         int64      `json:"id"`
	Service    string     `json:"service"`
	Kind       string     `json:"kind"`
	Key        string     `json:"key"`
	Until      *time.Time `json:"until"`
	Note       string     `json:"note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  string     `json:"created_by,omitempty"`
	Suppressed int        `json:"suppressed"`
}

type MutesRep struct {
	Mutes []MuteRep `json:"mutes"`
	// Muted — последние уведомления, скрытые действующими приглушениями, новые — первыми
	Muted []NotificationRep `json:"muted"`
}

type UnmuteRep struct {
	Unmuted bool `json:"unmuted"`
}

// ChatRep — контекст беседы: заметки, которые участники попросили запомнить.
type ChatRep struct {
	ConversationId string     `json:"conversation_id"`
	Notes          string     `json:"notes"`
	NotesUpdatedAt *time.Time `json:"notes_updated_at"`
	NotesUpdatedBy string     `json:"notes_updated_by,omitempty"`
}

type NotesReq struct {
	ConversationId string  `json:"conversation_id"`
	Notes          string  `json:"notes"`
	User           UserReq `json:"user,omitzero"`
}

func EncodeNotification(loc *time.Location) func(item *notifyModel.FeedItem, _ int) NotificationRep {
	return func(item *notifyModel.FeedItem, _ int) NotificationRep {
		n := item.Notification
		rep := NotificationRep{
			Id: n.Id, At: n.At.In(loc), Kind: n.Kind, Service: n.Service, Key: n.Key, Severity: n.Severity,
			Title: n.Title, Text: n.Text, Investigated: n.Investigated,
		}
		if item.MutedBy != nil {
			rep.MutedBy = &item.MutedBy.Id
		}
		return rep
	}
}

func EncodeMute(loc *time.Location) func(v *notifyModel.MuteState, _ int) MuteRep {
	return func(v *notifyModel.MuteState, _ int) MuteRep {
		m := v.Mute
		rep := MuteRep{
			Id: m.Id, Service: m.Service, Kind: m.Kind, Key: m.Key, Note: m.Note,
			CreatedAt: m.CreatedAt.In(loc), CreatedBy: m.CreatedBy, Suppressed: v.Suppressed,
		}
		if m.Until != nil {
			rep.Until = new(m.Until.In(loc))
		}
		return rep
	}
}

func EncodeChat(v *chatModel.Chat, loc *time.Location) *ChatRep {
	rep := &ChatRep{ConversationId: v.ConversationId, Notes: v.Notes, NotesUpdatedBy: v.NotesUpdatedBy}
	if v.NotesUpdatedAt != nil {
		rep.NotesUpdatedAt = new(v.NotesUpdatedAt.In(loc))
	}
	return rep
}

// UserName — кто действовал: имя, иначе id.
func (u UserReq) UserName() string {
	return lo.CoalesceOrEmpty(u.Name, u.Id)
}
