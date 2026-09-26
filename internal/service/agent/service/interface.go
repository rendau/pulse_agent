package service

import (
	"context"

	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	chartModel "github.com/mechta-market/pulse_agent/internal/service/chart/model"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
	pulseModel "github.com/mechta-market/pulse_agent/internal/service/pulse/model"
)

type llmI interface {
	Name() string
	Complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error)
}

// chartI — рисование графиков к ответу (render_chart).
type chartI interface {
	Render(spec *chartModel.Spec) ([]byte, error)
}

// piiI — персональные данные токенами на границе с моделью: модель видит pii:<вид>:<код>,
// настоящие значения — только запросы к pulse и итоговый ответ.
type piiI interface {
	Mask(text string) string
	MaskToolOutput(text string) string
	RevealArgs(args string) string
	Reveal(text string) string
}

type pulseI interface {
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)
	Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error)
}

// ChatToolsI — инструменты беседы (заметки, приглушение уведомлений): выполняет сам агент, не pulse.
type ChatToolsI interface {
	Defs() []llmModel.ToolDef
	Has(name string) bool
	Call(ctx context.Context, chat *agentModel.Chat, name, arguments string) (string, error)
}
