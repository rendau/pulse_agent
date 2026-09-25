package model

import (
	"time"

	chartModel "github.com/mechta-market/pulse_agent/internal/service/chart/model"
	llmModel "github.com/mechta-market/pulse_agent/internal/service/llm/model"
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
}

// Result — итог разбора.
type Result struct {
	// Answer — текст ответа; в формате json — собран из Structured
	Answer string

	// Structured — ответ по полям (формат json); nil — текстовый формат или модель не
	// выдала разбираемый JSON
	Structured *Structured

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
	Arguments string
	Status    string // ToolStatus*
	Output    string // что ушло модели
	Duration  time.Duration
	Chart     *Chart // построенный график (render_chart)
}
