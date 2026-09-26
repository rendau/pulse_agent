package model

import (
	"time"

	chartModel "github.com/rendau/pulse_agent/internal/service/chart/model"
	llmModel "github.com/rendau/pulse_agent/internal/service/llm/model"
)

// причины, по которым разбор закончен раньше, чем модель решила сама
const (
	IncompleteToolCalls = "tool_calls" // исчерпан лимит вызовов инструментов
	IncompleteTimeout   = "timeout"    // исчерпано время на разбор
	IncompleteOutput    = "output"     // ответ модели оборван провайдером (лимит токенов и т.п.)
)

// статусы вызова инструмента (ToolTrace.Status, метрики)
const (
	ToolStatusOk        = "ok"
	ToolStatusError     = "error"      // вызов не удался (pulse недоступен и т.п.)
	ToolStatusToolError = "tool_error" // инструмент ответил ошибкой (неверный параметр и т.п.)
	ToolStatusSkipped   = "skipped"    // не выполнен: исчерпан лимит вызовов
)

// Turn — прошлая пара «вопрос — ответ» из истории чата.
type Turn struct {
	Question string
	Answer   string
}

// Req — вопрос с историей беседы. Format — оформление ответа (telegram, markdown, plain,
// json; пусто — telegram); Charts — клиент принимает графики (иначе render_chart не предлагается).
type Req struct {
	History  []Turn
	Question string
	Format   string
	Charts   bool
	// ResponseSchema — JSON Schema ответа от системы-клиента (RPC): ответ — JSON строго по ней
	// (Result.Json); nil — формат Format
	ResponseSchema map[string]any
	// Chat — беседа клиента: её заметки идут модели, модель может менять их и приглушения
	// уведомлений (инструменты беседы); nil — вопрос без беседы
	Chat *Chat
}

// Chat — беседа, в которой задан вопрос: кто спросил и что беседа попросила запомнить.
type Chat struct {
	Client         string
	ConversationId string
	User           string // кто спросил (имя или id) — автор заметок и приглушений
	Notes          string
}

// Result — итог разбора.
type Result struct {
	// Answer — текст ответа с настоящими значениями; в формате json — собран из Structured
	Answer string

	// ModelAnswer — ответ, как его написала модель: персональные данные — токенами (журнал,
	// история беседы)
	ModelAnswer string

	// Structured — ответ по полям схемы по умолчанию (формат json без своей схемы)
	Structured *Structured

	// Json — ответ в JSON (формат json или схема клиента), как его выдала модель; nil —
	// текстовый формат или модель не выдала разбираемый JSON (оборванный ответ)
	Json []byte

	// Incomplete — почему разбор закончен досрочно (Incomplete*); пусто — модель
	// ответила сама.
	Incomplete string

	Steps     int // шагов модели
	ToolCalls int // вызовов инструментов pulse
	Usage     llmModel.Usage

	// Trace — вызовы инструментов по порядку (ход разбора для отладки).
	Trace []ToolTrace

	// Charts — графики к ответу (render_chart), по порядку построения.
	Charts []Chart
}

// Chart — график к ответу: картинка и данные, по которым она нарисована (клиент может
// нарисовать сам).
type Chart struct {
	Title string
	Png   []byte
	Spec  *chartModel.Spec
}

// Structured — ответ по полям: системе не нужно разбирать текст.
type Structured struct {
	Summary            string
	Status             string // ok | degraded | down | not_found | unknown
	Severity           string // none | low | medium | high | critical
	Services           []StructuredService
	Facts              []Fact
	NextSteps          []string
	UnavailableSources []string
}

type StructuredService struct {
	Name   string
	Status string
	Note   string
}

// Fact — факт, на котором основан вывод; Time и Service — если есть.
type Fact struct {
	Text    string
	Time    *time.Time
	Service string
}

// ToolTrace — вызов инструмента в ходе разбора.
type ToolTrace struct {
	Step      int // шаг модели, запросивший вызов (с 1)
	Name      string
	Arguments string // как их написала модель (токенами)
	Status    string // ToolStatus*
	Output    string // что ушло модели (токенами)
	Duration  time.Duration
	Chart     *Chart // построенный график (render_chart)
}

// Skill — навык: руководство по теме, которое модель открывает сама (open_skill). В системном
// промпте — только имя, название и когда нужен.
type Skill struct {
	Name  string
	Title string
	When  string
	Body  string
	// RequiresChat — только в беседе (нужны инструменты беседы)
	RequiresChat bool
}
