package constant

// ChartTool — инструмент бота (не pulse): график к ответу.
const ChartTool = "render_chart"

// MaxCharts — графиков на один ответ.
const MaxCharts = 3

// ChartDescription — описание для модели: когда рисовать и откуда брать данные.
const ChartDescription = `Рисует график и прикладывает его картинкой к ответу. ` +
	`Выбирай, когда ответ про динамику за период (нагрузка, ошибки, задержки, память, CPU) или сравнение нескольких сервисов/подов: картинка понятнее списка чисел. ` +
	`type=line — ряды во времени, type=bar — сравнение по категориям. ` +
	`Временные ряды — ссылкой metrics на уже выполненный query_metrics (service + metric_id), точки не переписывай; ` +
	`series со своими точками — только для небольших данных (до ~30 значений), например ошибки по сервисам из get_cluster_health. ` +
	`Не больше двух графиков на ответ и не график из 1–3 значений.`

// ChartSchema — JSON Schema входа render_chart.
var ChartSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"type": map[string]any{
			"type": "string", "enum": []string{"line", "bar"},
			"description": "line — ряды во времени; bar — сравнение по категориям",
		},
		"title": map[string]any{"type": "string", "description": "заголовок по-русски: что и за какой период"},
		"unit": map[string]any{
			"type":        "string",
			"description": "единица значений: rps, ratio, seconds, bytes, cores, count или своя подпись; по ней бот сам переведёт в МБ, %, мс. У metrics — берётся из query_metrics",
		},
		"metrics": map[string]any{
			"type":        "array",
			"description": "ряды из уже выполненных query_metrics этого разбора (для line)",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"service":   map[string]any{"type": "string"},
					"metric_id": map[string]any{"type": "string", "description": "как в вызове query_metrics; пусто — последний query_metrics сервиса"},
				},
				"required": []string{"service"},
			},
		},
		"series": map[string]any{
			"type":        "array",
			"description": "свои ряды: у line x — время RFC3339, у bar x — подпись категории",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "подпись ряда в легенде"},
					"points": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"x": map[string]any{"type": "string"},
								"y": map[string]any{"type": "number"},
							},
							"required": []string{"x", "y"},
						},
					},
				},
				"required": []string{"points"},
			},
		},
	},
	"required": []string{"type", "title"},
}

// ChartDone — ответ модели на построенный график.
const ChartDone = "OK: график «%s» построен (%d рядов, %d точек) и уйдёт картинкой вместе с ответом. " +
	"В тексте не перечисляй его точки — только вывод и ключевые цифры."

// ChartLimit — ответ модели сверх лимита графиков.
const ChartLimit = "не построен: на ответ не больше %d графиков"
