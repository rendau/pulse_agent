package pulsekit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RequestIdHeader — заголовок, с которым pulse вызывает ручку: по нему вызов находится в логе
// сервиса.
const RequestIdHeader = "X-Pulse-Request-Id"

// Param — параметр ручки. Строковому нужен Pattern, Enum или Personal (вид персональных
// данных: phone, email, iin, customer_id — значение приходит настоящим, приведённым к одному
// виду: телефон — цифры с кодом страны). Default — в виде типа параметра ("20" у integer).
type Param struct {
	Type        string // string | integer | number | boolean
	Pattern     string
	Enum        []string
	Min, Max    *float64
	Default     string
	Required    bool
	Description string // ≤ 500
	Personal    string
}

// Endpoint — диагностическая ручка: только чтение, быстро, без побочных эффектов.
type Endpoint struct {
	Id          string
	Title       string // ≤ 100
	Description string // для агента: когда вызывать, когда нет, что вернёт (≤ 1000)
	Path        string // /diag/…, параметры пути — {name}
	Params      map[string]Param
	Timeout     time.Duration
	// RowsPath — где в ответе список (для лимита строк на стороне pulse); MaxRows — сколько
	// строк показать агенту (по умолчанию 50, не больше 100)
	RowsPath string
	MaxRows  int
}

// Error — ошибка ручки для человека: {"error": Message} с HTTP-статусом (по умолчанию 500).
type Error struct {
	Status  int
	Message string
}

func (e Error) Error() string { return e.Message }

type endpointDecl struct {
	Endpoint
	response *schema
}

type requestIdKey struct{}

// RequestId — X-Pulse-Request-Id вызова ручки (пусто — вызвал не pulse): для своих строк лога.
func RequestId(ctx context.Context) string {
	id, _ := ctx.Value(requestIdKey{}).(string)
	return id
}

var paramPathRe = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

// Handle объявляет ручку: схема ответа — из типа T (json-теги, `pulse:"personal=phone"`).
// fn получает проверенные параметры и контекст с RequestId; ошибка типа Error — её статус и
// текст, иначе 500 и «внутренняя ошибка» (текст ошибки наружу не уходит: в нём бывают строки
// подключения; в лог сервиса — уходит). Каждый вызов пишется в лог: ручка, X-Pulse-Request-Id,
// параметры (персональные — только вид), статус, время.
// Нарушение стандарта в объявлении — ручка не публикуется (Problems), сервис работает дальше.
func Handle[T any](k *Kit, e Endpoint, fn func(ctx context.Context, params map[string]string) (T, error)) {
	defer k.catch()
	k.validateEndpoint(e)
	var zero T
	response := schemaOf(zeroType(zero), e.Id, k.warn)

	k.mu.Lock()
	defer k.mu.Unlock()
	if slices.ContainsFunc(k.endpoints, func(d endpointDecl) bool { return d.Id == e.Id }) {
		fail("endpoint %s: id is already declared — not published", e.Id)
	}
	k.endpoints = append(k.endpoints, endpointDecl{Endpoint: e, response: response})
	pattern := paramPathRe.ReplaceAllString(e.Path, "{$1}")
	k.handlers[pattern] = func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestId := r.Header.Get(RequestIdHeader)
		params, err := readParams(e, r)
		status := http.StatusOK
		defer func() {
			slog.Info("pulse endpoint", "endpoint", e.Id, "request_id", requestId, "params", logParams(e, params),
				"status", status, "duration_ms", time.Since(started).Milliseconds())
		}()
		if err != nil {
			status = http.StatusBadRequest
			writeJson(w, status, map[string]string{"error": err.Error()})
			return
		}
		timeout := e.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), requestIdKey{}, requestId), timeout)
		defer cancel()

		result, err := fn(ctx, params)
		if err != nil {
			if e, ok := errors.AsType[Error](err); ok {
				status = cmpOr(e.Status, http.StatusInternalServerError)
				writeJson(w, status, map[string]string{"error": e.Message})
				return
			}
			status = http.StatusInternalServerError
			slog.Error("pulse endpoint failed", "endpoint", e.Id, "request_id", requestId, "error", err)
			writeJson(w, status, map[string]string{"error": "внутренняя ошибка"})
			return
		}
		writeJson(w, status, result)
	}
}

// logParams — параметры для лога: персональные — только вид.
func logParams(e Endpoint, params map[string]string) map[string]string {
	result := make(map[string]string, len(params))
	for name, value := range params {
		if kind := e.Params[name].Personal; kind != "" {
			value = "<" + kind + ">"
		}
		result[name] = value
	}
	return result
}

