package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse_agent/internal/domain/journal/model"
)

// questionChars — текст вопроса в журнале обрезается: журнал для обзора, не для архива.
const questionChars = 300

type Service struct {
	repo RepoI
}

func New(repo RepoI) *Service {
	return &Service{repo: repo}
}

func (s *Service) Append(ctx context.Context, e *model.Entry) error {
	if r := []rune(e.Question); len(r) > questionChars {
		e.Question = string(r[:questionChars]) + "…"
	}
	if err := s.repo.Append(ctx, e); err != nil {
		return fmt.Errorf("repo.Append: %w", err)
	}
	return nil
}

// Recent — последние записи по фильтру, новые — первыми.
func (s *Service) Recent(ctx context.Context, f model.Filter) ([]*model.Entry, error) {
	entries, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("repo.List: %w", err)
	}

	result := make([]*model.Entry, 0, min(len(entries), max(f.Limit, 0)))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if (f.Client != "" && e.Client != f.Client) || (f.Outcome != "" && e.Outcome != f.Outcome) {
			continue
		}
		result = append(result, e)
		if f.Limit > 0 && len(result) >= f.Limit {
			break
		}
	}
	return result, nil
}

// Stats — сводка по окну журнала: по системам — исходы, время, токены; частота инструментов.
func (s *Service) Stats(ctx context.Context) (*model.Stats, error) {
	entries, err := s.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("repo.List: %w", err)
	}

	stats := &model.Stats{Questions: len(entries)}
	if len(entries) > 0 {
		stats.Since = entries[0].At
	}

	byClient := lo.GroupBy(entries, func(e *model.Entry) string { return e.Client })
	for client, list := range byClient {
		cs := model.ClientStats{Client: client, Questions: len(list), Outcomes: map[string]int{}}
		durations := make([]time.Duration, 0, len(list))
		for _, e := range list {
			cs.Outcomes[e.Outcome]++
			cs.InputTokens += e.InputTokens
			cs.CachedTokens += e.CachedTokens
			cs.OutputTokens += e.OutputTokens
			if e.Outcome == model.OutcomeAnswered || e.Outcome == model.OutcomeIncomplete {
				durations = append(durations, e.Duration)
			}
		}
		if len(durations) > 0 {
			slices.Sort(durations)
			cs.AvgDuration = lo.Sum(durations) / time.Duration(len(durations))
			cs.P95Duration = durations[min(len(durations)-1, len(durations)*95/100)]
		}
		stats.Clients = append(stats.Clients, cs)
	}
	sort.Slice(stats.Clients, func(i, j int) bool { return stats.Clients[i].Client < stats.Clients[j].Client })

	calls := lo.CountValues(lo.FlatMap(entries, func(e *model.Entry, _ int) []string { return e.Tools }))
	stats.Tools = lo.MapToSlice(calls, func(tool string, n int) model.ToolStats { return model.ToolStats{Tool: tool, Calls: n} })
	sort.Slice(stats.Tools, func(i, j int) bool {
		if stats.Tools[i].Calls != stats.Tools[j].Calls {
			return stats.Tools[i].Calls > stats.Tools[j].Calls
		}
		return strings.Compare(stats.Tools[i].Tool, stats.Tools[j].Tool) < 0
	})

	return stats, nil
}
