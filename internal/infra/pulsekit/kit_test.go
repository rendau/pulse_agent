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

	"github.com/samber/lo"
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

// testDomain — бизнес-смысл сервиса из newKit (вопрос ссылается на его ручку).
var testDomain = &Domain{
	Responsibilities: []string{"Принимает заказы", "Ведёт заказ до выдачи"},
	NotResponsible: []Boundary{
		{What: "оплата", Service: "payments"},
	},
	Entities: []Entity{
		{Name: "заказ", IdPattern: "[0-9]{5,12}", IdExample: "234115", Statuses: []EntityStatus{
			{Name: "paid", Meaning: "оплачен, ждёт сборки", StuckAfter: 2 * time.Hour},
			{Name: "shipped", Meaning: "отгружен"},
		}},
	},
	Questions: []Question{
		{Question: "где заказ", Endpoint: "order_status"},
	},
}

func newKit() *Kit {
	service := testService
	service.Domain = testDomain
	k := New(Config{SlowAfter: 50 * time.Millisecond}, service, Build{Version: "v1", Commit: "9f597a7c1e2d4b8a0f3c6e5d7b9a1c2e4f6a8b0c"})
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
	domain := m["domain"].(map[string]any)
	entity := domain["entities"].([]any)[0].(map[string]any)
	assert.Equal(t, "[0-9]{5,12}", entity["id_pattern"])
	assert.Equal(t, "2h0m0s", entity["statuses"].([]any)[0].(map[string]any)["stuck_after"])
	assert.Empty(t, newKit().Problems(), "вопрос ссылается на объявленную ручку")
}

// Формат номера проверяется: не RE2 или пример не подходит — шаблон не публикуется; вопрос со
// ссылкой на необъявленную ручку — в Problems.
func TestDomainRules(t *testing.T) {
	service := testService
	service.Domain = &Domain{
		Entities: []Entity{
			{Name: "рейс", IdPattern: "(?=x)"},
			{Name: "курьер", IdPattern: "[0-9]{4}", IdExample: "12345"},
			{Name: "доставка", IdPattern: "[0-9]{7}", IdExample: "7784512"},
		},
		Questions: []Question{
			{Question: "где курьер", Endpoint: "courier_status"},
		},
	}
	k := New(Config{}, service, Build{})
	entities := k.Manifest().Domain.Entities
	assert.Empty(t, entities[0].IdPattern)
	assert.Empty(t, entities[1].IdPattern)
	assert.Equal(t, "[0-9]{7}", entities[2].IdPattern)
	assert.Len(t, k.Problems(), 3, k.Problems())
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

	got := status()
	assert.Equal(t, "ok", got.Status, "вызовов не было — не с чем сравнить")
	assert.Equal(t, "вызовов не было за 1 мин", got.Message)
	assert.True(t, bank.Idle())

	bank.Observe(nil)
	bank.Observe(errors.New("POST https://api.bank.kz?key=s3cr3t: timeout"))
	bank.Observe(errors.New("timeout"))
	assert.False(t, bank.Idle())
	got = status()
	assert.Equal(t, "degraded", got.Status, "неудачных больше половины")
	assert.Equal(t, "неудачных вызовов 2 из 3 за 1 мин: таймаут", got.Message, "своими словами, без адреса с ключом")

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
		"postgres://app:s3cr3t@ocenter-pg:5432/orders?sslmode=disable":  "ocenter-pg",
		"host=ocenter-pg port=5432 user=app password=s3cr3t dbname=app": "ocenter-pg",
		"host='db1,db2' password=x":                                     "db1",
		"postgres://app:pw@db1:5432,db2:5432/orders":                    "db1",
		"dns:///orders-api:9090":                                        "orders-api",
		"https://api.bank.kz/v1/pay":                                    "api.bank.kz",
		"redis:6379":                                                    "redis",
		"[::1]:5432":                                                    "::1",
		"password=s3cr3t":                                               "",
		"что-то странное с пробелами":                                   "",
		"": "",
	} {
		assert.Equal(t, host, Host(addr), addr)
	}
}

