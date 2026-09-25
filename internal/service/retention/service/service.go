// Package service — чистка журнала вопросов по сроку хранения: раз в Interval удаляет записи
// старше Keep (первый проход — сразу на старте).
package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Config struct {
	Keep     time.Duration // сколько хранить
	Interval time.Duration // как часто чистить
}

type Service struct {
	conf    Config
	journal JournalI
	now     func() time.Time
	wg      sync.WaitGroup
}

func New(conf Config, journal JournalI) *Service {
	return &Service{conf: conf, journal: journal, now: time.Now}
}

func (s *Service) Run(ctx context.Context) error {
	deleted, err := s.journal.DeleteBefore(ctx, s.now().Add(-s.conf.Keep))
	if err != nil {
		return fmt.Errorf("journal.DeleteBefore: %w", err)
	}
	if deleted > 0 {
		slog.Info("journal retention", "deleted", deleted, "keep", s.conf.Keep)
	}
	return nil
}

func (s *Service) Start(ctx context.Context) {
	s.wg.Go(func() {
		s.runLogged(ctx)

		ticker := time.NewTicker(s.conf.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runLogged(ctx)
			}
		}
	})
}

func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) runLogged(ctx context.Context) {
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("journal retention failed", "error", err)
	}
}
