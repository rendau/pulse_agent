// Package service — адаптер OpenAI (Responses API) для провайдер-независимого контракта llm.
//
// Работаем без хранения на стороне OpenAI (store=false): в запросы уходят логи и
// конфигурация сервисов из pulse, им незачем оседать у провайдера. Поэтому контекст
// разбора накапливается у нас — входные элементы плюс выходные элементы каждого шага
// (включая зашифрованный reasoning, чтобы модель не теряла ход рассуждений между
// вызовами инструментов) — и целиком передаётся в следующий шаг через Response.State.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/samber/lo"

	"github.com/rendau/pulse_agent/internal/constant"
	"github.com/rendau/pulse_agent/internal/errs"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

// Config — параметры адаптера.
type Config struct {
	ApiKey          string
	BaseUrl         string // пусто — api.openai.com
	Model           string
	ReasoningEffort string
	MaxOutputTokens int64
}

type Service struct {
	cfg    Config
	client openai.Client
}

// New создаёт адаптер; httpClient — из internal/infra/httpx.
func New(cfg Config, httpClient *http.Client) *Service {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.ApiKey),
		option.WithHTTPClient(httpClient),
		option.WithMaxRetries(2),
	}
	if cfg.BaseUrl != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseUrl))
	}

	return &Service{
		cfg:    cfg,
		client: openai.NewClient(opts...),
	}
}

// Ping — GET /models/{model}: проверяет ключ и доступность модели, токены не тратит.
// noCreditsCodes — коды и типы ошибок OpenAI «кончились деньги или квота аккаунта» (429, но не
// частота запросов: повтор не поможет, нужно пополнить).
var noCreditsCodes = []string{"insufficient_quota", "credit_balance_exhausted"}

func (s *Service) Ping(ctx context.Context) error {
	if _, err := s.client.Models.Get(ctx, s.cfg.Model, option.WithMaxRetries(0)); err != nil {
		return fmt.Errorf("Models.Get: %w", err)
	}
	return nil
}

func (s *Service) Name() string {
	return constant.LlmProviderOpenai
}

func (s *Service) Complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	var items []responses.ResponseInputItemUnionParam
	if state, ok := req.State.([]responses.ResponseInputItemUnionParam); ok && state != nil {
		items = append(items, state...)
		items = append(items, lo.Map(req.ToolResults, encodeToolResult)...)
	} else {
		items = lo.Map(req.Messages, encodeMessage)
	}

	params := responses.ResponseNewParams{
		Model:           s.cfg.Model,
		Instructions:    openai.String(req.System),
		Input:           responses.ResponseNewParamsInputUnion{OfInputItemList: items},
		Tools:           lo.Map(req.Tools, encodeTool),
		Store:           openai.Bool(false),
		Include:         []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent},
		MaxOutputTokens: openai.Int(s.cfg.MaxOutputTokens),
		// один ключ на все разборы: системный промпт и инструменты одинаковы,
		// запросы попадают на один кэш префикса
		PromptCacheKey: openai.String(constant.ServiceName),
	}
	if s.cfg.ReasoningEffort != "" {
		params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(s.cfg.ReasoningEffort)}
	}
	if req.Output != nil {
		params.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
					Name: req.Output.Name, Schema: req.Output.Schema, Strict: openai.Bool(true),
				},
			},
		}
	}
	if req.NoTools {
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsNone),
		}
	}

	resp, err := s.client.Responses.New(ctx, params)
	if err != nil {
		// 400 — запрос не принят (в т.ч. схема ответа от клиента не подходит под strict):
		// ошибка клиента с объяснением провайдера, а не сбой сервиса
		if apiErr, ok := errors.AsType[*openai.Error](err); ok {
			switch {
			case apiErr.StatusCode == http.StatusBadRequest:
				return nil, fmt.Errorf("%w: openai: %s", errs.InvalidRequest, lo.CoalesceOrEmpty(apiErr.Message, err.Error()))
			case lo.Contains(noCreditsCodes, apiErr.Code) || lo.Contains(noCreditsCodes, apiErr.Type):
				return nil, fmt.Errorf("%w: openai: %s", llmModel.ErrNoCredits, lo.CoalesceOrEmpty(apiErr.Message, err.Error()))
			}
		}
		return nil, fmt.Errorf("openai responses.new: %w", err)
	}

	switch resp.Status {
	case responses.ResponseStatusFailed:
		return nil, fmt.Errorf("openai response failed: %s: %s", resp.Error.Code, resp.Error.Message)
	case responses.ResponseStatusCancelled:
		return nil, errors.New("openai response cancelled")
	}

	// выходные элементы (reasoning, message, function_call) возвращаются во вход
	// следующего шага как есть
	for _, item := range resp.Output {
		items = append(items, param.Override[responses.ResponseInputItemUnionParam](json.RawMessage(item.RawJSON())))
	}

	result := &llmModel.Response{
		Text: resp.OutputText(),
		ToolCalls: lo.FilterMap(resp.Output, func(item responses.ResponseOutputItemUnion, _ int) (llmModel.ToolCall, bool) {
			if item.Type != "function_call" {
				return llmModel.ToolCall{}, false
			}
			call := item.AsFunctionCall()
			return llmModel.ToolCall{Id: call.CallID, Name: call.Name, Arguments: call.Arguments}, true
		}),
		State: items,
		Usage: llmModel.Usage{
			InputTokens:     resp.Usage.InputTokens,
			CachedTokens:    resp.Usage.InputTokensDetails.CachedTokens,
			OutputTokens:    resp.Usage.OutputTokens,
			ReasoningTokens: resp.Usage.OutputTokensDetails.ReasoningTokens,
		},
	}
	if resp.Status == responses.ResponseStatusIncomplete {
		result.Incomplete = lo.CoalesceOrEmpty(resp.IncompleteDetails.Reason, string(resp.Status))
	}

	return result, nil
}

func encodeMessage(m llmModel.Message, _ int) responses.ResponseInputItemUnionParam {
	role := responses.EasyInputMessageRoleUser
	if m.Role == llmModel.RoleAssistant {
		role = responses.EasyInputMessageRoleAssistant
	}

	return responses.ResponseInputItemUnionParam{
		OfMessage: &responses.EasyInputMessageParam{
			Role:    role,
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(m.Text)},
		},
	}
}

func encodeToolResult(r llmModel.ToolResult, _ int) responses.ResponseInputItemUnionParam {
	return responses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
			CallID: openai.String(r.CallId),
			Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: openai.String(r.Output)},
		},
	}
}

func encodeTool(t llmModel.ToolDef, _ int) responses.ToolUnionParam {
	return responses.ToolUnionParam{
		OfFunction: &responses.FunctionToolParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  t.Parameters,
			// схемы pulse не рассчитаны на strict-режим (все поля required,
			// additionalProperties: false); аргументы проверяет сам pulse
			Strict: openai.Bool(false),
		},
	}
}
