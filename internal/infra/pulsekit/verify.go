package pulsekit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// CheckEndpoint — для тестов сервиса: вызывает ручку id с params так же, как pulse (GET на путь,
// параметры пути — в путь, остальные — в query), и сверяет ответ со схемой из манифеста по
// правилам стандарта: JSON, у ошибки — только {"error": текст}; у ответа — ни одного
// необъявленного поля, типы, maxLength, maxItems, enum, время RFC 3339. nil — ответ по
// стандарту. Зависимости ручки в тесте — свои (фейки), pulsekit их не знает.
func (k *Kit) CheckEndpoint(id string, params map[string]string) error {
	k.mu.RLock()
	idx := slices.IndexFunc(k.endpoints, func(e endpointDecl) bool { return e.Id == id })
	var decl endpointDecl
	if idx >= 0 {
		decl = k.endpoints[idx]
	}
	k.mu.RUnlock()
	if idx < 0 {
		return fmt.Errorf("endpoint %s is not declared", id)
	}

	path := paramPathRe.ReplaceAllStringFunc(decl.Path, func(m string) string {
		return url.PathEscape(params[m[1:len(m)-1]])
	})
	query := url.Values{}
	for name, value := range params {
		if !strings.Contains(decl.Path, "{"+name+"}") {
			query.Set(name, value)
		}
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	mux := http.NewServeMux()
	k.Register(mux)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(RequestIdHeader, "pulse-check")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("%s: Content-Type %q, expected application/json", id, ct)
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		return fmt.Errorf("%s: not JSON: %w", id, err)
	}

	if rec.Code < 200 || rec.Code > 299 {
		obj, ok := body.(map[string]any)
		text, isText := obj["error"].(string)
		if !ok || len(obj) != 1 || !isText || text == "" {
			return fmt.Errorf("%s: status %d: an error must be exactly {\"error\": text}", id, rec.Code)
		}
		return nil
	}

	var problems []error
	checkValue(body, decl.response, "response", &problems)
	return errors.Join(problems...)
}

func checkValue(v any, s *schema, path string, problems *[]error) {
	if v == nil {
		return // null — «нет значения»: по стандарту
	}
	fail := func(format string, args ...any) {
		*problems = append(*problems, fmt.Errorf(path+": "+format, args...))
	}
	switch s.Type {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			fail("expected object")
			return
		}
		for key, child := range obj {
			switch {
			case s.Properties[key] != nil:
				checkValue(child, s.Properties[key], path+"."+key, problems)
			case s.AdditionalProperties != nil:
				checkValue(child, s.AdditionalProperties, path+"."+key, problems)
			default:
				fail("field %q is not declared in the schema — pulse will drop it", key)
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			fail("expected array")
			return
		}
		if s.MaxItems > 0 && len(arr) > s.MaxItems {
			fail("%d items, maxItems %d", len(arr), s.MaxItems)
		}
		for i, item := range arr {
			checkValue(item, s.Items, fmt.Sprintf("%s[%d]", path, i), problems)
		}
	case "string":
		str, ok := v.(string)
		switch {
		case !ok:
			fail("expected string")
		case s.MaxLength > 0 && utf8.RuneCountInString(str) > s.MaxLength:
			fail("%d characters, maxLength %d", utf8.RuneCountInString(str), s.MaxLength)
		case len(s.Enum) > 0 && !slices.Contains(s.Enum, str):
			fail("%q is not in enum %s", str, strings.Join(s.Enum, ", "))
		case s.Format == "date-time":
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				fail("%q is not RFC 3339 time", str)
			}
		}
	case "integer", "number":
		num, ok := v.(json.Number)
		if !ok {
			fail("expected %s", s.Type)
			return
		}
		if _, err := num.Int64(); s.Type == "integer" && err != nil {
			fail("%s is not an integer", num)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			fail("expected boolean")
		}
	}
}
