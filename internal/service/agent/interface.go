package agent

import (
	"context"

	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
)

// Agent — агентный цикл: вопрос → шаги модели с инструментами pulse → ответ.
// Не знает ни про клиентов API, ни про конкретного LLM-провайдера.
type Agent interface {
	Run(ctx context.Context, req *agentModel.Req) (*agentModel.Result, error)
}
