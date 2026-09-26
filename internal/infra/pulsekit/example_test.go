package pulsekit

// Образцы подключения стандарта pulse (docs/service-manifest.md): что писать в сервисе (там —
// с префиксом pulsekit.). Это тесты — они компилируются и выполняются вместе с остальными,
// поэтому образец не устаревает.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// OrderStatus — статус заказа в коде сервиса: перечень для схемы берётся отсюда (PulseEnum), а не
// переписывается в тег.
type OrderStatus string

const (
	OrderNew     OrderStatus = "new"
	OrderPaid    OrderStatus = "paid"
	OrderShipped OrderStatus = "shipped"
)

func (OrderStatus) PulseEnum() []string {
	return []string{string(OrderNew), string(OrderPaid), string(OrderShipped)}
}

// exampleOrderRep — ответ ручки: только то, что можно показать на общем дашборде.
type exampleOrderRep struct {
	Number    string      `json:"number"`
	Status    OrderStatus `json:"status"`
	Phone     string      `json:"customer_phone" pulse:"personal=phone"` // модель увидит токен, человек — номер
	Reason    string      `json:"stuck_reason" pulse:"maxLength=300,description=почему застрял, своими словами"`
	ShippedAt *time.Time  `json:"shipped_at"` // nil — null: ещё не отгружен
}

// exampleOrders — хранилище заказов сервиса (в тесте — фейк).
type exampleOrders interface {
	Get(ctx context.Context, number string) (*exampleOrderRep, error)
}

var errOrderNotFound = errors.New("not found")

type fakeExampleOrders struct{}

func (fakeExampleOrders) Get(_ context.Context, number string) (*exampleOrderRep, error) {
	if number == "00000" {
		return nil, errOrderNotFound
	}
	return &exampleOrderRep{Number: number, Status: OrderPaid, Phone: "+77011234567", Reason: "ждём склад"}, nil
}

// handleExampleOrderStatus — диагностическая ручка: так она объявляется в сервисе (обычно рядом с
// newPulsekit в internal/app/manifest.go).
func handleExampleOrderStatus(kit *Kit, orders exampleOrders) {
	Handle(kit, Endpoint{
		Id:          "order_status",
		Title:       "Где заказ",
		Description: "Вызывай, когда спрашивают о конкретном заказе по номеру: статус, почему застрял. Не для поиска заказов клиента.",
		Path:        "/diag/order/{number}",
		Params:      map[string]Param{"number": {Pattern: "[0-9]{5,12}", Description: "номер заказа"}},
		Timeout:     3 * time.Second,
	}, func(ctx context.Context, params map[string]string) (exampleOrderRep, error) {
		order, err := orders.Get(ctx, params["number"])
		switch {
		case errors.Is(err, errOrderNotFound):
			return exampleOrderRep{}, Error{Status: http.StatusNotFound, Message: "заказ не найден"}
		case err != nil:
			return exampleOrderRep{}, err // наружу — «внутренняя ошибка», текст — только в лог сервиса
		}
		return *order, nil
	})
}

// Ручка и её проверка в тесте сервиса: CheckEndpoint вызывает ручку, как pulse, и сверяет ответ
// со схемой (лишние поля, типы, длины, enum, время; у ошибки — только {"error"}).
func ExampleKit_CheckEndpoint() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	}, Build{})
	handleExampleOrderStatus(kit, fakeExampleOrders{})

	fmt.Println(kit.CheckEndpoint("order_status", map[string]string{"number": "234115"}))
	fmt.Println(kit.CheckEndpoint("order_status", map[string]string{"number": "00000"}))
	// Output:
	// <nil>
	// <nil>
}

// Зависимости: одной строкой рядом с созданием клиента; адрес — через Host, последствие — Affects.
// Платный API без health-ручки — пассивная проверка по настоящим вызовам.
func ExampleNewPassive() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	}, Build{})

	pingDb := func(context.Context) error { return nil }
	kit.Depend("pg", "postgres", Host("postgres://app:secret@orders-pg:5432/orders"), true, pingDb).
		Affects("весь сервис")

	bank := NewPassive(5 * time.Minute)
	kit.Depend("bank", "http", Host("https://api.bank.kz/v1"), false, bank.Check).
		Affects("онлайн-оплата")

	// в клиенте банка после каждого вызова: сбой связи или 5xx — ошибка, любой ответ банка
	// (даже отказ в оплате) — nil
	bank.Observe(nil)

	for _, d := range kit.Manifest().Dependencies {
		fmt.Println(d.Id, d.Target, d.Affects)
	}
	// Output:
	// pg orders-pg весь сервис
	// bank api.bank.kz онлайн-оплата
}