// Нарушение стандарта не роняет сервис: элемент не публикуется, причина — в Problems; длинный
// текст — только предупреждение.
func TestRegistrationRules(t *testing.T) {
	k := New(Config{}, testService, Build{})
	ok := func(context.Context, map[string]string) (int, error) { return 0, nil }
	register := func() {
		Handle(k, Endpoint{Id: "free", Title: "x", Description: "x", Path: "/free", Params: map[string]Param{"q": {}}}, ok)
		Handle(k, Endpoint{Id: "bad_default", Title: "x", Description: "x", Path: "/d", Params: map[string]Param{"n": {Type: "integer", Default: "двадцать"}}}, ok)
		Handle(k, Endpoint{Id: "card", Title: "x", Description: "x", Path: "/c", Params: map[string]Param{"p": {Personal: "card"}}}, ok)
		Handle(k, Endpoint{Id: "secret_field", Title: "y", Description: "y", Path: "/y"},
			func(context.Context, map[string]string) (struct {
				Token string `json:"access_token"`
			}, error) {
				return struct {
					Token string `json:"access_token"`
				}{}, nil
			})
		Handle(k, Endpoint{Id: "long", Title: "x", Description: strings.Repeat("д", 1001), Path: "/long"}, ok)
		Handle(k, Endpoint{Id: "long", Title: "x", Description: "дубль", Path: "/long2"}, ok)
		k.Metric(Metric{Id: "m", Title: "m", PromQL: "up", Unit: "percent"})
		k.ErrorPattern("x", "(?=lookahead)")
		k.Depend("Bad-Id", "postgres", "pg", true, ok0).Affects("всё")
		k.Depend("s3", "s3", "minio", false, ok0).Affects(strings.Repeat("д", 101))
		k.Depend("pg", "postgres", "user:pass@pg", true, ok0)
		k.Depend("pg2", "postgres", "host=pg password=x", true, ok0)
	}
	require.NotPanics(t, register, "сервис не падает из-за манифеста")

	problems := strings.Join(k.Problems(), "\n")
	for _, want := range []string{"needs Pattern, Enum or Personal", `default "двадцать"`, `personal "card"`,
		"looks like a secret", "id is already declared", `unit "percent"`, "not an RE2 regexp", `dependency id "Bad-Id"`} {
		assert.Contains(t, problems, want)
	}
	assert.Len(t, k.Problems(), 8)

	m := k.Manifest()
	assert.Equal(t, []string{"long"}, lo.Map(m.Endpoints, func(e endpointRep, _ int) string { return e.Id }), "опубликована только ручка с длинным описанием")
	assert.Empty(t, m.Metrics)
	assert.Nil(t, m.Logs)
	targets := map[string]string{}
	for _, d := range m.Dependencies {
		targets[d.Id] = d.Target
	}
	assert.Equal(t, map[string]string{"s3": "minio", "pg": "unknown", "pg2": "unknown"}, targets, "учётные данные в target — unknown")
	require.Error(t, k.CheckEndpoint("free", nil), "неопубликованная ручка — видно и в тесте ручки")

	warnings := strings.Join(k.Warnings(), "\n")
	assert.Contains(t, warnings, "endpoint long description is 1001 characters, the standard allows 1000")
	assert.Contains(t, warnings, "dependency s3 affects is 101 characters")

	assert.NotPanics(t, func() { New(Config{}, Service{}, Build{}) })
}

func ok0(context.Context) error { return nil }

func TestDescribe(t *testing.T) {
	for text, want := range map[string]string{
		"context deadline exceeded":                           "таймаут",
		"dial tcp 10.0.0.1:5432: connect: connection refused": "в соединении отказано",
		"rpc error: code = Unavailable desc = no connection":  "не отвечает",
		"POST https://mdm/api: 503 Service Unavailable":       "не отвечает",
		"warehouse not found in MDM":                          "не найдено",
		"GET /orders/14040: 404":                              "не найдено",
		"receipt: invalid sum":                                "отклонено",
		"billing account not found":                           "не найдено",
		"payment already captured":                            "отклонено",
		"POST /v1/responses: 429 Too Many Requests":           "превышен лимит запросов",
		"status 500: internal server error":                   "внутренняя ошибка",
		"401 Unauthorized":                                    "отказ в авторизации",
		"что-то пошло не так":                                 "ошибка",
	} {
		assert.Equal(t, want, DescribeText(text), text)
	}
	assert.Empty(t, DescribeText("  "), "пустой текст — пусто, а не «не отвечает»")
	assert.Equal(t, "ошибка", DescribeText("order 14040 failed"), "число внутри — не код 404")
	assert.Empty(t, Describe(nil))
}

