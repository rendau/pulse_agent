package model

import (
	"slices"
	"time"
)

// виды сигналов и уведомлений
const (
	KindAlert  = "alert"  // сработал алерт
	KindDeploy = "deploy" // проверка после выкатки
	KindLogs   = "logs"   // всплеск ошибок в логах сервиса
	KindSelf   = "self"   // сервис сам сообщает о проблеме (манифест)
	KindPublic = "public" // проблема публичного приложения API-gateway (сбои, backend, скрипт, задержка, трафик)
	// KindSecurity — атака из интернета по журналу edge-прокси (перебор, пути взломщиков,
	// чувствительные ручки): адреса и подробности атак — только по явной подписке
	KindSecurity = "security"
)

// Kinds — все виды (подписки и приглушения).
var Kinds = []string{KindAlert, KindDeploy, KindLogs, KindSelf, KindPublic, KindSecurity}

// explicitKinds — виды только по явной подписке: подписка «любой вид» их не покрывает.
var explicitKinds = []string{KindSecurity}

// KindExplicit — вид приходит только по подписке, где он указан явно.
func KindExplicit(kind string) bool {
	return slices.Contains(explicitKinds, kind)
}

// KindKnown — вид, который можно приглушить (пусто — любой).
func KindKnown(kind string) bool {
	return kind == "" || slices.Contains(Kinds, kind)
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

// важность уведомления по возрастанию
var severityRank = map[string]int{"info": 1, "warning": 2, "critical": 3}

// SeverityKnown — важность, которую можно указать в подписке (пусто — любая).
func SeverityKnown(severity string) bool {
	_, ok := severityRank[severity]
	return severity == "" || ok
}

// Subscription — подписка беседы: какие уведомления ей присылать (пустое поле — любое; подписка
// без полей — всё, кроме видов только по явной подписке — security). Уведомления — по желанию:
// нет подписок — не приходит ничего, есть — только подходящее хотя бы под одну.
type Subscription struct {
	Id             int64
	Client         string
	ConversationId string

	Service     string
	Kind        string
	MinSeverity string // info | warning | critical; пусто — любая

	Note      string
	CreatedAt time.Time
	CreatedBy string
}

// Covers — уведомление подходит под подписку. Вид только по явной подписке (security) — только
// если он указан в подписке: «любой вид» его не покрывает.
func (s *Subscription) Covers(n *Notification) bool {
	return (s.Service == "" || s.Service == n.Service) &&
		(s.Kind == n.Kind || s.Kind == "" && !KindExplicit(n.Kind)) &&
		(s.MinSeverity == "" || severityRank[n.Severity] >= severityRank[s.MinSeverity])
}

// SubscriptionSpec — на что подписать беседу.
type SubscriptionSpec struct {
	Client         string
	ConversationId string

	Service     string
	Kind        string
	MinSeverity string
	Note        string
	By          string
}

// FeedItem — уведомление для беседы; MutedBy — приглушение, под которое оно попало (беседе
// не присылается, но видно в списке приглушённых); NotSubscribed — уведомление не подходит ни
// под одну подписку беседы (или подписок нет): не присылается.
type FeedItem struct {
	Notification  *Notification
	MutedBy       *Mute
	NotSubscribed bool
}

// MuteState — приглушение и сколько уведомлений оно уже скрыло.
type MuteState struct {
	Mute       *Mute
	Suppressed int
}

// InvestigatedFilter — разобранные агентом сигналы с Since: Kinds — только этих видов (пусто —
// любых), ExceptKinds — кроме этих. У атак (security) свой лимит разборов в час.
type InvestigatedFilter struct {
	Since       time.Time
	Kinds       []string
	ExceptKinds []string
}

// NotificationFilter — выборка уведомлений по возрастанию id.
type NotificationFilter struct {
	AfterId int64
	Since   time.Time
	Limit   int
}
