// Package model — журнал вопросов агенту: кто, что, чем кончилось, сколько стоило (мониторинг:
// /debug/recent, /debug/stats). В памяти процесса, последние N.
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
	At             time.Time
	Client         string
	ConversationId string
	UserId         string
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
}

// Filter — выборка журнала: пустые поля — без фильтра; Limit ≤ 0 — все.
type Filter struct {
	Client  string
	Outcome string
	Limit   int
}

// Stats — сводка по журналу (окно — последние N вопросов, Since — самый старый в окне).
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
