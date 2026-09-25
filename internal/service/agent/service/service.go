package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	localConstant "github.com/mechta-market/pulse_agent/internal/service/agent/service/constant"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
)

// finalReserve — время, которое остаётся на финальный ответ модели: после
// loopDeadline инструменты больше не вызываются.
const finalReserve = time.Minute

// Config — ограничители разбора.
type Config struct {
	MaxToolCalls int
	Timeout      time.Duration
}

type Service struct {
	cfg   Config
	llm   llmI
	pulse pulseI
	chart chartI // nil — без графиков
	now   func() time.Time
}

func New(cfg Config, llm llmI, pulse pulseI, chart chartI) *Service {
	return &Service{cfg: cfg, llm: llm, pulse: pulse, chart: chart, now: time.Now}
}

func (s *Service) Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error) {
	started := s.now()

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	loopDeadline := started.Add(s.cfg.Timeout - min(finalReserve, s.cfg.Timeout/4))

	catalog, err := s.pulse.Catalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("pulse.Catalog: %w", err)
	}

	llmReq := &llmModel.Request{
		System:   localConstant.SystemPrompt(catalog.Instructions, req.Format, s.chart != nil && req.Charts),
		Messages: buildMessages(req, started),
		Tools:    lo.Map(catalog.Tools, encodeTool),
	}
	if req.Format == localConstant.FormatJson {
		llmReq.Output = &llmModel.OutputSchema{Name: localConstant.ResultSchemaName, Schema: localConstant.ResultSchema}
	}
	if s.chart != nil && req.Charts {
		llmReq.Tools = append(llmReq.Tools, llmModel.ToolDef{
			Name: localConstant.ChartTool, Description: localConstant.ChartDescription, Parameters: localConstant.ChartSchema,
		})
	}

	result := &agentModel.Result{}
	defer func() { metricRunSteps.Observe(float64(result.Steps)) }()

	for {
		if !llmReq.NoTools && s.now().After(loopDeadline) {
			result.Incomplete = agentModel.IncompleteTimeout
			llmReq.NoTools = true
		}

		resp, err := s.complete(ctx, llmReq)
		if err != nil {
			return nil, err
		}
		result.Steps++
		result.Usage.Add(resp.Usage)

		if len(resp.ToolCalls) == 0 || llmReq.NoTools {
			result.Answer = strings.TrimSpace(resp.Text)
			if resp.Incomplete != "" && result.Incomplete == "" {
				result.Incomplete = agentModel.IncompleteOutput
			}
			if llmReq.Output != nil {
				s.structure(result)
			}
			return result, nil
		}

		llmReq.State = resp.State

		// лимит вызовов: на запрошенные вызовы отвечаем отказом и просим закончить
		if result.ToolCalls+len(resp.ToolCalls) > s.cfg.MaxToolCalls {
			result.Incomplete = agentModel.IncompleteToolCalls
			llmReq.NoTools = true
			traces := lo.Map(resp.ToolCalls, func(c llmModel.ToolCall, _ int) agentModel.ToolTrace {
				return agentModel.ToolTrace{
					Step:      result.Steps,
					Name:      c.Name,
					Arguments: c.Arguments,
					Status:    agentModel.ToolStatusSkipped,
					Output:    localConstant.ToolSkipped,
				}
			})
			llmReq.ToolResults = toolResults(resp.ToolCalls, traces)
			result.Trace = append(result.Trace, traces...)
			continue
		}

		toolCtx, toolCancel := context.WithDeadline(ctx, loopDeadline)
		traces := s.callTools(toolCtx, result.Steps, resp.ToolCalls, result.Trace)
		toolCancel()
		result.Charts = collectCharts(result.Charts, traces)
		llmReq.ToolResults = toolResults(resp.ToolCalls, traces)
		result.Trace = append(result.Trace, traces...)
		result.ToolCalls += len(resp.ToolCalls)
	}
}

// structure разбирает ответ в формате json; не разобрался (ответ оборван и т.п.) — остаётся
// текст как есть, без полей.
func (s *Service) structure(result *agentModel.Result) {
	structured, err := decodeStructured(result.Answer)
	if err != nil {
		slog.Warn("agent: structured answer", "error", err, "incomplete", result.Incomplete)
		return
	}
	result.Structured = structured
	result.Answer = renderStructured(structured)
}

// complete — шаг модели с метриками.
func (s *Service) complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	provider := s.llm.Name()
	started := s.now()

	resp, err := s.llm.Complete(ctx, req)
	metricLlmRequestDuration.WithLabelValues(provider).Observe(s.now().Sub(started).Seconds())
	if err != nil {
		metricLlmRequests.WithLabelValues(provider, statusError).Inc()
		return nil, fmt.Errorf("llm.Complete: %w", err)
	}
	metricLlmRequests.WithLabelValues(provider, statusOk).Inc()

	metricLlmTokens.WithLabelValues(provider, "input").Add(float64(resp.Usage.InputTokens))
	metricLlmTokens.WithLabelValues(provider, "cached").Add(float64(resp.Usage.CachedTokens))
	metricLlmTokens.WithLabelValues(provider, "output").Add(float64(resp.Usage.OutputTokens))
	metricLlmTokens.WithLabelValues(provider, "reasoning").Add(float64(resp.Usage.ReasoningTokens))

	return resp, nil
}