// Показатели состояния: то, чего нет в метриках, но что объясняет состояние.
func ExampleKit_Gauge() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	}, Build{})

	outboxCount := func(context.Context) (int, error) { return 1840, nil }
	kit.Gauge("outbox_backlog", "Неотправленные события", "count", func(ctx context.Context) (float64, string, error) {
		n, err := outboxCount(ctx)
		status := "ok"
		if n > 1000 { // порог — решение владельца сервиса
			status = "degraded"
		}
		return float64(n), status, err
	})
}

// Своё сообщение вместо текста чужой ошибки — для ручки состояния и сохранённых причин.
func ExampleDescribeText() {
	fmt.Println(DescribeText("warehouse not found in MDM"))
	fmt.Println(DescribeText("receipt: invalid sum"))
	fmt.Println(Describe(errors.New("dial tcp 10.0.0.7:5432: connect: connection refused")))
	// Output:
	// не найдено
	// отклонено
	// в соединении отказано
}

// Редкий критичный вызов (брокер оплаты — только при выдаче): были вызовы — судит пассивная
// проверка, не было — хотя бы сеть до хоста, статус ok с пометкой.
func ExamplePassive_Idle() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
	}, Build{})

	broker := NewPassive(5 * time.Minute)
	kit.Depend("broker", "http", Host("https://pay.broker.kz"), true, func(ctx context.Context) error {
		if !broker.Idle() {
			return broker.Check(ctx)
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", "pay.broker.kz:443")
		if err != nil {
			return err
		}
		_ = conn.Close()
		return Problem{Status: "ok", Message: "вызовов не было, сеть до хоста есть"}
	}).Affects("выдача заказов с оплатой")
}

// Бизнес-смысл сервиса для агента: за что отвечает, чем не занимается, объекты (формат номера,
// статусы, когда застрял) и типичные вопросы. Заполняется в internal/app/manifest.go.
func ExampleDomain() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы и ведёт их до выдачи", OwnerTeam: "orders", Criticality: "high",
		Domain: &Domain{
			Responsibilities: []string{"Принимает заказы с сайта и из магазинов", "Ведёт заказ до выдачи: оплата, сборка, отгрузка"},
			NotResponsible: []Boundary{
				{What: "оплата и возвраты", Service: "payments"},
				{What: "доставка курьером", Service: "caravan"},
			},
			Entities: []Entity{
				{
					Name: "заказ", IdPattern: "[0-9]{5,12}", IdExample: "234115", Description: "заказ клиента от создания до выдачи",
					Statuses: []EntityStatus{
						{Name: string(OrderNew), Meaning: "создан, ждёт оплаты", StuckAfter: 30 * time.Minute},
						{Name: string(OrderPaid), Meaning: "оплачен, ждёт сборки", StuckAfter: 2 * time.Hour},
						{Name: string(OrderShipped), Meaning: "отгружен в доставку"},
					},
				},
			},
			Questions: []Question{
				{Question: "где заказ и почему застрял", Endpoint: "order_status"},
				{Question: "почему заказ не попал в 1С", How: "логи сервиса по номеру заказа"},
			},
		},
	}, Build{})
	handleExampleOrderStatus(kit, fakeExampleOrders{})

	fmt.Println(len(kit.Problems()))
	// Output:
	// 0
}

// Отчёт по объектам в ручке состояния: сколько заказов в каждом статусе и сколько застряло.
// Порог застревания берётся из Domain (StuckAfter) — здесь только запрос к своей БД.
func ExampleKit_Activity() {
	kit := New(Config{}, Service{
		Name: "orders", Title: "Заказы", Description: "Принимает заказы", OwnerTeam: "orders", Criticality: "high",
		Domain: &Domain{Entities: []Entity{
			{Name: "заказ", Statuses: []EntityStatus{
				{Name: string(OrderPaid), Meaning: "оплачен, ждёт сборки", StuckAfter: 2 * time.Hour},
				{Name: string(OrderShipped), Meaning: "отгружен"},
			}},
		}},
	}, Build{})

	// в сервисе: SELECT status, count(*), count(*) FILTER (WHERE status_changed_at < now() - порог статуса),
	// max(now() - status_changed_at) FROM orders WHERE status <> 'delivered' GROUP BY status —
	// время входа в статус, не создания; кто ждёт намеренно — не в stuck и не в oldest
	countOrders := func(_ context.Context, stuckAfter map[string]time.Duration) (map[string]StatusCount, error) {
		_ = stuckAfter[string(OrderPaid)] // 2h — из Domain
		return map[string]StatusCount{
			string(OrderPaid):    {Count: 42, Stuck: 3, Oldest: 3 * time.Hour},
			string(OrderShipped): {Count: 900},
		}, nil
	}
	// 10 и больше застрявших — сервис degraded; 0 — только показывать
	kit.Activity("заказ", 10, func(ctx context.Context, stuckAfter map[string]time.Duration) (EntityCounts, error) {
		statuses, err := countOrders(ctx, stuckAfter)
		return EntityCounts{Statuses: statuses}, err
	})

	fmt.Println(len(kit.Problems()))
	// Output:
	// 0
}
