// Package eval — эталонные вопросы агенту: набор вопросов с проверками поведения
// (какие инструменты вызваны и с какими параметрами, чего не должно быть в ответе, нужен ли
// график, время и токены), прогон через HTTP-ручку агента и отчёт со сравнением с прошлым.
// Данные живые, поэтому проверяется поведение, а не точные числа.
package eval

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/samber/lo"
	"go.yaml.in/yaml/v3"
)

// Suite — набор вопросов; Defaults применяются к каждому вопросу (списки дополняются,
// лимиты — если у вопроса свой не задан).
type Suite struct {
	Defaults Checks `yaml:"defaults"`
	Cases    []Case `yaml:"cases"`
}

// Case — эталонный вопрос. Before — вопросы той же беседы перед ним (уточнения вида
// «а за сутки?»); проверки — только по последнему ответу.
type Case struct {
	Id       string   `yaml:"id"`
	Question string   `yaml:"question"`
	Before   []string `yaml:"before"`
	// Format — формат ответа (telegram по умолчанию); json — проверяется и ответ по полям
	Format string `yaml:"format"`
	// ResponseSchema — своя схема ответа (RPC); проверки полей — checks.result_fields
	ResponseSchema map[string]any `yaml:"response_schema"`
	Checks         Checks         `yaml:"checks"`
	// SkipAfter — дата, после которой вопрос устарел (данные ушли из хранения логов)
	SkipAfter string `yaml:"skip_after"`
	Note      string `yaml:"note"`
}

// Checks — что проверить в ответе.
type Checks struct {
	// Calls — вызовы, которые должны быть: инструмент и подмножество аргументов
	Calls []CallCheck `yaml:"calls"`
	// NoCalls — инструменты, которых быть не должно
	NoCalls []string `yaml:"no_calls"`
	// ToolLimits — не больше стольких вызовов инструмента (повторы одного и того же поиска)
	ToolLimits map[string]int `yaml:"tool_limits"`
	// FirstCall — каким инструментом разбор должен начаться
	FirstCall string `yaml:"first_call"`
	// NoTools — отвечает без инструментов (вопрос не про инфраструктуру)
	NoTools bool `yaml:"no_tools"`

	AnswerRegex    []string `yaml:"answer_regex"`
	AnswerNotRegex []string `yaml:"answer_not_regex"`

	Charts *Range `yaml:"charts"`

	MaxToolCalls   int           `yaml:"max_tool_calls"`
	MaxDuration    time.Duration `yaml:"max_duration"`
	MaxInputTokens int64         `yaml:"max_input_tokens"`
	// AllowIncomplete — разбор может закончиться досрочно (лимит времени/вызовов)
	AllowIncomplete bool `yaml:"allow_incomplete"`
	// ResultFields — поля ответа по своей схеме (как args в calls)
	ResultFields map[string]string `yaml:"result_fields"`
}

// CallCheck — вызов инструмента. Args — подмножество аргументов: значение сравнивается
// строкой; "" — аргумента нет или он пустой; "~регэксп" — строка аргумента подходит.
type CallCheck struct {
	Tool string            `yaml:"tool"`
	Args map[string]string `yaml:"args"`
}

type Range struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

// Load читает набор и проверяет регэкспы и уникальность id.
func Load(path string) (*Suite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var s Suite
	if err = yaml.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	seen := map[string]struct{}{}
	for i := range s.Cases {
		c := &s.Cases[i]
		if c.Id == "" || c.Question == "" {
			return nil, fmt.Errorf("case #%d: id and question are required", i+1)
		}
		if _, ok := seen[c.Id]; ok {
			return nil, fmt.Errorf("case %s: duplicate id", c.Id)
		}
		seen[c.Id] = struct{}{}

		c.Checks = merge(s.Defaults, c.Checks)
		for _, re := range append(append([]string{}, c.Checks.AnswerRegex...), c.Checks.AnswerNotRegex...) {
			if _, err = regexp.Compile(re); err != nil {
				return nil, fmt.Errorf("case %s: regexp %q: %w", c.Id, re, err)
			}
		}
		if c.SkipAfter != "" {
			if _, err = time.Parse(time.DateOnly, c.SkipAfter); err != nil {
				return nil, fmt.Errorf("case %s: skip_after %q: expected 2026-10-20", c.Id, c.SkipAfter)
			}
		}
	}

	return &s, nil
}

// merge — проверки вопроса поверх общих: списки дополняются, лимиты — свои приоритетнее.
func merge(defaults, own Checks) Checks {
	own.NoCalls = lo.Uniq(append(append([]string{}, defaults.NoCalls...), own.NoCalls...))
	own.AnswerRegex = append(append([]string{}, defaults.AnswerRegex...), own.AnswerRegex...)
	own.AnswerNotRegex = append(append([]string{}, defaults.AnswerNotRegex...), own.AnswerNotRegex...)
	own.MaxToolCalls = lo.CoalesceOrEmpty(own.MaxToolCalls, defaults.MaxToolCalls)
	own.MaxDuration = lo.CoalesceOrEmpty(own.MaxDuration, defaults.MaxDuration)
	own.MaxInputTokens = lo.CoalesceOrEmpty(own.MaxInputTokens, defaults.MaxInputTokens)
	return own
}

// Skipped — вопрос устарел к дате now.
func (c *Case) Skipped(now time.Time) bool {
	if c.SkipAfter == "" {
		return false
	}
	after, _ := time.Parse(time.DateOnly, c.SkipAfter)
	return now.After(after.AddDate(0, 0, 1))
}
