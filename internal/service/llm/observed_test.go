package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rendau/pulse_agent/internal/errs"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

type stubProvider struct {
	Provider
	err error
}

func (s stubProvider) Complete(context.Context, *llmModel.Request) (*llmModel.Response, error) {
	return &llmModel.Response{}, s.err
}

func TestObserved(t *testing.T) {
	var seen []error
	observe := func(err error) { seen = append(seen, err) }

	_, _ = Observed(stubProvider{}, observe).Complete(context.Background(), &llmModel.Request{})
	quota := errors.New("429 Too Many Requests: insufficient_quota")
	_, _ = Observed(stubProvider{err: quota}, observe).Complete(context.Background(), &llmModel.Request{})
	_, _ = Observed(stubProvider{err: fmt.Errorf("%w: bad schema", errs.InvalidRequest)}, observe).Complete(context.Background(), &llmModel.Request{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = Observed(stubProvider{err: context.Canceled}, observe).Complete(canceled, &llmModel.Request{})

	assert.Equal(t, []error{nil, quota}, seen, "схема клиента и отмена — не сбой провайдера")
}
