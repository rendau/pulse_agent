package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/mechta-market/pulse_agent/internal/handler/http/dto"
)

// Runner задаёт вопросы API агента (POST /v1/ask); у каждого вопроса своя беседа.
type Runner struct {
	Url      string
	Token    string
	Parallel int
	Client   *http.Client
}

// Run прогоняет вопросы параллельно (не больше Parallel одновременно: у каждого — запросы
// в Loki и Prometheus) и собирает отчёт в порядке набора.
func (r *Runner) Run(ctx context.Context, cases []Case) *Report {
	report := &Report{StartedAt: time.Now(), Url: r.Url, Cases: make([]CaseResult, len(cases))}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(max(r.Parallel, 1))
	for i, c := range cases {
		eg.Go(func() error {
			report.Cases[i] = r.runCase(egCtx, fmt.Sprintf("eval-%d-%s", report.StartedAt.Unix(), c.Id), c)
			return nil
		})
	}
	_ = eg.Wait()

	report.Totals = totals(report.Cases)
	return report
}

func (r *Runner) runCase(ctx context.Context, conversation string, c Case) CaseResult {
	result := CaseResult{Id: c.Id, Question: c.Question, Format: c.Format}
	user := dto.UserReq{Id: "eval"}

	for _, q := range c.Before {
		if _, err := r.ask(ctx, &dto.AskReq{Question: q, ConversationId: conversation, User: user, Format: c.Format}); err != nil {
			result.Error = fmt.Sprintf("вопрос перед основным %q: %s", q, err)
			return result
		}
	}

	rep, err := r.ask(ctx, &dto.AskReq{
		Question: c.Question, ConversationId: conversation, User: user, Format: c.Format,
		ResponseSchema: c.ResponseSchema, Trace: true, TraceOutputLimit: 300,
	})
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Failures = Evaluate(c.Checks, rep)
	if c.Format == "json" || c.ResponseSchema != nil {
		result.Failures = append(result.Failures, evaluateResult(rep, c.ResponseSchema != nil, c.Checks.ResultFields)...)
	}
	result.Pass = len(result.Failures) == 0
	result.Answer = rep.Answer
	result.Result = rep.Result
	result.Incomplete = rep.Incomplete
	result.DurationMs = rep.DurationMs
	result.Steps = rep.Steps
	result.ToolCalls = rep.ToolCalls
	result.Usage = rep.Usage
	result.Tools = lo.Map(rep.Trace, func(t *dto.ToolTraceRep, _ int) string { return t.Tool + " " + t.Arguments })
	result.Charts = rep.Charts
	return result
}

func (r *Runner) ask(ctx context.Context, req *dto.AskReq) (*dto.AskRep, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.Url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+r.Token)

	resp, err := r.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ask: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e dto.ErrorRep
		_ = json.Unmarshal(raw, &e)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, lo.CoalesceOrEmpty(e.Error, string(raw)))
	}

	var rep dto.AskRep
	if err = json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &rep, nil
}
