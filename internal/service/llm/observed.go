package llm

import (
	"context"
	"errors"

	"github.com/rendau/pulse_agent/internal/errs"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

// Observed — провайдер, который сообщает observe исход каждого шага модели: проверка
// зависимости по настоящим вызовам (pulsekit.Passive). Ping без генерации не видит, что
// кончились деньги или квота, — это видно только по ответам. Не сбой провайдера: схема,
// которую он не принял (errs.InvalidRequest — ошибка клиента), и отмена разбора с нашей стороны.
func Observed(p Provider, observe func(err error)) Provider {
	return &observed{Provider: p, observe: observe}
}

type observed struct {
	Provider
	observe func(err error)
}

func (o *observed) Complete(ctx context.Context, req *llmModel.Request) (*llmModel.Response, error) {
	resp, err := o.Provider.Complete(ctx, req)
	switch {
	case err == nil:
		o.observe(nil)
	case errors.Is(err, errs.InvalidRequest), ctx.Err() != nil:
	default:
		o.observe(err)
	}
	return resp, err
}
