package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/samber/lo"

	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
	"github.com/mechta-market/pulse_agent/internal/util/duration"
)

// пути ленты уведомлений и контекста беседы
const (
	PathNotifications = "/v1/notifications"
	PathAck           = "/v1/notifications/ack"
	PathMutes         = "/v1/mutes"
	PathSubscriptions = "/v1/subscriptions"
	PathChat          = "/v1/chat"
	PathChatNotes     = "/v1/chat/notes"
)

// codeUnavailable — лента и контекст бесед без хранилища (PG_DSN пуст)
const codeUnavailable = "unavailable"

func (h *Handler) registerNotify(mux *http.ServeMux) {
	mux.HandleFunc("GET "+PathNotifications, h.withClient(h.withNotify(h.Notifications)))
	mux.HandleFunc("POST "+PathAck, h.withClient(h.withNotify(h.Ack)))
	mux.HandleFunc("GET "+PathMutes, h.withClient(h.withNotify(h.Mutes)))
	mux.HandleFunc("POST "+PathMutes, h.withClient(h.withNotify(h.Mute)))
	mux.HandleFunc("DELETE "+PathMutes+"/{id}", h.withClient(h.withNotify(h.Unmute)))
	mux.HandleFunc("GET "+PathSubscriptions, h.withClient(h.withNotify(h.Subscriptions)))
	mux.HandleFunc("POST "+PathSubscriptions, h.withClient(h.withNotify(h.Subscribe)))
	mux.HandleFunc("DELETE "+PathSubscriptions+"/{id}", h.withClient(h.withNotify(h.Unsubscribe)))
	mux.HandleFunc("GET "+PathChat, h.withClient(h.withNotify(h.Chat)))
	mux.HandleFunc("PUT "+PathChatNotes, h.withClient(h.withNotify(h.SaveNotes)))
}

// withNotify — без хранилища ленты и бесед нет: 503.
func (h *Handler) withNotify(next func(w http.ResponseWriter, r *http.Request, client string)) func(w http.ResponseWriter, r *http.Request, client string) {
	return func(w http.ResponseWriter, r *http.Request, client string) {
		if h.notify == nil {
			writeError(w, http.StatusServiceUnavailable, codeUnavailable, "notifications and chat context need PG_DSN")
			return
		}
		next(w, r, client)
	}
}

// Notifications — GET /v1/notifications?conversation_id=&limit=: новые уведомления беседы
// (после подтверждённых). Первый вызов беседы подписывает её с текущего места — пусто.
func (h *Handler) Notifications(w http.ResponseWriter, r *http.Request, client string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.notify.Feed(r.Context(), client, r.URL.Query().Get("conversation_id"), limit)
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.NotificationsRep{Items: lo.Map(items, dto.EncodeNotification(h.loc))})
}

// Ack — POST /v1/notifications/ack: беседа получила уведомления до last_id включительно.
func (h *Handler) Ack(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.AckReq{}
	if !decode(w, r, req) {
		return
	}
	if err := h.notify.Ack(r.Context(), client, req.ConversationId, req.LastId); err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.AckRep{Acked: true})
}

// Mutes — GET /v1/mutes?conversation_id=&muted_limit=: приглушения беседы и что они скрыли.
func (h *Handler) Mutes(w http.ResponseWriter, r *http.Request, client string) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("muted_limit"))
	mutes, muted, err := h.notify.Mutes(r.Context(), client, r.URL.Query().Get("conversation_id"), lo.Ternary(limit > 0, limit, 10))
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.MutesRep{
		Mutes: lo.Map(mutes, dto.EncodeMute(h.loc)),
		Muted: lo.Map(muted, dto.EncodeNotification(h.loc)),
	})
}

// Mute — POST /v1/mutes: приглушить в беседе.
func (h *Handler) Mute(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.MuteReq{}
	if !decode(w, r, req) {
		return
	}
	muteFor, err := duration.Parse(req.Duration)
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	m, err := h.notify.Mute(r.Context(), client, req.ConversationId, &notifyModel.MuteSpec{
		NotificationId: req.NotificationId, Service: req.Service, Kind: req.Kind, Key: req.Key,
		For: muteFor, Note: req.Note, By: req.User.UserName(),
	})
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, new(dto.EncodeMute(h.loc)(&notifyModel.MuteState{Mute: m}, 0)))
}

// Unmute — DELETE /v1/mutes/{id}?conversation_id=: снять приглушение.
func (h *Handler) Unmute(w http.ResponseWriter, r *http.Request, client string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "id: expected positive integer")
		return
	}
	if err = h.notify.Unmute(r.Context(), client, r.URL.Query().Get("conversation_id"), id); err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.UnmuteRep{Unmuted: true})
}

// Subscriptions — GET /v1/subscriptions?conversation_id=: подписки беседы (пусто — приходит всё).
func (h *Handler) Subscriptions(w http.ResponseWriter, r *http.Request, client string) {
	subs, err := h.notify.Subscriptions(r.Context(), client, r.URL.Query().Get("conversation_id"))
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.SubscriptionsRep{Subscriptions: lo.Map(subs, dto.EncodeSubscription(h.loc))})
}

// Subscribe — POST /v1/subscriptions: подписать беседу.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.SubscriptionReq{}
	if !decode(w, r, req) {
		return
	}
	sub, err := h.notify.Subscribe(r.Context(), client, req.ConversationId, &notifyModel.SubscriptionSpec{
		Service: req.Service, Kind: req.Kind, MinSeverity: req.MinSeverity, Note: req.Note, By: req.User.UserName(),
	})
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, new(dto.EncodeSubscription(h.loc)(sub, 0)))
}

// Unsubscribe — DELETE /v1/subscriptions/{id}?conversation_id=: убрать подписку.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request, client string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "id: expected positive integer")
		return
	}
	if err = h.notify.Unsubscribe(r.Context(), client, r.URL.Query().Get("conversation_id"), id); err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, &dto.UnsubscribeRep{Unsubscribed: true})
}

// Chat — GET /v1/chat?conversation_id=: заметки беседы.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request, client string) {
	chat, err := h.notify.Chat(r.Context(), client, r.URL.Query().Get("conversation_id"))
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, dto.EncodeChat(chat, h.loc))
}

// SaveNotes — PUT /v1/chat/notes: перезаписать заметки беседы (пусто — забыть).
func (h *Handler) SaveNotes(w http.ResponseWriter, r *http.Request, client string) {
	req := &dto.NotesReq{}
	if !decode(w, r, req) {
		return
	}
	if err := h.notify.SaveNotes(r.Context(), client, req.ConversationId, req.Notes, req.User.UserName()); err != nil {
		writeFail(w, r, client, err)
		return
	}
	chat, err := h.notify.Chat(r.Context(), client, strings.TrimSpace(req.ConversationId))
	if err != nil {
		writeFail(w, r, client, err)
		return
	}
	writeJson(w, http.StatusOK, dto.EncodeChat(chat, h.loc))
}
