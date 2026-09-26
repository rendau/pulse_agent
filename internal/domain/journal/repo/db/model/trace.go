package model

import (
	"encoding/json"
	"time"

	domainModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
)

// traceJSON — repo-локальная DTO формата хранения jsonb-колонки trace (вызов инструмента).
// Только она знает про json-теги; доменная модель тегов не несёт.
type traceJSON struct {
	Step int    `json:"step"`
	Name string `json:"name"`
	// Arguments — JSON аргументов как есть (в SQL доступен как объект: trace->0->'arguments'->>'service');
	// невалидный JSON от модели — строкой
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Status     string          `json:"status"`
	Output     string          `json:"output,omitempty"`
	DurationMs int64           `json:"duration_ms"`
}

func encodeTrace(v traceJSON, _ int) domainModel.ToolCall {
	return domainModel.ToolCall{
		Step:      v.Step,
		Name:      v.Name,
		Arguments: encodeArguments(v.Arguments),
		Status:    v.Status,
		Output:    v.Output,
		Duration:  time.Duration(v.DurationMs) * time.Millisecond,
	}
}

func decodeTrace(v domainModel.ToolCall, _ int) traceJSON {
	return traceJSON{
		Step:       v.Step,
		Name:       v.Name,
		Arguments:  decodeArguments(v.Arguments),
		Status:     v.Status,
		Output:     v.Output,
		DurationMs: v.Duration.Milliseconds(),
	}
}

// encodeArguments — строка аргументов: JSON-строка (невалидные аргументы) — её значение,
// остальное — JSON как есть.
func encodeArguments(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func decodeArguments(s string) json.RawMessage {
	switch {
	case s == "":
		return nil
	case json.Valid([]byte(s)):
		return json.RawMessage(s)
	default:
		raw, _ := json.Marshal(s)
		return raw
	}
}
