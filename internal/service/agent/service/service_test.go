package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	localConstant "github.com/mechta-market/pulse_agent/internal/service/agent/service/constant"
	chartModel "github.com/mechta-market/pulse_agent/internal/service/chart/model"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
)

// fakeLlm отвечает заранее заданными шагами и запоминает запросы.
type fakeLlm struct {
	steps    []*llmModel.Response
	requests []llmModel.Request
}

func (f *fakeLlm) Name() string { return "fake" }

func (f *fakeLlm) Complete(_ context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	f.requests = append(f.requests, *req)
	if len(f.steps) == 0 {
		return nil, errors.New("unexpected step")
	}
	resp := f.steps[0]
	f.steps = f.steps[1:]
	return resp, nil
}

type fakePulse struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (f *fakePulse) Catalog(context.Context) (*pulseModel.Catalog, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &pulseModel.Catalog{
		Tools:        []pulseModel.Tool{{Name: "resolve_service", InputSchema: map[string]any{"type": "object"}}},
		Instructions: "сначала resolve_service",
	}, nil
}

func (f *fakePulse) Call(_ context.Context, name, arguments string) (*pulseModel.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+arguments)

	switch name {
	case "query_metrics":
		return &pulseModel.CallResult{Text: metricsOutput}, nil
	case "broken":
		return nil, errors.New("connection refused")
	case "bad_args":
		return &pulseModel.CallResult{Text: "unknown service", IsError: true}, nil
	}
	return &pulseModel.CallResult{Text: `{"ok":true}`}, nil
}

func toolStep(state string, calls ...llmModel.ToolCall) *llmModel.Response {
	return &llmModel.Response{ToolCalls: calls, State: state, Usage: llmModel.Usage{InputTokens: 100, OutputTokens: 10}}
}

func textStep(text string) *llmModel.Response {
	return &llmModel.Response{Text: text, Usage: llmModel.Usage{InputTokens: 100, CachedTokens: 80, OutputTokens: 20}}
}

func newService(llm *fakeLlm, pulse *fakePulse, maxToolCalls int) *Service {
	return New(Config{MaxToolCalls: maxToolCalls, Timeout: 5 * time.Minute}, llm, pulse, nil)
}

func TestRun_ToolLoop(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1",
			llmModel.ToolCall{Id: "c1", Name: "resolve_service", Arguments: `{"query":"caravan"}`},
			llmModel.ToolCall{Id: "c2", Name: "bad_args", Arguments: `{}`},
			llmModel.ToolCall{Id: "c3", Name: "broken", Arguments: `{}`},
		),
		textStep("  caravan в порядке  "),
	}}
	pulse := &fakePulse{}

	res, err := newService(llm, pulse, 20).Run(context.Background(), &agentModel.Req{
		History:  []agentModel.Turn{{Question: "что с кластером?", Answer: "всё ок"}},
		Question: "что с caravan?",
	})
	require.NoError(t, err)

	assert.Equal(t, "caravan в порядке", res.Answer)
	assert.Empty(t, res.Incomplete)
	assert.Equal(t, 2, res.Steps)
	assert.Equal(t, 3, res.ToolCalls)
	assert.Equal(t, llmModel.Usage{InputTokens: 200, CachedTokens: 80, OutputTokens: 30}, res.Usage)

	// первый шаг: системный промпт с instructions pulse, история + вопрос со временем
	first := llm.requests[0]
	assert.Contains(t, first.System, "сначала resolve_service")
	require.Len(t, first.Messages, 3)
	assert.Equal(t, llmModel.Message{Role: llmModel.RoleUser, Text: "что с кластером?"}, first.Messages[0])
	assert.Equal(t, llmModel.Message{Role: llmModel.RoleAssistant, Text: "всё ок"}, first.Messages[1])
	assert.True(t, strings.HasSuffix(first.Messages[2].Text, "что с caravan?"))
	assert.Contains(t, first.Messages[2].Text, "Текущее время: ")
	require.Len(t, first.Tools, 1)
	assert.Nil(t, first.State)

	// второй шаг: состояние адаптера и результаты вызовов в порядке вызовов
	second := llm.requests[1]
	assert.Equal(t, "s1", second.State)
	assert.False(t, second.NoTools)
	assert.Equal(t, []llmModel.ToolResult{
		{CallId: "c1", Output: `{"ok":true}`},
		{CallId: "c2", Output: localConstant.ToolErrorPrefix + "unknown service"},
		{CallId: "c3", Output: localConstant.ToolErrorPrefix + "connection refused"},
	}, second.ToolResults)

	// ход разбора: вызовы по порядку, со статусами и тем, что ушло модели
	require.Len(t, res.Trace, 3)
	assert.Equal(t, []string{agentModel.ToolStatusOk, agentModel.ToolStatusToolError, agentModel.ToolStatusError},
		lo.Map(res.Trace, func(tr agentModel.ToolTrace, _ int) string { return tr.Status }))
	assert.Equal(t, 1, res.Trace[0].Step)
	assert.Equal(t, "resolve_service", res.Trace[0].Name)
	assert.JSONEq(t, `{"query":"caravan"}`, res.Trace[0].Arguments)
	assert.Equal(t, second.ToolResults[2].Output, res.Trace[2].Output)
}

