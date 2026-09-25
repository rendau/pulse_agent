package service

import (
	"context"

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

type pulseI interface {
	Catalog(ctx context.Context) (*pulseModel.Catalog, error)
	Call(ctx context.Context, name string, arguments string) (*pulseModel.CallResult, error)
}
