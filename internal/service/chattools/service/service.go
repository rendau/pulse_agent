// Package service — инструменты беседы для модели: заметки (что беседа попросила запомнить),
// подписки (что присылать) и приглушения (что не присылать) уведомлений. Выполняет агент сам, pulse их не видит. Предлагаются, только когда
// вопрос задан в беседе (есть conversation_id) и есть хранилище.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	chatModel "github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	notifyModel "github.com/mechta-market/pulse_agent/internal/domain/notify/model"
	"github.com/mechta-market/pulse_agent/internal/errs"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
	"github.com/mechta-market/pulse_agent/internal/util/duration"
)

// имена инструментов
const (
	ToolNotesSave   = "chat_notes_save"
	ToolSubscribe   = "notifications_subscribe"
	ToolUnsubscribe = "notifications_unsubscribe"
	ToolMute        = "notifications_mute"
	ToolUnmute      = "notifications_unmute"
	ToolSettings    = "notifications_settings"
)

// mutedShown — сколько последних приглушённых уведомлений показывать модели.
const mutedShown = 10

var defs = []llmModel.ToolDef{
	{
		Name: ToolNotesSave,
		Description: fmt.Sprintf("Перезаписывает заметки этой беседы целиком (до %d символов) — их видишь ты в каждом "+
			"вопросе беседы. Вызывай, когда просят запомнить или забыть что-то о беседе: чья это команда, какие сервисы "+
			"интересуют, как отвечать. Передай полный новый текст: прежние заметки с изменением; пусто — забыть всё.", chatModel.MaxNotesChars),
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"notes": map[string]any{"type": "string", "description": "полный текст заметок"}},
			"required":   []any{"notes"},
		},
	},
	{
		Name: ToolSubscribe,
		Description: "Подписывает эту беседу на уведомления наблюдателя: без подписок не приходит ничего, с ними — " +
			"подходящее хотя бы под одну. Пустое поле — любое (все пустые — всё). Вызывай на «присылайте нам уведомления», " +
			"«только про X», «только критичные» — по подписке на каждый сервис. Потом назови, что будет приходить.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":      map[string]any{"type": "string", "description": "точное имя сервиса каталога (resolve_service); пусто — любой"},
				"kind":         map[string]any{"type": "string", "enum": []any{"", notifyModel.KindAlert, notifyModel.KindDeploy}, "description": "alert — алерты, deploy — проверки после выкатки; пусто — любые"},
				"min_severity": map[string]any{"type": "string", "enum": []any{"", "info", "warning", "critical"}, "description": "не ниже этой важности; пусто — любая"},
				"note":         map[string]any{"type": "string", "description": "зачем подписались, словами участника"},
			},
			"required": []any{"service", "kind", "min_severity", "note"},
		},
	},
	{
		Name:        ToolUnsubscribe,
		Description: "Убирает подписку этой беседы по id (из notifications_settings). Последнюю убрали — уведомления не приходят.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "id подписки"}},
			"required":   []any{"id"},
		},
	},
	{
		Name: ToolMute,
		Description: "Приглушает уведомления наблюдателя в этой беседе: не присылать про сервис, вид или конкретный алерт " +
			"— на время или навсегда. Пустое поле — любое (всё пустое — приглушить все уведомления). Вызывай на «не " +
			"присылай про X», «заглуши до завтра». Потом назови, что приглушено и до когда.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":  map[string]any{"type": "string", "description": "точное имя сервиса каталога (resolve_service); пусто — любой"},
				"kind":     map[string]any{"type": "string", "enum": []any{"", notifyModel.KindAlert, notifyModel.KindDeploy}, "description": "alert — алерты, deploy — проверки после выкатки; пусто — любые"},
				"key":      map[string]any{"type": "string", "description": "имя алерта (alertname), если только его; иначе пусто"},
				"duration": map[string]any{"type": "string", "description": "на сколько: 30m, 2h, 1d, 7d; пусто — навсегда (до отмены)"},
				"note":     map[string]any{"type": "string", "description": "почему приглушили, словами участника"},
			},
			"required": []any{"service", "kind", "key", "duration", "note"},
		},
	},
	{
		Name:        ToolUnmute,
		Description: "Снимает приглушение уведомлений этой беседы по id (из notifications_settings): уведомления снова приходят.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"id": map[string]any{"type": "integer", "description": "id приглушения"}},
			"required":   []any{"id"},
		},
	},
	{
		Name: ToolSettings,
		Description: "Уведомления этой беседы: подписки (что присылать; пусто — ничего), приглушения (что не присылать, до " +
			"когда, сколько уже скрыто) и последние скрытые уведомления. Вызывай на «что нам приходит», «что приглушено», " +
			"«что мы пропустили», перед тем как убрать подписку или приглушение.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	},
}

type Service struct {
	chat   chatI
	notify notifyI
}

func New(chat chatI, notify notifyI) *Service {
	return &Service{chat: chat, notify: notify}
}

func (s *Service) Defs() []llmModel.ToolDef {
	return defs
}

func (s *Service) Has(name string) bool {
	return lo.ContainsBy(defs, func(d llmModel.ToolDef) bool { return d.Name == name })
}