func TestRun_ToolCallsLimit(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1", llmModel.ToolCall{Id: "c1", Name: "resolve_service"}),
		toolStep("s2",
			llmModel.ToolCall{Id: "c2", Name: "resolve_service"},
			llmModel.ToolCall{Id: "c3", Name: "resolve_service"},
		),
		textStep("что успел"),
	}}
	pulse := &fakePulse{}

	res, err := newService(llm, pulse, 2).Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
	require.NoError(t, err)

	assert.Equal(t, "что успел", res.Answer)
	assert.Equal(t, agentModel.IncompleteToolCalls, res.Incomplete)
	assert.Equal(t, 1, res.ToolCalls)
	assert.Len(t, pulse.calls, 1)

	// вызовы сверх лимита не выполняются, модель просят закончить без инструментов
	last := llm.requests[2]
	assert.True(t, last.NoTools)
	assert.Equal(t, "s2", last.State)
	assert.Equal(t, []llmModel.ToolResult{
		{CallId: "c2", Output: localConstant.ToolSkipped},
		{CallId: "c3", Output: localConstant.ToolSkipped},
	}, last.ToolResults)

	require.Len(t, res.Trace, 3)
	assert.Equal(t, agentModel.ToolStatusOk, res.Trace[0].Status)
	assert.Equal(t, agentModel.ToolStatusSkipped, res.Trace[1].Status)
	assert.Equal(t, 2, res.Trace[2].Step)
}

func TestRun_Timeout(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1", llmModel.ToolCall{Id: "c1", Name: "resolve_service"}),
		textStep("по времени"),
	}}
	svc := newService(llm, &fakePulse{}, 20)

	// часы: старт, затем сразу после loopDeadline
	start := time.Now()
	ticks := 0
	svc.now = func() time.Time {
		ticks++
		if ticks <= 2 {
			return start
		}
		return start.Add(5 * time.Minute)
	}

	res, err := svc.Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
	require.NoError(t, err)

	assert.Equal(t, "по времени", res.Answer)
	assert.Equal(t, agentModel.IncompleteTimeout, res.Incomplete)
	assert.True(t, llm.requests[1].NoTools)
}

func TestRun_OutputTruncated(t *testing.T) {
	resp := textStep("обрыв")
	resp.Incomplete = "max_output_tokens"
	llm := &fakeLlm{steps: []*llmModel.Response{resp}}

	res, err := newService(llm, &fakePulse{}, 20).Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
	require.NoError(t, err)

	assert.Equal(t, agentModel.IncompleteOutput, res.Incomplete)
}

func TestRun_Errors(t *testing.T) {
	t.Run("pulse unavailable", func(t *testing.T) {
		_, err := newService(&fakeLlm{}, &fakePulse{err: errors.New("dial tcp: refused")}, 20).
			Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pulse.Catalog")
	})

	t.Run("llm failed", func(t *testing.T) {
		_, err := newService(&fakeLlm{}, &fakePulse{}, 20).
			Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "llm.Complete")
	})
}

