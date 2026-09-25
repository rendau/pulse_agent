package model

// Tool — инструмент pulse; InputSchema — JSON Schema входа.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Catalog — что pulse отдаёт при подключении: инструменты и порядок диагностики.
type Catalog struct {
	Tools        []Tool
	Instructions string
}

// CallResult — результат вызова инструмента. IsError — ошибка уровня инструмента
// (неизвестный сервис, неверный параметр): текст предназначен модели, чтобы она
// поправила вызов.
type CallResult struct {
	Text    string
	IsError bool
}