type reasonCode string

func (reasonCode) PulseEnum() []string { return []string{"no_stock", "unpaid"} }

func TestEnumFromType(t *testing.T) {
	type rep struct {
		Reason  reasonCode `json:"reason"`
		Tagged  reasonCode `json:"tagged" pulse:"enum=a|b"`
		Reasons []reasonCode
	}
	s := schemaOf(zeroType(rep{}), "rep", nil)
	assert.Equal(t, []string{"no_stock", "unpaid"}, s.Properties["reason"].Enum, "перечень — из типа")
	assert.Equal(t, []string{"a", "b"}, s.Properties["tagged"].Enum, "тег — вместо типа")
	assert.Equal(t, []string{"no_stock", "unpaid"}, s.Properties["Reasons"].Items.Enum)
}

func TestProblemOk(t *testing.T) {
	k := New(Config{}, testService, Build{})
	k.Depend("broker", "http", "broker.kz", true, func(context.Context) error {
		return Problem{Status: "ok", Message: "вызовов не было, сеть до хоста есть"}
	})
	k.checkAll(context.Background())
	status := k.Status()
	assert.Equal(t, "ok", status.Status)
	assert.Equal(t, "вызовов не было, сеть до хоста есть", status.Dependencies[0].Message, "пометка у ok")
}

// Отчёт по объектам в ручке состояния: пороги застревания — из Domain, статусы — в порядке Domain,
// застрявших не меньше порога — объект и сервис degraded; объект не из Domain — в Problems.
func TestActivity(t *testing.T) {
	k := newKit()
	var gotThresholds map[string]time.Duration
	k.Activity("заказ", 10, func(_ context.Context, stuckAfter map[string]time.Duration) (EntityCounts, error) {
		gotThresholds = stuckAfter
		return EntityCounts{
			Statuses: map[string]StatusCount{
				"shipped": {Count: 900},
				"paid":    {Count: 42, Stuck: 12, Oldest: 3 * time.Hour},
				"draft":   {Count: 3},
			},
			Created: new(120),
		}, nil
	})
	k.Activity("рейс", 0, func(context.Context, map[string]time.Duration) (EntityCounts, error) { return EntityCounts{}, nil })
	require.Len(t, k.Problems(), 1, "рейса нет в Domain")

	k.checkAll(context.Background())
	status := k.Status()
	assert.Equal(t, map[string]time.Duration{"paid": 2 * time.Hour}, gotThresholds)
	require.Len(t, status.Entities, 1)
	e := status.Entities[0]
	assert.Equal(t, "degraded", e.Status, "12 застрявших при пороге 10")
	assert.Equal(t, "degraded", status.Status)
	assert.Equal(t, []string{"paid", "shipped", "draft"}, lo.Map(e.Statuses, func(s EntityStatusRep, _ int) string { return s.Name }), "порядок Domain, необъявленные — в конце")
	assert.Equal(t, int64(3*3600), e.Statuses[0].OldestS)
	assert.Equal(t, 120, *e.Created1h)
	assert.Nil(t, e.Finished1h)
}

// Ручка для человека: audience human, схема ответа не публикуется, ответ — как есть.
func TestHumanEndpoint(t *testing.T) {
	k := newKit()
	Handle(k, Endpoint{
		Id: "order_raw", Title: "Заказ как есть", Description: "когда просят показать заказ целиком, как есть",
		Path: "/diag/order/{number}/raw", Params: map[string]Param{"number": {Pattern: "[0-9]{5,12}"}},
		RowsPath: "history", Human: true,
	}, func(_ context.Context, params map[string]string) (map[string]any, error) {
		return map[string]any{"number": params["number"], "raw": map[string]string{"source": "1c"}}, nil
	})
	assert.Empty(t, k.Problems())

	endpoint := k.Manifest().Endpoints[1]
	assert.Equal(t, "human", endpoint.Audience)
	assert.Nil(t, endpoint.Response)
	assert.Empty(t, endpoint.RowsPath, "rows_path — только со схемой")
	require.NoError(t, k.CheckEndpoint("order_raw", map[string]string{"number": "234115"}))

	mux := http.NewServeMux()
	k.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/diag/order/234115/raw", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"number":"234115","raw":{"source":"1c"}}`, rec.Body.String())
}