const metricsOutput = `{"service":"caravan","metric_id":"memory_bytes","title":"Память","unit":"bytes","series":[
	{"labels":{"pod":"caravan-api-1"},"points":[{"ts":"2026-09-25T10:00:00+05:00","value":500},{"ts":"2026-09-25T10:05:00+05:00","value":520}]},
	{"labels":{"pod":"caravan-api-2"},"points":[{"ts":"2026-09-25T10:00:00+05:00","value":300}]}]}`

// fakeChart запоминает, что просили нарисовать.
type fakeChart struct {
	mu    sync.Mutex
	specs []*chartModel.Spec
}

func (f *fakeChart) Render(spec *chartModel.Spec) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(spec.Series) == 0 {
		return nil, errors.New("no points")
	}
	f.specs = append(f.specs, spec)
	return []byte("png"), nil
}

func TestRun_Charts(t *testing.T) {
	llm := &fakeLlm{steps: []*llmModel.Response{
		toolStep("s1", llmModel.ToolCall{Id: "c1", Name: "query_metrics", Arguments: `{"service":"caravan","metric_id":"memory_bytes","window":"1h"}`}),
		toolStep("s2",
			// ряды — из ответа query_metrics, единица и заголовок — оттуда же, если не заданы
			llmModel.ToolCall{Id: "c2", Name: localConstant.ChartTool, Arguments: `{"type":"line","title":"","metrics":[{"service":"caravan","metric_id":"memory_bytes"}]}`},
			// свои точки
			llmModel.ToolCall{Id: "c3", Name: localConstant.ChartTool, Arguments: `{"type":"bar","title":"Ошибки за сутки","unit":"count","series":[{"name":"ошибки","points":[{"x":"seller","y":832},{"x":"stg","y":245}]}]}`},
			// query_metrics для сервиса не вызывался
			llmModel.ToolCall{Id: "c4", Name: localConstant.ChartTool, Arguments: `{"type":"line","title":"x","metrics":[{"service":"orders"}]}`},
		),
		textStep("память в норме"),
	}}
	chart := &fakeChart{}

	res, err := New(Config{MaxToolCalls: 20, Timeout: 5 * time.Minute}, llm, &fakePulse{}, chart).Run(context.Background(), &agentModel.Req{Question: "память caravan?", Charts: true})
	require.NoError(t, err)

	assert.True(t, lo.ContainsBy(llm.requests[0].Tools, func(d llmModel.ToolDef) bool { return d.Name == localConstant.ChartTool }))

	require.Len(t, res.Charts, 2)
	assert.Equal(t, "Память caravan", res.Charts[0].Title)
	assert.Equal(t, "Ошибки за сутки", res.Charts[1].Title)

	require.Len(t, chart.specs, 2)
	line := lo.FindOrElse(chart.specs, nil, func(s *chartModel.Spec) bool { return s.Type == "line" })
	require.NotNil(t, line)
	assert.Equal(t, "bytes", line.Unit)
	require.Len(t, line.Series, 2)
	assert.Equal(t, "caravan-api-1", line.Series[0].Name)
	assert.InDelta(t, 520, line.Series[0].Points[1].Value, 0)

	results := llm.requests[2].ToolResults
	require.Len(t, results, 3)
	assert.Contains(t, results[0].Output, "OK: график")
	assert.Contains(t, results[1].Output, "OK: график")
	assert.Contains(t, results[2].Output, "was not called in this conversation")
}

