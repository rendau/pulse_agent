package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mechta-market/pulse_agent/internal/domain/chat/model"
	"github.com/mechta-market/pulse_agent/internal/errs"
)

type Service struct {
	repo RepoI
	now  func() time.Time
}

func New(repo RepoI) *Service {
	return &Service{repo: repo, now: time.Now}
}

// Get — контекст беседы; о беседе ничего не записано — пустой (без заметок и курсора).
func (s *Service) Get(ctx context.Context, client, conversationId string) (*model.Chat, error) {
	chat, err := s.repo.Get(ctx, client, conversationId)
	if err != nil {
		return nil, fmt.Errorf("repo.Get: %w", err)
	}
	if chat == nil {
		chat = &model.Chat{Client: client, ConversationId: conversationId}
	}
	return chat, nil
}

// SaveNotes перезаписывает заметки беседы целиком; пустые — забыть всё. Ошибки:
// errs.InvalidRequest — длиннее model.MaxNotesChars.
func (s *Service) SaveNotes(ctx context.Context, client, conversationId, notes, by string) error {
	notes = strings.TrimSpace(notes)
	if n := len([]rune(notes)); n > model.MaxNotesChars {
		return fmt.Errorf("%w: notes are %d characters, max %d — keep only what matters", errs.InvalidRequest, n, model.MaxNotesChars)
	}
	if err := s.repo.SaveNotes(ctx, client, conversationId, notes, by, s.now()); err != nil {
		return fmt.Errorf("repo.SaveNotes: %w", err)
	}
	return nil
}

// SetCursor — беседа получила уведомления до cursor включительно.
func (s *Service) SetCursor(ctx context.Context, client, conversationId string, cursor int64) error {
	if err := s.repo.SetCursor(ctx, client, conversationId, cursor); err != nil {
		return fmt.Errorf("repo.SetCursor: %w", err)
	}
	return nil
}
