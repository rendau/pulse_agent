package model

import "time"

// виды сигналов и уведомлений
const (
	KindAlert  = "alert"  // сработал алерт
	KindDeploy = "deploy" // проверка после выкатки
)

// KindKnown — вид, который можно приглушить (пусто — любой).
func KindKnown(kind string) bool {
	return kind == "" || kind == KindAlert || kind == KindDeploy
}

// статусы сигнала
const (
	SignalPending = "pending"
	SignalDone    = "done"
)

// чем закончился сигнал
const (
	OutcomeNotified   = "notified"   // разобран, есть уведомление
	OutcomeRaw        = "raw"        // уведомление без разбора: лимит разборов или разбор не удался
	OutcomeQuiet      = "quiet"      // разобран агентом — сообщать не о чем
	OutcomeHealthy    = "healthy"    // выкатка проверена: снапшот healthy, без разбора
	OutcomeSuperseded = "superseded" // выкатку сменила следующая — проверяется она
)

// Signal — событие из pulse, которое наблюдатель разбирает: выкатка (через DeployDelay после
// неё) или алерт (сразу). Key — дедупликация: выкатка — один раз, алерт сервиса — не чаще раза
// в AlertRepeat.
type Signal struct {
	Key     string
	Kind    string
	Service string
	// At — время события; DueAt — когда разбирать
	At    time.Time
	DueAt time.Time

	Status   string
	Attempts int

	// Summary и Details — событие, как его отдал pulse (Details — JSON)
	Summary string
	Details []byte

	Outcome        string
	NotificationId *int64
	DoneAt         *time.Time
}

// Notification — уведомление: разбор сигнала, одно на все беседы (приглушение — при выдаче).
type Notification struct {
	Id      int64
	At      time.Time
	Kind    string
	Service string
	// Key — что именно (имя алерта, коммит выкатки): приглушить можно и точечно
	Key      string
	Severity string // critical | warning | info
	Title    string
	// Text — разбор в Markdown (как формат telegram ответов)
	Text string
	// Investigated — разбор агента; false — сигнал как есть (лимит или сбой разбора)
	Investigated bool
	SignalKey    string
}

// Mute — приглушение в беседе: уведомления, подходящие по полям (пустое — любое), не
// присылаются с CreatedAt до Until (nil — навсегда). Беседа видит, что было приглушено.
type Mute struct {
	Id             int64
	Client         string
	ConversationId string

	Service string
	Kind    string
	Key     string

	Until *time.Time
	Note  string

	CreatedAt time.Time
	CreatedBy string
}

// Covers — уведомление подходит под приглушение по полям.
func (m *Mute) Covers(n *Notification) bool {
	return (m.Service == "" || m.Service == n.Service) &&
		(m.Kind == "" || m.Kind == n.Kind) &&
		(m.Key == "" || m.Key == n.Key)
}

// Active — приглушение действует в момент t.
func (m *Mute) Active(t time.Time) bool {
	return !t.Before(m.CreatedAt) && (m.Until == nil || t.Before(*m.Until))
}

// MuteSpec — что приглушить. NotificationId — взять сервис из уведомления (кнопка под ним);
// For — на сколько, 0 — навсегда.
type MuteSpec struct {
	Client         string
	ConversationId string

	NotificationId int64
	Service        string
	Kind           string
	Key            string

	For  time.Duration
	Note string
	By   string
}

// FeedItem — уведомление для беседы; MutedBy — приглушение, под которое оно попало (беседе
// не присылается, но видно в списке приглушённых).
type FeedItem struct {
	Notification *Notification
	MutedBy      *Mute
}

// MuteState — приглушение и сколько уведомлений оно уже скрыло.
type MuteState struct {
	Mute       *Mute
	Suppressed int
}

// NotificationFilter — выборка уведомлений по возрастанию id.
type NotificationFilter struct {
	AfterId int64
	Since   time.Time
	Limit   int
}
