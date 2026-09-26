package pii

// Tokenizer — персональные данные токенами на границе с моделью (стандарт pulse,
// docs/service-manifest.md, «Персональные данные — токенами»): модель видит pii:<вид>:<код>,
// настоящее значение уходит только в запросы к pulse и в готовый ответ клиенту. Один и тот же
// телефон — один и тот же токен. Клиенты агента (бот, service desk) токенов не видят.
type Tokenizer interface {
	// Mask — телефоны и email в тексте токенами, карты — маской; готовые токены не трогаются.
	Mask(text string) string
	// MaskToolOutput — ответ инструмента pulse: JSON — поля из personal_fields (путь → вид)
	// токенами по виду, во всех строках — как Mask; не JSON — как Mask.
	MaskToolOutput(text string) string
	// RevealArgs — аргументы вызова pulse (JSON): токены видов с поиском — настоящими
	// значениями (телефон — +цифры); неизвестный токен остаётся как есть.
	RevealArgs(args string) string
	// Reveal — готовый ответ человеку: все известные токены — настоящими значениями. JSON
	// раскрывается по строкам (значение не сломает JSON), иначе — как текст.
	Reveal(text string) string
}
