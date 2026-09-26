package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse_agent/internal/errs"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

const step1Response = `{
  "id": "resp_1", "object": "response", "created_at": 1, "status": "completed", "model": "gpt-6-sol",
  "output": [
    {"id": "rs_1", "type": "reasoning", "summary": [], "encrypted_content": "ENC"},
    {"id": "fc_1", "type": "function_call", "call_id": "call_1", "name": "resolve_service",
     "arguments": "{\"query\":\"caravan\"}", "status": "completed"}
  ],
  "usage": {"input_tokens": 1000, "input_tokens_details": {"cached_tokens": 800},
            "output_tokens": 50, "output_tokens_details": {"reasoning_tokens": 30}, "total_tokens": 1050}
}`

const step2Response = `{
  "id": "resp_2", "object": "response", "created_at": 2, "status": "incomplete", "model": "gpt-6-sol",
  "incomplete_details": {"reason": "max_output_tokens"},
  "output": [
    {"id": "msg_1", "type": "message", "role": "assistant", "status": "completed",
     "content": [{"type": "output_text", "text": "caravan в порядке", "annotations": []}]}
  ],
  "usage": {"input_tokens": 1200, "input_tokens_details": {"cached_tokens": 1000},
            "output_tokens": 20, "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": 1220}
}`

func TestComplete_RoundTrip(t *testing.T) {
	var bodies []map[string]any
	replies := []string{step1Response, step2Response}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		body := map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &body))
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(replies[len(bodies)-1]))
	}))
	defer srv.Close()

	svc := New(Config{
		ApiKey: "sk-test", BaseUrl: srv.URL, Model: "gpt-6-sol", ReasoningEffort: "medium", MaxOutputTokens: 32000,
	}, srv.Client())

	// шаг 1: история и вопрос
	resp, err := svc.Complete(context.Background(), &llmModel.Request{
		System: "system prompt",
		Messages: []llmModel.Message{
			{Role: llmModel.RoleUser, Text: "прошлый вопрос"},
			{Role: llmModel.RoleAssistant, Text: "прошлый ответ"},
			{Role: llmModel.RoleUser, Text: "что с caravan?"},
		},
		Tools: []llmModel.ToolDef{{Name: "resolve_service", Description: "d", Parameters: map[string]any{"type": "object"}}},
	})
	require.NoError(t, err)

	assert.Equal(t, []llmModel.ToolCall{{Id: "call_1", Name: "resolve_service", Arguments: `{"query":"caravan"}`}}, resp.ToolCalls)
	assert.Equal(t, llmModel.Usage{InputTokens: 1000, CachedTokens: 800, OutputTokens: 50, ReasoningTokens: 30}, resp.Usage)
	assert.Empty(t, resp.Incomplete)

	b1 := bodies[0]
	assert.Equal(t, "gpt-6-sol", b1["model"])
	assert.Equal(t, "system prompt", b1["instructions"])
	assert.Equal(t, false, b1["store"])
	assert.Equal(t, []any{"reasoning.encrypted_content"}, b1["include"])
	assert.Equal(t, map[string]any{"effort": "medium"}, b1["reasoning"])
	assert.EqualValues(t, 32000, b1["max_output_tokens"])
	assert.NotContains(t, b1, "tool_choice")
	assert.Equal(t, []any{
		map[string]any{"role": "user", "content": "прошлый вопрос"},
		map[string]any{"role": "assistant", "content": "прошлый ответ"},
		map[string]any{"role": "user", "content": "что с caravan?"},
	}, b1["input"])
	assert.Equal(t, []any{map[string]any{
		"type": "function", "name": "resolve_service", "description": "d",
		"parameters": map[string]any{"type": "object"}, "strict": false,
	}}, b1["tools"])

	// шаг 2: накопленный вход + выход шага 1 + результат вызова; инструменты запрещены
	resp, err = svc.Complete(context.Background(), &llmModel.Request{
		System:      "system prompt",
		Messages:    []llmModel.Message{{Role: llmModel.RoleUser, Text: "не должно попасть в запрос"}},
		State:       resp.State,
		ToolResults: []llmModel.ToolResult{{CallId: "call_1", Output: `{"name":"caravan"}`}},
		NoTools:     true,
	})
	require.NoError(t, err)

	assert.Equal(t, "caravan в порядке", resp.Text)
	assert.Empty(t, resp.ToolCalls)
	assert.Equal(t, "max_output_tokens", resp.Incomplete)

	b2 := bodies[1]
	assert.Equal(t, "none", b2["tool_choice"])
	input, ok := b2["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 6)
	assert.Equal(t, map[string]any{"role": "user", "content": "что с caravan?"}, input[2])
	assert.Equal(t, "reasoning", input[3].(map[string]any)["type"])
	assert.Equal(t, "ENC", input[3].(map[string]any)["encrypted_content"])
	assert.Equal(t, "function_call", input[4].(map[string]any)["type"])
	assert.Equal(t, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": `{"name":"caravan"}`}, input[5])
}

func TestComplete_Failed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_x","object":"response","status":"failed","output":[],
			"error":{"code":"server_error","message":"boom"}}`))
	}))
	defer srv.Close()

	svc := New(Config{ApiKey: "k", BaseUrl: srv.URL, Model: "m"}, srv.Client())

	_, err := svc.Complete(context.Background(), &llmModel.Request{Messages: []llmModel.Message{{Role: llmModel.RoleUser, Text: "q"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestComplete_HttpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	svc := New(Config{ApiKey: "k", BaseUrl: srv.URL, Model: "m"}, srv.Client())

	_, err := svc.Complete(context.Background(), &llmModel.Request{Messages: []llmModel.Message{{Role: llmModel.RoleUser, Text: "q"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestComplete_OutputSchema(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(step2Response))
	}))
	defer srv.Close()

	schema := map[string]any{"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}},
		"required": []string{"summary"}, "additionalProperties": false}
	_, err := New(Config{ApiKey: "sk-test", BaseUrl: srv.URL, Model: "gpt-6-sol"}, srv.Client()).Complete(context.Background(),
		&llmModel.Request{System: "s", Messages: []llmModel.Message{{Role: llmModel.RoleUser, Text: "q"}}, Output: &llmModel.OutputSchema{Name: "answer", Schema: schema}})
	require.NoError(t, err)

	format := body["text"].(map[string]any)["format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	assert.Equal(t, "answer", format["name"])
	assert.Equal(t, true, format["strict"])
	assert.NotNil(t, format["schema"])
}

func TestComplete_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid schema for response_format 'client_answer'","type":"invalid_request_error","param":"text.format.schema","code":"invalid_json_schema"}}`))
	}))
	defer srv.Close()

	_, err := New(Config{ApiKey: "sk-test", BaseUrl: srv.URL, Model: "gpt-6-sol"}, srv.Client()).
		Complete(context.Background(), &llmModel.Request{Messages: []llmModel.Message{{Role: llmModel.RoleUser, Text: "q"}}})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.ErrorContains(t, err, "Invalid schema")
}

func TestComplete_NoCredits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"You have no credits remaining.","type":"insufficient_quota","param":null,"code":"credit_balance_exhausted"}}`))
	}))
	defer srv.Close()

	_, err := New(Config{ApiKey: "sk-test", BaseUrl: srv.URL, Model: "gpt-6-sol"}, srv.Client()).
		Complete(context.Background(), &llmModel.Request{Messages: []llmModel.Message{{Role: llmModel.RoleUser, Text: "q"}}})
	require.ErrorIs(t, err, llmModel.ErrNoCredits)
	assert.ErrorContains(t, err, "no credits remaining")
}
