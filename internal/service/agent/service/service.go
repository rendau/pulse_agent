package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	localConstant "github.com/rendau/pulse_agent/internal/service/agent/service/constant"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
	pulseModel "github.com/rendau/pulse_agent/internal/service/pulse/model"
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
	pii   piiI
	chat  ChatToolsI // nil — без инструментов беседы (нет хранилища)
	// skills — навыки (open_skill); пусто — без навыков
	skills []agentModel.Skill
	now    func() time.Time
}

func New(cfg Config, llm llmI, pulse pulseI, chart chartI, pii piiI, chat ChatToolsI, skills []agentModel.Skill) *Service {
	return &Service{cfg: cfg, llm: llm, pulse: pulse, chart: chart, pii: pii, chat: chat, skills: skills, now: time.Now}
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

	var clientSchema map[string]any
	if req.ResponseSchema != nil {
		if clientSchema, err = strictSchema(req.ResponseSchema); err != nil {
			return nil, err
		}
	}

	chat := lo.Ternary(s.chat != nil, req.Chat, nil)
	skills := availableSkills(s.skills, chat != nil)

	llmReq := &llmModel.Request{
		System:   localConstant.SystemPrompt(catalog.Instructions, skillsList(skills), req.Format, clientSchema != nil, s.chart != nil && req.Charts),
		Messages: buildMessages(req, started, s.pii.Mask),
		Tools:    lo.Map(catalog.Tools, encodeTool),
	}
	switch {
	case clientSchema != nil:
		llmReq.Output = &llmModel.OutputSchema{Name: localConstant.ResponseSchemaName, Schema: clientSchema}
	case req.Format == localConstant.FormatJson:
		llmReq.Output = &llmModel.OutputSchema{Name: localConstant.ResultSchemaName, Schema: localConstant.ResultSchema}
	}
	if s.chart != nil && req.Charts {
		llmReq.Tools = append(llmReq.Tools, llmModel.ToolDef{
			Name: localConstant.ChartTool, Description: localConstant.ChartDescription, Parameters: localConstant.ChartSchema,
		})
	}
	if chat != nil {
		llmReq.Tools = append(llmReq.Tools, s.chat.Defs()...)
	}
	if len(skills) > 0 {
		llmReq.Tools = append(llmReq.Tools, skillTool(skills))
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
			// модель пишет токенами; клиенту — настоящие значения
			result.ModelAnswer = strings.TrimSpace(resp.Text)
			result.Answer = s.pii.Reveal(result.ModelAnswer)
			if resp.Incomplete != "" && result.Incomplete == "" {
				result.Incomplete = agentModel.IncompleteOutput
			}
			if llmReq.Output != nil {
				s.structure(result, clientSchema == nil)
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
		traces := s.callTools(toolCtx, chat, skills, result.Steps, resp.ToolCalls, result.Trace)
		toolCancel()
		result.Charts = collectCharts(result.Charts, traces)
		llmReq.ToolResults = toolResults(resp.ToolCalls, traces)
		result.Trace = append(result.Trace, traces...)
		result.ToolCalls += len(resp.ToolCalls)
	}
}

// structure разбирает ответ в JSON; не разобрался (ответ оборван и т.п.) — остаётся текст как
// есть, без полей. Схема по умолчанию (byDefault) — ещё и поля с текстом из них; схема клиента —
// JSON как есть, текст ответа — тот же JSON.
func (s *Service) structure(result *agentModel.Result, byDefault bool) {
	raw := []byte(strings.TrimSpace(result.Answer))
	if !json.Valid(raw) {
		slog.Warn("agent: answer is not valid JSON", "incomplete", result.Incomplete)
		return
	}
	if !byDefault {
		result.Json = raw
		return
	}

	structured, err := decodeStructured(result.Answer)
	if err != nil {
		slog.Warn("agent: structured answer", "error", err, "incomplete", result.Incomplete)
		return
	}
	result.Json = raw
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
func (s *Service) callTools(ctx context.Context, chat *agentModel.Chat, skills []agentModel.Skill, step int, calls []llmModel.ToolCall, prior []agentModel.ToolTrace) []agentModel.ToolTrace {
	traces := make([]agentModel.ToolTrace, len(calls))

	var g errgroup.Group
	for i, call := range calls {
		g.Go(func() error {
			switch {
			case call.Name == localConstant.ChartTool && s.chart != nil:
				traces[i] = s.callChart(step, call, prior)
			case call.Name == localConstant.SkillTool && len(skills) > 0:
				traces[i] = s.callSkill(step, call, skills)
			case chat != nil && s.chat.Has(call.Name):
				traces[i] = s.callChatTool(ctx, chat, step, call)
			default:
				traces[i] = s.callTool(ctx, step, call)
			}
			return nil
		})
	}
	_ = g.Wait()

	return traces
}

// callChart — render_chart: график рисует сам агент, pulse не нужен. Картинку видит человек —
// подписи с настоящими значениями.
func (s *Service) callChart(step int, call llmModel.ToolCall, prior []agentModel.ToolTrace) agentModel.ToolTrace {
	started := s.now()

	chart, output, err := s.renderChart(s.pii.Reveal(call.Arguments), prior)
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

// callChatTool — инструмент беседы (заметки, приглушения): настоящие значения — в хранилище,
// ответ — модели токенами.
func (s *Service) callChatTool(ctx context.Context, chat *agentModel.Chat, step int, call llmModel.ToolCall) agentModel.ToolTrace {
	started := s.now()

	output, err := s.chat.Call(ctx, chat, call.Name, s.pii.Reveal(call.Arguments))
	trace := agentModel.ToolTrace{Step: step, Name: call.Name, Arguments: call.Arguments, Duration: s.now().Sub(started)}
	metricToolCallDuration.WithLabelValues(call.Name).Observe(trace.Duration.Seconds())

	if err != nil {
		trace.Status, trace.Output = agentModel.ToolStatusToolError, localConstant.ToolErrorPrefix+s.pii.Mask(err.Error())
		slog.Debug("chat tool error", "tool", call.Name, "error", err)
	} else {
		trace.Status, trace.Output = agentModel.ToolStatusOk, s.pii.Mask(output)
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

// callTool — вызов pulse: токены в аргументах — настоящими значениями (pulse ищет сам номер),
// ответ — модели токенами. В ходе разбора — аргументы и ответ, как их видела модель.
func (s *Service) callTool(ctx context.Context, step int, call llmModel.ToolCall) agentModel.ToolTrace {
	started := s.now()

	res, err := s.pulse.Call(ctx, call.Name, s.pii.RevealArgs(call.Arguments))
	trace := agentModel.ToolTrace{Step: step, Name: call.Name, Arguments: call.Arguments, Duration: s.now().Sub(started)}
	metricToolCallDuration.WithLabelValues(call.Name).Observe(trace.Duration.Seconds())

	switch {
	case err != nil:
		trace.Status, trace.Output = agentModel.ToolStatusError, localConstant.ToolErrorPrefix+s.pii.Mask(err.Error())
		slog.Warn("pulse tool call failed", "tool", call.Name, "error", trace.Output)
	case res.IsError:
		trace.Status, trace.Output = agentModel.ToolStatusToolError, localConstant.ToolErrorPrefix+s.pii.Mask(res.Text)
		slog.Debug("pulse tool error", "tool", call.Name, "arguments", call.Arguments, "text", trace.Output)
	default:
		trace.Status, trace.Output = agentModel.ToolStatusOk, s.pii.MaskToolOutput(res.Text)
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

// buildMessages — заметки беседы, история и вопрос; персональные данные в них — токенами (mask).
func buildMessages(req *agentModel.Req, now time.Time, mask func(string) string) []llmModel.Message {
	messages := make([]llmModel.Message, 0, 2*len(req.History)+2)
	if req.Chat != nil && strings.TrimSpace(req.Chat.Notes) != "" {
		messages = append(messages, llmModel.Message{Role: llmModel.RoleUser, Text: fmt.Sprintf(localConstant.ChatNotesTemplate, mask(req.Chat.Notes))})
	}
	messages = append(messages, lo.FlatMap(req.History, func(t agentModel.Turn, _ int) []llmModel.Message {
		return []llmModel.Message{
			{Role: llmModel.RoleUser, Text: mask(t.Question)},
			{Role: llmModel.RoleAssistant, Text: mask(t.Answer)},
		}
	})...)

	return append(messages, llmModel.Message{
		Role: llmModel.RoleUser,
		Text: fmt.Sprintf(localConstant.QuestionTemplate, now.UTC().Format(time.RFC3339), mask(req.Question)),
	})
}

func encodeTool(t pulseModel.Tool, _ int) llmModel.ToolDef {
	return llmModel.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}
}
