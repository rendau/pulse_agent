package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/errs"
	agentModel "github.com/mechta-market/pulse_agent/internal/service/agent/model"
	chartModel "github.com/mechta-market/pulse_agent/internal/service/chart/model"
	askModel "github.com/mechta-market/pulse_agent/internal/usecase/ask/model"
)

type fakeAsk struct {
	questions []*askModel.Question
	resets    []string
	answer    *askModel.Answer
	err       error
}

func (f *fakeAsk) Ask(_ context.Context, q *askModel.Question) (*askModel.Answer, error) {
	f.questions = append(f.questions, q)
	return f.answer, f.err
}

func (f *fakeAsk) Reset(_ context.Context, client, conversationId string) error {
	f.resets = append(f.resets, client+"/"+conversationId)
	return nil
}

var almaty, _ = time.LoadLocation("Asia/Almaty")

func serve(t *testing.T, ask *fakeAsk, path, key, body string) (*httptest.ResponseRecorder, map[string]any) {
	mux := http.NewServeMux()
	New(ask, map[string]string{"pulse_bot": "k-bot", "service-desk": "k-sd"}, almaty).Register(mux)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var rep map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep), rec.Body.String())
	return rec, rep
}

func TestAsk_Auth(t *testing.T) {
	ask := &fakeAsk{answer: &askModel.Answer{Text: "ok"}}

	rec, rep := serve(t, ask, PathAsk, "", `{"question":"q"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "unauthorized", rep["code"])

	rec, _ = serve(t, ask, PathAsk, "wrong", `{"question":"q"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec, _ = serve(t, ask, PathAsk, "k-sd", `{"question":"q"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "service-desk", ask.questions[0].Client, "система — по ключу")
}

func TestAsk_JsonAndCharts(t *testing.T) {
	at := time.Date(2026, 9, 25, 9, 58, 0, 0, time.UTC)
	ask := &fakeAsk{answer: &askModel.Answer{
		Text: "caravan деградировал",
		Json: []byte(`{"summary":"caravan деградировал","status":"degraded","facts":[{"text":"p95 650 мс","time":"2026-09-25T14:58:00+05:00"}],"next_steps":[]}`),
		Charts: []agentModel.Chart{{Title: "p95", Png: []byte("PNG"), Spec: &chartModel.Spec{Type: "line", Unit: "seconds",
			Series: []chartModel.Series{{Name: "caravan", Points: []chartModel.Point{{Time: at, Value: 0.65}}}}}}},
		Trace: []agentModel.ToolTrace{{Name: "get_service_snapshot", Output: strings.Repeat("x", 5000)}},
	}}

	rec, rep := serve(t, ask, PathAsk, "k-sd", `{"question":"что с caravan?","conversation_id":"T-1","user":{"id":"ivanov"},
		"format":"json","charts":"data","trace":true,"trace_output_limit":100}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	q := ask.questions[0]
	assert.Equal(t, "json", q.Format)
	assert.Equal(t, "T-1", q.ConversationId)
	assert.Equal(t, "ivanov", q.User.Id)
	assert.True(t, q.Charts)

	result := rep["result"].(map[string]any)
	assert.Equal(t, "degraded", result["status"], "result — JSON агента как есть")
	assert.Equal(t, []any{}, result["next_steps"])

	chart := rep["charts"].([]any)[0].(map[string]any)
	assert.Nil(t, chart["png"], "charts=data — без картинки")
	assert.Equal(t, "seconds", chart["unit"])
	point := chart["data"].(map[string]any)["series"].([]any)[0].(map[string]any)["points"].([]any)[0].(map[string]any)
	assert.Equal(t, "2026-09-25T14:58:00+05:00", point["x"])
	assert.InDelta(t, 0.65, point["y"], 1e-9)

	trace := rep["trace"].([]any)[0].(map[string]any)
	assert.Len(t, trace["output"], 100)
	assert.Equal(t, true, trace["truncated"])

	// png — только картинка; none — агент без графиков; без trace — хода разбора нет
	rec, rep = serve(t, ask, PathAsk, "k-bot", `{"question":"q","charts":"png"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	ask.answer.Json = nil
	chart = rep["charts"].([]any)[0].(map[string]any)
	assert.Equal(t, "UE5H", chart["png"])
	assert.Nil(t, chart["data"])
	assert.Nil(t, rep["trace"])

	_, _ = serve(t, ask, PathAsk, "k-bot", `{"question":"q","charts":"none"}`)
	assert.False(t, ask.questions[2].Charts)
}

func TestAsk_ResponseSchema(t *testing.T) {
	ask := &fakeAsk{answer: &askModel.Answer{Text: `{"order_status":"paid"}`, Json: []byte(`{"order_status":"paid"}`)}}

	rec, rep := serve(t, ask, PathAsk, "k-sd", `{"question":"что по заказу 1?",
		"response_schema":{"type":"object","properties":{"order_status":{"type":"string"}},"required":["order_status"]}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]any{"order_status": "paid"}, rep["result"])
	schema := ask.questions[0].ResponseSchema
	require.NotNil(t, schema)
	assert.Equal(t, "object", schema["type"])

	// текстовый ответ — result null
	ask.answer = &askModel.Answer{Text: "ok"}
	_, rep = serve(t, ask, PathAsk, "k-sd", `{"question":"q"}`)
	assert.Nil(t, rep["result"])
}

func TestAsk_Errors(t *testing.T) {
	ask := &fakeAsk{}

	rec, rep := serve(t, ask, PathAsk, "k-bot", `{"question":`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid_request", rep["code"])

	rec, _ = serve(t, ask, PathAsk, "k-bot", `{"question":"q","text":"опечатка в поле"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "неизвестное поле — ошибка, а не тишина")

	rec, _ = serve(t, ask, PathAsk, "k-bot", `{"question":"q","charts":"svg"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	ask.err = errs.Busy
	rec, rep = serve(t, ask, PathAsk, "k-bot", `{"question":"q","conversation_id":"1"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "busy", rep["code"])

	ask.err = context.DeadlineExceeded
	rec, rep = serve(t, ask, PathAsk, "k-bot", `{"question":"q"}`)
	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assert.Equal(t, "timeout", rep["code"])
}

func TestReset(t *testing.T) {
	ask := &fakeAsk{answer: &askModel.Answer{Text: "ok"}}

	rec, rep := serve(t, ask, PathReset, "k-bot", `{"conversation_id":"42"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, rep["reset"])
	assert.Equal(t, []string{"pulse_bot/42"}, ask.resets)

	// reset в вопросе — перед ответом
	rec, _ = serve(t, ask, PathAsk, "k-bot", `{"question":"q","conversation_id":"42","reset":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, ask.resets, 2)
}
