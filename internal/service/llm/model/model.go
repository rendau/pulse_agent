// Package model — провайдер-независимый контракт LLM: один шаг агентного цикла
// (запрос → текст и/или вызовы инструментов). Адаптеры провайдеров
// (internal/service/llm/<provider>) переводят его в свой API.
package model

// роли сообщений истории
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message — текстовое сообщение истории диалога (без вызовов инструментов).
type Message struct {
	Role string
	Text string
}

// ToolDef — инструмент, доступный модели; Parameters — JSON Schema входа.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall — вызов инструмента, запрошенный моделью; Arguments — JSON-объект.
type ToolCall struct {
	Id        string
	Name      string
	Arguments string
}

// ToolResult — результат вызова инструмента для следующего шага.
type ToolResult struct {
	CallId string
	Output string
}

// Request — один шаг модели.
//
// Первый шаг разбора: State пуст, Messages — история и текущий вопрос.
// Следующие шаги: State — из предыдущего Response (непрозрачное состояние адаптера:
// накопленный контекст, reasoning и т.п.), ToolResults — ответы на его ToolCalls;
// Messages адаптер при непустом State не читает.
type Request struct {
	System      string
	Messages    []Message
	Tools       []ToolDef
	State       any
	ToolResults []ToolResult

	// NoTools — модель должна ответить текстом, без новых вызовов инструментов
	// (исчерпан лимит шагов или времени).
	NoTools bool

	// Output — итоговый ответ строго JSON по схеме (структурированный вывод); nil — текст.
	// Вызовы инструментов схема не ограничивает.
	Output *OutputSchema
}

// OutputSchema — JSON Schema итогового ответа (strict: все поля required,
// additionalProperties: false, необязательное — через null).
type OutputSchema struct {
	Name   string
	Schema map[string]any
}

// Response — результат шага модели.
type Response struct {
	Text      string
	ToolCalls []ToolCall
	State     any
	Usage     Usage

	// Incomplete — причина, по которой ответ оборван провайдером (лимит токенов,
	// фильтр контента); пусто — ответ полный.
	Incomplete string
}

// Usage — токены шага.
type Usage struct {
	InputTokens     int64
	CachedTokens    int64 // часть InputTokens, прочитанная из кэша провайдера
	OutputTokens    int64
	ReasoningTokens int64 // часть OutputTokens, ушедшая на рассуждения
}

// Add суммирует токены (итог по разбору).
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.CachedTokens += o.CachedTokens
	u.OutputTokens += o.OutputTokens
	u.ReasoningTokens += o.ReasoningTokens
}
