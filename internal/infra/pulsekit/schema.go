package pulsekit

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// schema — подмножество JSON Schema стандарта манифеста.
type schema struct {
	Type                 string             `json:"type"`
	Properties           map[string]*schema `json:"properties,omitempty"`
	Items                *schema            `json:"items,omitempty"`
	AdditionalProperties *schema            `json:"additionalProperties,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	Format               string             `json:"format,omitempty"`
	MaxLength            int                `json:"maxLength,omitempty"`
	MaxItems             int                `json:"maxItems,omitempty"`
	Description          string             `json:"description,omitempty"`
	Personal             string             `json:"x-personal,omitempty"`
}

var (
	timeType = reflect.TypeFor[time.Time]()
	enumType = reflect.TypeFor[Enum]()
)

// Enum — тип поля ответа со своим перечнем значений: pulsekit берёт enum схемы из него, и
// перечень не расходится с константами кода.
//
//	type ReasonCode string
//	func (ReasonCode) PulseEnum() []string { return []string{"no_stock", "unpaid", "address"} }
type Enum interface {
	PulseEnum() []string
}

func zeroType(v any) reflect.Type {
	return reflect.TypeOf(v)
}

// schemaOf строит схему ответа из Go-типа: struct — object (поля по json-тегам), slice — array,
// map[string]число/bool — словарь, time.Time — date-time (RFC 3339 со смещением; UTC — «Z»,
// это тоже по стандарту), указатель — то же поле, nil — null («нет значения»), строковый тип с
// методом PulseEnum — enum из него. Теги поля (enum в теге — вместо PulseEnum):
//
//	`pulse:"personal=phone,maxLength=300,maxItems=50,enum=new|paid|shipped,description=…"`
//
// description — последним: всё до конца тега, запятые можно. Поле, похожее на секрет, — ручка
// не публикуется: стандарт такие поля запрещает.
func schemaOf(t reflect.Type, path string, warn func(string)) *schema {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return &schema{Type: "string", Format: "date-time"}
	case t.Kind() == reflect.Struct:
		result := &schema{Type: "object", Properties: map[string]*schema{}}
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if secretRe.MatchString(name) {
				fail("%s.%s: field name looks like a secret — forbidden by the manifest standard — endpoint not published", path, name)
			}
			child := schemaOf(f.Type, path+"."+name, warn)
			applyTag(child, f.Tag.Get("pulse"), path+"."+name, warn)
			result.Properties[name] = child
		}
		return result
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		return &schema{Type: "array", Items: schemaOf(t.Elem(), path+"[]", warn)}
	case t.Kind() == reflect.Map:
		values := schemaOf(t.Elem(), path+"{}", warn)
		if t.Key().Kind() != reflect.String || (values.Type != "integer" && values.Type != "number" && values.Type != "boolean") {
			fail("%s: map is allowed only as map[string]number or map[string]bool — endpoint not published", path)
		}
		return &schema{Type: "object", AdditionalProperties: values}
	case t.Kind() == reflect.String:
		result := &schema{Type: "string"}
		if t.Implements(enumType) {
			result.Enum = reflect.Zero(t).Interface().(Enum).PulseEnum()
		}
		return result
	case t.Kind() == reflect.Bool:
		return &schema{Type: "boolean"}
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		return &schema{Type: "integer"}
	case t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64:
		return &schema{Type: "number"}
	default:
		fail("%s: type %s is not supported in a response — endpoint not published", path, t)
		return nil
	}
}

func applyTag(s *schema, tag, path string, warn func(string)) {
	for tag != "" {
		var part string
		if strings.HasPrefix(strings.TrimSpace(tag), "description=") {
			part, tag = strings.TrimSpace(tag), "" // описание — до конца тега, с запятыми
		} else {
			part, tag, _ = strings.Cut(tag, ",")
		}
		key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		var err error
		switch key {
		case "personal":
			s.Personal = value
		case "maxLength":
			s.MaxLength, err = strconv.Atoi(value)
		case "maxItems":
			s.MaxItems, err = strconv.Atoi(value)
		case "enum":
			s.Enum = strings.Split(value, "|")
		case "description":
			s.Description = value
			if n := utf8.RuneCountInString(value); n > maxTextChars && warn != nil {
				warn(fmt.Sprintf("%s description is %d characters, the standard allows %d — pulse will cut it", path, n, maxTextChars))
			}
		case "":
		default:
			err = fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			fail("%s: tag pulse: %s — endpoint not published", path, err)
		}
	}
	switch {
	case s.Personal != "" && !slices.Contains(personalKinds, s.Personal):
		fail("%s: personal %q: expected one of %s — endpoint not published", path, s.Personal, strings.Join(personalKinds, ", "))
	case s.Personal != "" && s.Type != "string" && s.Type != "integer":
		fail("%s: personal is allowed only on strings and integers — endpoint not published", path)
	case len(s.Enum) > 0 && s.Type != "string":
		fail("%s: enum is allowed only on strings — endpoint not published", path)
	case s.MaxLength > 0 && s.Type != "string", s.MaxItems > 0 && s.Type != "array":
		fail("%s: maxLength is for strings, maxItems — for slices — endpoint not published", path)
	}
}
