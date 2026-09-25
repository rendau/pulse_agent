package eval

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
)

// Evaluate — какие проверки ответ не прошёл (пусто — прошёл).
func Evaluate(c Checks, rep *dto.AskRep) []string {
	var failures []string
	fail := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }

	tools := lo.Map(rep.Trace, func(t *dto.ToolTraceRep, _ int) string { return t.Tool })

	for _, want := range c.Calls {
		if !lo.ContainsBy(rep.Trace, func(t *dto.ToolTraceRep) bool { return t.Tool == want.Tool && argsMatch(t.Arguments, want.Args) }) {
			fail("нет вызова %s", describeCall(want))
		}
	}
	for _, tool := range c.NoCalls {
		if lo.Contains(tools, tool) {
			fail("лишний вызов %s", tool)
		}
	}
	for tool, limit := range c.ToolLimits {
		if n := lo.Count(tools, tool); n > limit {
			fail("вызовов %s %d > %d", tool, n, limit)
		}
	}
	if c.FirstCall != "" && lo.FirstOr(tools, "") != c.FirstCall {
		fail("первый вызов %s, а не %s", lo.FirstOr(tools, "—"), c.FirstCall)
	}
	if c.NoTools && len(tools) > 0 {
		fail("вызваны инструменты: %s", strings.Join(tools, ", "))
	}

	for _, re := range c.AnswerRegex {
		if !regexp.MustCompile(re).MatchString(rep.Answer) {
			fail("в ответе нет /%s/", re)
		}
	}
	for _, re := range c.AnswerNotRegex {
		if m := regexp.MustCompile(re).FindString(rep.Answer); m != "" {
			fail("в ответе есть /%s/: %q", re, m)
		}
	}

	if c.Charts != nil {
		n := len(rep.Charts)
		if n < c.Charts.Min || (c.Charts.Max > 0 && n > c.Charts.Max) {
			fail("графиков %d, ожидалось %d–%d", n, c.Charts.Min, c.Charts.Max)
		}
	}

	if c.MaxToolCalls > 0 && rep.ToolCalls > c.MaxToolCalls {
		fail("вызовов %d > %d", rep.ToolCalls, c.MaxToolCalls)
	}
	if d := time.Duration(rep.DurationMs) * time.Millisecond; c.MaxDuration > 0 && d > c.MaxDuration {
		fail("время %s > %s", d.Round(time.Second), c.MaxDuration)
	}
	if c.MaxInputTokens > 0 && rep.Usage.InputTokens > c.MaxInputTokens {
		fail("входных токенов %d > %d", rep.Usage.InputTokens, c.MaxInputTokens)
	}
	if rep.Incomplete != "" && !c.AllowIncomplete {
		fail("разбор не закончен: %s", rep.Incomplete)
	}
	if strings.TrimSpace(rep.Answer) == "" {
		fail("пустой ответ")
	}

	return failures
}

// evaluateResult — ответ по полям (format=json): есть, и значения из допустимых.
func evaluateResult(rep *dto.AskRep) []string {
	r := rep.Result
	if r == nil {
		return []string{"нет result при format=json"}
	}
	var failures []string
	if strings.TrimSpace(r.Summary) == "" {
		failures = append(failures, "пустой result.summary")
	}
	if !lo.Contains([]string{"ok", "degraded", "down", "not_found", "unknown"}, r.Status) {
		failures = append(failures, fmt.Sprintf("result.status %q", r.Status))
	}
	if !lo.Contains([]string{"none", "low", "medium", "high", "critical"}, r.Severity) {
		failures = append(failures, fmt.Sprintf("result.severity %q", r.Severity))
	}
	if len(r.Facts) == 0 {
		failures = append(failures, "нет result.facts")
	}
	return failures
}

// argsMatch — аргументы вызова (JSON от модели) подходят под ожидания.
func argsMatch(arguments string, want map[string]string) bool {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return len(want) == 0
	}

	for key, expected := range want {
		actual := argString(args[key])
		switch {
		case expected == "":
			if actual != "" {
				return false
			}
		case strings.HasPrefix(expected, "~"):
			re, err := regexp.Compile(expected[1:])
			if err != nil || !re.MatchString(actual) {
				return false
			}
		default:
			if actual != expected {
				return false
			}
		}
	}
	return true
}

func argString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		raw, _ := json.Marshal(v)
		return string(raw)
	}
}

func describeCall(c CallCheck) string {
	if len(c.Args) == 0 {
		return c.Tool
	}
	keys := lo.Keys(c.Args)
	sort.Strings(keys)
	return c.Tool + "(" + strings.Join(lo.Map(keys, func(k string, _ int) string { return k + "=" + c.Args[k] }), ", ") + ")"
}
