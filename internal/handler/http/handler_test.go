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

	journalModel "github.com/rendau/pulse_agent/internal/domain/journal/model"
	"github.com/rendau/pulse_agent/internal/errs"
	"github.com/rendau/pulse_agent/internal/eval"
	agentModel "github.com/rendau/pulse_agent/internal/service/agent/model"
	chartModel "github.com/rendau/pulse_agent/internal/service/chart/model"
	askModel "github.com/rendau/pulse_agent/internal/usecase/ask/model"
	monitorModel "github.com/rendau/pulse_agent/internal/usecase/monitor/model"
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

type fakeMonitor struct{}

func (fakeMonitor) Recent(_ context.Context, f journalModel.Filter) ([]*journalModel.Entry, error) {
	return []*journalModel.Entry{{Id: 7, Client: f.Client, Question: "q", Outcome: "answered", Duration: time.Second, Tools: []string{"ping"}}}, nil
}

func (fakeMonitor) Entry(_ context.Context, id int64) (*journalModel.Entry, error) {
	if id != 7 {
		return nil, errs.ObjectNotFound
	}
	return &journalModel.Entry{Id: 7, Client: "pulse_bot", Question: "q", Outcome: "answered", Answer: "всё ок",
		Trace: []journalModel.ToolCall{
			{Step: 1, Name: "resolve_service", Arguments: `{"query":"caravan"}`, Status: "ok", Output: "{}"},
			{Step: 2, Name: "get_service_snapshot", Arguments: `{oops`, Status: "tool_error"},
		}}, nil
}

func (fakeMonitor) Stats(context.Context, string) (*journalModel.Stats, error) {
	return &journalModel.Stats{Questions: 1, Clients: []journalModel.ClientStats{{Client: "pulse_bot", Questions: 1}}}, nil
}

func (fakeMonitor) Info(context.Context) *monitorModel.Info {
	return &monitorModel.Info{Version: "v1", LlmModel: "gpt-6-sol", PulseTools: []string{"ping"}}
}

const testCases = `
cases:
  - id: a
    question: что с caravan?
    checks: {calls: [{tool: get_service_snapshot}]}
  - id: b
    question: q2
`

func newHandler(t *testing.T, ask *fakeAsk) *Handler {
	keeper, err := eval.NewKeeper([]byte(testCases), []byte(`{"cases":[]}`), 2)
	require.NoError(t, err)
	return New(Config{
		Keys:        map[string]string{"pulse_bot": "k-bot", "service-desk": "k-sd"},
		DebugToken:  "k-debug",
		EvalClients: []string{"pulse_bot"},
	}, ask, nil, fakeMonitor{}, keeper, almaty)
}

func serve(t *testing.T, ask *fakeAsk, path, key, body string) (*httptest.ResponseRecorder, map[string]any) {
	return serveMethod(t, newHandler(t, ask), http.MethodPost, path, key, body)
}

func serveMethod(t *testing.T, h *Handler, method, path, key, body string) (*httptest.ResponseRecorder, map[string]any) {
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var rep map[string]any
	if strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "{") {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rep), rec.Body.String())
	}
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

func TestDebug(t *testing.T) {
	h := newHandler(t, &fakeAsk{answer: &askModel.Answer{Text: "ok"}})

	// ключ системы — не ключ разработчика
	rec, rep := serveMethod(t, h, http.MethodGet, PathDebugStats, "k-bot", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "forbidden", rep["code"])

	rec, rep = serveMethod(t, h, http.MethodGet, PathDebugStats, "k-debug", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.InDelta(t, 1, rep["questions"], 0)

	rec, rep = serveMethod(t, h, http.MethodGet, PathDebugInfo, "k-debug", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gpt-6-sol", rep["llm_model"])

	rec, _ = serveMethod(t, h, http.MethodGet, PathDebugRecent+"?client=pulse_bot&limit=5", "k-debug", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var recent []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &recent))
	require.Len(t, recent, 1)
	assert.Equal(t, "pulse_bot", recent[0]["client"])
	assert.InDelta(t, 1000, recent[0]["duration_ms"], 0)
	assert.InDelta(t, 7, recent[0]["id"], 0)

	// вопрос целиком: ответ и ход разбора; аргументы — объектом, невалидные — строкой
	rec, rep = serveMethod(t, h, http.MethodGet, "/debug/journal/7", "k-debug", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "всё ок", rep["answer"])
	trace := rep["trace"].([]any)
	require.Len(t, trace, 2)
	assert.Equal(t, map[string]any{"query": "caravan"}, trace[0].(map[string]any)["arguments"])
	assert.Equal(t, "{oops", trace[1].(map[string]any)["arguments"])

	rec, rep = serveMethod(t, h, http.MethodGet, "/debug/journal/8", "k-debug", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "not_found", rep["code"])

	rec, _ = serveMethod(t, h, http.MethodGet, "/debug/journal/x", "k-debug", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// ключ разработчика годится и для вопросов — как система debug
	ask := &fakeAsk{answer: &askModel.Answer{Text: "ok"}}
	rec, _ = serveMethod(t, newHandler(t, ask), http.MethodPost, PathAsk, "k-debug", `{"question":"q"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, ClientDebug, ask.questions[0].Client)
}

func TestEval(t *testing.T) {
	ask := &fakeAsk{answer: &askModel.Answer{Text: "ok", Trace: []agentModel.ToolTrace{{Name: "get_service_snapshot", Arguments: "{}"}}}}
	h := newHandler(t, ask)

	// service-desk не в EVAL_CLIENTS
	rec, _ := serveMethod(t, h, http.MethodPost, PathEval, "k-sd", `{}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec, rep := serveMethod(t, h, http.MethodPost, PathEval, "k-bot", `{"only":["a"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rep["text"], "✅ a")
	assert.Contains(t, rep["text"], "Итого: 1/1 прошли")
	require.Len(t, ask.questions, 1)
	assert.Equal(t, ClientEval, ask.questions[0].Client, "вопросы прогона — от системы eval")

	rec, rep = serveMethod(t, h, http.MethodPost, PathEval, "k-bot", `{"only":["нет-такого"]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rep["error"], "unknown cases")

	// последний прогон — разработчику
	rec, rep = serveMethod(t, h, http.MethodGet, PathDebugEvalLast, "k-debug", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rep["text"], "✅ a")
	assert.Equal(t, nil, rep["running"])
}
