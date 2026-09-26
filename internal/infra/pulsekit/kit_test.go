package pulsekit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type orderRep struct {
	Number   string         `json:"number"`
	Status   string         `json:"status" pulse:"enum=new|paid|shipped,description=статус заказа: new, paid или shipped"`
	Phone    string         `json:"customer_phone" pulse:"personal=phone"`
	Reason   string         `json:"stuck_reason" pulse:"maxLength=30"`
	History  []historyRep   `json:"history" pulse:"maxItems=2"`
	ByState  map[string]int `json:"by_state"`
	Updated  *time.Time     `json:"updated_at"`
	Extra    map[string]any `json:"-"`
	internal string         // неэкспортируемое — не в схеме
}

var _ = orderRep{internal: ""}

type historyRep struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

var testService = Service{
	Name: "orders-center", Title: "Центр заказов", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	Runbooks: []Runbook{
		{Title: "Заказы не уходят в 1С", Url: "https://wiki.company.kz/orders/1c"},
	},
}

// order — что отдаст ручка на номер (для проверок ответа по схеме).
var order = map[string]orderRep{}

func newKit() *Kit {
	k := New(Config{SlowAfter: 50 * time.Millisecond}, testService, Build{Version: "v1", Commit: "9f597a7c1e2d4b8a0f3c6e5d7b9a1c2e4f6a8b0c"})
	k.Depend("pg", "postgres", "ocenter-pg", true, func(context.Context) error { return nil }).Affects("приём и выдача заказов")
	k.Depend("onec", "http", "onec-proxy", false, func(context.Context) error {
		return errors.New("dial tcp postgres://app:s3cr3t@onec: connection refused")
	})
	k.Metric(Metric{Id: "orders_created", Title: "Созданные заказы в минуту", PromQL: "sum(rate(ocenter_orders_created_total[5m])) * 60", Unit: "count", Direction: "higher_is_better"})
	k.ErrorPattern("1С недоступна", "onec: .*(timeout|connection refused)")
	Handle(k, Endpoint{
		Id: "order_status", Title: "Где заказ", Description: "когда спрашивают о заказе по номеру", Path: "/diag/order/{number}",
		Params: map[string]Param{
			"number": {Pattern: "[0-9]{5,12}"},
			"limit":  {Type: "integer", Default: "20", Description: "сколько событий истории, по умолчанию 20"},
		},
		RowsPath: "history", MaxRows: 20,
	}, func(ctx context.Context, params map[string]string) (orderRep, error) {
		switch params["number"] {
		case "00000":
			return orderRep{}, Error{Status: http.StatusNotFound, Message: "заказ не найден"}
		case "11111":
			return orderRep{}, errors.New("pgx: postgres://app:s3cr3t@db failed")
		case "22222":
			return orderRep{Number: RequestId(ctx)}, nil
		}
		if rep, ok := order[params["number"]]; ok {
			return rep, nil
		}
		return orderRep{Number: params["number"], Status: "paid", Phone: "+77011234567"}, nil
	})
	return k
}

