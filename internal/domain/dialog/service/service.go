package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mechta-market/pulse_agent/internal/domain/dialog/model"
)

// Config — правила истории: сколько пар держать и через сколько тишины забывать.
type Config struct {
	MaxTurns int
	Ttl      time.Duration
}

type Service struct {
	cfg  Config
	repo RepoI
	now  func() time.Time
}

func New(cfg Config, repo RepoI) *Service {
	return &Service{cfg: cfg, repo: repo, now: time.Now}
}

// History — история беседы; после Ttl тишины (от последней пары) начинается заново.
func (s *Service) History(ctx context.Context, conversationId string) ([]*model.Turn, error) {
	turns, err := s.repo.List(ctx, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.List: %w", err)
	}

	if len(turns) > 0 && s.cfg.Ttl > 0 && s.now().Sub(turns[len(turns)-1].At) > s.cfg.Ttl {
		if err = s.repo.Replace(ctx, conversationId, nil); err != nil {
			return nil, fmt.Errorf("repo.Replace: %w", err)
		}
		return nil, nil
	}

	return turns, nil
}

// Append добавляет пару и оставляет последние MaxTurns.
func (s *Service) Append(ctx context.Context, conversationId string, question, answer string) error {
	turns, err := s.History(ctx, conversationId)
	if err != nil {
		return err
	}

	turns = append(turns, &model.Turn{Question: question, Answer: answer, At: s.now()})
	if s.cfg.MaxTurns > 0 && len(turns) > s.cfg.MaxTurns {
		turns = turns[len(turns)-s.cfg.MaxTurns:]
	}

	if err = s.repo.Replace(ctx, conversationId, turns); err != nil {
		return fmt.Errorf("repo.Replace: %w", err)
	}

	return nil
}

// Reset забывает историю беседы (/reset).
func (s *Service) Reset(ctx context.Context, conversationId string) error {
	if err := s.repo.Replace(ctx, conversationId, nil); err != nil {
		return fmt.Errorf("repo.Replace: %w", err)
	}
	return nil
}
