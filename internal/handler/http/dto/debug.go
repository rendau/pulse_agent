package dto

import (
	"encoding/json"
	"time"

	"github.com/samber/lo"

	journalModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
	monitorModel "github.com/rendau/pulse_agent/internal/usecase/monitor/model"
)

// EvalReq — прогон эталонных вопросов на стороне агента.
type EvalReq struct {
	// Only — id вопросов; пусто — все (кроме устаревших)
	Only []string `json:"only,omitempty"`
}

// EvalRep — прогон: Text — таблица (как в консоли), сравнение с эталоном из образа;
// Report — отчёт целиком (internal/eval.Report).
type EvalRep struct {
	Running bool   `json:"running,omitempty"`
	Text    string `json:"text,omitempty"`
	Report  any    `json:"report"`
}

// JournalEntryRep — вопрос из журнала (список: без ответа и хода разбора).
type JournalEntryRep struct {
	Id             int64     `json:"id"`
	At             time.Time `json:"at"`
	Client         string    `json:"client"`
	ConversationId string    `json:"conversation_id,omitempty"`
	UserId         string    `json:"user_id,omitempty"`
	UserName       string    `json:"user_name,omitempty"`
	Question       string    `json:"question"`
	Format         string    `json:"format,omitempty"`
	ClientSchema   bool      `json:"client_schema,omitempty"`
	Outcome        string    `json:"outcome"`
	Error          string    `json:"error,omitempty"`
	Incomplete     string    `json:"incomplete,omitempty"`
	DurationMs     int64     `json:"duration_ms"`
	Steps          int       `json:"steps"`
	ToolCalls      int       `json:"tool_calls"`
	Tools          []string  `json:"tools,omitempty"`
	InputTokens    int64     `json:"input_tokens"`
	CachedTokens   int64     `json:"cached_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	Charts         int       `json:"charts,omitempty"`
}

func EncodeJournalEntry(loc *time.Location) func(e *journalModel.Entry, _ int) JournalEntryRep {
	return func(e *journalModel.Entry, _ int) JournalEntryRep {
		return JournalEntryRep{
			Id: e.Id, At: e.At.In(loc), Client: e.Client, ConversationId: e.ConversationId, UserId: e.UserId, UserName: e.UserName,
			Question: e.Question, Format: e.Format, ClientSchema: e.ClientSchema,
			Outcome: e.Outcome, Error: e.Error, Incomplete: e.Incomplete,
			DurationMs: e.Duration.Milliseconds(), Steps: e.Steps, ToolCalls: e.ToolCalls, Tools: e.Tools,
			InputTokens: e.InputTokens, CachedTokens: e.CachedTokens, OutputTokens: e.OutputTokens, Charts: e.Charts,
		}
	}
}

// JournalEntryFullRep — вопрос целиком: ответ и ход разбора.
type JournalEntryFullRep struct {
	JournalEntryRep
	Answer string            `json:"answer"`
	Trace  []JournalToolCall `json:"trace"`
}

// JournalToolCall — вызов инструмента: аргументы — JSON как есть, output — что ушло модели.
type JournalToolCall struct {
	Step       int             `json:"step"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Status     string          `json:"status"`
	DurationMs int64           `json:"duration_ms"`
	Output     string          `json:"output,omitempty"`
}

func EncodeJournalEntryFull(e *journalModel.Entry, loc *time.Location) *JournalEntryFullRep {
	return &JournalEntryFullRep{
		JournalEntryRep: EncodeJournalEntry(loc)(e, 0),
		Answer:          e.Answer,
		Trace:           lo.Map(e.Trace, encodeJournalToolCall),
	}
}

func encodeJournalToolCall(c journalModel.ToolCall, _ int) JournalToolCall {
	rep := JournalToolCall{Step: c.Step, Name: c.Name, Status: c.Status, DurationMs: c.Duration.Milliseconds(), Output: c.Output}
	switch {
	case c.Arguments == "":
	case json.Valid([]byte(c.Arguments)):
		rep.Arguments = json.RawMessage(c.Arguments)
	default:
		// невалидный JSON от модели — строкой
		rep.Arguments, _ = json.Marshal(c.Arguments)
	}
	return rep
}

// StatsRep — сводка по журналу за окно с Since.
type StatsRep struct {
	Since     *time.Time       `json:"since"`
	Questions int              `json:"questions"`
	Clients   []ClientStatsRep `json:"clients"`
	Tools     []ToolStatsRep   `json:"tools"`
}

type ClientStatsRep struct {
	Client        string         `json:"client"`
	Questions     int            `json:"questions"`
	Outcomes      map[string]int `json:"outcomes"`
	AvgDurationMs int64          `json:"avg_duration_ms"`
	P95DurationMs int64          `json:"p95_duration_ms"`
	InputTokens   int64          `json:"input_tokens"`
	CachedTokens  int64          `json:"cached_tokens"`
	OutputTokens  int64          `json:"output_tokens"`
}

type ToolStatsRep struct {
	Tool  string `json:"tool"`
	Calls int    `json:"calls"`
}

func EncodeStats(s *journalModel.Stats, loc *time.Location) *StatsRep {
	rep := &StatsRep{
		Questions: s.Questions,
		Clients: lo.Map(s.Clients, func(c journalModel.ClientStats, _ int) ClientStatsRep {
			return ClientStatsRep{
				Client: c.Client, Questions: c.Questions, Outcomes: c.Outcomes,
				AvgDurationMs: c.AvgDuration.Milliseconds(), P95DurationMs: c.P95Duration.Milliseconds(),
				InputTokens: c.InputTokens, CachedTokens: c.CachedTokens, OutputTokens: c.OutputTokens,
			}
		}),
		Tools: lo.Map(s.Tools, func(t journalModel.ToolStats, _ int) ToolStatsRep { return ToolStatsRep{Tool: t.Tool, Calls: t.Calls} }),
	}
	if !s.Since.IsZero() {
		rep.Since = new(s.Since.In(loc))
	}
	return rep
}

// InfoRep — что за агент работает.
type InfoRep struct {
	Version         string    `json:"version"`
	StartedAt       time.Time `json:"started_at"`
	LlmProvider     string    `json:"llm_provider"`
	LlmModel        string    `json:"llm_model"`
	ReasoningEffort string    `json:"reasoning_effort"`
	MaxToolCalls    int       `json:"max_tool_calls"`
	TimeoutSec      int64     `json:"timeout_sec"`
	Clients         []string  `json:"clients"`
	EvalClients     []string  `json:"eval_clients"`
	EvalCases       []string  `json:"eval_cases"`
	// Journal — где журнал вопросов: postgres | memory; срок хранения в днях (0 — последние N в памяти)
	Journal              string   `json:"journal"`
	JournalRetentionDays int      `json:"journal_retention_days,omitempty"`
	PulseTools           []string `json:"pulse_tools"`
	PulseError           string   `json:"pulse_error,omitempty"`
}

func EncodeInfo(i *monitorModel.Info, loc *time.Location) *InfoRep {
	return &InfoRep{
		Version: i.Version, StartedAt: i.StartedAt.In(loc), LlmProvider: i.LlmProvider, LlmModel: i.LlmModel,
		ReasoningEffort: i.ReasoningEffort, MaxToolCalls: i.MaxToolCalls, TimeoutSec: int64(i.Timeout.Seconds()),
		Clients: i.Clients, EvalClients: i.EvalClients, EvalCases: i.EvalCases,
		Journal: i.Journal, JournalRetentionDays: int(i.JournalRetention.Hours() / 24),
		PulseTools: i.PulseTools, PulseError: i.PulseError,
	}
}
