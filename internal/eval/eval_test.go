package eval

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
)

func TestLoad_RealSuite(t *testing.T) {
	s, err := Load("../../evals/cases.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, s.Cases)

	// общие запреты добавлены к каждому вопросу, свои лимиты сильнее общих
	c := s.Cases[0]
	assert.Contains(t, c.Checks.AnswerNotRegex, `(?i)мcpu|mcpu`)
	assert.NotContains(t, c.Checks.AnswerNotRegex[1], `\bмиб`, "\\b в RE2 — только латиница")
	assert.Equal(t, 12, c.Checks.MaxToolCalls)
	assert.Equal(t, 3*time.Minute, c.Checks.MaxDuration)
}

func TestLoad_Invalid(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		path := filepath.Join(dir, "cases.yaml")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		return path
	}

	_, err := Load(write("cases: [{id: a, question: q}, {id: a, question: q}]"))
	require.ErrorContains(t, err, "duplicate")

	_, err = Load(write("cases: [{id: a, question: q, checks: {answer_regex: ['(']}}]"))
	require.ErrorContains(t, err, "regexp")

	_, err = Load(write("cases: [{id: a}]"))
	require.ErrorContains(t, err, "required")
}

func rep(answer string, tools ...[2]string) *dto.AskRep {
	r := &dto.AskRep{Answer: answer, DurationMs: 20_000, ToolCalls: len(tools), Usage: dto.UsageRep{InputTokens: 30_000}}
	for _, t := range tools {
		r.Trace = append(r.Trace, &dto.ToolTraceRep{Tool: t[0], Arguments: t[1], Status: "ok"})
	}
	return r
}

func TestEvaluate(t *testing.T) {
	checks := Checks{
		Calls: []CallCheck{
			{Tool: "query_logs", Args: map[string]string{"service": "", "pattern": "~41314079", "window": ""}},
		},
		NoCalls:        []string{"get_service_info"},
		ToolLimits:     map[string]int{"get_service_info": 1},
		FirstCall:      "query_logs",
		AnswerRegex:    []string{`(?i)delivery`},
		AnswerNotRegex: []string{`(?i)(^|\P{L})миб(\P{L}|$)`},
		Charts:         &Range{Min: 0, Max: 1},
		MaxToolCalls:   3,
		MaxDuration:    time.Minute,
	}

	ok := rep("Заказ прошёл через Delivery", [2]string{"query_logs", `{"pattern":"41314079"}`})
	assert.Empty(t, Evaluate(checks, ok))

	bad := rep("Память 500 МиБ",
		[2]string{"resolve_service", `{"query":"заказ"}`},
		[2]string{"query_logs", `{"service":"delivery","pattern":"41314079"}`},
		[2]string{"get_service_info", `{"service":"delivery"}`},
		[2]string{"get_service_info", `{"service":"mb-broker"}`},
	)
	bad.DurationMs, bad.Incomplete = 90_000, "timeout"
	failures := Evaluate(checks, bad)
	assert.Contains(t, failures, "нет вызова query_logs(pattern=~41314079, service=, window=)")
	assert.Contains(t, failures, "лишний вызов get_service_info")
	assert.Contains(t, failures, "вызовов get_service_info 2 > 1")
	assert.Contains(t, failures, "первый вызов resolve_service, а не query_logs")
	assert.Contains(t, failures, "в ответе нет /(?i)delivery/")
	assert.Contains(t, failures, `в ответе есть /(?i)(^|\P{L})миб(\P{L}|$)/: " МиБ"`)
	assert.Contains(t, failures, "вызовов 4 > 3")
	assert.Contains(t, failures, "время 1m30s > 1m0s")
	assert.Contains(t, failures, "разбор не закончен: timeout")

	// без инструментов и с графиками
	charts := rep("я про инфраструктуру")
	charts.Charts = []dto.ChartRep{{}, {}}
	failures = Evaluate(Checks{NoTools: true, Charts: &Range{Min: 1, Max: 1}}, charts)
	assert.Equal(t, []string{"графиков 2, ожидалось 1–1"}, failures)
}

func TestArgsMatch(t *testing.T) {
	assert.True(t, argsMatch(`{"window":"24h","limit":5}`, map[string]string{"window": "24h", "limit": "5"}))
	assert.False(t, argsMatch(`{"window":"6h"}`, map[string]string{"window": "24h"}))
	assert.True(t, argsMatch(`{"end":"2026-09-24T14:00"}`, map[string]string{"end": "~^2026-09-24T14", "service": ""}))
	assert.False(t, argsMatch(`{"service":"delivery"}`, map[string]string{"service": ""}))
	assert.True(t, argsMatch(`not json`, nil))
}

func TestSkipped(t *testing.T) {
	c := Case{SkipAfter: "2026-10-20"}
	assert.False(t, c.Skipped(time.Date(2026, 10, 20, 23, 0, 0, 0, time.UTC)))
	assert.True(t, c.Skipped(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)))
	assert.False(t, (&Case{}).Skipped(time.Now()))
}
