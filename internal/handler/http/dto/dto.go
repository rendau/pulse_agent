// Package dto — JSON API агента (контракт для систем-клиентов: docs/agent-api.md).
package dto

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/samber/lo"

	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	chartModel "github.com/mechta-market/pulse_agent/internal/service/chart/model"
	askModel "github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

// режимы графиков в ответе (AskReq.Charts)
const (
	ChartsAll  = "all"  // картинка и данные (по умолчанию)
	ChartsPng  = "png"  // только картинка
	ChartsData = "data" // только данные — клиент рисует сам
	ChartsNone = "none" // без графиков: агент их и не строит
)

// лимиты вывода ответа инструмента в trace
const (
	DefaultTraceOutputLimit = 2000
	MaxTraceOutputLimit     = 100 * 1024 // ответ pulse ≤ 100 KB
)

// AskReq — вопрос агенту.
type AskReq struct {
	Question string `json:"question"`
	// ConversationId — беседа в системе клиента (чат, тикет): своя история; пусто — без истории
	ConversationId string `json:"conversation_id,omitempty"`
	// Reset — забыть историю беседы перед вопросом
	Reset bool    `json:"reset,omitempty"`
	User  UserReq `json:"user"`
	// Format — telegram (по умолчанию) | markdown | plain | json (ответ по полям — result)
	Format string `json:"format,omitempty"`
	// Charts — all (по умолчанию) | png | data | none
	Charts string `json:"charts,omitempty"`
	// Trace — ход разбора в ответе (вызовы инструментов); TraceOutputLimit — байт ответа
	// каждого инструмента (0 — DefaultTraceOutputLimit)
	Trace            bool `json:"trace,omitempty"`
	TraceOutputLimit int  `json:"trace_output_limit,omitempty"`
}

