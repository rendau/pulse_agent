package service

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/mechta-market/pulse_agent/internal/errs"
)

// maxSchemaDepth — глубже объекты схемы клиента не разбираются (у провайдера свой предел — 5
// уровней вложенности; ошибку про него клиент получит от провайдера).
const maxSchemaDepth = 10

// strictSchema приводит JSON Schema системы-клиента к строгому виду структурированного вывода
// (OpenAI strict): у каждого объекта additionalProperties=false и все поля в required, а поля,
// которые клиент не сделал обязательными, допускают null — «значения нет» приходит null, а не
// выдумкой. oneOf → anyOf (strict понимает только anyOf). Схема клиента не меняется — копия.
func strictSchema(schema map[string]any) (map[string]any, error) {
	if schema == nil {
		return nil, fmt.Errorf("%w: response_schema is empty", errs.InvalidRequest)
	}
	if t, _ := schema["type"].(string); t != "object" {
		return nil, fmt.Errorf("%w: response_schema: root must be {\"type\": \"object\", ...}", errs.InvalidRequest)
	}

	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("%w: response_schema: %s", errs.InvalidRequest, err)
	}
	var copied map[string]any
	if err = json.Unmarshal(raw, &copied); err != nil {
		return nil, fmt.Errorf("%w: response_schema: %s", errs.InvalidRequest, err)
	}

	if err = strictNode(copied, 0); err != nil {
		return nil, err
	}
	return copied, nil
}

func strictNode(node map[string]any, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%w: response_schema is nested deeper than %d levels", errs.InvalidRequest, maxSchemaDepth)
	}

	if oneOf, ok := node["oneOf"]; ok {
		node["anyOf"] = oneOf
		delete(node, "oneOf")
	}

	if props, ok := node["properties"].(map[string]any); ok {
		required := map[string]bool{}
		if list, ok := node["required"].([]any); ok {
			for _, name := range list {
				if s, ok := name.(string); ok {
					required[s] = true
				}
			}
		}

		names := make([]string, 0, len(props))
		for name, prop := range props {
			names = append(names, name)
			child, ok := prop.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: response_schema: property %q must be an object", errs.InvalidRequest, name)
			}
			if err := strictNode(child, depth+1); err != nil {
				return err
			}
			if !required[name] {
				nullable(child)
			}
		}
		sort.Strings(names)
		node["required"] = names
		node["additionalProperties"] = false
	}

	for _, key := range []string{"items"} {
		if child, ok := node[key].(map[string]any); ok {
			if err := strictNode(child, depth+1); err != nil {
				return err
			}
		}
	}
	for _, key := range []string{"anyOf"} {
		if list, ok := node[key].([]any); ok {
			for _, item := range list {
				if child, ok := item.(map[string]any); ok {
					if err := strictNode(child, depth+1); err != nil {
						return err
					}
				}
			}
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		if defs, ok := node[key].(map[string]any); ok {
			for _, def := range defs {
				if child, ok := def.(map[string]any); ok {
					if err := strictNode(child, depth+1); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// nullable — поле допускает null: к type добавляется "null" (у anyOf — вариант {"type":"null"},
// у enum — значение null).
func nullable(node map[string]any) {
	switch t := node["type"].(type) {
	case string:
		if t != "null" {
			node["type"] = []any{t, "null"}
		}
	case []any:
		if !slices.Contains(t, any("null")) {
			node["type"] = append(t, "null")
		}
	default:
		if list, ok := node["anyOf"].([]any); ok {
			node["anyOf"] = append(list, map[string]any{"type": "null"})
			return
		}
	}
	if enum, ok := node["enum"].([]any); ok && !slices.Contains(enum, nil) {
		node["enum"] = append(enum, nil)
	}
}
