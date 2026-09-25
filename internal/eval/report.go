package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
)

// Report — итог прогона (JSON: сравнивается со следующим прогоном).
type Report struct {
	StartedAt time.Time    `json:"started_at"`
	Url       string       `json:"url"`
	Totals    Totals       `json:"totals"`
	Cases     []CaseResult `json:"cases"`
}

type Totals struct {
	Cases         int   `json:"cases"`
	Pass          int   `json:"pass"`
	Fail          int   `json:"fail"`
	Errors        int   `json:"errors"`
	AvgDurationMs int64 `json:"avg_duration_ms"`
	InputTokens   int64 `json:"input_tokens"`
	CachedTokens  int64 `json:"cached_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
}

type CaseResult struct {
	Id         string         `json:"id"`
	Question   string         `json:"question"`
	Format     string         `json:"format,omitempty"`
	Pass       bool           `json:"pass"`
	Failures   []string       `json:"failures,omitempty"`
	Error      string         `json:"error,omitempty"`
	Answer     string         `json:"answer,omitempty"`
	Result     *dto.ResultRep `json:"result,omitempty"`
	Incomplete string         `json:"incomplete,omitempty"`
	DurationMs int64          `json:"duration_ms"`
	Steps      int            `json:"steps"`
	ToolCalls  int            `json:"tool_calls"`
	Usage      dto.UsageRep   `json:"usage"`
	Tools      []string       `json:"tools,omitempty"`
	Charts     []dto.ChartRep `json:"charts,omitempty"`
}

func totals(cases []CaseResult) Totals {
	t := Totals{Cases: len(cases)}
	var duration int64
	for _, c := range cases {
		switch {
		case c.Error != "":
			t.Errors++
		case c.Pass:
			t.Pass++
		default:
			t.Fail++
		}
		duration += c.DurationMs
		t.InputTokens += c.Usage.InputTokens
		t.CachedTokens += c.Usage.CachedTokens
		t.OutputTokens += c.Usage.OutputTokens
	}
	if answered := len(cases) - t.Errors; answered > 0 {
		t.AvgDurationMs = duration / int64(answered)
	}
	return t
}

// Save пишет отчёт; картинки графиков в JSON не кладутся (их можно сохранить отдельно).
func (r *Report) Save(path string) error {
	light := *r
	light.Cases = lo.Map(r.Cases, func(c CaseResult, _ int) CaseResult {
		c.Charts = lo.Map(c.Charts, func(ch dto.ChartRep, _ int) dto.ChartRep { ch.Png = ""; return ch })
		return c
	})
	raw, err := json.MarshalIndent(light, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if err = os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// LoadReport читает прошлый отчёт для сравнения.
func LoadReport(path string) (*Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var r Report
	if err = json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &r, nil
}

// Print — таблица прогона; с baseline — что сломалось и что починилось по сравнению с ним.
func (r *Report) Print(w io.Writer, baseline *Report) {
	prev := map[string]CaseResult{}
	if baseline != nil {
		prev = lo.SliceToMap(baseline.Cases, func(c CaseResult) (string, CaseResult) { return c.Id, c })
	}

	for _, c := range r.Cases {
		mark := lo.Ternary(c.Pass, "✅", lo.Ternary(c.Error != "", "💥", "❌"))
		change := ""
		if p, ok := prev[c.Id]; ok && p.Pass != c.Pass {
			change = lo.Ternary(c.Pass, "  ↑ починилось", "  ↓ сломалось")
		}
		_, _ = fmt.Fprintf(w, "%s %-24s %5.1fs  вызовов %2d  токенов %6d%s\n", mark, c.Id,
			float64(c.DurationMs)/1000, c.ToolCalls, c.Usage.InputTokens, change)
		if c.Error != "" {
			_, _ = fmt.Fprintf(w, "     ошибка: %s\n", c.Error)
		}
		for _, f := range c.Failures {
			_, _ = fmt.Fprintf(w, "     - %s\n", f)
		}
	}

	t := r.Totals
	_, _ = fmt.Fprintf(w, "\nИтого: %d/%d прошли, %d не прошли, %d ошибок; в среднем %.1fs; токенов: вход %d (кэш %d), выход %d\n",
		t.Pass, t.Cases, t.Fail, t.Errors, float64(t.AvgDurationMs)/1000, t.InputTokens, t.CachedTokens, t.OutputTokens)
	if baseline != nil {
		// прошлый прогон — по тем же вопросам: прогон части набора сравнивается с той же частью
		ids := lo.SliceToMap(r.Cases, func(c CaseResult) (string, struct{}) { return c.Id, struct{}{} })
		b := totals(lo.Filter(baseline.Cases, func(c CaseResult, _ int) bool { _, ok := ids[c.Id]; return ok }))
		_, _ = fmt.Fprintf(w, "Было:  %d/%d прошли; в среднем %.1fs; вход %d, выход %d (%s)\n",
			b.Pass, b.Cases, float64(b.AvgDurationMs)/1000, b.InputTokens, b.OutputTokens, baseline.StartedAt.Format(time.DateTime))
	}
}

// Failed — есть непрошедшие вопросы или ошибки.
func (r *Report) Failed() bool {
	return r.Totals.Fail > 0 || r.Totals.Errors > 0
}

// ChartsTo сохраняет картинки графиков в каталог: <id>-<n>.png.
func (r *Report) ChartsTo(dir string, decode func(string) ([]byte, error)) error {
	for _, c := range r.Cases {
		for i, ch := range c.Charts {
			png, err := decode(ch.Png)
			if err != nil {
				return fmt.Errorf("chart %s #%d: %w", c.Id, i+1, err)
			}
			name := fmt.Sprintf("%s/%s-%d.png", strings.TrimRight(dir, "/"), c.Id, i+1)
			if err = os.WriteFile(name, png, 0o644); err != nil {
				return fmt.Errorf("write chart: %w", err)
			}
		}
	}
	return nil
}
