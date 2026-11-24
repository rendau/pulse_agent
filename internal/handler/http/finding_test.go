package http

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_agent/internal/errs"
	findingModel "github.com/rendau/pulse_agent/internal/usecase/finding/model"
)

type fakeFinding struct {
	got    []*findingModel.Finding
	client string
	res    *findingModel.Result
	err    error
}

func (f *fakeFinding) Report(_ context.Context, client string, x *findingModel.Finding) (*findingModel.Result, error) {
	f.got, f.client = append(f.got, x), client
	return f.res, f.err
}

const findingBody = `{"key":"security:129.226.55.176","kind":"security","severity":"critical","title":"атака","text":"перебор","source":"claude-routine"}`

func TestFinding(t *testing.T) {
	finding := &fakeFinding{res: &findingModel.Result{Id: 42}}
	h := newHandler(t, &fakeAsk{})
	h.finding = finding

	rec, rep := serveMethod(t, h, http.MethodPost, PathFindings, "k-sd", findingBody)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]any{"id": float64(42), "deduplicated": false}, rep)
	assert.Equal(t, "service-desk", finding.client, "клиент — по ключу")
	assert.Equal(t, &findingModel.Finding{Key: "security:129.226.55.176", Kind: "security", Severity: "critical",
		Title: "атака", Text: "перебор", Source: "claude-routine"}, finding.got[0])

	finding.res = &findingModel.Result{Deduplicated: true}
	_, rep = serveMethod(t, h, http.MethodPost, PathFindings, "k-sd", findingBody)
	assert.Equal(t, map[string]any{"id": float64(0), "deduplicated": true}, rep)

	rec, _ = serveMethod(t, h, http.MethodPost, PathFindings, "", findingBody)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec, rep = serveMethod(t, h, http.MethodPost, PathFindings, "k-sd", `{"key":"security:1.2.3.4","extra":1}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "неизвестное поле")
	assert.Equal(t, "invalid_request", rep["code"])

	finding.err = fmt.Errorf("%w: kind %q", errs.InvalidRequest, "alert")
	rec, rep = serveMethod(t, h, http.MethodPost, PathFindings, "k-sd", findingBody)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid_request", rep["code"])

	finding.err = fmt.Errorf("%w: more than 10 findings per hour", errs.RateLimited)
	rec, rep = serveMethod(t, h, http.MethodPost, PathFindings, "k-sd", findingBody)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.Equal(t, "rate_limited", rep["code"])
}

func TestFinding_NoStorage(t *testing.T) {
	rec, rep := serve(t, &fakeAsk{}, PathFindings, "k-sd", findingBody)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "unavailable", rep["code"])
}