// Манифест по стандарту: схема ответа из типа, персональные поля, enum, описания с запятыми,
// метрики, узнаваемые ошибки, инструкции. Полная проверка — тест pulse/internal/infra/pulsekit
// (сверка с ParseManifest).
func TestManifest(t *testing.T) {
	raw, err := json.Marshal(newKit().Manifest())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	assert.InDelta(t, 1, m["pulse_manifest"], 0)
	endpoints := m["endpoints"].([]any)
	require.Len(t, endpoints, 1)
	endpoint := endpoints[0].(map[string]any)
	props := endpoint["response"].(map[string]any)["properties"].(map[string]any)
	assert.Equal(t, "phone", props["customer_phone"].(map[string]any)["x-personal"])
	assert.InDelta(t, 30, props["stuck_reason"].(map[string]any)["maxLength"], 0)
	assert.Equal(t, "integer", props["by_state"].(map[string]any)["additionalProperties"].(map[string]any)["type"])
	assert.Equal(t, []any{"new", "paid", "shipped"}, props["status"].(map[string]any)["enum"])
	assert.Equal(t, "статус заказа: new, paid или shipped", props["status"].(map[string]any)["description"], "описание с запятыми — целиком")
	assert.NotContains(t, props, "internal")
	assert.InDelta(t, 20, endpoint["max_rows"], 0)
	assert.InDelta(t, 20, endpoint["params"].(map[string]any)["limit"].(map[string]any)["default"], 0, "default числового параметра — числом")
	require.Len(t, m["dependencies"], 2)
	assert.Equal(t, "приём и выдача заказов", m["dependencies"].([]any)[0].(map[string]any)["affects"])
	assert.Len(t, m["metrics"], 1)
	assert.Len(t, m["logs"].(map[string]any)["error_patterns"], 1)
	assert.Len(t, m["runbooks"], 1)
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

func TestPassive(t *testing.T) {
	bank := NewPassive(time.Minute)
	k := New(Config{}, testService, Build{})
	k.Depend("bank", "http", "api.bank.kz", false, bank.Check).Affects("онлайн-оплата")
	status := func() DependencyStatusRep {
		k.checkAll(context.Background())
		return k.Status().Dependencies[0]
	}

	assert.Equal(t, "ok", status().Status, "вызовов не было — не с чем сравнить")

	bank.Observe(nil)
	bank.Observe(errors.New("POST https://api.bank.kz?key=s3cr3t: timeout"))
	bank.Observe(errors.New("timeout"))
	got := status()
	assert.Equal(t, "degraded", got.Status, "неудачных больше половины")
	assert.Equal(t, "неудачных вызовов 2 из 3 за 1m0s: таймаут", got.Message, "своими словами, без адреса с ключом")

	bank.Observe(errors.New("connection refused"))
	got = status()
	assert.Equal(t, "down", got.Status, "три неудачи подряд")
	assert.Equal(t, "последние 3 вызовов неудачны: в соединении отказано", got.Message)

	bank.Observe(nil)
	assert.Equal(t, "degraded", status().Status, "серия прервана успехом")
}

func TestGauges(t *testing.T) {
	k := New(Config{}, testService, Build{})
	k.Depend("pg", "postgres", "pg", true, func(context.Context) error { return nil })
	k.Gauge("outbox_backlog", "Неотправленные события", "count", func(context.Context) (float64, string, error) {
		return 1840, "degraded", nil
	})
	synced := time.Date(2026, 9, 25, 18, 52, 0, 0, time.FixedZone("", 5*3600))
	k.GaugeTime("last_sync", "Последняя выгрузка", func(context.Context) (time.Time, string, error) { return synced, "ok", nil })
	k.Gauge("broken", "Сломанный", "count", func(context.Context) (float64, string, error) { return 0, "", errors.New("pg: password=x") })

	k.checkAll(context.Background())
	status := k.Status()
	assert.Equal(t, "degraded", status.Status, "показатель degraded — сервис degraded")
	require.Len(t, status.Gauges, 2, "непрочитанный показатель — не в ответе")
	assert.InDelta(t, 1840, status.Gauges[0].Value, 0)
	assert.Equal(t, "2026-09-25T18:52:00+05:00", status.Gauges[1].Value)
	assert.Equal(t, "time", status.Gauges[1].Unit)
}

func TestHandlers(t *testing.T) {
	mux := http.NewServeMux()
	newKit().Register(mux)
	get := func(path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(RequestIdHeader, "pulse-42")
		mux.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := get("/diag/order/234115")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "+77011234567", body["customer_phone"], "значение как есть — от модели его прячет pulse_agent")

	code, body = get("/diag/order/22222")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "pulse-42", body["number"], "X-Pulse-Request-Id — в контексте ручки")

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

func TestCheckEndpoint(t *testing.T) {
	k := newKit()
	require.NoError(t, k.CheckEndpoint("order_status", map[string]string{"number": "234115"}))
	require.NoError(t, k.CheckEndpoint("order_status", map[string]string{"number": "00000"}), "ошибка по стандарту")

	updated := time.Now()
	order["33333"] = orderRep{Number: "33333", Status: "lost", Reason: strings.Repeat("я", 31), Updated: &updated,
		History: []historyRep{
			{Status: "new"}, {Status: "paid"}, {Status: "lost"},
		}}
	defer delete(order, "33333")
	err := k.CheckEndpoint("order_status", map[string]string{"number": "33333"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"lost" is not in enum`)
	assert.Contains(t, err.Error(), "maxLength 30")
	assert.Contains(t, err.Error(), "maxItems 2")

	require.Error(t, k.CheckEndpoint("unknown", nil))
}

func TestHost(t *testing.T) {
	for addr, host := range map[string]string{
		"postgres://app:s3cr3t@ocenter-pg:5432/orders?sslmode=disable":  "ocenter-pg:5432",
		"host=ocenter-pg port=5432 user=app password=s3cr3t dbname=app": "ocenter-pg:5432",
		"host='db1,db2' password=x":                                     "db1",
		"postgres://app:pw@db1:5432,db2:5432/orders":                    "db1:5432",
		"dns:///orders-api:9090":                                        "orders-api:9090",
		"https://api.bank.kz/v1/pay":                                    "api.bank.kz",
		"redis:6379":                                                    "redis:6379",
		"[::1]:5432":                                                    "[::1]:5432",
		"password=s3cr3t":                                               "",
		"что-то странное с пробелами":                                   "",
		"": "",
	} {
		assert.Equal(t, host, Host(addr), addr)
	}
}

func TestRegistrationRules(t *testing.T) {
	k := New(Config{}, testService, Build{})
	handle := func(e Endpoint) func() {
		return func() {
			Handle(k, e, func(context.Context, map[string]string) (int, error) { return 0, nil })
		}
	}
	assert.Panics(t, handle(Endpoint{Id: "x", Title: "x", Description: "x", Path: "/x", Params: map[string]Param{"q": {}}}), "свободная строка")
	assert.Panics(t, handle(Endpoint{Id: "x", Title: "x", Description: strings.Repeat("д", 501), Path: "/x"}), "описание длиннее 500")
	assert.Panics(t, handle(Endpoint{Id: "x", Title: "x", Description: "x", Path: "/x", Params: map[string]Param{"n": {Type: "integer", Default: "двадцать"}}}), "default не в типе")
	assert.Panics(t, handle(Endpoint{Id: "x", Title: "x", Description: "x", Path: "/x", Params: map[string]Param{"p": {Personal: "card"}}}), "по карте не ищут")
	assert.Panics(t, func() {
		type rep struct {
			Token string `json:"access_token"`
		}
		Handle(k, Endpoint{Id: "y", Title: "y", Description: "y", Path: "/y"},
			func(context.Context, map[string]string) (rep, error) { return rep{}, nil })
	}, "поле-секрет")
	assert.Panics(t, func() {
		type rep struct {
			Phone string `json:"phone" pulse:"personal=telephone"`
		}
		Handle(k, Endpoint{Id: "z", Title: "z", Description: "z", Path: "/z"},
			func(context.Context, map[string]string) (rep, error) { return rep{}, nil })
	}, "неизвестный вид")
	assert.Panics(t, func() { New(Config{}, Service{}, Build{}) }, "сведения о сервисе обязательны")
	assert.Panics(t, func() { k.Metric(Metric{Id: "m", Title: "m", PromQL: "up", Unit: "percent"}) }, "unit не из стандарта")
	assert.Panics(t, func() { k.ErrorPattern("x", "(?=lookahead)") }, "не RE2")
	assert.Panics(t, func() {
		k.Depend("s3", "s3", "minio", false, func(context.Context) error { return nil }).Affects(strings.Repeat("д", 101))
	}, "affects длиннее 100")

	k.Depend("pg", "postgres", "user:pass@pg", true, func(context.Context) error { return nil })
	k.Depend("pg2", "postgres", "host=pg password=x", true, func(context.Context) error { return nil })
	targets := map[string]string{}
	for _, d := range k.Manifest().Dependencies {
		targets[d.Id] = d.Target
	}
	assert.Equal(t, "unknown", targets["pg"], "учётные данные в target — не в манифест, сервис не падает")
	assert.Equal(t, "unknown", targets["pg2"])
}
