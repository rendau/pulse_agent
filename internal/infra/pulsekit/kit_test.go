package pulsekit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderRep struct {
	Number   string         `json:"number"`
	Status   string         `json:"status"`
	Phone    string         `json:"customer_phone" pulse:"personal=phone"`
	Reason   string         `json:"stuck_reason" pulse:"maxLength=300"`
	History  []historyRep   `json:"history" pulse:"maxItems=50"`
	ByState  map[string]int `json:"by_state"`
	Updated  time.Time      `json:"updated_at"`
	internal string         // неэкспортируемое — не в схеме
}

var _ = orderRep{internal: ""}

type historyRep struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

func newKit() *Kit {
	k := New(Config{SlowAfter: 50 * time.Millisecond}, Service{
		Name: "orders-center", Title: "Центр заказов", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	}, Build{Version: "v1", Commit: "9f597a7c1e2d4b8a0f3c6e5d7b9a1c2e4f6a8b0c"})
	k.Depend("pg", "postgres", "ocenter-pg", true, func(context.Context) error { return nil })
	k.Depend("onec", "http", "onec-proxy", false, func(context.Context) error {
		return errors.New("dial tcp postgres://app:s3cr3t@onec: connection refused")
	})
	Handle(k, Endpoint{
		Id: "order_status", Title: "Где заказ", Description: "когда спрашивают о заказе по номеру", Path: "/diag/order/{number}",
		Params: map[string]Param{"number": {Pattern: "[0-9]{5,12}"}},
	}, func(_ context.Context, params map[string]string) (orderRep, error) {
		if params["number"] == "00000" {
			return orderRep{}, Error{Status: http.StatusNotFound, Message: "заказ не найден"}
		}
		if params["number"] == "11111" {
			return orderRep{}, errors.New("pgx: postgres://app:s3cr3t@db failed")
		}
		return orderRep{Number: params["number"], Status: "paid", Phone: "+77011234567"}, nil
	})
	return k
}

// Манифест по стандарту: схема ответа из типа, персональные поля, коммит. Полная проверка —
// тест pulse/internal/infra/pulsekit (сверка с ParseManifest).
func TestManifest(t *testing.T) {
	raw, err := json.Marshal(newKit().Manifest())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	assert.InDelta(t, 1, m["pulse_manifest"], 0)
	endpoints := m["endpoints"].([]any)
	require.Len(t, endpoints, 1)
	props := endpoints[0].(map[string]any)["response"].(map[string]any)["properties"].(map[string]any)
	assert.Equal(t, "phone", props["customer_phone"].(map[string]any)["x-personal"])
	assert.InDelta(t, 300, props["stuck_reason"].(map[string]any)["maxLength"], 0)
	assert.Equal(t, "integer", props["by_state"].(map[string]any)["additionalProperties"].(map[string]any)["type"])
	assert.NotContains(t, props, "internal")
	assert.Len(t, m["dependencies"], 2)
}

func TestStatus(t *testing.T) {
	k := newKit()
	assert.Equal(t, "ok", k.Status().Status, "до первой проверки")

	k.checkAll(context.Background())
	status := k.Status()
	assert.Equal(t, "degraded", status.Status, "некритичная зависимость down — degraded")
	assert.NotEmpty(t, status.CheckedAt)
	require.Len(t, status.Dependencies, 2)
	assert.Equal(t, "down", status.Dependencies[1].Status)
	assert.Equal(t, "в соединении отказано", status.Dependencies[1].Message, "своё сообщение, а не текст ошибки с паролем")

	k.Depend("kafka", "kafka", "kafka", true, func(ctx context.Context) error {
		time.Sleep(60 * time.Millisecond)
		return nil
	})
	k.Depend("redis", "redis", "redis", true, func(context.Context) error { return context.DeadlineExceeded })
	k.checkAll(context.Background())
	status = k.Status()
	assert.Equal(t, "down", status.Status, "критичная зависимость down")
	assert.Equal(t, "degraded", status.Dependencies[2].Status, "медленная")
	assert.Equal(t, "таймаут", status.Dependencies[3].Message)
}

func TestHandlers(t *testing.T) {
	mux := http.NewServeMux()
	newKit().Register(mux)
	get := func(path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := get("/diag/order/234115")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "+77011234567", body["customer_phone"], "значение как есть — токеном его заменит pulse")

	code, body = get("/diag/order/00000")
	assert.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "заказ не найден", body["error"])

	code, body = get("/diag/order/11111")
	assert.Equal(t, http.StatusInternalServerError, code)
	assert.Equal(t, "внутренняя ошибка", body["error"], "текст ошибки наружу не уходит")

	code, _ = get("/diag/order/12")
	assert.Equal(t, http.StatusBadRequest, code, "pattern")

	code, body = get(ManifestPath)
	assert.Equal(t, http.StatusOK, code)
	assert.InDelta(t, 1, body["pulse_manifest"], 0)
	code, _ = get(StatusPath)
	assert.Equal(t, http.StatusOK, code)
}

func TestRegistrationRules(t *testing.T) {
	k := New(Config{}, Service{}, Build{})
	assert.Panics(t, func() {
		Handle(k, Endpoint{Id: "x", Title: "x", Description: "x", Path: "/x", Params: map[string]Param{"q": {}}},
			func(context.Context, map[string]string) (int, error) { return 0, nil })
	}, "свободная строка")
	assert.Panics(t, func() {
		type rep struct {
			Token string `json:"access_token"`
		}
		Handle(k, Endpoint{Id: "y", Title: "y", Description: "y", Path: "/y"},
			func(context.Context, map[string]string) (rep, error) { return rep{}, nil })
	}, "поле-секрет")
	assert.Panics(t, func() { k.Depend("pg", "postgres", "user:pass@pg", true, nil) }, "учётные данные в target")
}
