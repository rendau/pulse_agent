package model

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/samber/lo"

	domainModel "github.com/mechta-market/pulse_agent/internal/domain/journal/model"
)

// BriefColumns — колонки выборки списком: без ответа и хода разбора (они тяжёлые — только в Get).
var BriefColumns = []string{
	"id", "at", "client", "conversation_id", "user_id", "user_name", "question", "format", "client_schema",
	"outcome", "error", "incomplete", "duration_ms", "steps", "tool_calls", "tools",
	"input_tokens", "cached_tokens", "output_tokens", "charts",
}

type Select struct {
	Id             int64
	At             time.Time
	Client         string
	ConversationId string
	UserId         string
	UserName       string
	Question       string
	Format         string
	ClientSchema   bool
	Outcome        string
	Error          string
	Incomplete     string
	DurationMs     int64
	Steps          int
	ToolCalls      int
	Tools          []string
	InputTokens    int64
	CachedTokens   int64
	OutputTokens   int64
	Charts         int
	Answer         string
	Trace          []byte // jsonb, ручная десериализация через traceJSON
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"id":              &m.Id,
		"at":              &m.At,
		"client":          &m.Client,
		"conversation_id": &m.ConversationId,
		"user_id":         &m.UserId,
		"user_name":       &m.UserName,
		"question":        &m.Question,
		"format":          &m.Format,
		"client_schema":   &m.ClientSchema,
		"outcome":         &m.Outcome,
		"error":           &m.Error,
		"incomplete":      &m.Incomplete,
		"duration_ms":     &m.DurationMs,
		"steps":           &m.Steps,
		"tool_calls":      &m.ToolCalls,
		"tools":           &m.Tools,
		"input_tokens":    &m.InputTokens,
		"cached_tokens":   &m.CachedTokens,
		"output_tokens":   &m.OutputTokens,
		"charts":          &m.Charts,
		"answer":          &m.Answer,
		"trace":           &m.Trace,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{"id": m.Id}
}

func (m *Select) DefaultSortColumns() []string {
	return []string{"id desc"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Entry {
	result := &domainModel.Entry{
		Id:             v.Id,
		At:             v.At,
		Client:         v.Client,
		ConversationId: v.ConversationId,
		UserId:         v.UserId,
		UserName:       v.UserName,
		Question:       v.Question,
		Format:         v.Format,
		ClientSchema:   v.ClientSchema,
		Outcome:        v.Outcome,
		Error:          v.Error,
		Incomplete:     v.Incomplete,
		Duration:       time.Duration(v.DurationMs) * time.Millisecond,
		Steps:          v.Steps,
		ToolCalls:      v.ToolCalls,
		Tools:          v.Tools,
		InputTokens:    v.InputTokens,
		CachedTokens:   v.CachedTokens,
		OutputTokens:   v.OutputTokens,
		Charts:         v.Charts,
		Answer:         v.Answer,
	}

	if len(v.Trace) > 0 {
		var trace []traceJSON
		if err := json.Unmarshal(v.Trace, &trace); err != nil {
			// повреждённый jsonb не должен прятать саму запись
			slog.Warn("journal trace decode failed", "id", v.Id, "error", err)
		}
		result.Trace = lo.Map(trace, encodeTrace)
	}

	return result
}
