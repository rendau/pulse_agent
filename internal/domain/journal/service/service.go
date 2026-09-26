package service

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse_agent/internal/domain/journal/model"
)

type Service struct {
	repo RepoI
}

func New(repo RepoI) *Service {
	return &Service{repo: repo}
}

func (s *Service) Append(ctx context.Context, e *model.Entry) error {
	if err := s.repo.Append(ctx, e); err != nil {
		return fmt.Errorf("repo.Append: %w", err)
	}
	return nil
}

// Recent — записи по фильтру, новые — первыми, без ответа и хода разбора.
func (s *Service) Recent(ctx context.Context, f model.Filter) ([]*model.Entry, error) {
	entries, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("repo.List: %w", err)
	}
	return entries, nil
}

// Get — запись целиком: ответ и ход разбора; nil — нет такой (или уже удалена по сроку).
func (s *Service) Get(ctx context.Context, id int64) (*model.Entry, error) {
	e, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("repo.Get: %w", err)
	}
	return e, nil
}

// DeleteBefore удаляет записи старше before (срок хранения).
func (s *Service) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.repo.DeleteBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("repo.DeleteBefore: %w", err)
	}
	return n, nil
}

// Stats — сводка за окно с since: по системам — исходы, время, токены; частота инструментов.
func (s *Service) Stats(ctx context.Context, since time.Time) (*model.Stats, error) {
	entries, err := s.repo.List(ctx, model.Filter{Since: since})
	if err != nil {
		return nil, fmt.Errorf("repo.List: %w", err)
	}

	stats := &model.Stats{Since: since, Questions: len(entries)}

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