// UserReq — кто спросил в системе клиента: журнал (позже — доступ к данным).
type UserReq struct {
	Id   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// AskRep — ответ агента.
type AskRep struct {
	// Answer — текст ответа; при format=json — собран из result
	Answer string `json:"answer"`
	// Result — ответ по полям (format=json); null — другой формат или модель не выдала JSON
	Result *ResultRep `json:"result"`
	// Incomplete — почему разбор закончен досрочно: timeout | tool_calls | output; пусто — полный
	Incomplete string          `json:"incomplete,omitempty"`
	Charts     []ChartRep      `json:"charts"`
	DurationMs int64           `json:"duration_ms"`
	Steps      int             `json:"steps"`
	ToolCalls  int             `json:"tool_calls"`
	Usage      UsageRep        `json:"usage"`
	Trace      []*ToolTraceRep `json:"trace,omitempty"`
}

// ResultRep — ответ по полям: системе не нужно разбирать текст.
type ResultRep struct {
	Summary            string       `json:"summary"`
	Status             string       `json:"status"`
	Severity           string       `json:"severity"`
	Services           []ServiceRep `json:"services"`
	Facts              []FactRep    `json:"facts"`
	NextSteps          []string     `json:"next_steps"`
	UnavailableSources []string     `json:"unavailable_sources"`
}

type ServiceRep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

type FactRep struct {
	Text    string     `json:"text"`
	Time    *time.Time `json:"time"`
	Service *string    `json:"service"`
}

// ChartRep — график: картинка (PNG в base64) и/или данные, по которым она нарисована.
type ChartRep struct {
	Title string        `json:"title"`
	Type  string        `json:"type"`
	Unit  string        `json:"unit,omitempty"`
	Png   string        `json:"png,omitempty"`
	Data  *ChartDataRep `json:"data,omitempty"`
}

// ChartDataRep — ряды графика: у line x — время RFC3339, у bar — подпись категории;
// y — в единицах unit, как пришло из источника (байты, доли, секунды).
type ChartDataRep struct {
	Series []ChartSeriesRep `json:"series"`
}

type ChartSeriesRep struct {
	Name   string          `json:"name"`
	Points []ChartPointRep `json:"points"`
}

type ChartPointRep struct {
	X string  `json:"x"`
	Y float64 `json:"y"`
}

type UsageRep struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// ToolTraceRep — вызов инструмента; Output — что ушло модели (с усечением).
type ToolTraceRep struct {
	Step        int    `json:"step"`
	Tool        string `json:"tool"`
	Arguments   string `json:"arguments"`
	Status      string `json:"status"`
	DurationMs  int64  `json:"duration_ms"`
	OutputBytes int    `json:"output_bytes"`
	Truncated   bool   `json:"truncated,omitempty"`
	Output      string `json:"output"`
}

// ResetReq — забыть историю беседы.
type ResetReq struct {
	ConversationId string `json:"conversation_id"`
}

type ResetRep struct {
	Reset bool `json:"reset"`
}

// ErrorRep — ошибка: code — машинный (invalid_request, unauthorized, busy, timeout, …).
type ErrorRep struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

// EncodeAskRep собирает ответ; charts и trace — из запроса.
func EncodeAskRep(a *askModel.Answer, req *AskReq, loc *time.Location) *AskRep {
	rep := &AskRep{
		Answer:     a.Text,
		Result:     encodeResult(a.Structured, loc),
		Incomplete: a.Incomplete,
		Charts:     lo.Map(a.Charts, func(c agentModel.Chart, _ int) ChartRep { return encodeChart(c, req.Charts, loc) }),
		Steps:      a.Steps,
		ToolCalls:  a.ToolCalls,
		Usage: UsageRep{
			InputTokens:     a.Usage.InputTokens,
			CachedTokens:    a.Usage.CachedTokens,
			OutputTokens:    a.Usage.OutputTokens,
			ReasoningTokens: a.Usage.ReasoningTokens,
		},
	}
	if req.Trace {
		limit := lo.Ternary(req.TraceOutputLimit <= 0, DefaultTraceOutputLimit, min(req.TraceOutputLimit, MaxTraceOutputLimit))
		rep.Trace = lo.Map(a.Trace, func(t agentModel.ToolTrace, _ int) *ToolTraceRep {
			output, truncated := truncate(t.Output, limit)
			return &ToolTraceRep{
				Step: t.Step, Tool: t.Name, Arguments: t.Arguments, Status: t.Status,
				DurationMs: t.Duration.Milliseconds(), OutputBytes: len(t.Output), Truncated: truncated, Output: output,
			}
		})
	}
	return rep
}

func encodeResult(s *agentModel.Structured, loc *time.Location) *ResultRep {
	if s == nil {
		return nil
	}
	return &ResultRep{
		Summary:  s.Summary,
		Status:   s.Status,
		Severity: s.Severity,
		Services: lo.Map(s.Services, func(v agentModel.StructuredService, _ int) ServiceRep {
			return ServiceRep{Name: v.Name, Status: v.Status, Note: v.Note}
		}),
		Facts: lo.Map(s.Facts, func(f agentModel.Fact, _ int) FactRep {
			fact := FactRep{Text: f.Text, Service: lo.EmptyableToPtr(f.Service)}
			if f.Time != nil {
				fact.Time = new(f.Time.In(loc))
			}
			return fact
		}),
		NextSteps:          lo.Ternary(s.NextSteps == nil, []string{}, s.NextSteps),
		UnavailableSources: lo.Ternary(s.UnavailableSources == nil, []string{}, s.UnavailableSources),
	}
}

func encodeChart(c agentModel.Chart, mode string, loc *time.Location) ChartRep {
	rep := ChartRep{Title: c.Title}
	if c.Spec != nil {
		rep.Type, rep.Unit = lo.CoalesceOrEmpty(c.Spec.Type, chartModel.TypeLine), c.Spec.Unit
	}
	if mode != ChartsData {
		rep.Png = base64.StdEncoding.EncodeToString(c.Png)
	}
	if mode != ChartsPng && c.Spec != nil {
		rep.Data = &ChartDataRep{Series: lo.Map(c.Spec.Series, func(s chartModel.Series, _ int) ChartSeriesRep {
			return ChartSeriesRep{Name: s.Name, Points: lo.Map(s.Points, func(p chartModel.Point, _ int) ChartPointRep {
				x := p.Label
				if !p.Time.IsZero() {
					x = p.Time.In(loc).Format(time.RFC3339)
				}
				return ChartPointRep{X: x, Y: p.Value}
			})}
		})}
	}
	return rep
}

// truncate режет s до limit байт, не разрывая UTF-8.
func truncate(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	return strings.ToValidUTF8(s[:limit], ""), true
}
