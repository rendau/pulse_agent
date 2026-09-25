// Package model — журнал вопросов агенту: кто, что спросил, что агент ответил и как к этому
// пришёл, сколько стоило (мониторинг и разбор ответов: /debug/recent, /debug/journal/{id},
// /debug/stats). Хранится в Postgres (срок — JOURNAL_RETENTION_DAYS), без PG_DSN — в памяти.
package model

import "time"

// исходы вопроса
const (
	OutcomeAnswered   = "answered"
	OutcomeIncomplete = "incomplete"
	OutcomeBusy       = "busy"
	OutcomeInvalid    = "invalid"
	OutcomeError      = "error"
)

// Entry — один вопрос.
type Entry struct {
	Id             int64
	At             time.Time
	Client         string
	ConversationId string
	UserId         string
	UserName       string
	Question       string
	Format         string
	ClientSchema   bool

	Outcome    string
	Error      string
	Incomplete string

	Duration     time.Duration
	Steps        int
	ToolCalls    int
	Tools        []string // инструменты по порядку вызова
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
	Charts       int

	// Answer и Trace — только у записи целиком (Get); в выборках списком пусто.
	// Answer — итоговый ответ (формат json и схема клиента — JSON как есть).
	Answer string
	Trace  []ToolCall
}

// ToolCall — вызов инструмента в ходе разбора: что модель попросила и что получила.
type ToolCall struct {
	Step      int // шаг модели, запросивший вызов (с 1)
	Name      string
	Arguments string // JSON аргументов от модели
	Status    string // ok | error | tool_error | skipped (agent/model.ToolStatus*)
	Output    string // что ушло модели, целиком
	Duration  time.Duration
}

// Filter — выборка журнала, новые — первыми: пустые поля — без фильтра; BeforeId — записи
// старше этой (листание); Limit ≤ 0 — все.
type Filter struct {
	Client   string
	Outcome  string
	Since    time.Time
	BeforeId int64
	Limit    int
}

// Stats — сводка по журналу за окно (Since — начало окна).
type Stats struct {
	Since     time.Time
	Questions int
	Clients   []ClientStats
	// Tools — вызовы инструментов по имени, больше всего — первым
	Tools []ToolStats
}

type ClientStats struct {
	Client       string
	Questions    int
	Outcomes     map[string]int
	AvgDuration  time.Duration
	P95Duration  time.Duration
	InputTokens  int64
	CachedTokens int64
	OutputTokens int64
}

type ToolStats struct {
	Tool  string
	Calls int
}
