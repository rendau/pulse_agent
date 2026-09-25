package llm

import (
	"context"

	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
)

// Provider — адаптер LLM-провайдера. Реализации: internal/service/llm/<provider>/service,
// выбор — по LLM_PROVIDER в internal/app.
type Provider interface {
	// Name — имя провайдера (метрики, логи).
	Name() string

	// Complete делает один шаг модели.
	Complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error)

	// Ping — провайдер доступен и модель есть (без генерации: токены не тратятся).
	Ping(ctx context.Context) error
}
