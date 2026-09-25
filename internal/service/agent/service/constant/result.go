package constant

// ResultSchemaName — имя схемы структурированного ответа у провайдера.
const ResultSchemaName = "pulse_answer"

func nullable(t string) map[string]any { return map[string]any{"type": []string{t, "null"}} }

func enum(values ...string) map[string]any { return map[string]any{"type": "string", "enum": values} }

func object(properties map[string]any) map[string]any {
	required := make([]string, 0, len(properties))
	for key := range properties {
		required = append(required, key)
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

var str = map[string]any{"type": "string"}

// статусы и важность в структурированном ответе
var (
	Statuses   = []string{"ok", "degraded", "down", "not_found", "unknown"}
	Severities = []string{"none", "low", "medium", "high", "critical"}
)

// ResultSchema — JSON Schema ответа в формате json (strict: все поля обязательны, пустое — null
// или пустой список). Поля описаны в formatRules[FormatJson].
var ResultSchema = object(map[string]any{
	"summary":  str,
	"status":   enum(Statuses...),
	"severity": enum(Severities...),
	"services": array(object(map[string]any{
		"name":   str,
		"status": enum(Statuses...),
		"note":   str,
	})),
	"facts": array(object(map[string]any{
		"text":    str,
		"time":    nullable("string"),
		"service": nullable("string"),
	})),
	"next_steps":          array(str),
	"unavailable_sources": array(str),
})
