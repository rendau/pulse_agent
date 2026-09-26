package model

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/samber/lo"

	domainModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
)

// Upsert — новая запись журнала (записи не меняются: только Create).
type Upsert struct {
	NewId int64 // id новой записи, заполняется в Create через RETURNING

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
	Trace          []byte // jsonb через traceJSON; nil — вызовов не было
}

func (m *Upsert) CreateColumnMap() map[string]any {
	return map[string]any{
		"at":              m.At,
		"client":          m.Client,
		"conversation_id": m.ConversationId,
		"user_id":         m.UserId,
		"user_name":       m.UserName,
		"question":        m.Question,
		"format":          m.Format,
		"client_schema":   m.ClientSchema,
		"outcome":         m.Outcome,
		"error":           m.Error,
		"incomplete":      m.Incomplete,
		"duration_ms":     m.DurationMs,
		"steps":           m.Steps,
		"tool_calls":      m.ToolCalls,
		"tools":           m.Tools,
		"input_tokens":    m.InputTokens,
		"cached_tokens":   m.CachedTokens,
		"output_tokens":   m.OutputTokens,
		"charts":          m.Charts,
		"answer":          m.Answer,
		"trace":           m.Trace,
	}
}

func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{"id": &m.NewId}
}

// DTO

func DecodeUpsert(v *domainModel.Entry) (*Upsert, error) {
	result := &Upsert{
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
		DurationMs:     v.Duration.Milliseconds(),
		Steps:          v.Steps,
		ToolCalls:      v.ToolCalls,
		Tools:          lo.CoalesceSliceOrEmpty(v.Tools),
		InputTokens:    v.InputTokens,
		CachedTokens:   v.CachedTokens,
		OutputTokens:   v.OutputTokens,
		Charts:         v.Charts,
		Answer:         v.Answer,
	}

	if len(v.Trace) > 0 {
		trace, err := json.Marshal(lo.Map(v.Trace, decodeTrace))
		if err != nil {
			return nil, fmt.Errorf("json.Marshal trace: %w", err)
		}
		result.Trace = trace
	}

	return result, nil
}
