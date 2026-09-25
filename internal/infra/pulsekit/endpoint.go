package pulsekit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Param — параметр ручки. Строковому нужен Pattern, Enum или Personal (вид персональных
// данных: phone, email, iin, customer_id — принимает токен, значение подставит pulse).
type Param struct {
	Type        string // string | integer | number | boolean
	Pattern     string
	Enum        []string
	Min, Max    *float64
	Default     string
	Required    bool
	Description string
	Personal    string
}

// Endpoint — диагностическая ручка: только чтение, быстро, без побочных эффектов.
type Endpoint struct {
	Id          string
	Title       string
	Description string // для агента: когда вызывать, когда нет, что вернёт
	Path        string // /diag/…, параметры пути — {name}
	Params      map[string]Param
	Timeout     time.Duration
	// RowsPath — где в ответе список (для лимита строк на стороне pulse)
	RowsPath string
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

var paramPathRe = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

// Handle объявляет ручку: схема ответа — из типа T (json-теги, `pulse:"personal=phone"`).
// fn получает проверенные параметры; ошибка типа Error — её статус и текст, иначе 500 и
// «внутренняя ошибка» (текст ошибки наружу не уходит: в нём бывают строки подключения).
func Handle[T any](k *Kit, e Endpoint, fn func(ctx context.Context, params map[string]string) (T, error)) {
	validateEndpoint(e)
	var zero T
	response := schemaOf(zeroType(zero), e.Id)

	k.mu.Lock()
	defer k.mu.Unlock()
	k.endpoints = append(k.endpoints, endpointDecl{Endpoint: e, response: response})
	pattern := paramPathRe.ReplaceAllString(e.Path, "{$1}")
	k.handlers[pattern] = func(w http.ResponseWriter, r *http.Request) {
		params, err := readParams(e, r)
		if err != nil {
			writeJson(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		timeout := e.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		result, err := fn(ctx, params)
		if err != nil {
			if e, ok := errors.AsType[Error](err); ok {
				writeJson(w, cmpOr(e.Status, http.StatusInternalServerError), map[string]string{"error": e.Message})
				return
			}
			writeJson(w, http.StatusInternalServerError, map[string]string{"error": "внутренняя ошибка"})
			return
		}
		writeJson(w, http.StatusOK, result)
	}
}

func validateEndpoint(e Endpoint) {
	if !idRe.MatchString(e.Id) {
		panic(fmt.Sprintf("pulsekit: endpoint id %q: expected %s", e.Id, idRe))
	}
	if e.Title == "" || e.Description == "" {
		panic(fmt.Sprintf("pulsekit: endpoint %s: Title and Description are required (Description — for the agent: when to call)", e.Id))
	}
	if !strings.HasPrefix(e.Path, "/") || strings.Contains(e.Path, "..") || strings.ContainsAny(e.Path, "?#") {
		panic(fmt.Sprintf("pulsekit: endpoint %s: path %q", e.Id, e.Path))
	}
	for _, m := range paramPathRe.FindAllStringSubmatch(e.Path, -1) {
		if _, ok := e.Params[m[1]]; !ok {
			panic(fmt.Sprintf("pulsekit: endpoint %s: path parameter %s is not declared", e.Id, m[0]))
		}
	}
	for name, p := range e.Params {
		if secretRe.MatchString(name) {
			panic(fmt.Sprintf("pulsekit: endpoint %s: parameter %q looks like a secret", e.Id, name))
		}
		if cmpOr(p.Type, "string") == "string" && p.Pattern == "" && len(p.Enum) == 0 && p.Personal == "" {
			panic(fmt.Sprintf("pulsekit: endpoint %s: string parameter %q needs Pattern, Enum or Personal", e.Id, name))
		}
		if p.Pattern != "" {
			regexp.MustCompile(p.Pattern)
		}
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