// callTools выполняет вызовы шага step параллельно; ошибка вызова не роняет
// разбор, а уходит модели текстом. prior — вызовы прошлых шагов (данные для графиков).
func (s *Service) callTools(ctx context.Context, step int, calls []llmModel.ToolCall, prior []agentModel.ToolTrace) []agentModel.ToolTrace {
	traces := make([]agentModel.ToolTrace, len(calls))

	var g errgroup.Group
	for i, call := range calls {
		g.Go(func() error {
			if call.Name == localConstant.ChartTool && s.chart != nil {
				traces[i] = s.callChart(step, call, prior)
			} else {
				traces[i] = s.callTool(ctx, step, call)
			}
			return nil
		})
	}
	_ = g.Wait()

	return traces
}

// callChart — render_chart: график рисует сам бот, pulse не нужен.
func (s *Service) callChart(step int, call llmModel.ToolCall, prior []agentModel.ToolTrace) agentModel.ToolTrace {
	started := s.now()

	chart, output, err := s.renderChart(call.Arguments, prior)
	trace := agentModel.ToolTrace{Step: step, Name: call.Name, Arguments: call.Arguments, Duration: s.now().Sub(started)}
	metricToolCallDuration.WithLabelValues(call.Name).Observe(trace.Duration.Seconds())

	if err != nil {
		trace.Status, trace.Output = agentModel.ToolStatusToolError, localConstant.ToolErrorPrefix+err.Error()
		slog.Debug("chart error", "arguments", call.Arguments, "error", err)
	} else {
		trace.Status, trace.Output, trace.Chart = agentModel.ToolStatusOk, output, chart
	}
	metricToolCalls.WithLabelValues(call.Name, trace.Status).Inc()

	return trace
}

// collectCharts добавляет графики шага к построенным; сверх MaxCharts — отказ модели
// вместо «построен».
func collectCharts(charts []agentModel.Chart, traces []agentModel.ToolTrace) []agentModel.Chart {
	for i := range traces {
		if traces[i].Chart == nil {
			continue
		}
		if len(charts) >= localConstant.MaxCharts {
			traces[i].Chart = nil
			traces[i].Status = agentModel.ToolStatusToolError
			traces[i].Output = localConstant.ToolErrorPrefix + fmt.Sprintf(localConstant.ChartLimit, localConstant.MaxCharts)
			continue
		}
		charts = append(charts, *traces[i].Chart)
	}
	return charts
}

func (s *Service) callTool(ctx context.Context, step int, call llmModel.ToolCall) agentModel.ToolTrace {
	started := s.now()

	res, err := s.pulse.Call(ctx, call.Name, call.Arguments)
	trace := agentModel.ToolTrace{Step: step, Name: call.Name, Arguments: call.Arguments, Duration: s.now().Sub(started)}
	metricToolCallDuration.WithLabelValues(call.Name).Observe(trace.Duration.Seconds())

	switch {
	case err != nil:
		trace.Status, trace.Output = agentModel.ToolStatusError, localConstant.ToolErrorPrefix+err.Error()
		slog.Warn("pulse tool call failed", "tool", call.Name, "error", err)
	case res.IsError:
		trace.Status, trace.Output = agentModel.ToolStatusToolError, localConstant.ToolErrorPrefix+res.Text
		slog.Debug("pulse tool error", "tool", call.Name, "arguments", call.Arguments, "text", res.Text)
	default:
		trace.Status, trace.Output = agentModel.ToolStatusOk, res.Text
		slog.Debug("pulse tool call", "tool", call.Name, "arguments", call.Arguments, "bytes", strconv.Itoa(len(res.Text)))
	}
	metricToolCalls.WithLabelValues(call.Name, trace.Status).Inc()

	return trace
}

// toolResults — ответы модели на вызовы шага в их порядке.
func toolResults(calls []llmModel.ToolCall, traces []agentModel.ToolTrace) []llmModel.ToolResult {
	return lo.Map(calls, func(c llmModel.ToolCall, i int) llmModel.ToolResult {
		return llmModel.ToolResult{CallId: c.Id, Output: traces[i].Output}
	})
}

func buildMessages(req *agentModel.Req, now time.Time) []llmModel.Message {
	messages := lo.FlatMap(req.History, func(t agentModel.Turn, _ int) []llmModel.Message {
		return []llmModel.Message{
			{Role: llmModel.RoleUser, Text: t.Question},
			{Role: llmModel.RoleAssistant, Text: t.Answer},
		}
	})

	return append(messages, llmModel.Message{
		Role: llmModel.RoleUser,
		Text: fmt.Sprintf(localConstant.QuestionTemplate, now.UTC().Format(time.RFC3339), req.Question),
	})
}

func encodeTool(t pulseModel.Tool, _ int) llmModel.ToolDef {
	return llmModel.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}
}
