package eval

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse_agent/internal/errs"
)

// Keeper — прогоны на стороне сервиса: набор и эталон вшиты в образ, одновременно идёт один
// прогон (у каждого — десятки запросов в Loki и Prometheus и сотни тысяч токенов), последний
// прогон хранится в памяти до рестарта.
type Keeper struct {
	suite    *Suite
	baseline *Report
	parallel int
	now      func() time.Time

	mu      sync.Mutex
	running bool
	last    *Report
}

// NewKeeper — cases и baseline — вшитые evals.Cases и evals.Baseline.
func NewKeeper(cases, baseline []byte, parallel int) (*Keeper, error) {
	suite, err := Parse(cases, "evals/cases.yaml")
	if err != nil {
		return nil, err
	}
	base, err := ParseReport(baseline)
	if err != nil {
		return nil, fmt.Errorf("evals/baseline.json: %w", err)
	}
	return &Keeper{suite: suite, baseline: base, parallel: parallel, now: time.Now}, nil
}

// Run прогоняет набор (only — id вопросов; пусто — все, кроме устаревших). errs.Busy —
// прогон уже идёт; errs.InvalidRequest — неизвестный id.
func (k *Keeper) Run(ctx context.Context, ask AskFunc, source string, only []string) (*Report, error) {
	cases, err := k.cases(only)
	if err != nil {
		return nil, err
	}

	k.mu.Lock()
	if k.running {
		k.mu.Unlock()
		return nil, fmt.Errorf("%w: eval is already running", errs.Busy)
	}
	k.running = true
	k.mu.Unlock()

	report := (&Runner{Ask: ask, Parallel: k.parallel, Source: source}).Run(ctx, cases)

	k.mu.Lock()
	k.running, k.last = false, report
	k.mu.Unlock()

	return report, nil
}

func (k *Keeper) cases(only []string) ([]Case, error) {
	ids := lo.Filter(lo.Map(only, func(s string, _ int) string { return strings.TrimSpace(s) }), func(s string, _ int) bool { return s != "" })
	known := lo.Map(k.suite.Cases, func(c Case, _ int) string { return c.Id })
	if unknown := lo.Without(ids, known...); len(unknown) > 0 {
		return nil, fmt.Errorf("%w: unknown cases %s; known: %s", errs.InvalidRequest, strings.Join(unknown, ", "), strings.Join(known, ", "))
	}

	now := k.now()
	cases := lo.Filter(k.suite.Cases, func(c Case, _ int) bool {
		return (len(ids) == 0 || lo.Contains(ids, c.Id)) && !c.Skipped(now)
	})
	if len(cases) == 0 {
		return nil, fmt.Errorf("%w: no cases to run (all skipped)", errs.InvalidRequest)
	}
	return cases, nil
}

// Last — последний прогон (nil — не было с рестарта); Running — идёт ли прогон сейчас.
func (k *Keeper) Last() (last *Report, running bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.last, k.running
}

// Baseline — эталонный прогон из образа.
func (k *Keeper) Baseline() *Report {
	return k.baseline
}

// Cases — id вопросов набора.
func (k *Keeper) Cases() []string {
	return lo.Map(k.suite.Cases, func(c Case, _ int) string { return c.Id })
}
