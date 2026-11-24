package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rendau/pulse_agent/internal/domain/block/model"
)

type Service struct {
	repo RepoI
	now  func() time.Time
}

func New(repo RepoI) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Decisions — решения по уведомлению, по порядку.
func (s *Service) Decisions(ctx context.Context, notificationId int64) ([]*model.Decision, error) {
	decisions, err := s.repo.ListDecisions(ctx, notificationId)
	if err != nil {
		return nil, fmt.Errorf("repo.ListDecisions: %w", err)
	}
	return decisions, nil
}

// Record сохраняет решение (At — сейчас). Уникальность держит хранилище: если итоговое решение
// (или для skip — первое «не блокировать») уже записано, возвращается оно и false.
func (s *Service) Record(ctx context.Context, d *model.Decision) (*model.Decision, bool, error) {
	d.At = s.now()
	created, err := s.repo.CreateDecision(ctx, d)
	if err != nil {
		return nil, false, fmt.Errorf("repo.CreateDecision: %w", err)
	}
	if created {
		return d, true, nil
	}

	decisions, err := s.repo.ListDecisions(ctx, d.NotificationId)
	if err != nil {
		return nil, false, fmt.Errorf("repo.ListDecisions: %w", err)
	}
	if final := model.Final(decisions); final != nil {
		return final, false, nil
	}
	if skip := model.FirstSkip(decisions); skip != nil && d.Action == model.ActionSkip {
		return skip, false, nil
	}
	return nil, false, fmt.Errorf("decision for notification %d was not recorded and no earlier one found", d.NotificationId)
}