// Call выполняет инструмент беседы; ответ — JSON для модели. Ошибка — модели текстом.
func (s *Service) Call(ctx context.Context, chat *agentModel.Chat, name, arguments string) (string, error) {
	var args struct {
		Notes       string `json:"notes"`
		Service     string `json:"service"`
		Kind        string `json:"kind"`
		MinSeverity string `json:"min_severity"`
		Key         string `json:"key"`
		Duration    string `json:"duration"`
		Note        string `json:"note"`
		Id          int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(lo.CoalesceOrEmpty(strings.TrimSpace(arguments), "{}")), &args); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}

	switch name {
	case ToolNotesSave:
		if err := s.chat.SaveNotes(ctx, chat.Client, chat.ConversationId, args.Notes, chat.User); err != nil {
			return "", err
		}
		return `{"saved":true}`, nil
	case ToolSubscribe:
		sub, err := s.notify.Subscribe(ctx, &notifyModel.SubscriptionSpec{
			Client: chat.Client, ConversationId: chat.ConversationId,
			Service: args.Service, Kind: args.Kind, MinSeverity: args.MinSeverity, Note: args.Note, By: chat.User,
		})
		if err != nil {
			return "", err
		}
		return marshal(encodeSubscription(sub, 0))
	case ToolUnsubscribe:
		if err := s.notify.Unsubscribe(ctx, chat.Client, chat.ConversationId, args.Id); err != nil {
			return "", err
		}
		return `{"unsubscribed":true}`, nil
	case ToolMute:
		muteFor, err := duration.Parse(args.Duration)
		if err != nil {
			return "", err
		}
		m, err := s.notify.Mute(ctx, &notifyModel.MuteSpec{
			Client: chat.Client, ConversationId: chat.ConversationId,
			Service: args.Service, Kind: args.Kind, Key: args.Key, For: muteFor, Note: args.Note, By: chat.User,
		})
		if err != nil {
			return "", err
		}
		return marshal(encodeMute(&notifyModel.MuteState{Mute: m}))
	case ToolUnmute:
		if err := s.notify.Unmute(ctx, chat.Client, chat.ConversationId, args.Id); err != nil {
			return "", err
		}
		return `{"unmuted":true}`, nil
	case ToolSettings:
		subs, err := s.notify.Subscriptions(ctx, chat.Client, chat.ConversationId)
		if err != nil {
			return "", err
		}
		mutes, err := s.notify.Mutes(ctx, chat.Client, chat.ConversationId)
		if err != nil {
			return "", err
		}
		muted, err := s.notify.Muted(ctx, chat.Client, chat.ConversationId, mutedShown)
		if err != nil {
			return "", err
		}
		return marshal(map[string]any{
			"subscriptions": lo.Map(subs, encodeSubscription),
			"mutes":         lo.Map(mutes, func(m *notifyModel.MuteState, _ int) muteRep { return encodeMute(m) }),
			"recent_muted": lo.Map(muted, func(item *notifyModel.FeedItem, _ int) map[string]any {
				return map[string]any{"at": item.Notification.At.Format(time.RFC3339), "service": item.Notification.Service,
					"title": item.Notification.Title, "mute_id": item.MutedBy.Id}
			}),
		})
	}
	return "", fmt.Errorf("%w: unknown chat tool %q", errs.InvalidRequest, name)
}

type subscriptionRep struct {
	Id          int64  `json:"id"`
	Service     string `json:"service"`
	Kind        string `json:"kind"`
	MinSeverity string `json:"min_severity"`
	Note        string `json:"note,omitempty"`
	By          string `json:"by,omitempty"`
}

// encodeSubscription — подписка для модели: пустые поля — «любой».
func encodeSubscription(v *notifyModel.Subscription, _ int) subscriptionRep {
	return subscriptionRep{
		Id: v.Id, Service: lo.CoalesceOrEmpty(v.Service, "любой"), Kind: lo.CoalesceOrEmpty(v.Kind, "любой"),
		MinSeverity: lo.CoalesceOrEmpty(v.MinSeverity, "любая"), Note: v.Note, By: v.CreatedBy,
	}
}

type muteRep struct {
	Id         int64   `json:"id"`
	Service    string  `json:"service"`
	Kind       string  `json:"kind"`
	Key        string  `json:"key,omitempty"`
	Until      *string `json:"until"`
	Note       string  `json:"note,omitempty"`
	By         string  `json:"by,omitempty"`
	Suppressed int     `json:"suppressed"`
}

// encodeMute — приглушение для модели: пустые поля — «любой», until null — навсегда.
func encodeMute(v *notifyModel.MuteState) muteRep {
	m := v.Mute
	rep := muteRep{
		Id: m.Id, Service: lo.CoalesceOrEmpty(m.Service, "любой"), Kind: lo.CoalesceOrEmpty(m.Kind, "любой"), Key: m.Key,
		Note: m.Note, By: m.CreatedBy, Suppressed: v.Suppressed,
	}
	if m.Until != nil {
		rep.Until = new(m.Until.Format(time.RFC3339))
	}
	return rep
}

func marshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("json.Marshal: %w", err)
	}
	return string(raw), nil
}
