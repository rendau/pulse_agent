package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/errs"
)

func TestStrictSchema(t *testing.T) {
	var client map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"order_status": {"type": "string", "enum": ["paid", "delivered", "failed"], "description": "статус заказа"},
			"last_event_at": {"type": "string", "description": "время последнего события, RFC3339"},
			"failed_service": {"type": "string"},
			"events": {"type": "array", "items": {"type": "object", "properties": {
				"service": {"type": "string"}, "text": {"type": "string"}
			}, "required": ["text"]}}
		},
		"required": ["order_status"]
	}`), &client))

	strict, err := strictSchema(client)
	require.NoError(t, err)

	assert.Equal(t, false, strict["additionalProperties"])
	assert.Equal(t, []string{"events", "failed_service", "last_event_at", "order_status"}, strict["required"])

	props := strict["properties"].(map[string]any)
	assert.Equal(t, "string", props["order_status"].(map[string]any)["type"], "обязательное поле — без null")
	assert.Equal(t, []any{"string", "null"}, props["last_event_at"].(map[string]any)["type"])
	assert.Equal(t, "время последнего события, RFC3339", props["last_event_at"].(map[string]any)["description"], "описания остаются")

	item := props["events"].(map[string]any)["items"].(map[string]any)
	assert.Equal(t, false, item["additionalProperties"])
	assert.Equal(t, []any{"string", "null"}, item["properties"].(map[string]any)["service"].(map[string]any)["type"])

	// схема клиента не изменилась
	assert.Nil(t, client["additionalProperties"])

	// enum необязательного поля допускает null
	strict, err = strictSchema(map[string]any{"type": "object", "properties": map[string]any{
		"level": map[string]any{"type": "string", "enum": []any{"low", "high"}},
		"any":   map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}},
	}})
	require.NoError(t, err)
	level := strict["properties"].(map[string]any)["level"].(map[string]any)
	assert.Equal(t, []any{"low", "high", nil}, level["enum"])
	anyOf := strict["properties"].(map[string]any)["any"].(map[string]any)
	assert.Nil(t, anyOf["oneOf"])
	assert.Len(t, anyOf["anyOf"], 3, "oneOf → anyOf + null")
}

func TestStrictSchema_Invalid(t *testing.T) {
	_, err := strictSchema(map[string]any{"type": "array"})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = strictSchema(map[string]any{"type": "object", "properties": map[string]any{"x": "string"}})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = strictSchema(nil)
	require.ErrorIs(t, err, errs.InvalidRequest)
}