// validateEndpoint — правила стандарта: нарушение — fail (ручка не публикуется), длинный
// текст — предупреждение.
func (k *Kit) validateEndpoint(e Endpoint) {
	if !idRe.MatchString(e.Id) {
		fail("endpoint id %q: expected %s — not published", e.Id, idRe)
	}
	if e.Title == "" || e.Description == "" {
		fail("endpoint %s: Title and Description are required (Description — for the agent: when to call) — not published", e.Id)
	}
	if !strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, "..") || strings.ContainsAny(e.Path, "?#") {
		fail("endpoint %s: path %q — not published", e.Id, e.Path)
	}
	if e.MaxRows < 0 || e.MaxRows > 100 {
		fail("endpoint %s: MaxRows %d: expected 1..100 (0 — default 50) — not published", e.Id, e.MaxRows)
	}
	for _, m := range paramPathRe.FindAllStringSubmatch(e.Path, -1) {
		if _, ok := e.Params[m[1]]; !ok {
			fail("endpoint %s: path parameter %s is not declared — not published", e.Id, m[0])
		}
	}
	for name, p := range e.Params {
		if secretRe.MatchString(name) {
			fail("endpoint %s: parameter %q looks like a secret — not published", e.Id, name)
		}
		if !slices.Contains([]string{"string", "integer", "number", "boolean"}, cmpOr(p.Type, "string")) {
			fail("endpoint %s: parameter %q: type %q — not published", e.Id, name, p.Type)
		}
		if p.Personal != "" && (!slices.Contains(personalKinds, p.Personal) || p.Personal == "card" || cmpOr(p.Type, "string") != "string") {
			fail("endpoint %s: parameter %q: personal %q — a string of a searchable kind (phone, email, iin, customer_id…; not card) — not published", e.Id, name, p.Personal)
		}
		if cmpOr(p.Type, "string") == "string" && p.Pattern == "" && len(p.Enum) == 0 && p.Personal == "" {
			fail("endpoint %s: string parameter %q needs Pattern, Enum or Personal — not published", e.Id, name)
		}
		if p.Pattern != "" {
			if _, err := regexp.Compile(p.Pattern); err != nil {
				fail("endpoint %s: parameter %q: pattern is not an RE2 regexp: %s — not published", e.Id, name, err)
			}
		}
		if _, err := typedDefault(p); err != nil {
			fail("endpoint %s: parameter %q: default %q: %s — not published", e.Id, name, p.Default, err)
		}
		k.checkText("endpoint "+e.Id+" parameter "+name+" description", p.Description, maxTextChars)
	}
	k.checkText("endpoint "+e.Id+" title", e.Title, maxTitleChars)
	k.checkText("endpoint "+e.Id+" description", e.Description, maxEndpointDescChars)
}

// typedDefault — Default в типе параметра: число у integer/number, bool у boolean.
func typedDefault(p Param) (any, error) {
	if p.Default == "" {
		return nil, nil
	}
	switch cmpOr(p.Type, "string") {
	case "integer", "number":
		num, err := strconv.ParseFloat(p.Default, 64)
		if err != nil || (p.Type == "integer" && num != float64(int64(num))) {
			return nil, fmt.Errorf("not a %s", p.Type)
		}
		return num, nil
	case "boolean":
		return strconv.ParseBool(p.Default)
	default:
		return p.Default, nil
	}
}

// readParams — параметры запроса по декларации (pulse уже проверил их, здесь — вторая линия).
func readParams(e Endpoint, r *http.Request) (map[string]string, error) {
	result := make(map[string]string, len(e.Params))
	for name, p := range e.Params {
		value := r.PathValue(name)
		if value == "" {
			value = r.URL.Query().Get(name)
		}
		if value == "" {
			value = p.Default
		}
		if value == "" {
			if p.Required || strings.Contains(e.Path, "{"+name+"}") {
				return nil, fmt.Errorf("параметр %s обязателен", name)
			}
			continue
		}
		switch cmpOr(p.Type, "string") {
		case "integer", "number":
			num, err := strconv.ParseFloat(value, 64)
			if err != nil || (p.Min != nil && num < *p.Min) || (p.Max != nil && num > *p.Max) {
				return nil, fmt.Errorf("параметр %s: неверное число", name)
			}
		case "boolean":
			if _, err := strconv.ParseBool(value); err != nil {
				return nil, fmt.Errorf("параметр %s: ожидается true или false", name)
			}
		default:
			if len(p.Enum) > 0 && !slices.Contains(p.Enum, value) {
				return nil, fmt.Errorf("параметр %s: одно из %s", name, strings.Join(p.Enum, ", "))
			}
			if p.Pattern != "" && !regexp.MustCompile(`^(?:`+p.Pattern+`)$`).MatchString(value) {
				return nil, fmt.Errorf("параметр %s не подходит под %s", name, p.Pattern)
			}
		}
		result[name] = value
	}
	return result, nil
}

func cmpOr[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}