func TestRun_ChartsLimit(t *testing.T) {
	calls := make([]llmModel.ToolCall, 0, localConstant.MaxCharts+1)
	for i := range localConstant.MaxCharts + 1 {
		calls = append(calls, llmModel.ToolCall{Id: fmt.Sprintf("c%d", i), Name: localConstant.ChartTool,
			Arguments: `{"type":"bar","title":"t","series":[{"points":[{"x":"a","y":1}]}]}`})
	}
	llm := &fakeLlm{steps: []*llmModel.Response{toolStep("s1", calls...), textStep("ok")}}

	res, err := New(Config{MaxToolCalls: 20, Timeout: 5 * time.Minute}, llm, &fakePulse{}, &fakeChart{}).Run(context.Background(), &agentModel.Req{Question: "q", Charts: true})
	require.NoError(t, err)
	assert.Len(t, res.Charts, localConstant.MaxCharts)
	assert.Contains(t, llm.requests[1].ToolResults[localConstant.MaxCharts].Output, "не больше")
}

func TestRun_JsonFormat(t *testing.T) {
	structured := `{"summary":"caravan деградировал: задержка выросла","status":"degraded","severity":"medium",
		"services":[{"name":"caravan","status":"degraded","note":"p95 650 мс"}],
		"facts":[{"text":"p95 650 мс против 19 мс час назад","time":"2026-09-25T14:58:00+05:00","service":"caravan"},
		         {"text":"деплоев за сутки не было","time":null,"service":null}],
		"next_steps":["проверить медленные запросы"],"unavailable_sources":["loki"]}`
	llm := &fakeLlm{steps: []*llmModel.Response{textStep(structured)}}

	res, err := New(Config{MaxToolCalls: 20, Timeout: 5 * time.Minute}, llm, &fakePulse{}, &fakeChart{}).
		Run(context.Background(), &agentModel.Req{Question: "что с caravan?", Format: localConstant.FormatJson})
	require.NoError(t, err)

	req := llm.requests[0]
	require.NotNil(t, req.Output, "итог — по схеме")
	assert.Equal(t, localConstant.ResultSchemaName, req.Output.Name)
	assert.Contains(t, req.System, "Итог — JSON по заданной схеме")
	assert.NotContains(t, req.System, "Markdown для Telegram")
	assert.False(t, lo.ContainsBy(req.Tools, func(d llmModel.ToolDef) bool { return d.Name == localConstant.ChartTool }),
		"клиент не просил графиков — инструмент не предлагается")

	require.NotNil(t, res.Structured)
	assert.Equal(t, "degraded", res.Structured.Status)
	require.Len(t, res.Structured.Facts, 2)
	require.NotNil(t, res.Structured.Facts[0].Time)
	assert.Equal(t, "caravan", res.Structured.Facts[0].Service)
	assert.Nil(t, res.Structured.Facts[1].Time)
	assert.Equal(t, "caravan деградировал: задержка выросла\n\n"+
		"- 25.09 14:58 +05:00 — caravan: p95 650 мс против 19 мс час назад\n"+
		"- деплоев за сутки не было\n\n"+
		"Дальше:\n- проверить медленные запросы\n\n"+
		"Не ответили источники: loki.", res.Answer)

	// модель не выдала JSON — текст как есть, без полей
	llm = &fakeLlm{steps: []*llmModel.Response{textStep("не JSON")}}
	res, err = New(Config{MaxToolCalls: 20, Timeout: 5 * time.Minute}, llm, &fakePulse{}, nil).
		Run(context.Background(), &agentModel.Req{Question: "q", Format: localConstant.FormatJson})
	require.NoError(t, err)
	assert.Nil(t, res.Structured)
	assert.Equal(t, "не JSON", res.Answer)
}

func TestSystemPrompt_Formats(t *testing.T) {
	telegram := localConstant.SystemPrompt("instr", "", true)
	plain := localConstant.SystemPrompt("instr", localConstant.FormatPlain, false)

	assert.Contains(t, telegram, "Markdown для Telegram")
	assert.Contains(t, telegram, "render_chart")
	assert.Contains(t, plain, "Без разметки")
	assert.NotContains(t, plain, "render_chart")

	// общий префикс — до оформления: кэш провайдера один на все форматы
	prefix := telegram[:strings.Index(telegram, "Оформление ответа")]
	assert.True(t, strings.HasPrefix(plain, prefix))
	assert.Contains(t, prefix, "instr")
}
